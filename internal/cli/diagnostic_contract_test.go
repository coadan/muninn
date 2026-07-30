package cli

import (
	"strings"
	"testing"
	"time"
)

func TestBuildDiagnosticContractFindingsRequiresRecurringDominantGenericFailures(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), time.Time{}, time.Now().UTC())
	report.Summary.OwnedOperationFailureReasons["void-cli/context"] = map[string]codexOccurrenceMetrics{
		"other non-zero exit": {Count: 7, Sessions: 3},
		"timeout":             {Count: 2, Sessions: 1},
	}
	report.Summary.Activity[sessionActivityKey("owned-operation-friction", "void-cli/context")] =
		time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	findings := buildDiagnosticContractFindings(report, ownershipCatalog{})
	if len(findings) != 1 {
		t.Fatalf("diagnostic contract findings=%#v", findings)
	}
	finding := findings[0]
	if finding.Target != "void-cli/context" ||
		finding.Confidence != "high" ||
		!strings.Contains(finding.Evidence, "7 of 9") ||
		!strings.Contains(finding.Action, "machine-readable failure class") ||
		finding.LastSeen != "2026-07-30T10:00:00Z" {
		t.Fatalf("diagnostic contract finding=%#v", finding)
	}

	report.Summary.OwnedOperationFailureReasons["void-cli/context"]["timeout"] =
		codexOccurrenceMetrics{Count: 8, Sessions: 3}
	if findings := buildDiagnosticContractFindings(report, ownershipCatalog{}); len(findings) != 0 {
		t.Fatalf("minority generic failures produced findings: %#v", findings)
	}
}

func TestDiagnosticContractBecomesPrimaryOperationIntervention(t *testing.T) {
	operation := sessionFinding{
		Category:   "owned-operation",
		Control:    "local",
		Title:      "locally controlled operation has recurring friction: void-cli/context",
		Target:     "void-cli/context",
		Lever:      "tooling",
		Confidence: "high",
		Evidence:   "recurring failures",
		Action:     "generic operation recommendation",
	}
	diagnostic := sessionFinding{
		Category:   "diagnostic-contract",
		Control:    "local",
		Title:      "owned operation lacks an actionable failure contract: void-cli/context",
		Target:     "void-cli/context",
		Lever:      "tooling",
		Confidence: "high",
		Evidence:   "generic failures dominate",
		Action:     "emit structured failures",
	}
	for index, finding := range []*sessionFinding{&operation, &diagnostic} {
		finding.Signal = sessionFindingSignal(*finding)
		finding.score = 100 + index
	}
	interventions := buildSessionInterventions([]sessionFinding{operation, diagnostic})
	if len(interventions) != 1 ||
		interventions[0].ID != "intervention/operation/void-cli/context" ||
		interventions[0].Title != diagnostic.Title ||
		interventions[0].Action != diagnostic.Action ||
		interventions[0].FindingCount != 2 {
		t.Fatalf("diagnostic operation intervention=%#v", interventions)
	}
}

func TestBuildDiagnosticContractFindingsExcludesExpectedVerificationExits(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), time.Time{}, time.Now().UTC())
	report.Summary.OwnedOperationFailureReasons["bwb/test"] = map[string]codexOccurrenceMetrics{
		"other non-zero exit": {Count: 11, Sessions: 3},
	}
	if findings := buildDiagnosticContractFindings(report, ownershipCatalog{}); len(findings) != 0 {
		t.Fatalf("expected verification exits produced diagnostic findings: %#v", findings)
	}
}
