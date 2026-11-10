package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestToolRefusalsAreRecordedApartFromFailures(t *testing.T) {
	at := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	session := normalizedSession{
		Provider: "test", CWD: "/workspace/app",
		Events: []normalizedSessionEvent{
			{Sequence: 1, OccurredAt: at, Kind: sessionEventToolCall, ToolName: "exec_command", ToolRound: 1},
			{Sequence: 2, OccurredAt: at, Kind: sessionEventToolOutput, ToolName: "exec_command", ToolRound: 1, CallOccurredAt: at, Refusal: toolRefusalBlockedByHook},
			{Sequence: 3, OccurredAt: at, Kind: sessionEventToolCall, ToolName: "exec_command", ToolRound: 2},
			// A label outside the fixed vocabulary is never recorded.
			{Sequence: 4, OccurredAt: at, Kind: sessionEventToolOutput, ToolName: "exec_command", ToolRound: 2, CallOccurredAt: at, Refusal: "raw provider text"},
		},
	}
	record, err := sessionRecordFromNormalized(session, "/workspace/app", at.Add(-time.Hour), at.Add(time.Hour), ownershipCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	if record.FailedToolCalls != 0 {
		t.Fatalf("a refusal must not be a tool failure: %d", record.FailedToolCalls)
	}
	if len(record.ToolRefusals) != 1 || record.ToolRefusals[toolRefusalBlockedByHook]["exec_command"] != 1 {
		t.Fatalf("refusals=%#v", record.ToolRefusals)
	}
}

func TestToolRefusalFindingRequiresRecurrenceAcrossSessions(t *testing.T) {
	report := codexSessionInsightsReport{}
	report.Summary.ToolRefusals = map[string]map[string]codexOccurrenceMetrics{
		toolRefusalBlockedByHook:      {"exec_command": {Count: 3, Sessions: 2}},
		toolRefusalDeniedByPermission: {"exec_command": {Count: 5, Sessions: 1}, "WebFetch": {Count: 2, Sessions: 2}},
		toolRefusalDeclinedByUser:     {"apply_patch": {Count: 4, Sessions: 3}},
	}
	findings := buildToolRefusalFindings(report)
	got := map[string]sessionFinding{}
	for _, finding := range findings {
		got[finding.Target] = finding
	}
	if len(findings) != 2 {
		t.Fatalf("findings=%#v", findings)
	}
	hook := got["blocked-by-hook/exec_command"]
	if hook.Category != "tool-refusal" || hook.Lever != "tooling" || hook.Confidence != "medium" ||
		!strings.Contains(hook.Evidence, "3 refused calls across 2 sessions") {
		t.Fatalf("hook finding=%#v", hook)
	}
	if declined := got["declined-by-user/apply_patch"]; declined.Confidence != "low" || declined.Lever != "instructions/docs" {
		t.Fatalf("declined finding=%#v", declined)
	}
	signals := assignAndSuppressSessionFindingSignals(findings, nil)
	if signals[0].Signal == signals[1].Signal || !strings.HasPrefix(signals[0].Signal, "tool-refusal/") {
		t.Fatalf("signals=%q %q", signals[0].Signal, signals[1].Signal)
	}
	if filtered, err := filterSessionFindings(findings, "interface"); err != nil || len(filtered) != 2 {
		t.Fatalf("interface focus=%#v err=%v", filtered, err)
	}
}

func TestRefusedOutputsAreNotOutcomes(t *testing.T) {
	at := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	tick := func(second int) time.Time { return at.Add(time.Duration(second) * time.Second) }
	session := normalizedSession{Provider: "test", CWD: "/workspace/app"}
	add := func(second int, event normalizedSessionEvent) {
		event.Sequence = len(session.Events) + 1
		event.OccurredAt = tick(second)
		if event.Kind == sessionEventToolOutput {
			event.CallOccurredAt = tick(second - 1)
		}
		session.Events = append(session.Events, event)
	}
	add(1, normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "apply_patch", ToolRound: 1, TargetCandidates: []string{"/workspace/app/main.go"}})
	add(2, normalizedSessionEvent{Kind: sessionEventToolOutput, ToolName: "apply_patch", ToolRound: 1})
	for round, family := range []string{"tests", "delivery"} {
		add(3+2*round, normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "exec_command", ToolRound: 2 + round, Family: family})
		add(4+2*round, normalizedSessionEvent{Kind: sessionEventToolOutput, ToolName: "exec_command", ToolRound: 2 + round, Family: family, Refusal: toolRefusalBlockedByHook})
	}
	record, err := sessionRecordFromNormalized(session, "/workspace/app", at.Add(-time.Hour), at.Add(time.Hour), ownershipCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	if record.ToolRefusals[toolRefusalBlockedByHook]["exec_command"] != 2 {
		t.Fatalf("refusals=%#v", record.ToolRefusals)
	}
	if record.DeliveryRework.Deliveries != 0 || record.DownstreamQuality.Deliveries != 0 {
		t.Fatalf("a hook-blocked push counted as a delivery: %#v %#v", record.DeliveryRework, record.DownstreamQuality)
	}
	if record.FailedToolCalls != 0 || record.ToolMetricsByName["exec_command"].Calls != 2 {
		t.Fatalf("failed=%d tools=%#v", record.FailedToolCalls, record.ToolMetricsByName)
	}
	// Control: the same push that actually ran is a delivery.
	session.Events[len(session.Events)-1].Refusal = ""
	record, err = sessionRecordFromNormalized(session, "/workspace/app", at.Add(-time.Hour), at.Add(time.Hour), ownershipCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	if record.DeliveryRework.Deliveries != 1 {
		t.Fatalf("control push was not a delivery: %#v", record.DeliveryRework)
	}
}

func TestRefusedRetryLeavesTheFailurePendingForTheRealRetry(t *testing.T) {
	root := t.TempDir()
	start := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "repo",
		Executables: []string{"repo"},
		Operations:  []ownedOperationConfig{{ID: "test", Args: []string{"test"}}},
	}})
	sequence := 0
	var events []normalizedSessionEvent
	run := func(round int, failed bool, refusal string) {
		sequence += 2
		events = append(events,
			normalizedSessionEvent{
				Sequence: sequence - 1, OccurredAt: start.Add(time.Duration(sequence-1) * time.Second),
				Kind: sessionEventToolCall, ToolName: "exec_command", ToolRound: round,
				OwnedOperations: []string{"repo/test"},
			},
			normalizedSessionEvent{
				Sequence: sequence, OccurredAt: start.Add(time.Duration(sequence) * time.Second),
				CallOccurredAt: start.Add(time.Duration(sequence-1) * time.Second),
				Kind:           sessionEventToolOutput, ToolName: "exec_command", ToolRound: round,
				OwnedOperations: []string{"repo/test"}, Failed: failed, Refusal: refusal,
			})
	}
	run(1, true, "")
	run(2, false, toolRefusalBlockedByHook)
	run(3, false, "")
	record, err := sessionRecordFromNormalized(
		normalizedSession{Provider: "codex", CWD: root, Events: events},
		root, start.Add(-time.Hour), start.Add(time.Hour), catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := record.OwnedOperationRetries["repo/test"]; got.Attempts != 1 || got.SuccessfulRetries != 1 || got.RepeatedFailures != 0 {
		t.Fatalf("retries=%#v", got)
	}
}

// A refused edit never changed a file, so it must not make a later push a
// delivery of edited work.
func TestRefusedEditIsNotAPendingEdit(t *testing.T) {
	at := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	tick := func(second int) time.Time { return at.Add(time.Duration(second) * time.Second) }
	build := func(refusal string) normalizedSession {
		session := normalizedSession{Provider: "test", CWD: "/workspace/app"}
		add := func(second int, event normalizedSessionEvent) {
			event.Sequence = len(session.Events) + 1
			event.OccurredAt = tick(second)
			if event.Kind == sessionEventToolOutput {
				event.CallOccurredAt = tick(second - 1)
			}
			session.Events = append(session.Events, event)
		}
		add(1, normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "apply_patch", ToolRound: 1, TargetCandidates: []string{"/workspace/app/main.go"}})
		add(2, normalizedSessionEvent{Kind: sessionEventToolOutput, ToolName: "apply_patch", ToolRound: 1, Refusal: refusal})
		add(3, normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "exec_command", ToolRound: 2, Family: "delivery"})
		add(4, normalizedSessionEvent{Kind: sessionEventToolOutput, ToolName: "exec_command", ToolRound: 2, Family: "delivery"})
		return session
	}
	for _, refusal := range []string{toolRefusalDeclinedByUser, ""} {
		record, err := sessionRecordFromNormalized(build(refusal), "/workspace/app", at.Add(-time.Hour), at.Add(time.Hour), ownershipCatalog{})
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if refusal == "" {
			want = 1
		}
		if len(record.DeliveryRework.Cohorts) != want || record.DownstreamQuality.Deliveries != want {
			t.Fatalf("refusal=%q edit cohorts=%d downstream deliveries=%d want %d", refusal, len(record.DeliveryRework.Cohorts), record.DownstreamQuality.Deliveries, want)
		}
	}
}

// A refusal after the report cutoff is unseen by the index, so direct analysis
// must not use it either.
func TestRefusalAfterCutoffMatchesIndexedAnalysis(t *testing.T) {
	root, sources := t.TempDir(), t.TempDir()
	start := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	events := []normalizedSessionEvent{
		{Kind: sessionEventToolCall, ToolName: "apply_patch", ToolRound: 1, TargetCandidates: []string{filepath.Join(root, "main.go")}},
		{Kind: sessionEventToolCall, ToolName: "exec_command", ToolRound: 2, Family: "delivery"},
		{Kind: sessionEventToolOutput, ToolName: "exec_command", ToolRound: 2, Family: "delivery"},
		{Kind: sessionEventToolOutput, ToolName: "apply_patch", ToolRound: 1, Refusal: toolRefusalDeclinedByUser},
	}
	for i := range events {
		events[i].Sequence = i + 1
		events[i].OccurredAt = start.Add(time.Duration(i) * time.Second)
	}
	events[2].CallOccurredAt = events[1].OccurredAt
	events[3].CallOccurredAt = events[0].OccurredAt
	session := normalizedSession{Provider: "codex", CWD: root, SourcePath: filepath.Join(sources, "r.jsonl"), Events: events}
	since, until := start.Add(-time.Minute), start.Add(2*time.Second)
	direct, err := sessionRecordFromNormalized(session, root, since, until, ownershipCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	indexed := indexAndAnalyzeForTest(t, session, root, since, until)
	if direct.DownstreamQuality.Deliveries != 1 || indexed.Summary.DownstreamQuality.Deliveries != direct.DownstreamQuality.Deliveries {
		t.Fatalf("direct=%d indexed=%d downstream deliveries", direct.DownstreamQuality.Deliveries, indexed.Summary.DownstreamQuality.Deliveries)
	}
}
