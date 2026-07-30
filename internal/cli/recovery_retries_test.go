package cli

import (
	"strings"
	"testing"
	"time"
)

func TestSessionRecordTracksImmediateOwnedOperationRetryOutcomes(t *testing.T) {
	root := t.TempDir()
	startedAt := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	call := func(sequence, round int, operation string) normalizedSessionEvent {
		return normalizedSessionEvent{
			Sequence:        sequence,
			OccurredAt:      startedAt.Add(time.Duration(sequence) * time.Second),
			Kind:            sessionEventToolCall,
			ToolName:        "exec_command",
			ToolRound:       round,
			OwnedOperations: []string{operation},
		}
	}
	output := func(sequence, round int, operation string, failed bool) normalizedSessionEvent {
		return normalizedSessionEvent{
			Sequence:        sequence,
			OccurredAt:      startedAt.Add(time.Duration(sequence) * time.Second),
			CallOccurredAt:  startedAt.Add(time.Duration(sequence-1) * time.Second),
			Kind:            sessionEventToolOutput,
			ToolName:        "exec_command",
			ToolRound:       round,
			OwnedOperations: []string{operation},
			Failed:          failed,
			FailureReason:   genericOwnedOperationFailureReason,
		}
	}
	events := []normalizedSessionEvent{
		call(1, 1, "void-cli/context"),
		output(2, 1, "void-cli/context", true),
		call(3, 2, "void-cli/context"),
		output(4, 2, "void-cli/context", true),
		call(5, 3, "void-cli/context"),
		output(6, 3, "void-cli/context", false),
		call(7, 4, "void-cli/context"),
		output(8, 4, "void-cli/context", true),
		call(9, 5, "bwb/inspect"),
		output(10, 5, "bwb/inspect", false),
		call(11, 6, "void-cli/context"),
		output(12, 6, "void-cli/context", false),
	}
	record, err := sessionRecordFromNormalized(
		normalizedSession{Provider: "codex", CWD: root, Events: events},
		root,
		startedAt.Add(-time.Hour),
		startedAt.Add(time.Hour),
		ownershipCatalog{},
	)
	if err != nil {
		t.Fatalf("record owned operation retries: %v", err)
	}
	metrics := record.OwnedOperationRetries["void-cli/context"]
	if metrics.Attempts != 2 || metrics.RepeatedFailures != 1 || metrics.SuccessfulRetries != 1 {
		t.Fatalf("retry metrics=%#v", metrics)
	}
}

func TestBuildOwnedOperationRetryFindingsRequiresRepeatedFailuresAcrossSessions(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), time.Time{}, time.Now().UTC())
	report.Summary.OwnedOperationRetries["void-cli/context"] = ownedOperationRetryMetrics{
		Attempts:                5,
		RepeatedFailures:        4,
		SuccessfulRetries:       1,
		Sessions:                3,
		RepeatedFailureSessions: 2,
	}
	report.Summary.Activity[sessionActivityKey("owned-operation-retry", "void-cli/context")] =
		time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)

	findings := buildOwnedOperationRetryFindings(report)
	if len(findings) != 1 ||
		findings[0].Target != "void-cli/context" ||
		!strings.Contains(findings[0].Evidence, "4 failed again") ||
		!strings.Contains(findings[0].Action, "without a state change") {
		t.Fatalf("retry findings=%#v", findings)
	}

	metrics := report.Summary.OwnedOperationRetries["void-cli/context"]
	metrics.RepeatedFailureSessions = 1
	report.Summary.OwnedOperationRetries["void-cli/context"] = metrics
	if findings := buildOwnedOperationRetryFindings(report); len(findings) != 0 {
		t.Fatalf("single-session retries produced findings: %#v", findings)
	}
}

func TestAddOwnedOperationRetryMetricsCountsAffectedSessionsSeparately(t *testing.T) {
	aggregate := map[string]ownedOperationRetryMetrics{}
	addOwnedOperationRetryMetrics(aggregate, map[string]ownedOperationRetryMetrics{
		"void-cli/context": {
			Attempts:          2,
			RepeatedFailures:  1,
			SuccessfulRetries: 1,
		},
	})
	addOwnedOperationRetryMetrics(aggregate, map[string]ownedOperationRetryMetrics{
		"void-cli/context": {
			Attempts:          3,
			SuccessfulRetries: 3,
		},
	})
	metrics := aggregate["void-cli/context"]
	if metrics.Attempts != 5 ||
		metrics.RepeatedFailures != 1 ||
		metrics.SuccessfulRetries != 4 ||
		metrics.Sessions != 2 ||
		metrics.RepeatedFailureSessions != 1 {
		t.Fatalf("aggregate retry metrics=%#v", metrics)
	}
}
