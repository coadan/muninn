package cli

import (
	"strings"
	"testing"
	"time"
)

func TestSessionRecordTracksOnlyStrictDefiniteOwnedOperationChains(t *testing.T) {
	root := t.TempDir()
	generatedAt := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	call := func(round int, operation string) normalizedSessionEvent {
		return normalizedSessionEvent{
			OccurredAt:      generatedAt.Add(time.Duration(round) * time.Second),
			Kind:            sessionEventToolCall,
			ToolName:        "exec_command",
			ToolRound:       round,
			OwnedOperations: []string{operation},
		}
	}
	events := []normalizedSessionEvent{
		call(1, "bwb/status"),
		call(2, "bwb/inspect"),
		call(3, "bwb/test"),
		call(4, "bwb/publish"),
		call(6, "bwb/status"),
		call(7, "bwb/inspect"),
		{
			OccurredAt:                    generatedAt.Add(8 * time.Second),
			Kind:                          sessionEventToolCall,
			ToolName:                      "exec_command",
			ToolRound:                     8,
			OwnedOperations:               []string{"bwb/test", "bwb/check"},
			OperationAttributionAmbiguous: true,
		},
		call(9, "bwb/publish"),
	}
	record, err := sessionRecordFromNormalized(
		normalizedSession{Provider: "codex", CWD: root, Events: events},
		root,
		generatedAt.Add(-time.Hour),
		generatedAt.Add(time.Hour),
		ownershipCatalog{},
	)
	if err != nil {
		t.Fatalf("record operation chains: %v", err)
	}
	want := map[string]int{
		"bwb/status -> bwb/inspect -> bwb/test":  1,
		"bwb/inspect -> bwb/test -> bwb/publish": 1,
	}
	if len(record.OwnedOperationChains) != len(want) {
		t.Fatalf("operation chains=%#v want %#v", record.OwnedOperationChains, want)
	}
	for chain, count := range want {
		if record.OwnedOperationChains[chain] != count {
			t.Fatalf("operation chain %q=%d want %d", chain, record.OwnedOperationChains[chain], count)
		}
	}
}

func TestBuildOwnedOperationChainFindingsRequiresCrossSessionRecurrence(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), time.Time{}, time.Now().UTC())
	chain := "void-cli/context -> void-cli/verify -> void-cli/worktree-land"
	report.Summary.OwnedOperationChains[chain] = codexTransitionMetrics{Count: 6, Sessions: 2}
	report.Summary.Activity[sessionActivityKey("owned-operation-chain", chain)] = time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	findings := buildOwnedOperationChainFindings(report)
	if len(findings) != 1 {
		t.Fatalf("operation chain findings=%#v", findings)
	}
	finding := findings[0]
	if finding.Target != chain ||
		finding.Control != "local" ||
		finding.Confidence != "medium" ||
		!strings.Contains(finding.Action, "one void-cli operation") ||
		finding.LastSeen != "2026-07-30T10:00:00Z" {
		t.Fatalf("operation chain finding=%#v", finding)
	}

	report.Summary.OwnedOperationChains[chain] = codexTransitionMetrics{Count: 5, Sessions: 2}
	if findings := buildOwnedOperationChainFindings(report); len(findings) != 0 {
		t.Fatalf("sub-threshold chain produced findings: %#v", findings)
	}
}

func TestOwnedOperationChainInterventionUsesWorkflowIdentity(t *testing.T) {
	finding := sessionFinding{
		Category:   "operation-chain",
		Control:    "local",
		Title:      "recurring owned-operation chain: bwb/status -> bwb/inspect -> bwb/test",
		Target:     "bwb/status -> bwb/inspect -> bwb/test",
		Lever:      "tooling",
		Confidence: "medium",
	}
	finding.Signal = sessionFindingSignal(finding)
	interventions := buildSessionInterventions([]sessionFinding{finding})
	if len(interventions) != 1 ||
		!strings.HasPrefix(interventions[0].ID, "intervention/workflow/owned-operations/") ||
		interventions[0].Focus != "interface" {
		t.Fatalf("operation chain intervention=%#v", interventions)
	}
}
