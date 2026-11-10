package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestContextProfileSeparatesGrowthAndObservedRecovery(t *testing.T) {
	start := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	events := []normalizedSessionEvent{
		{Kind: sessionEventToken, ContextTokens: 100, ContextWindowTokens: 1000},
		{Kind: sessionEventToken, ContextTokens: 200},
		{Kind: sessionEventCompaction, ContextTokens: 240, CompactionTrigger: "auto"},
		{Kind: sessionEventToolCall, ToolRound: 1, FirstFamily: "file reads"},
		{Kind: sessionEventToolOutput, ToolRound: 1, Failed: true},
		{Kind: sessionEventToken, ContextTokens: 60},
		{Kind: sessionEventToken, ContextTokens: 80},
		{Kind: sessionEventComplete},
		{Kind: sessionEventCompaction},
		{Kind: sessionEventToken, ContextTokens: 90},
		{Kind: sessionEventCompaction, CompactionTrigger: "manual"},
	}
	for i := range events {
		events[i].Sequence = i + 1
		events[i].OccurredAt = start.Add(time.Duration(i) * time.Second)
	}
	session := normalizedSession{CWD: "/workspace/app", Events: events}
	record := codexSessionRecord{ContextProfile: profileSessionContext(session, session.CWD, start, start.Add(time.Minute))}
	p := analyzeContextProfiles([]codexSessionRecord{record, {}})
	if p.Sessions != 2 || p.SessionsWithSamples != 1 || p.SessionsWithoutSamples != 1 ||
		p.ObservedInputTokens.Count != 5 || p.FirstObservedInputTokens.P50 != 100 || p.PeakInputTokens.P50 != 200 {
		t.Fatalf("request coverage and starting/peak context: %+v", p)
	}
	// The measured segments grow 100 -> 200 and 60 -> 80. A one-sample
	// segment is insufficient growth evidence; compaction is a reset.
	if p.SegmentGrowthTokens.Count != 2 || p.SegmentGrowthTokens.P50 != 20 || p.SegmentGrowthTokens.Max != 100 {
		t.Fatalf("segment growth: %+v", p.SegmentGrowthTokens)
	}
	r := p.CompactionRecovery
	if r.Boundaries != 3 || r.WithReportedBeforeTokens != 1 || r.WithBeforeSample != 2 ||
		r.WithAfterSample != 2 || r.PairedSamples != 2 || r.CompletedWindows != 1 || r.CensoredWindows != 2 {
		t.Fatalf("recovery coverage: %+v", r)
	}
	// Paired changes are 240 -> 60 and 80 -> 90. The latter must remain
	// a negative reduction, rather than being clipped into an improvement.
	if r.InputReductionTokens.P50 != -10 || r.InputReductionTokens.Max != 180 ||
		r.RequestSamplesToCompletion.P50 != 2 || r.ToolCallsToCompletion.P50 != 1 ||
		r.ReadSearchCallsToCompletion.P50 != 1 || r.FailedCallsToCompletion.P50 != 1 ||
		r.ElapsedSecondsToCompletion.P50 != 5 || r.Triggers["auto"] != 1 || r.Triggers["manual"] != 1 || r.Triggers["unknown"] != 1 {
		t.Fatalf("observed recovery work: %+v", r)
	}
}

func TestContextProfileIndexPreservesWindowAndRepositoryBoundaries(t *testing.T) {
	root, sessions := t.TempDir(), t.TempDir()
	start := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	events := []normalizedSessionEvent{
		{Kind: sessionEventToken, ContextTokens: 100, ContextWindowTokens: 1000, Tokens: normalizedTokenUsage{InputTokens: 100, TotalTokens: 100}},
		{Kind: sessionEventComplete},
		{Kind: sessionEventToken, ContextTokens: 150, Tokens: normalizedTokenUsage{InputTokens: 250, TotalTokens: 250}},
		{Kind: sessionEventCompaction, ContextTokens: 185, CompactionTrigger: "manual"},
		{Kind: sessionEventToken, ContextTokens: 50, Tokens: normalizedTokenUsage{InputTokens: 300, TotalTokens: 300}},
		{Kind: sessionEventToolCall, ToolName: "exec_command", WorkingDirectories: []string{filepath.Dir(root)}},
		{Kind: sessionEventToken, ContextTokens: 1000, Tokens: normalizedTokenUsage{InputTokens: 1300, TotalTokens: 1300}},
		{Kind: sessionEventToolCall, ToolName: "exec_command", WorkingDirectories: []string{root}},
		{Kind: sessionEventCompaction},
		{Kind: sessionEventToken, ContextTokens: 70, Tokens: normalizedTokenUsage{InputTokens: 1370, TotalTokens: 1370}},
		{Kind: sessionEventComplete},
		{Kind: sessionEventToken, ContextTokens: 9999, Tokens: normalizedTokenUsage{InputTokens: 11369, TotalTokens: 11369}},
	}
	for i := range events {
		events[i].Sequence = i + 1
		events[i].OccurredAt = start.Add(time.Duration(i) * time.Second)
	}
	session := normalizedSession{Provider: "codex", SourcePath: filepath.Join(sessions, "context.jsonl"), CWD: root, Events: events}
	markRepositoryEventScope(&session, root)
	store, err := openSessionStore(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.replaceSession(context.Background(), repositoryStoreScopeKey(root, ownershipCatalog{}), session, 1, 1, true); err != nil {
		t.Fatal(err)
	}
	until := start.Add(10 * time.Second)
	for _, second := range []int{0, 3, 8} {
		since := start.Add(time.Duration(second) * time.Second)
		direct, err := sessionRecordFromNormalized(session, root, since, until, ownershipCatalog{})
		if err != nil {
			t.Fatal(err)
		}
		indexed, err := store.analyze(context.Background(), "codex", []string{sessions}, root, since, until, "", ownershipCatalog{}, sessionRefreshStats{})
		if err != nil {
			t.Fatal(err)
		}
		want := analyzeContextProfiles([]codexSessionRecord{direct})
		if !reflect.DeepEqual(indexed.ContextProfile, want) || indexed.Summary.Tokens != direct.Tokens {
			t.Fatalf("lookback %d: indexed=%+v direct=%+v", second, indexed.ContextProfile, want)
		}
		if second == 3 {
			p := indexed.ContextProfile
			if p.FirstObservedInputTokens.Count != 0 || p.ObservedInputTokens.Count != 2 || p.PeakInputTokens.Max != 70 ||
				p.CompactionRecovery.PairedSamples != 1 || p.CompactionRecovery.WithReportedBeforeTokens != 1 ||
				p.CompactionRecovery.WithBeforeSample != 0 || p.CompactionRecovery.CensoredWindows != 1 ||
				indexed.Summary.Tokens.InputTokens != 120 {
				t.Fatalf("clipped history/outside activity leaked into profile: %+v", p)
			}
		}
		for _, detailed := range []bool{false, true} {
			encoded, err := json.Marshal(analysisJSONPayload(indexed, detailed))
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				ContextProfile contextProfileAnalysis `json:"contextProfile"`
			}
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.ContextProfile, want) || strings.Contains(string(encoded), root) {
				t.Fatal("context profile changed during rendering or local path exposed")
			}
		}
	}
}

func TestContextRecoveryRequiresResumedWorkAndExcludesEarlierFailures(t *testing.T) {
	start := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	events := []normalizedSessionEvent{
		{Kind: sessionEventToken, ContextTokens: 100},
		{Kind: sessionEventToolCall, ToolRound: 1},
		{Kind: sessionEventCompaction},
		{Kind: sessionEventComplete}, // A synthetic compaction completion.
		{Kind: sessionEventToolOutput, ToolRound: 1, Failed: true},
		{Kind: sessionEventToolCall, ToolRound: 2, FirstFamily: "search"},
		{Kind: sessionEventToolOutput, ToolRound: 2, Failed: true, Refusal: "denied-by-permission"},
		{Kind: sessionEventToken, ContextTokens: 30},
		{Kind: sessionEventComplete},
	}
	for i := range events {
		events[i].OccurredAt = start.Add(time.Duration(i) * time.Second)
	}
	p := profileSessionContext(normalizedSession{CWD: "/workspace/app", Events: events}, "/workspace/app", start, start.Add(time.Minute))
	if len(p.Recoveries) != 1 {
		t.Fatalf("recoveries: %+v", p)
	}
	r := p.Recoveries[0]
	if !r.Completed || r.Before != 100 || r.After != 30 || r.Requests != 1 || r.ToolCalls != 1 ||
		r.ReadSearch != 1 || r.FailedCalls != 0 || r.ElapsedSeconds != 6 {
		t.Fatalf("synthetic completion, refusal, or earlier failure contaminated recovery: %+v", r)
	}
}

func TestContextProfileDoesNotReuseContextAcrossHistoricalScopeGap(t *testing.T) {
	root, sources := t.TempDir(), t.TempDir()
	start := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	events := []normalizedSessionEvent{
		{Kind: sessionEventToken, ContextTokens: 100, Tokens: normalizedTokenUsage{InputTokens: 100, TotalTokens: 100}},
		{Kind: sessionEventToolCall, ToolName: "exec_command", WorkingDirectories: []string{filepath.Dir(root)}},
		{Kind: sessionEventToolOutput},
		{Kind: sessionEventToolCall, ToolName: "exec_command", WorkingDirectories: []string{root}},
		// Input accounting resumed, but no request-context observation exists
		// after the agent left and re-entered this repository.
		{Kind: sessionEventToken, Tokens: normalizedTokenUsage{InputTokens: 200, TotalTokens: 200}},
		{Kind: sessionEventCompaction},
		{Kind: sessionEventToken, ContextTokens: 80, Tokens: normalizedTokenUsage{InputTokens: 280, TotalTokens: 280}},
		{Kind: sessionEventComplete},
	}
	for i := range events {
		events[i].Sequence = i + 1
		events[i].OccurredAt = start.Add(time.Duration(i) * time.Second)
	}
	session := normalizedSession{Provider: "codex", CWD: root, SourcePath: filepath.Join(sources, "scope.jsonl"), Events: events}
	markRepositoryEventScope(&session, root)
	store, err := openSessionStore(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.replaceSession(context.Background(), repositoryStoreScopeKey(root, ownershipCatalog{}), session, 1, 1, true); err != nil {
		t.Fatal(err)
	}
	report, err := store.analyze(context.Background(), "codex", []string{sources}, root, start.Add(5*time.Second), start.Add(7*time.Second), "", ownershipCatalog{}, sessionRefreshStats{})
	if err != nil {
		t.Fatal(err)
	}
	r := report.ContextProfile.CompactionRecovery
	if r.Boundaries != 1 || r.WithAfterSample != 1 || r.WithBeforeSample != 0 || r.PairedSamples != 0 {
		t.Fatalf("stale context from before a scope gap was reused: %+v", r)
	}
}

// A late result from another repository is not a foreground exit, so it must
// not censor a recovery or split a growth segment, in either analysis path.
func TestContextProfileIgnoresLateResultFromOtherRepository(t *testing.T) {
	root, sources := t.TempDir(), t.TempDir()
	start := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	events := []normalizedSessionEvent{
		{Kind: sessionEventToken, ContextTokens: 100, Tokens: normalizedTokenUsage{InputTokens: 100, TotalTokens: 100}},
		{Kind: sessionEventCompaction},
		{Kind: sessionEventToken, ContextTokens: 30, Tokens: normalizedTokenUsage{InputTokens: 130, TotalTokens: 130}},
		{Kind: sessionEventToolCall, ToolName: "exec_command", ToolRound: 1},
		{Kind: sessionEventToolOutput, ToolName: "exec_command", ToolRound: 1, WorkingDirectories: []string{filepath.Dir(root)}},
		{Kind: sessionEventToken, ContextTokens: 45, Tokens: normalizedTokenUsage{InputTokens: 175, TotalTokens: 175}},
		{Kind: sessionEventComplete},
	}
	for i := range events {
		events[i].Sequence = i + 1
		events[i].OccurredAt = start.Add(time.Duration(i) * time.Second)
	}
	session := normalizedSession{Provider: "codex", CWD: root, SourcePath: filepath.Join(sources, "late.jsonl"), Events: events}
	direct := profileSessionContext(session, root, start, start.Add(time.Minute))
	if len(direct.Recoveries) != 1 || !direct.Recoveries[0].Completed || len(direct.Growth) != 1 || direct.Growth[0] != 15 {
		t.Fatalf("late outside result broke continuity: %+v", direct)
	}
	markRepositoryEventScope(&session, root)
	store, err := openSessionStore(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.replaceSession(context.Background(), repositoryStoreScopeKey(root, ownershipCatalog{}), session, 1, 1, true); err != nil {
		t.Fatal(err)
	}
	indexed, err := store.analyze(context.Background(), "codex", []string{sources}, root, start, start.Add(time.Minute), "", ownershipCatalog{}, sessionRefreshStats{})
	if err != nil {
		t.Fatal(err)
	}
	if r := indexed.ContextProfile.CompactionRecovery; r.CompletedWindows != 1 || r.CensoredWindows != 0 {
		t.Fatalf("indexed recovery censored by a late outside result: %+v", r)
	}
}

// A token stream the accounting rejects as implausible must not feed request
// context measurements either, in direct or indexed analysis.
func TestContextProfileSkipsRejectedTokenStream(t *testing.T) {
	root, sources := t.TempDir(), t.TempDir()
	start := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	var events []normalizedSessionEvent
	for index := 0; index <= minTokenTelemetrySnapshots; index++ {
		tokens := int64(index+1) * 1_000_000
		events = append(events, normalizedSessionEvent{
			Kind: sessionEventToken, OccurredAt: start.Add(time.Duration(index) * 2 * time.Millisecond),
			ContextTokens: 5000 + int64(index), ContextWindowTokens: 200_000,
			Tokens: normalizedTokenUsage{InputTokens: tokens, UncachedInputTokens: tokens, OutputTokens: 200_000, TotalTokens: tokens + 200_000},
		})
	}
	events = append(events, normalizedSessionEvent{Kind: sessionEventComplete, OccurredAt: start.Add(3 * time.Second)})
	for i := range events {
		events[i].Sequence = i + 1
	}
	session := normalizedSession{Provider: "codex", CWD: root, SourcePath: filepath.Join(sources, "x.jsonl"), Events: events}
	until := start.Add(time.Minute)
	direct, err := sessionRecordFromNormalized(session, root, start.Add(-time.Minute), until, ownershipCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	if !direct.TokenTelemetryExcluded {
		t.Fatal("fixture stream was not rejected")
	}
	want := analyzeContextProfiles([]codexSessionRecord{direct})
	if want.ObservedInputTokens.Count != 0 || want.ReportedWindowTokens.Count != 0 {
		t.Fatalf("rejected stream reached the context profile: %+v", want.ObservedInputTokens)
	}
	if got := indexAndAnalyzeForTest(t, session, root, start.Add(-time.Minute), until).ContextProfile; !reflect.DeepEqual(got, want) {
		t.Fatalf("indexed=%+v direct=%+v", got.ObservedInputTokens, want.ObservedInputTokens)
	}
}

// A read in the middle of a bundled command, including one past the bounded
// Shape label, still makes the call a read/search attempt, counted once.
func TestRecoveryCountsReadInsideBundledCommand(t *testing.T) {
	for _, command := range []string{
		"git status --short && cat main.go && git diff --stat",
		"git status; go test ./...; git diff; go build ./...; printf ready; cat README.md; git diff",
	} {
		assertRecoveryCountsOneReadSearch(t, command)
	}
}

func assertRecoveryCountsOneReadSearch(t *testing.T, command string) {
	t.Helper()
	dir := t.TempDir()
	arguments, _ := json.Marshal(map[string]string{"cmd": command})
	path := writeCodexSessionFixture(t, dir, "bundle", []any{
		map[string]any{"timestamp": "2026-07-29T08:00:00Z", "type": "session_meta", "payload": map[string]any{"cwd": dir}},
		map[string]any{"timestamp": "2026-07-29T08:00:01Z", "type": "event_msg", "payload": map[string]any{"type": "context_compacted"}},
		map[string]any{"timestamp": "2026-07-29T08:00:02Z", "type": "response_item", "payload": map[string]any{
			"type": "function_call", "call_id": "c1", "name": "exec_command",
			"arguments": string(arguments),
		}},
		map[string]any{"timestamp": "2026-07-29T08:00:03Z", "type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "c1", "output": "ok"}},
		map[string]any{"timestamp": "2026-07-29T08:00:04Z", "type": "event_msg", "payload": map[string]any{"type": "task_complete"}},
	})
	session, err := parseCodexNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 7, 29, 7, 0, 0, 0, time.UTC)
	p := profileSessionContext(session, dir, start, start.Add(2*time.Hour))
	if len(p.Recoveries) != 1 || p.Recoveries[0].ToolCalls != 1 || p.Recoveries[0].ReadSearch != 1 {
		t.Fatalf("recoveries=%+v", p.Recoveries)
	}
}

// A poll of a yielded search after compaction starts no search, so it adds a
// tool call but no read/search attempt, in direct and indexed analysis.
func TestRecoveryIgnoresInheritedSearchOnContinuation(t *testing.T) {
	root, sources := t.TempDir(), t.TempDir()
	start := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	events := []normalizedSessionEvent{
		{Kind: sessionEventToolCall, ToolName: "exec_command", ToolRound: 1, Family: "search", FirstFamily: "search", LastFamily: "search", ReadSearch: true},
		{Kind: sessionEventToolOutput, ToolName: "exec_command", ToolRound: 1, Family: "search", OperationContinues: true},
		{Kind: sessionEventToken, ContextTokens: 100, Tokens: normalizedTokenUsage{InputTokens: 100, TotalTokens: 100}},
		{Kind: sessionEventCompaction},
		{Kind: sessionEventToken, ContextTokens: 40, Tokens: normalizedTokenUsage{InputTokens: 140, TotalTokens: 140}},
		{Kind: sessionEventToolCall, ToolName: "write_stdin", ToolRound: 2, Family: "search"},
		{Kind: sessionEventToolOutput, ToolName: "write_stdin", ToolRound: 2, Family: "search"},
		{Kind: sessionEventComplete},
	}
	for i := range events {
		events[i].Sequence = i + 1
		events[i].OccurredAt = start.Add(time.Duration(i) * time.Second)
		if events[i].Kind == sessionEventToolOutput {
			events[i].CallOccurredAt = events[i-1].OccurredAt
		}
	}
	session := normalizedSession{Provider: "codex", CWD: root, SourcePath: filepath.Join(sources, "poll.jsonl"), Events: events}
	until := start.Add(time.Minute)
	direct, err := sessionRecordFromNormalized(session, root, start.Add(-time.Minute), until, ownershipCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	want := analyzeContextProfiles([]codexSessionRecord{direct})
	r := want.CompactionRecovery
	if r.CompletedWindows != 1 || r.ToolCallsToCompletion.Max != 1 || r.ReadSearchCallsToCompletion.Max != 0 {
		t.Fatalf("continuation counted as a search: %+v", r)
	}
	if got := indexAndAnalyzeForTest(t, session, root, start.Add(-time.Minute), until).ContextProfile; !reflect.DeepEqual(got, want) {
		t.Fatalf("indexed=%+v direct=%+v", got.CompactionRecovery, r)
	}
}
