package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCodexToolWorkingDirectories(t *testing.T) {
	t.Run("direct exec", func(t *testing.T) {
		got := codexToolWorkingDirectories("exec_command", `{"cmd":"go test ./...","workdir":"/workspace/muninn"}`, "")
		if len(got) != 1 || got[0] != "/workspace/muninn" {
			t.Fatalf("working directories=%v", got)
		}
	})

	t.Run("nested exec", func(t *testing.T) {
		input := `await tools.exec_command({cmd:"one", workdir:"/workspace/muninn"}); await tools.exec_command({cmd:"two", workdir:'/workspace/heimdal'})`
		got := codexToolWorkingDirectories("exec", "", input)
		if len(got) != 2 || got[0] != "/workspace/muninn" || got[1] != "/workspace/heimdal" {
			t.Fatalf("working directories=%v", got)
		}
	})
}

func TestEventRepositoryScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "void")
	cases := []struct {
		name     string
		event    normalizedSessionEvent
		inside   bool
		explicit bool
	}{
		{
			name:     "outside workdir",
			event:    normalizedSessionEvent{WorkingDirectories: []string{filepath.Join(filepath.Dir(root), "muninn")}},
			explicit: true,
		},
		{
			name:     "one nested call inside",
			event:    normalizedSessionEvent{WorkingDirectories: []string{"/elsewhere", root}},
			inside:   true,
			explicit: true,
		},
		{
			name:     "relative workdir",
			event:    normalizedSessionEvent{WorkingDirectories: []string{"subdir"}},
			inside:   true,
			explicit: true,
		},
		{
			name:     "exec defaults to session cwd",
			event:    normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "exec_command"},
			inside:   true,
			explicit: true,
		},
		{
			name:  "unscoped connector preserves state",
			event: normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "web"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			inside, explicit := eventRepositoryScope(test.event, root, root)
			if inside != test.inside || explicit != test.explicit {
				t.Fatalf("scope=(%t,%t) want (%t,%t)", inside, explicit, test.inside, test.explicit)
			}
		})
	}
}

func TestSessionRecordExcludesWorkInAnotherRepository(t *testing.T) {
	root := t.TempDir()
	startedAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	outside := filepath.Join(filepath.Dir(root), "muninn")
	session := normalizedSession{
		Provider: "codex",
		CWD:      root,
		Events: []normalizedSessionEvent{
			{Sequence: 1, OccurredAt: startedAt, Kind: sessionEventToolCall, ToolName: "exec_command"},
			{Sequence: 2, OccurredAt: startedAt.Add(time.Second), CallOccurredAt: startedAt, Kind: sessionEventToolOutput, ToolName: "exec_command", OutputBytes: 10},
			{Sequence: 3, OccurredAt: startedAt.Add(2 * time.Second), Kind: sessionEventToken, Tokens: normalizedTokenUsage{TotalTokens: 100}},
			{Sequence: 4, OccurredAt: startedAt.Add(3 * time.Second), Kind: sessionEventToolCall, ToolName: "exec_command", WorkingDirectories: []string{outside}},
			{Sequence: 5, OccurredAt: startedAt.Add(4 * time.Second), CallOccurredAt: startedAt.Add(3 * time.Second), Kind: sessionEventToolOutput, ToolName: "exec_command", WorkingDirectories: []string{outside}, Failed: true, OutputBytes: 1000},
			{Sequence: 6, OccurredAt: startedAt.Add(5 * time.Second), Kind: sessionEventToken, Tokens: normalizedTokenUsage{TotalTokens: 500}},
			{Sequence: 7, OccurredAt: startedAt.Add(6 * time.Second), Kind: sessionEventComplete},
			{Sequence: 8, OccurredAt: startedAt.Add(7 * time.Second), Kind: sessionEventToolCall, ToolName: "exec_command"},
			{Sequence: 9, OccurredAt: startedAt.Add(8 * time.Second), CallOccurredAt: startedAt.Add(7 * time.Second), Kind: sessionEventToolOutput, ToolName: "exec_command", OutputBytes: 20},
			{Sequence: 10, OccurredAt: startedAt.Add(9 * time.Second), Kind: sessionEventToken, Tokens: normalizedTokenUsage{TotalTokens: 600}},
			{Sequence: 11, OccurredAt: startedAt.Add(10 * time.Second), Kind: sessionEventComplete},
		},
	}

	record, err := sessionRecordFromNormalized(
		session,
		root,
		startedAt.Add(-time.Minute),
		startedAt.Add(time.Hour),
		ownershipCatalog{},
	)
	if err != nil {
		t.Fatalf("sessionRecordFromNormalized: %v", err)
	}
	if record.ToolCalls != 2 || record.FailedToolCalls != 0 || record.ToolOutputBytes != 30 {
		t.Fatalf("tool metrics calls=%d failures=%d bytes=%d", record.ToolCalls, record.FailedToolCalls, record.ToolOutputBytes)
	}
	if record.Tokens.TotalTokens != 200 {
		t.Fatalf("total tokens=%d want 200", record.Tokens.TotalTokens)
	}
	if !record.Completed {
		t.Fatal("final in-repository completion was not retained")
	}
}

func TestSessionRecordIncludesWorkEnteredFromAnotherRepository(t *testing.T) {
	root := filepath.Join(t.TempDir(), "muninn")
	outside := filepath.Join(filepath.Dir(root), "bwb")
	startedAt := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	session := normalizedSession{
		Provider: "codex",
		CWD:      outside,
		Events: []normalizedSessionEvent{
			{
				Sequence: 1, OccurredAt: startedAt, Kind: sessionEventToolCall,
				ToolName: "exec_command", WorkingDirectories: []string{root},
			},
			{
				Sequence: 2, OccurredAt: startedAt.Add(time.Second),
				CallOccurredAt: startedAt, Kind: sessionEventToolOutput,
				ToolName: "exec_command", WorkingDirectories: []string{root},
				OutputBytes: 40,
			},
			{
				Sequence: 3, OccurredAt: startedAt.Add(2 * time.Second),
				Kind: sessionEventToken, Tokens: normalizedTokenUsage{TotalTokens: 100},
			},
			{Sequence: 4, OccurredAt: startedAt.Add(3 * time.Second), Kind: sessionEventComplete},
		},
	}

	record, err := sessionRecordFromNormalized(
		session,
		root,
		startedAt.Add(-time.Minute),
		startedAt.Add(time.Hour),
		ownershipCatalog{},
	)
	if err != nil {
		t.Fatalf("sessionRecordFromNormalized: %v", err)
	}
	if record.CWD != root || record.Task != "(root)" {
		t.Fatalf("cross-origin repository identity=(%q, %q)", record.CWD, record.Task)
	}
	if record.ToolCalls != 1 || record.ToolOutputBytes != 40 ||
		record.Tokens.TotalTokens != 100 || !record.Completed {
		t.Fatalf("cross-origin metrics=%#v", record)
	}
}

// Direct analysis must resolve targets and context scope exactly as the index
// does: relative targets against the call's working directory, and a result
// that reports another directory must not move the active repository.
func TestDirectAnalysisMatchesIndexForWorkdirTargetsAndLateResults(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "file.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions := t.TempDir()
	start := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	events := []normalizedSessionEvent{
		{Kind: sessionEventToolCall, ToolName: "exec_command", ToolRound: 1, WorkingDirectories: []string{filepath.Join(root, "pkg")}, TargetCandidates: []string{"file.go"}},
		{Kind: sessionEventToolOutput, ToolName: "exec_command", ToolRound: 1, WorkingDirectories: []string{parent}},
		{Kind: sessionEventToken, ContextTokens: 100, Tokens: normalizedTokenUsage{InputTokens: 100, TotalTokens: 100}},
		{Kind: sessionEventToken, ContextTokens: 160, Tokens: normalizedTokenUsage{InputTokens: 260, TotalTokens: 260}},
	}
	for i := range events {
		events[i].Sequence = i + 1
		events[i].OccurredAt = start.Add(time.Duration(i) * time.Second)
	}
	session := normalizedSession{Provider: "codex", SourcePath: filepath.Join(sessions, "s.jsonl"), CWD: parent, Events: events}
	direct, err := sessionRecordFromNormalized(session, root, start, start.Add(time.Minute), ownershipCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	indexedSession := session
	indexedSession.Events = append([]normalizedSessionEvent(nil), events...)
	markRepositoryEventScope(&indexedSession, root)
	for index := range indexedSession.Events {
		cwd := eventRepositoryWorkingDirectory(indexedSession.Events[index], indexedSession.CWD, root)
		indexedSession.Events[index].Targets = normalizeRepositoryTargets(indexedSession.Events[index].TargetCandidates, cwd, root)
	}
	store, err := openSessionStore(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.replaceSession(t.Context(), repositoryStoreScopeKey(root, ownershipCatalog{}), indexedSession, 1, 1, true); err != nil {
		t.Fatal(err)
	}
	indexed, err := store.analyze(t.Context(), "codex", []string{sessions}, root, start, start.Add(time.Minute), "", ownershipCatalog{}, sessionRefreshStats{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := indexed.Summary.ReadTargets["pkg/file.go"]; !ok {
		t.Fatalf("index lost the workdir-relative target: %v", indexed.Summary.ReadTargets)
	}
	if _, ok := direct.ReadTargets["pkg/file.go"]; !ok || len(direct.ReadTargets) != len(indexed.Summary.ReadTargets) {
		t.Fatalf("read targets differ: direct=%v indexed=%v", direct.ReadTargets, indexed.Summary.ReadTargets)
	}
	want := analyzeContextProfiles([]codexSessionRecord{direct})
	if want.ObservedInputTokens.Count != 2 || !reflect.DeepEqual(indexed.ContextProfile, want) {
		t.Fatalf("context profiles differ: direct=%+v indexed=%+v", want, indexed.ContextProfile)
	}
}

func indexAndAnalyzeForTest(t *testing.T, session normalizedSession, root string, since, until time.Time) codexSessionInsightsReport {
	t.Helper()
	indexed := session
	indexed.Events = append([]normalizedSessionEvent(nil), session.Events...)
	markRepositoryEventScope(&indexed, root)
	store, err := openSessionStore(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.replaceSession(t.Context(), repositoryStoreScopeKey(root, ownershipCatalog{}), indexed, 1, 1, true); err != nil {
		t.Fatal(err)
	}
	report, err := store.analyze(t.Context(), session.Provider, []string{filepath.Dir(session.SourcePath)}, root, since, until, "", ownershipCatalog{}, sessionRefreshStats{})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// A continuation from another repository just before the window is no
// foreground exit, so the index must keep the pre-compaction sample too.
func TestHistoricalOutsideContinuationMatchesDirectAnalysis(t *testing.T) {
	root, sources := t.TempDir(), t.TempDir()
	start := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	events := []normalizedSessionEvent{
		{Kind: sessionEventToken, ContextTokens: 100, Tokens: normalizedTokenUsage{InputTokens: 100, TotalTokens: 100}},
		{Kind: sessionEventToolCall, ToolName: "wait", Family: "tests", ToolRound: 1, WorkingDirectories: []string{filepath.Dir(root)}},
		{Kind: sessionEventCompaction},
		{Kind: sessionEventToken, ContextTokens: 40, Tokens: normalizedTokenUsage{InputTokens: 140, TotalTokens: 140}},
		{Kind: sessionEventComplete},
	}
	for i := range events {
		events[i].Sequence = i + 1
		events[i].OccurredAt = start.Add(time.Duration(i) * time.Second)
	}
	session := normalizedSession{Provider: "codex", CWD: root, SourcePath: filepath.Join(sources, "c.jsonl"), Events: events}
	since, until := start.Add(2*time.Second), start.Add(time.Minute)
	direct, err := sessionRecordFromNormalized(session, root, since, until, ownershipCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	want := analyzeContextProfiles([]codexSessionRecord{direct})
	if want.CompactionRecovery.WithBeforeSample != 1 {
		t.Fatalf("direct lost the before sample: %+v", want.CompactionRecovery)
	}
	if got := indexAndAnalyzeForTest(t, session, root, since, until).ContextProfile; !reflect.DeepEqual(got, want) {
		t.Fatalf("indexed=%+v direct=%+v", got.CompactionRecovery, want.CompactionRecovery)
	}
}

// A task completed inside the repository before an excursion elsewhere ends
// before the window, so the next task in the window is fully observed in both
// analysis paths.
func TestCompletionBeforeExcursionKeepsWindowTaskObserved(t *testing.T) {
	root, sources := t.TempDir(), t.TempDir()
	start := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	outside := []string{filepath.Dir(root)}
	events := []normalizedSessionEvent{
		{Kind: sessionEventToken, ContextTokens: 100, Tokens: normalizedTokenUsage{InputTokens: 100, TotalTokens: 100}},
		{Kind: sessionEventComplete},
		{Kind: sessionEventToolCall, ToolName: "exec_command", ToolRound: 1, WorkingDirectories: outside},
		{Kind: sessionEventToken, ContextTokens: 150, Tokens: normalizedTokenUsage{InputTokens: 250, TotalTokens: 250}},
		{Kind: sessionEventToolCall, ToolName: "exec_command", ToolRound: 2, WorkingDirectories: []string{root}},
		{Kind: sessionEventToken, ContextTokens: 160, Tokens: normalizedTokenUsage{InputTokens: 410, TotalTokens: 410}},
		{Kind: sessionEventComplete},
	}
	for i := range events {
		events[i].Sequence = i + 1
		events[i].OccurredAt = start.Add(time.Duration(i) * time.Second)
	}
	session := normalizedSession{Provider: "codex", CWD: root, SourcePath: filepath.Join(sources, "e.jsonl"), Events: events}
	since, until := start.Add(4*time.Second), start.Add(time.Minute)
	direct, err := sessionRecordFromNormalized(session, root, since, until, ownershipCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	if len(direct.TaskEpisodes) == 0 || direct.TaskEpisodes[0].LeftCensored {
		t.Fatalf("direct episodes=%+v", direct.TaskEpisodes)
	}
	indexed := indexAndAnalyzeForTest(t, session, root, since, until)
	if indexed.Outcomes.LeftCensoredCompleted != 0 {
		t.Fatalf("indexed marked the window task left-censored: %+v", indexed.Outcomes)
	}
}
