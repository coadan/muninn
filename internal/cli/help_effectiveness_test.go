package cli

import (
	"strings"
	"testing"
	"time"
)

func TestSessionRecordMeasuresWhetherHelpUnblocksOwnedOperation(t *testing.T) {
	root := t.TempDir()
	start := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "repo",
		Executables: []string{"repo"},
		Operations: []ownedOperationConfig{
			{ID: "help", Args: []string{"help"}, Kind: "help"},
			{ID: "test", Args: []string{"test"}},
		},
	}})
	call := func(sequence, round int, operation string) normalizedSessionEvent {
		return normalizedSessionEvent{
			Sequence: sequence, OccurredAt: start.Add(time.Duration(sequence) * time.Second),
			Kind: sessionEventToolCall, ToolName: "exec_command", ToolRound: round,
			OwnedOperations: []string{operation},
		}
	}
	output := func(sequence, round int, operation string, failed bool) normalizedSessionEvent {
		return normalizedSessionEvent{
			Sequence: sequence, OccurredAt: start.Add(time.Duration(sequence) * time.Second),
			CallOccurredAt: start.Add(time.Duration(sequence-1) * time.Second),
			Kind:           sessionEventToolOutput, ToolName: "exec_command", ToolRound: round,
			OwnedOperations: []string{operation}, Failed: failed,
		}
	}
	events := []normalizedSessionEvent{
		call(1, 1, "repo/help"), output(2, 1, "repo/help", false),
		call(3, 2, "repo/test"), output(4, 2, "repo/test", false),
		call(5, 3, "repo/help"), output(6, 3, "repo/help", false),
		call(7, 4, "repo/help"), output(8, 4, "repo/help", false),
		call(9, 5, "repo/test"), output(10, 5, "repo/test", true),
	}
	record, err := sessionRecordFromNormalized(
		normalizedSession{Provider: "codex", CWD: root, Events: events},
		root, start.Add(-time.Hour), start.Add(time.Hour), catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	metrics := record.HelpEffectiveness["repo/help"]
	if metrics.Lookups != 3 || metrics.SuccessfulUses != 1 ||
		metrics.FailedUses != 1 || metrics.RepeatedLookups != 1 ||
		metrics.IneffectiveSessions != 1 {
		t.Fatalf("help effectiveness=%#v", metrics)
	}
}

func TestBuildHelpEffectivenessFindingsRequiresMostlyIneffectiveCrossSessionHelp(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), time.Time{}, time.Now().UTC())
	report.Summary.HelpEffectiveness["repo/help"] = helpEffectivenessMetrics{
		Lookups: 8, SuccessfulUses: 2, FailedUses: 2, RepeatedLookups: 3,
		AbandonedLookups: 1, Sessions: 4, IneffectiveSessions: 3,
	}
	findings := buildHelpEffectivenessFindings(report)
	if len(findings) != 1 || findings[0].Target != "repo/help" ||
		!strings.Contains(findings[0].Title, "does not reliably unblock") {
		t.Fatalf("help findings=%#v", findings)
	}
}

func TestRefusedHelpCallsAreNotHelpOutcomes(t *testing.T) {
	root := t.TempDir()
	start := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "repo",
		Executables: []string{"repo"},
		Operations: []ownedOperationConfig{
			{ID: "help", Args: []string{"help"}, Kind: "help"},
			{ID: "test", Args: []string{"test"}},
		},
	}})
	sequence := 0
	var events []normalizedSessionEvent
	run := func(round int, operation, refusal string) {
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
				OwnedOperations: []string{operation}, Refusal: refusal,
			})
	}
	analyze := func() helpEffectivenessMetrics {
		t.Helper()
		record, err := sessionRecordFromNormalized(
			normalizedSession{Provider: "codex", CWD: root, Events: events},
			root, start.Add(-time.Hour), start.Add(time.Hour), catalog,
		)
		if err != nil {
			t.Fatal(err)
		}
		return record.HelpEffectiveness["repo/help"]
	}

	// A refused lookup alone is no lookup at all.
	run(1, "repo/help", toolRefusalBlockedByHook)
	if metrics := analyze(); metrics != (helpEffectivenessMetrics{}) {
		t.Fatalf("refused lookup counted: %#v", metrics)
	}

	// A lookup that ran, a refused second lookup and a refused use leave the
	// first lookup pending, so the use that ran counts as its success.
	events = nil
	run(1, "repo/help", "")
	run(2, "repo/help", toolRefusalDeniedByPermission)
	run(3, "repo/test", toolRefusalDeclinedByUser)
	run(4, "repo/test", "")
	if metrics := analyze(); metrics.Lookups != 1 || metrics.SuccessfulUses != 1 ||
		metrics.RepeatedLookups != 0 || metrics.AbandonedLookups != 0 || metrics.IneffectiveSessions != 0 {
		t.Fatalf("help effectiveness=%#v", metrics)
	}
}
