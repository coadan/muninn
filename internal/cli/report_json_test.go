package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalysisJSONPayloadKeepsCompactSignalSurfaceByDefault(t *testing.T) {
	report := newSessionInsightsReport("fake", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Tasks = []codexTaskInsights{{Task: "private-task"}}
	report.Summary.Sessions = 3
	report.Summary.ToolCalls = 12
	report.Summary.ExcludedTokenStreams = 2
	report.Summary.ToolCallsByName["large-detail"] = 10
	report.Outcomes.Completed = 2
	report.Outcomes.Phases = map[string]taskPhaseAnalysis{"large-detail": {}}
	report.Diagnostics = diagnosticFailureAnalysis{
		Available: true,
		Failures:  []diagnosticFailureAggregate{{Fingerprint: "privacy-safe"}},
		Passes:    []diagnosticPassAggregate{{Target: "privacy-safe"}},
	}
	report.Interventions = []sessionIntervention{
		{ID: "intervention/tool/example"},
		{ID: "intervention/tool/two"},
		{ID: "intervention/tool/three"},
		{ID: "intervention/tool/four"},
		{ID: "intervention/tool/five"},
		{ID: "intervention/tool/six"},
	}
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
		`"excludedTokenTelemetrySessions":2`,
		`"failureFingerprints":1`,
		`"totalInterventions":6`,
		`"intervention/tool/example"`,
	} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact JSON missing %q: %s", want, compact)
		}
	}
	for _, unwanted := range []string{
		`"tasks"`,
		`"phases"`,
		`"toolCallsByName"`,
		`"privacy-safe"`,
		`"focusEvidence"`,
		`"findings"`,
		`"intervention/tool/six"`,
	} {
		if strings.Contains(compact, unwanted) {
			t.Fatalf("compact JSON retained detailed field %q: %s", unwanted, compact)
		}
	}

	fullJSON, err := json.Marshal(analysisJSONPayload(report, true))
	if err != nil {
		t.Fatal(err)
	}
	full := string(fullJSON)
	for _, want := range []string{
		`"detailLevel":"full"`,
		`"tasks"`,
		`"phases"`,
		`"toolCallsByName"`,
		`"owned-tool/example"`,
		`"intervention/tool/six"`,
	} {
		if !strings.Contains(full, want) {
			t.Fatalf("full JSON missing %q", want)
		}
	}
}

func TestAnalysisJSONPayloadIncludesBoundedDiscoveryFocusEvidence(t *testing.T) {
	root := t.TempDir()
	report := newSessionInsightsReport("codex", nil, root, zeroTime(), zeroTime())
	report.AnalysisScope.Focus = "discovery"
	for index := 0; index < 7; index++ {
		target := string(rune('a'+index)) + ".go"
		if err := os.WriteFile(filepath.Join(root, target), []byte("current"), 0o644); err != nil {
			t.Fatalf("write %s: %v", target, err)
		}
		report.Summary.ReadTargets[target] = codexTargetMetrics{
			Reads: 10 - index, SearchReadLoops: 10 - index, Sessions: 2,
		}
	}
	report.Summary.MixedShellShapes["search -> file reads"] = codexToolMetrics{
		Calls: 3, Sessions: 2, OutputBytes: 40_000,
	}

	raw, err := json.Marshal(analysisJSONPayload(report, false))
	if err != nil {
		t.Fatal(err)
	}
	var decoded compactSessionInsightsReport
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.FocusEvidence == nil ||
		len(decoded.FocusEvidence.ReadTargets) != 5 ||
		decoded.FocusEvidence.ReadTargets[0].Target != "a.go" ||
		len(decoded.FocusEvidence.SearchReadShapes) != 1 {
		t.Fatalf("bounded discovery focus evidence mismatch: %#v", decoded.FocusEvidence)
	}
}

func TestComparisonJSONPayloadKeepsCohortsAndStructuredInterventionTrend(t *testing.T) {
	baseline := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	current := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	baseline.Summary.Sessions = minimumTrendSessions
	current.Summary.Sessions = minimumTrendSessions + 1
	baseline.Outcomes.ToolUsingCompleted = minimumTrendTasks
	current.Outcomes.ToolUsingCompleted = minimumTrendTasks
	current.AnalysisScope.LookbackSeconds = 24 * 60 * 60
	baseline.Profiles = modelEffortAnalysis{Available: true}
	current.Profiles = modelEffortAnalysis{Available: true}
	baseline.Outcomes.PerformanceCohorts = []taskPerformanceCohort{{
		AgentKind: "root", Model: "model", ReasoningEffort: "high",
		TaskFamily: "tooling", CompletedTasks: 3,
	}}
	current.Outcomes.PerformanceCohorts = []taskPerformanceCohort{{
		AgentKind: "root", Model: "model", ReasoningEffort: "high",
		TaskFamily: "tooling", CompletedTasks: 4,
	}}
	baseline.Outcomes.QualityCohorts = []taskQualityCohort{{
		AgentKind: "root", Model: "model", ReasoningEffort: "high",
		TaskFamily: "tooling", Deliveries: 5,
	}}
	current.Outcomes.QualityCohorts = []taskQualityCohort{{
		AgentKind: "root", Model: "model", ReasoningEffort: "high",
		TaskFamily: "tooling", Deliveries: 6,
	}}
	baseline.Interventions = []sessionIntervention{
		{ID: "intervention/resolved", Priority: "medium"},
		{ID: "intervention/persistent", Priority: "low"},
	}
	current.Interventions = []sessionIntervention{
		{ID: "intervention/persistent", Priority: "high"},
		{ID: "intervention/new", Priority: "medium"},
	}

	payload := comparisonJSONPayload(baseline, current, false)
	if payload.Comparison != "previous" || payload.BaselineLabel != "previous non-overlapping 1d" {
		t.Fatalf("comparison identity mismatch: %#v", payload)
	}
	trend := payload.InterventionTrend
	if !trend.SufficientEvidence ||
		len(trend.Resolved) != 1 || trend.Resolved[0].ID != "intervention/resolved" ||
		len(trend.Persistent) != 1 || trend.Persistent[0].Priority != "high" ||
		len(trend.New) != 1 || trend.New[0].ID != "intervention/new" {
		t.Fatalf("intervention trend mismatch: %#v", trend)
	}
	if len(payload.Cohorts.Performance) != 1 ||
		len(payload.Cohorts.Quality) != 1 ||
		payload.QualityVerdict == "" {
		t.Fatalf("comparison cohorts missing: %#v", payload)
	}
	if !payload.Trends.CompletedTasks.SufficientEvidence ||
		len(payload.Trends.CompletedTasks.Metrics) == 0 ||
		!payload.Trends.MatchedPerformance.SufficientEvidence ||
		!payload.Trends.MatchedQuality.SufficientEvidence ||
		len(payload.Trends.Rates) == 0 {
		t.Fatalf("structured comparison trends missing: %#v", payload.Trends)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"detailLevel":"summary"`,
		`"baseline"`,
		`"current"`,
		`"profiles"`,
		`"trends"`,
		`"diagnostics"`,
		`"interventionTrend"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("comparison JSON missing %q: %s", want, raw)
		}
	}
}

func TestComparisonJSONPayloadDoesNotDirectionLabelSparseWindows(t *testing.T) {
	baseline := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	current := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	baseline.Summary.Sessions = minimumTrendSessions - 1
	current.Summary.Sessions = minimumTrendSessions
	baseline.Interventions = []sessionIntervention{{ID: "intervention/resolved"}}

	payload := comparisonJSONPayload(baseline, current, false)
	trend := payload.InterventionTrend
	if trend.SufficientEvidence || len(trend.Resolved) != 0 {
		t.Fatalf("sparse windows received direction labels: %#v", trend)
	}
	if payload.Trends.CompletedTasks.SufficientEvidence ||
		payload.Trends.CompletedTasks.Direction != "insufficient" {
		t.Fatalf("sparse completed-task windows received a direction: %#v", payload.Trends.CompletedTasks)
	}
}

func TestCompactComparisonBoundsCohortsAndDiagnostics(t *testing.T) {
	var performance []matchedPerformanceCohort
	var quality []matchedQualityCohort
	baselineDiagnostics := diagnosticFailureAnalysis{}
	for index := 0; index < compactInterventionLimit+2; index++ {
		taskFamily := "family-" + string(rune('a'+index))
		performance = append(performance, matchedPerformanceCohort{
			Baseline: taskPerformanceCohort{TaskFamily: taskFamily},
			Current:  taskPerformanceCohort{TaskFamily: taskFamily},
		})
		quality = append(quality, matchedQualityCohort{
			Baseline: taskQualityCohort{TaskFamily: taskFamily},
			Current:  taskQualityCohort{TaskFamily: taskFamily},
		})
		baselineDiagnostics.Failures = append(
			baselineDiagnostics.Failures,
			diagnosticFailureAggregate{Fingerprint: "fingerprint-" + string(rune('a'+index))},
		)
	}

	compactCohorts := comparisonCohortPayload(performance, quality, false)
	if compactCohorts.TotalPerformance != len(performance) ||
		compactCohorts.TotalQuality != len(quality) ||
		len(compactCohorts.Performance) != compactInterventionLimit ||
		len(compactCohorts.Quality) != compactInterventionLimit {
		t.Fatalf("compact cohort bounds mismatch: %#v", compactCohorts)
	}
	fullCohorts := comparisonCohortPayload(performance, quality, true)
	if len(fullCohorts.Performance) != len(performance) ||
		len(fullCohorts.Quality) != len(quality) {
		t.Fatalf("full cohorts were bounded: %#v", fullCohorts)
	}

	compactDiagnostics := comparisonDiagnosticPayload(
		baselineDiagnostics,
		diagnosticFailureAnalysis{},
		false,
	)
	if compactDiagnostics.TotalFingerprints != len(baselineDiagnostics.Failures) ||
		len(compactDiagnostics.Fingerprints) != compactInterventionLimit {
		t.Fatalf("compact diagnostic bounds mismatch: %#v", compactDiagnostics)
	}
	fullDiagnostics := comparisonDiagnosticPayload(
		baselineDiagnostics,
		diagnosticFailureAnalysis{},
		true,
	)
	if len(fullDiagnostics.Fingerprints) != len(baselineDiagnostics.Failures) {
		t.Fatalf("full diagnostics were bounded: %#v", fullDiagnostics)
	}
}
