package main

import (
	"strings"
	"testing"
	"time"
)

func trendTestReport(since, generatedAt time.Time, scope sessionAnalysisScope) codexSessionInsightsReport {
	report := newSessionInsightsReport("codex", nil, tTempWorkspace, since, generatedAt)
	report.AnalysisScope = scope
	return report
}

const tTempWorkspace = "/workspace"

func TestPreviousLookbackWindowIsAdjacentAndNonOverlapping(t *testing.T) {
	currentUntil := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	currentSince := currentUntil.Add(-72 * time.Hour)
	previousSince, previousUntil, err := previousLookbackWindow(
		currentSince,
		currentUntil,
		int64((72*time.Hour)/time.Second),
	)
	if err != nil {
		t.Fatalf("previousLookbackWindow returned error: %v", err)
	}
	if previousUntil != currentSince {
		t.Fatalf("previous end=%s want current start=%s", previousUntil, currentSince)
	}
	if previousSince != currentSince.Add(-72*time.Hour) {
		t.Fatalf("previous start=%s", previousSince)
	}
}

func TestPreviousLookbackWindowRejectsMismatchedScope(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	if _, _, err := previousLookbackWindow(
		now.Add(-24*time.Hour),
		now,
		int64((72*time.Hour)/time.Second),
	); err == nil {
		t.Fatal("mismatched lookback unexpectedly accepted")
	}
}

func TestValidateSessionTrendComparisonAcceptsMatchedLookbackScope(t *testing.T) {
	now := time.Date(2026, 7, 25, 4, 0, 0, 0, time.UTC)
	scope := sessionAnalysisScope{WindowKind: "lookback", LookbackSeconds: int64((7 * 24 * time.Hour) / time.Second)}
	baseline := trendTestReport(now.Add(-7*24*time.Hour), now, scope)
	current := trendTestReport(now.Add(time.Hour-7*24*time.Hour), now.Add(time.Hour), scope)
	if err := validateSessionTrendComparison(baseline, current, "before"); err != nil {
		t.Fatalf("matched trend scope: %v", err)
	}
}

func TestValidateSessionTrendComparisonRejectsMismatchedLookbackWithCorrection(t *testing.T) {
	now := time.Date(2026, 7, 25, 4, 0, 0, 0, time.UTC)
	baseline := trendTestReport(now.Add(-7*24*time.Hour), now, sessionAnalysisScope{
		WindowKind:      "lookback",
		LookbackSeconds: int64((7 * 24 * time.Hour) / time.Second),
	})
	current := trendTestReport(now.Add(-4*time.Hour), now, sessionAnalysisScope{
		WindowKind:      "lookback",
		LookbackSeconds: int64((4 * time.Hour) / time.Second),
	})
	err := validateSessionTrendComparison(baseline, current, "before")
	if err == nil || !strings.Contains(err.Error(), "rerun with --since 1w") {
		t.Fatalf("expected exact matched-window correction, got %v", err)
	}
}

func TestValidateSessionTrendComparisonRejectsScopeMismatches(t *testing.T) {
	now := time.Date(2026, 7, 25, 4, 0, 0, 0, time.UTC)
	baseScope := sessionAnalysisScope{
		WindowKind:      "lookback",
		LookbackSeconds: int64((24 * time.Hour) / time.Second),
	}
	for _, test := range []struct {
		name    string
		current sessionAnalysisScope
		want    string
	}{
		{
			name: "task",
			current: sessionAnalysisScope{
				WindowKind:      "lookback",
				LookbackSeconds: int64((24 * time.Hour) / time.Second),
				Task:            "focused-task",
			},
			want: "--task scope",
		},
		{
			name: "archives",
			current: sessionAnalysisScope{
				WindowKind:      "lookback",
				LookbackSeconds: int64((24 * time.Hour) / time.Second),
				IncludeArchived: true,
			},
			want: "--include-archived",
		},
		{
			name: "focus",
			current: sessionAnalysisScope{
				WindowKind:      "lookback",
				LookbackSeconds: int64((24 * time.Hour) / time.Second),
				Focus:           "structure",
			},
			want: "--focus scope",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := trendTestReport(now.Add(-24*time.Hour), now, baseScope)
			current := trendTestReport(now.Add(-24*time.Hour), now, test.current)
			err := validateSessionTrendComparison(baseline, current, "before")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q mismatch, got %v", test.want, err)
			}
		})
	}
}

func TestValidateSessionTrendComparisonSupportsLegacyDefaultCheckpoint(t *testing.T) {
	now := time.Date(2026, 7, 25, 4, 0, 0, 0, time.UTC)
	baseline := trendTestReport(now.Add(-7*24*time.Hour), now, sessionAnalysisScope{})
	baseline.SchemaVersion = 28
	current := trendTestReport(now.Add(-7*24*time.Hour), now, sessionAnalysisScope{
		WindowKind:      "lookback",
		LookbackSeconds: int64((7 * 24 * time.Hour) / time.Second),
	})
	if err := validateSessionTrendComparison(baseline, current, "legacy"); err != nil {
		t.Fatalf("legacy default checkpoint should remain comparable: %v", err)
	}
}

func TestValidateSessionTrendComparisonTreatsBroadFrictionFocusAsDefault(t *testing.T) {
	now := time.Date(2026, 7, 25, 4, 0, 0, 0, time.UTC)
	baseline := trendTestReport(now.Add(-24*time.Hour), now, sessionAnalysisScope{
		WindowKind:      "lookback",
		LookbackSeconds: int64((24 * time.Hour) / time.Second),
	})
	current := baseline
	current.AnalysisScope.Focus = "friction"
	if err := validateSessionTrendComparison(baseline, current, "before"); err != nil {
		t.Fatalf("broad friction focus should match default findings: %v", err)
	}
}

func TestCompletedTaskTrendUsesTokenSplitForCurrentSchemas(t *testing.T) {
	baseline := codexSessionInsightsReport{SchemaVersion: 30}
	current := codexSessionInsightsReport{SchemaVersion: 30}
	baseline.Outcomes = completionEpisodeAnalysis{
		ToolUsingCompleted:  20,
		CachedInputTokens:   outcomeDistribution{P50: 100, P90: 200},
		UncachedInputTokens: outcomeDistribution{P50: 50, P90: 100},
		ModelOutputTokens:   outcomeDistribution{P50: 10, P90: 20},
		ToolCalls:           outcomeDistribution{P50: 10, P90: 30},
		DurationSeconds:     outcomeDistribution{P50: 60, P90: 180},
		FailedCalls:         outcomeDistribution{P90: 2},
		Compactions:         outcomeDistribution{P90: 1},
	}
	current.Outcomes = baseline.Outcomes
	current.Outcomes.CachedInputTokens.P50 = 80
	current.Outcomes.ToolCalls.P90 = 40

	metrics := completedTaskTrendMetrics(baseline, current)
	if len(metrics) != 12 {
		t.Fatalf("completed-task metrics=%d want 12: %#v", len(metrics), metrics)
	}
	label, improved, regressed, unchanged := summarizeTrendDirections(metrics)
	if label != "mixed" || improved != 1 || regressed != 1 || unchanged != 10 {
		t.Fatalf("direction=%q %d/%d/%d want mixed 1/1/10", label, improved, regressed, unchanged)
	}
}

func TestCompletedTaskTrendSupportsLegacyFreshTokenCheckpoint(t *testing.T) {
	baseline := codexSessionInsightsReport{SchemaVersion: 29}
	current := codexSessionInsightsReport{SchemaVersion: 30}
	baseline.Outcomes = completionEpisodeAnalysis{
		ToolUsingCompleted: 10,
		FreshTokens:        outcomeDistribution{P50: 100, P90: 300},
	}
	current.Outcomes = completionEpisodeAnalysis{
		ToolUsingCompleted: 10,
		FreshTokens:        outcomeDistribution{P50: 90, P90: 250},
	}
	metrics := completedTaskTrendMetrics(baseline, current)
	if len(metrics) != 8 || metrics[0].Name != "fresh tokens p50" || metrics[1].Name != "fresh tokens p90" {
		t.Fatalf("legacy metrics=%#v", metrics)
	}
}

func TestRoundtripTrendHelpersCountTransitionsAndWaits(t *testing.T) {
	transitions := map[string]codexTransitionMetrics{
		"file reads -> file reads": {Count: 4},
		"file reads -> search":     {Count: 3},
		"search -> search":         {Count: 2},
	}
	if got := totalCrossCallTransitions(transitions); got != 9 {
		t.Fatalf("total transitions=%d want 9", got)
	}
	if got := repeatedCrossCallTransitions(transitions); got != 6 {
		t.Fatalf("repeated transitions=%d want 6", got)
	}
	if got := totalWaitCalls(map[string]codexWaitMetrics{
		"tests": {Calls: 5},
		"poll":  {Calls: 2},
	}); got != 7 {
		t.Fatalf("wait calls=%d want 7", got)
	}
}

func TestMatchedPerformanceCohortsRequireSharedAdequateSamples(t *testing.T) {
	cohort := taskPerformanceCohort{
		AgentKind:       "root",
		Model:           "gpt-5.6-sol",
		ReasoningEffort: "high",
		TaskFamily:      "worker",
		CompletedTasks:  4,
	}
	other := cohort
	other.CompletedTasks = 2
	if got := matchedPerformanceCohorts(
		[]taskPerformanceCohort{cohort},
		[]taskPerformanceCohort{other},
		3,
	); len(got) != 0 {
		t.Fatalf("undersized cohort matched: %#v", got)
	}
	other.CompletedTasks = 5
	if got := matchedPerformanceCohorts(
		[]taskPerformanceCohort{cohort},
		[]taskPerformanceCohort{other},
		3,
	); len(got) != 1 {
		t.Fatalf("shared cohort mismatch: %#v", got)
	}
}

func TestQualityAdjustedVerdictDetectsShiftedDownstreamWork(t *testing.T) {
	baseline := codexSessionInsightsReport{}
	current := codexSessionInsightsReport{}
	baseline.Summary.DeliveryRework = deliveryReworkMetrics{Deliveries: 20}
	current.Summary.DeliveryRework = deliveryReworkMetrics{Deliveries: 20}
	baseline.Summary.DownstreamQuality = downstreamQualityMetrics{
		Deliveries:            20,
		DeliveriesWithFailure: 2,
	}
	current.Summary.DownstreamQuality = downstreamQualityMetrics{
		Deliveries:            20,
		DeliveriesWithFailure: 8,
	}
	verdict := qualityAdjustedPerformanceVerdict(
		baseline,
		current,
		[]trendMetric{{
			Name:          "matched duration",
			Baseline:      100,
			Current:       70,
			LowerIsBetter: true,
		}},
		nil,
	)
	if !strings.HasPrefix(verdict, "shifted downstream") {
		t.Fatalf("verdict=%q want shifted downstream", verdict)
	}
}

func TestQualityAdjustedVerdictRecognizesQualityTradeoff(t *testing.T) {
	baseline := codexSessionInsightsReport{}
	current := codexSessionInsightsReport{}
	baseline.Summary.DeliveryRework = deliveryReworkMetrics{
		Deliveries:           20,
		DeliveriesWithRework: 8,
	}
	current.Summary.DeliveryRework = deliveryReworkMetrics{
		Deliveries:           20,
		DeliveriesWithRework: 2,
	}
	baseline.Summary.DownstreamQuality = downstreamQualityMetrics{Deliveries: 20}
	current.Summary.DownstreamQuality = downstreamQualityMetrics{Deliveries: 20}
	verdict := qualityAdjustedPerformanceVerdict(
		baseline,
		current,
		[]trendMetric{{
			Name:          "matched duration",
			Baseline:      100,
			Current:       130,
			LowerIsBetter: true,
		}},
		nil,
	)
	if !strings.HasPrefix(verdict, "quality tradeoff") {
		t.Fatalf("verdict=%q want quality tradeoff", verdict)
	}
}

func TestMatchedQualityCohortsRequireSamePerformanceCohort(t *testing.T) {
	performance := []matchedPerformanceCohort{{
		Baseline: taskPerformanceCohort{
			AgentKind: "root", Model: "sol", ReasoningEffort: "high", TaskFamily: "src/app",
		},
		Current: taskPerformanceCohort{
			AgentKind: "root", Model: "sol", ReasoningEffort: "high", TaskFamily: "src/app",
		},
	}}
	baseline := []taskQualityCohort{
		{AgentKind: "root", Model: "sol", ReasoningEffort: "high", TaskFamily: "src/app", Deliveries: 8},
		{AgentKind: "root", Model: "sol", ReasoningEffort: "high", TaskFamily: "docs", Deliveries: 8},
	}
	current := []taskQualityCohort{
		{AgentKind: "root", Model: "sol", ReasoningEffort: "high", TaskFamily: "src/app", Deliveries: 6},
		{AgentKind: "root", Model: "sol", ReasoningEffort: "high", TaskFamily: "docs", Deliveries: 8},
	}
	got := matchedQualityCohorts(baseline, current, performance, 5)
	if len(got) != 1 || got[0].Baseline.TaskFamily != "src/app" {
		t.Fatalf("matched quality cohorts=%#v want only shared performance cohort", got)
	}
}

func TestQualityAdjustedVerdictPrefersMatchedQuality(t *testing.T) {
	baseline := codexSessionInsightsReport{}
	current := codexSessionInsightsReport{}
	baseline.Summary.DeliveryRework = deliveryReworkMetrics{
		Deliveries: 20, DeliveriesWithRework: 0,
	}
	current.Summary.DeliveryRework = deliveryReworkMetrics{
		Deliveries: 20, DeliveriesWithRework: 10,
	}
	baseline.Summary.DownstreamQuality = downstreamQualityMetrics{Deliveries: 20}
	current.Summary.DownstreamQuality = downstreamQualityMetrics{Deliveries: 20}
	verdict := qualityAdjustedPerformanceVerdict(
		baseline,
		current,
		[]trendMetric{{Name: "duration", Baseline: 100, Current: 70, LowerIsBetter: true}},
		[]trendMetric{{Name: "matched review", Baseline: 0, Current: 0, LowerIsBetter: true}},
	)
	if !strings.HasPrefix(verdict, "improved") {
		t.Fatalf("verdict=%q should use matched quality over aggregate regression", verdict)
	}
}
