package main

import (
	"strings"
	"testing"
)

func TestBuildOwnedOperationsDrilldownFiltersRanksAndBounds(t *testing.T) {
	report := codexSessionInsightsReport{
		SchemaVersion: codexSessionInsightsSchemaVersion,
		Provider:      "codex",
		Since:         "2026-07-24T00:00:00Z",
		Summary: codexSessionInsightsSummary{
			Sessions: 3,
			OwnedTooling: map[string]codexToolMetrics{
				"bwb": {Calls: 12},
			},
			OwnedOperations: map[string]codexOwnedOperationMetrics{
				"bwb/status":  {Calls: 8, OutputBytes: 800},
				"bwb/test":    {Calls: 2, FailedCalls: 1, OutputBytes: 200},
				"bwb/inspect": {Calls: 2, TruncatedCalls: 1, OutputBytes: 500},
				"heimdal/run": {Calls: 7, FailedCalls: 2, OutputBytes: 900},
			},
			OwnedOperationFailureReasons: map[string]map[string]codexOccurrenceMetrics{
				"bwb/test": {"test harness protocol": {Count: 1, Sessions: 1}},
			},
		},
	}
	config := repositoryConfig{OwnedTools: []ownedToolConfig{{
		ID:             "bwb",
		Recommendation: "Improve BWB directly.",
	}}}

	drilldown, err := buildOwnedOperationsDrilldown(report, config, "bwb", 2)
	if err != nil {
		t.Fatalf("build drilldown: %v", err)
	}
	if drilldown.Tool != "bwb" || drilldown.Sessions != 3 || drilldown.ToolCalls != 12 {
		t.Fatalf("unexpected drilldown scope: %#v", drilldown)
	}
	if len(drilldown.Operations) != 2 {
		t.Fatalf("expected bounded operations, got %#v", drilldown.Operations)
	}
	if drilldown.Operations[0].Operation != "bwb/test" ||
		drilldown.Operations[1].Operation != "bwb/inspect" {
		t.Fatalf("unexpected operation ranking: %#v", drilldown.Operations)
	}
	if got := drilldown.Operations[0].FailureReasons["test harness protocol"].Count; got != 1 {
		t.Fatalf("failure reasons were not retained: %#v", drilldown.Operations[0])
	}
}

func TestBuildOwnedOperationsDrilldownRejectsUnknownTool(t *testing.T) {
	_, err := buildOwnedOperationsDrilldown(
		codexSessionInsightsReport{},
		repositoryConfig{OwnedTools: []ownedToolConfig{{ID: "bwb"}, {ID: "heimdal"}}},
		"missing",
		10,
	)
	if err == nil || !strings.Contains(err.Error(), "available: bwb, heimdal") {
		t.Fatalf("expected bounded available-tool error, got %v", err)
	}
}
