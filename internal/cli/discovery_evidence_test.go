package cli

import "testing"

func TestBuildDiscoveryFocusEvidenceRanksFiltersAndBoundsRows(t *testing.T) {
	summary := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime()).Summary
	summary.ReadTargets["most-loops.go"] = codexTargetMetrics{
		Reads: 4, SearchReadLoops: 3, Sessions: 2, RediscoverySessions: 1,
	}
	summary.ReadTargets["most-reads.go"] = codexTargetMetrics{
		Reads: 20, SearchReadLoops: 1, Sessions: 1, RediscoverySessions: 1,
	}
	summary.ReadTargets["bounded-out.go"] = codexTargetMetrics{
		Reads: 10, SearchReadLoops: 0, Sessions: 2, RediscoverySessions: 2,
	}
	summary.MixedShellShapes["search -> file reads"] = codexToolMetrics{
		Calls: 2, Sessions: 2, OutputBytes: 80_000,
	}
	summary.MixedShellShapes["file reads -> search"] = codexToolMetrics{
		Calls: 5, Sessions: 1, OutputBytes: 40_000,
	}
	summary.MixedShellShapes["file reads"] = codexToolMetrics{
		Calls: 20, Sessions: 3, OutputBytes: 200_000,
	}

	evidence := buildDiscoveryFocusEvidence(summary, 2)
	if len(evidence.ReadTargets) != 2 ||
		evidence.ReadTargets[0].Target != "most-loops.go" ||
		evidence.ReadTargets[1].Target != "most-reads.go" {
		t.Fatalf("ranked read targets mismatch: %#v", evidence.ReadTargets)
	}
	if len(evidence.SearchReadShapes) != 2 ||
		evidence.SearchReadShapes[0].Shape != "search -> file reads" ||
		evidence.SearchReadShapes[0].EstimatedOutputTokens != 20_000 ||
		evidence.SearchReadShapes[1].Shape != "file reads -> search" {
		t.Fatalf("ranked search/read shapes mismatch: %#v", evidence.SearchReadShapes)
	}
}
