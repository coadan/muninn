package main

import (
	"strings"
	"testing"
	"time"
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
		operationTasks: map[string]map[string]codexOwnedOperationMetrics{
			"bwb/test": {
				"task-a": {Calls: 2, Sessions: 1, FailedCalls: 1, OutputBytes: 200},
			},
			"bwb/status": {
				"task-b": {Calls: 8, Sessions: 2, OutputBytes: 800},
			},
		},
	}
	config := repositoryConfig{OwnedTools: []ownedToolConfig{{
		ID:             "bwb",
		Recommendation: "Improve BWB directly.",
		Operations: []ownedOperationConfig{
			{ID: "status"},
			{ID: "test"},
			{ID: "inspect"},
		},
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
	if len(drilldown.TaskCohorts) != 1 ||
		drilldown.TaskCohorts[0].Operation != "bwb/test" ||
		drilldown.TaskCohorts[0].Task != "task-a" {
		t.Fatalf("task cohorts were not bounded to selected operations: %#v", drilldown.TaskCohorts)
	}

	exact, err := buildOwnedOperationsDrilldown(report, config, "bwb/status", 10)
	if err != nil {
		t.Fatalf("build exact operation drilldown: %v", err)
	}
	if exact.Tool != "bwb" || exact.Operation != "bwb/status" ||
		len(exact.Operations) != 1 || exact.Operations[0].Operation != "bwb/status" {
		t.Fatalf("unexpected exact operation drilldown: %#v", exact)
	}
	if len(exact.TaskCohorts) != 1 || exact.TaskCohorts[0].Task != "task-b" {
		t.Fatalf("exact operation task cohorts mismatch: %#v", exact.TaskCohorts)
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

func TestBuildOwnedOperationsDrilldownRejectsUnknownOperation(t *testing.T) {
	_, err := buildOwnedOperationsDrilldown(
		codexSessionInsightsReport{},
		repositoryConfig{OwnedTools: []ownedToolConfig{{
			ID:         "bwb",
			Operations: []ownedOperationConfig{{ID: "test"}},
		}}},
		"bwb/missing",
		10,
	)
	if err == nil || !strings.Contains(err.Error(), `unknown --operations operation "bwb/missing"`) {
		t.Fatalf("expected bounded unknown-operation error, got %v", err)
	}
}

func TestSessionRecordAttributesOwnedOperationsToLogicalTasks(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	operation := "bwb/test-nses"
	session := normalizedSession{
		Provider:   "codex",
		SourcePath: "session.jsonl",
		CWD:        root,
		Events: []normalizedSessionEvent{
			{
				Sequence:        1,
				OccurredAt:      now.Add(-2 * time.Minute),
				Kind:            sessionEventToolCall,
				OwnedOperations: []string{operation},
				OperationTask:   "task-a",
			},
			{
				Sequence:        2,
				OccurredAt:      now.Add(-time.Minute),
				CallOccurredAt:  now.Add(-2 * time.Minute),
				Kind:            sessionEventToolOutput,
				Failed:          true,
				FailureReason:   "test failure",
				OutputBytes:     120,
				OwnedOperations: []string{operation},
				OperationTask:   "task-a",
			},
		},
	}
	record, err := sessionRecordFromNormalized(
		session,
		root,
		now.Add(-time.Hour),
		now,
		ownershipCatalog{},
	)
	if err != nil {
		t.Fatalf("build session record: %v", err)
	}
	got := record.OwnedOperationTasks[operation]["task-a"]
	if got.Calls != 1 || got.Sessions != 1 || got.FailedCalls != 1 ||
		got.OutputBytes != 120 || got.EstimatedOutputTokens != 30 {
		t.Fatalf("logical task metrics mismatch: %#v", got)
	}

	report := newSessionInsightsReport("codex", nil, root, now.Add(-time.Hour), now)
	addCodexSessionToReport(&report, map[string]*codexTaskInsights{}, record)
	got = report.operationTasks[operation]["task-a"]
	if got.Calls != 1 || got.Sessions != 1 || got.FailedCalls != 1 {
		t.Fatalf("report logical task metrics mismatch: %#v", got)
	}
}
