package cli

import (
	"testing"
	"time"
)

func TestConfiguredExpectedOwnedOperationFailureRemainsQueryableWithoutFriction(t *testing.T) {
	generatedAt := time.Date(2026, 7, 25, 3, 0, 0, 0, time.UTC)
	ownership := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "bwb",
		Executables: []string{"bwb"},
		Operations: []ownedOperationConfig{{
			ID:                     "comments-wait",
			Args:                   []string{"task", "*", "comments", "**", "--wait"},
			ExpectedFailureReasons: []string{"timeout"},
		}},
	}})
	session := normalizedSession{
		Provider: "codex",
		CWD:      t.TempDir(),
		Events: []normalizedSessionEvent{{
			OccurredAt:      generatedAt,
			CallOccurredAt:  generatedAt.Add(-time.Minute),
			Kind:            sessionEventToolOutput,
			ToolName:        "exec_command",
			Failed:          true,
			FailureReason:   "timeout",
			OwnedOperations: []string{"bwb/comments-wait"},
		}},
	}
	record, err := sessionRecordFromNormalized(session, session.CWD, generatedAt.Add(-time.Hour), generatedAt, ownership)
	if err != nil {
		t.Fatalf("normalize configured expected failure: %v", err)
	}
	if got := record.OwnedOperationFailureReasons["bwb/comments-wait"]["timeout"]; got != 1 {
		t.Fatalf("configured expected failure was not retained: %#v", record.OwnedOperationFailureReasons)
	}
	if got := record.Activity[sessionActivityKey("owned-operation-friction", "bwb/comments-wait")]; !got.IsZero() {
		t.Fatalf("configured expected failure refreshed friction activity: %s", got)
	}
	reasons := map[string]codexOccurrenceMetrics{
		"timeout":                   {Count: 3, Sessions: 1},
		"transient service failure": {Count: 1, Sessions: 1},
	}
	actionable, expected := ownedOperationFailureCounts(ownership, "bwb/comments-wait", reasons)
	if actionable != 1 || expected != 3 {
		t.Fatalf("failure split=(%d,%d) want (1,3)", actionable, expected)
	}
}

func TestExpectedWaitTreatsInterruptedProcessAsExpectedFailure(t *testing.T) {
	ownership := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "bwb",
		Executables: []string{"bwb"},
		Operations: []ownedOperationConfig{
			{
				ID:           "publish",
				Args:         []string{"publish", "--wait"},
				ExpectedWait: true,
			},
			{
				ID:   "status",
				Args: []string{"status"},
			},
		},
	}})
	if !ownership.operationFailureExpected("bwb/publish", "interrupted process") {
		t.Fatal("expected wait interruption was actionable")
	}
	if ownership.operationFailureExpected("bwb/status", "interrupted process") {
		t.Fatal("non-wait interruption was expected")
	}
}

func TestOwnedOperationFailuresAreDefiniteOnlyForOneMatchedOperation(t *testing.T) {
	workspaceRoot := t.TempDir()
	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	session := normalizedSession{
		Provider: "codex",
		CWD:      workspaceRoot,
		Events: []normalizedSessionEvent{
			{
				OccurredAt:                    generatedAt.Add(-time.Minute),
				Kind:                          sessionEventToolCall,
				ToolName:                      "exec_command",
				OwnedOperations:               []string{"bwb/git", "bwb/test"},
				OperationAttributionAmbiguous: true,
			},
			{
				OccurredAt:                    generatedAt,
				CallOccurredAt:                generatedAt.Add(-time.Minute),
				Kind:                          sessionEventToolOutput,
				ToolName:                      "exec_command",
				Failed:                        true,
				FailureReason:                 "test failure",
				OutputBytes:                   100,
				OwnedOperations:               []string{"bwb/git", "bwb/test"},
				OperationAttributionAmbiguous: true,
			},
		},
	}
	record, err := sessionRecordFromNormalized(session, workspaceRoot, generatedAt.Add(-time.Hour), generatedAt, ownershipCatalog{})
	if err != nil {
		t.Fatalf("normalize session: %v", err)
	}
	for _, operation := range []string{"bwb/git", "bwb/test"} {
		if got := record.OwnedOperations[operation].FailedCalls; got != 0 {
			t.Fatalf("%s received ambiguous failure as definite: %d", operation, got)
		}
		if got := record.OwnedOperationAmbiguous[operation]; got.FailedCalls != 1 || got.OutputBytes != 100 {
			t.Fatalf("%s ambiguous metrics=%#v want one failure and 100 bytes", operation, got)
		}
	}
}

func TestOwnedOperationFailureReasonsSeparateExpectedFailures(t *testing.T) {
	workspaceRoot := t.TempDir()
	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	session := normalizedSession{
		Provider: "codex",
		CWD:      workspaceRoot,
		Events: []normalizedSessionEvent{
			{
				OccurredAt:      generatedAt.Add(-time.Minute),
				Kind:            sessionEventToolCall,
				ToolName:        "exec_command",
				OwnedOperations: []string{"bwb/test"},
			},
			{
				OccurredAt:      generatedAt,
				CallOccurredAt:  generatedAt.Add(-time.Minute),
				Kind:            sessionEventToolOutput,
				ToolName:        "exec_command",
				Failed:          true,
				FailureReason:   "test failure",
				OwnedOperations: []string{"bwb/test"},
			},
		},
	}
	record, err := sessionRecordFromNormalized(session, workspaceRoot, generatedAt.Add(-time.Hour), generatedAt, ownershipCatalog{})
	if err != nil {
		t.Fatalf("normalize session: %v", err)
	}
	if got := record.OwnedOperations["bwb/test"].FailedCalls; got != 1 {
		t.Fatalf("definite failures=%d want 1", got)
	}
	if got := record.OwnedOperationFailureReasons["bwb/test"]["test failure"]; got != 1 {
		t.Fatalf("test failure reasons=%d want 1", got)
	}
	if got := record.Activity[sessionActivityKey("owned-operation-friction", "bwb/test")]; !got.IsZero() {
		t.Fatalf("expected product failure refreshed friction activity: %s", got)
	}
	report := newSessionInsightsReport("codex", nil, workspaceRoot, generatedAt.Add(-time.Hour), generatedAt)
	addCodexSessionToReport(&report, map[string]*codexTaskInsights{}, record)
	actionable, expected := ownedOperationFailureCounts(ownershipCatalog{}, "bwb/test", report.Summary.OwnedOperationFailureReasons["bwb/test"])
	if actionable != 0 || expected != 1 {
		t.Fatalf("failure split=(%d,%d) want (0,1)", actionable, expected)
	}
}

func TestOwnedOperationFrictionActivityIgnoresLaterSuccessfulCalls(t *testing.T) {
	workspaceRoot := t.TempDir()
	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	frictionAt := generatedAt.Add(-10 * time.Minute)
	session := normalizedSession{
		Provider: "codex",
		CWD:      workspaceRoot,
		Events: []normalizedSessionEvent{
			{
				OccurredAt:      frictionAt.Add(-time.Second),
				Kind:            sessionEventToolCall,
				ToolName:        "exec_command",
				OwnedOperations: []string{"bwb/test"},
			},
			{
				OccurredAt:      frictionAt,
				CallOccurredAt:  frictionAt.Add(-time.Second),
				Kind:            sessionEventToolOutput,
				ToolName:        "exec_command",
				Failed:          true,
				FailureReason:   "test harness protocol",
				OwnedOperations: []string{"bwb/test"},
			},
			{
				OccurredAt:      generatedAt.Add(-time.Second),
				Kind:            sessionEventToolCall,
				ToolName:        "exec_command",
				OwnedOperations: []string{"bwb/test"},
			},
			{
				OccurredAt:      generatedAt,
				CallOccurredAt:  generatedAt.Add(-time.Second),
				Kind:            sessionEventToolOutput,
				ToolName:        "exec_command",
				OwnedOperations: []string{"bwb/test"},
			},
		},
	}
	record, err := sessionRecordFromNormalized(session, workspaceRoot, generatedAt.Add(-time.Hour), generatedAt, ownershipCatalog{})
	if err != nil {
		t.Fatalf("normalize session: %v", err)
	}
	if got := record.Activity[sessionActivityKey("owned-operation", "bwb/test")]; !got.Equal(generatedAt) {
		t.Fatalf("operation activity=%s want %s", got, generatedAt)
	}
	if got := record.Activity[sessionActivityKey("owned-operation-friction", "bwb/test")]; !got.Equal(frictionAt) {
		t.Fatalf("friction activity=%s want %s", got, frictionAt)
	}
}
