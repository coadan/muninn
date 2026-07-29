package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalysisJSONPayloadKeepsCompactSignalSurfaceByDefault(t *testing.T) {
	report := newSessionInsightsReport("fake", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Tasks = []codexTaskInsights{{Task: "private-task"}}
	report.Summary.Sessions = 3
	report.Summary.ToolCalls = 12
	report.Summary.ToolCallsByName["large-detail"] = 10
	report.Outcomes.Completed = 2
	report.Outcomes.Phases = map[string]taskPhaseAnalysis{"large-detail": {}}
	report.Diagnostics = diagnosticFailureAnalysis{
		Available: true,
		Failures:  []diagnosticFailureAggregate{{Fingerprint: "privacy-safe"}},
		Passes:    []diagnosticPassAggregate{{Target: "privacy-safe"}},
	}
	report.Interventions = []sessionIntervention{{ID: "intervention/tool/example"}}
	report.Findings = []sessionFinding{{Signal: "owned-tool/example"}}

	compactJSON, err := json.Marshal(analysisJSONPayload(report, false))
	if err != nil {
		t.Fatal(err)
	}
	compact := string(compactJSON)
	for _, want := range []string{
		`"detailLevel":"summary"`,
		`"sessions":3`,
		`"toolCalls":12`,
		`"failureFingerprints":1`,
		`"intervention/tool/example"`,
		`"owned-tool/example"`,
	} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact JSON missing %q: %s", want, compact)
		}
	}
	for _, unwanted := range []string{`"tasks"`, `"phases"`, `"toolCallsByName"`, `"privacy-safe"`} {
		if strings.Contains(compact, unwanted) {
			t.Fatalf("compact JSON retained detailed field %q: %s", unwanted, compact)
		}
	}

	fullJSON, err := json.Marshal(analysisJSONPayload(report, true))
	if err != nil {
		t.Fatal(err)
	}
	full := string(fullJSON)
	for _, want := range []string{`"detailLevel":"full"`, `"tasks"`, `"phases"`, `"toolCallsByName"`} {
		if !strings.Contains(full, want) {
			t.Fatalf("full JSON missing %q", want)
		}
	}
}
