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
	state.observeOutput(broadOutput, broadOutput.OwnedOperations)
	focusedCall := normalizedSessionEvent{Kind: sessionEventToolCall, ToolRound: 2, OwnedOperations: []string{"repo/test-focus"}}
	state.observeCall(&record, focusedCall, focusedCall.OwnedOperations, catalog)
	pair := "repo/test -> repo/test-focus"
	if record.VerificationEscalations[pair] != 1 {
		t.Fatalf("verification escalations=%#v", record.VerificationEscalations)
	}

	broadCall.ToolRound = 3
	state.observeCall(&record, broadCall, broadCall.OwnedOperations, catalog)
	broadOutput.ToolRound = 3
	state.observeOutput(broadOutput, broadOutput.OwnedOperations)
	state.observeCall(&record, normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "apply_patch", ToolRound: 4}, nil, catalog)
	focusedCall.ToolRound = 5
	state.observeCall(&record, focusedCall, focusedCall.OwnedOperations, catalog)
	if record.VerificationEscalations[pair] != 1 {
		t.Fatalf("post-edit focused check was misclassified: %#v", record.VerificationEscalations)
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
