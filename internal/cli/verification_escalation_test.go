package cli

import (
	"strings"
	"testing"
	"time"
)

func TestVerificationEscalationRequiresFailedBroadCheckThenFocusedCheckBeforeEdit(t *testing.T) {
	record := newCodexSessionRecord()
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "repo",
		Executables: []string{"repo"},
		Operations: []ownedOperationConfig{
			{ID: "test", Args: []string{"test"}, Kind: "verification-broad"},
			{ID: "test-focus", Args: []string{"test", "--focus"}, Kind: "verification-focused"},
		},
	}})
	state := verificationEscalationState{}
	broadCall := normalizedSessionEvent{Kind: sessionEventToolCall, ToolRound: 1, OwnedOperations: []string{"repo/test"}}
	state.observeCall(&record, broadCall, broadCall.OwnedOperations, catalog)
	broadOutput := normalizedSessionEvent{Kind: sessionEventToolOutput, ToolRound: 1, Failed: true, OwnedOperations: []string{"repo/test"}}
	state.observeOutput(&record, broadOutput, broadOutput.OwnedOperations)
	focusedCall := normalizedSessionEvent{Kind: sessionEventToolCall, ToolRound: 2, OwnedOperations: []string{"repo/test-focus"}}
	state.observeCall(&record, focusedCall, focusedCall.OwnedOperations, catalog)
	focusedOutput := normalizedSessionEvent{Kind: sessionEventToolOutput, ToolRound: 2, OwnedOperations: []string{"repo/test-focus"}}
	state.observeOutput(&record, focusedOutput, focusedOutput.OwnedOperations)
	pair := "repo/test -> repo/test-focus"
	if record.VerificationEscalations[pair] != 1 {
		t.Fatalf("verification escalations=%#v", record.VerificationEscalations)
	}

	broadCall.ToolRound = 3
	state.observeCall(&record, broadCall, broadCall.OwnedOperations, catalog)
	broadOutput.ToolRound = 3
	state.observeOutput(&record, broadOutput, broadOutput.OwnedOperations)
	state.observeCall(&record, normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "apply_patch", ToolRound: 4}, nil, catalog)
	focusedCall.ToolRound = 5
	state.observeCall(&record, focusedCall, focusedCall.OwnedOperations, catalog)
	if record.VerificationEscalations[pair] != 1 {
		t.Fatalf("post-edit focused check was misclassified: %#v", record.VerificationEscalations)
	}
}

func TestRefusedFocusedCheckIsNotAnEscalation(t *testing.T) {
	root := t.TempDir()
	start := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "repo",
		Executables: []string{"repo"},
		Operations: []ownedOperationConfig{
			{ID: "test", Args: []string{"test"}, Kind: "verification-broad"},
			{ID: "test-focus", Args: []string{"test", "--focus"}, Kind: "verification-focused"},
		},
	}})
	sequence := 0
	var events []normalizedSessionEvent
	run := func(round int, operation string, failed bool, refusal string) {
		sequence += 2
		events = append(events,
			normalizedSessionEvent{
				Sequence: sequence - 1, OccurredAt: start.Add(time.Duration(sequence-1) * time.Second),
				Kind: sessionEventToolCall, ToolName: "exec_command", ToolRound: round,
				OwnedOperations: []string{operation},
			},
			normalizedSessionEvent{
				Sequence: sequence, OccurredAt: start.Add(time.Duration(sequence) * time.Second),
				CallOccurredAt: start.Add(time.Duration(sequence-1) * time.Second),
				Kind:           sessionEventToolOutput, ToolName: "exec_command", ToolRound: round,
				OwnedOperations: []string{operation}, Failed: failed, Refusal: refusal,
			})
	}
	analyze := func() map[string]int {
		t.Helper()
		record, err := sessionRecordFromNormalized(
			normalizedSession{Provider: "codex", CWD: root, Events: events},
			root, start.Add(-time.Hour), start.Add(time.Hour), catalog,
		)
		if err != nil {
			t.Fatal(err)
		}
		return record.VerificationEscalations
	}
	pair := "repo/test -> repo/test-focus"

	run(1, "repo/test", true, "")
	run(2, "repo/test-focus", false, toolRefusalBlockedByHook)
	if got := analyze(); got[pair] != 0 {
		t.Fatalf("refused focused check counted as escalation: %#v", got)
	}
	// The failed broad check stays pending, so the focused check that runs counts.
	run(3, "repo/test-focus", false, "")
	if got := analyze(); got[pair] != 1 {
		t.Fatalf("escalations=%#v", got)
	}
}

func TestBuildVerificationEscalationFindingsRequiresRecurrence(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), time.Time{}, time.Now().UTC())
	pair := "repo/test -> repo/test-focus"
	report.Summary.VerificationEscalations[pair] = codexTransitionMetrics{Count: 4, Sessions: 2}
	findings := buildVerificationEscalationFindings(report)
	if len(findings) != 1 || findings[0].Target != pair ||
		!strings.Contains(findings[0].Action, "reserve repo/test") {
		t.Fatalf("verification escalation findings=%#v", findings)
	}
}
