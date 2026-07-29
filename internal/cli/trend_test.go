package cli

import (
	"strings"
	"testing"
	"time"
)

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

func TestInterventionTrendUsesStableInterventionIdentity(t *testing.T) {
	baseline := []sessionIntervention{
		{
			ID:       "intervention/workflow/verification",
			Title:    "old evidence title",
			Priority: "high",
		},
		{
			ID:       "intervention/tool/old",
			Title:    "remove old friction",
			Priority: "medium",
		},
	}
	current := []sessionIntervention{
		{
			ID:       "intervention/workflow/verification",
			Title:    "new evidence title",
			Priority: "highest",
		},
		{
			ID:       "intervention/tool/new",
			Title:    "fix new friction",
			Priority: "high",
		},
	}
	out, err := captureStdout(t, func() error {
		printInterventionTrend(baseline, current)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Intervention trend: 1 resolved, 1 persistent, 1 new.",
		"intervention/tool/old",
		"intervention/tool/new",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("intervention trend missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "old evidence title") || strings.Contains(out, "new evidence title") {
		t.Fatalf("persistent intervention should not be printed as churn:\n%s", out)
	}
}

func TestInterventionTrendRowsLeadWithActionability(t *testing.T) {
	interventions := []sessionIntervention{
		{ID: "intervention/a-low", Title: "low", Priority: "low", FindingCount: 9},
		{ID: "intervention/b-medium", Title: "medium", Priority: "medium", FindingCount: 1},
		{ID: "intervention/c-high", Title: "high", Priority: "high", FindingCount: 1},
		{ID: "intervention/d-high-corroborated", Title: "high corroborated", Priority: "high", FindingCount: 3},
		{ID: "intervention/z-highest", Title: "highest", Priority: "highest", FindingCount: 1},
	}
	out, err := captureStdout(t, func() error {
		printInterventionTrendRows("New", interventions, 4)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	order := []string{
		"intervention/z-highest",
		"intervention/d-high-corroborated",
		"intervention/c-high",
		"intervention/b-medium",
	}
	previous := -1
	for _, id := range order {
		index := strings.Index(out, id)
		if index < 0 || index <= previous {
			t.Fatalf("actionability order missing %q after offset %d:\n%s", id, previous, out)
		}
		previous = index
	}
	if strings.Contains(out, "intervention/a-low") {
		t.Fatalf("low-priority row displaced an actionable row:\n%s", out)
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

func TestCompletedTaskTrendSupportsLegacyFreshTokenSchema(t *testing.T) {
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

func TestMaterialTrendDirectionRejectsMissingWindowEvidence(t *testing.T) {
	metric := trendMetric{
		Baseline:       1,
		Current:        0,
		LowerIsBetter:  false,
		BaselineSample: 1,
		CurrentSample:  0,
		MinimumSample:  1,
	}
	if got := materialTrendDirection(metric); got != "insufficient" {
		t.Fatalf("direction=%q want insufficient", got)
	}
}

func TestSessionTrendDoesNotDirectionLabelSparseRates(t *testing.T) {
	baseline := codexSessionInsightsReport{SchemaVersion: codexSessionInsightsSchemaVersion}
	current := codexSessionInsightsReport{SchemaVersion: codexSessionInsightsSchemaVersion}
	baseline.Summary = codexSessionInsightsSummary{
		codexAggregateMetrics: codexAggregateMetrics{
			Sessions:          1,
			CompletedSessions: 1,
			ToolCalls:         50,
			FailedToolCalls:   5,
			Compactions:       1,
		},
	}
	current.Summary = codexSessionInsightsSummary{
		codexAggregateMetrics: codexAggregateMetrics{
			Sessions:          2,
			CompletedSessions: 2,
			ToolCalls:         200,
			FailedToolCalls:   2,
			Compactions:       8,
		},
	}
	baseline.Outcomes.ToolUsingCompleted = 1
	current.Outcomes.ToolUsingCompleted = 20
	baseline.Interventions = []sessionIntervention{{ID: "intervention/baseline"}}
	current.Interventions = []sessionIntervention{{ID: "intervention/current"}}

	out, err := captureStdout(t, func() error {
		printSessionTrend(baseline, current, "sparse", false)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"completion ratio",
		"tool roundtrips / session",
		"cross-call transitions / task",
		"failures / 1k calls",
		"compactions / session",
	} {
		assertTrendRowDirection(t, out, name, "insufficient")
	}
	if !strings.Contains(out, "Intervention trend: insufficient evidence (1 session baseline, 2 sessions current; need at least 5 each).") ||
		strings.Contains(out, "New interventions:") ||
		strings.Contains(out, "Resolved interventions:") {
		t.Fatalf("sparse intervention trend was direction-labeled:\n%s", out)
	}
}

func TestSessionTrendLabelsRatesWithAdequateDenominators(t *testing.T) {
	baseline := codexSessionInsightsReport{SchemaVersion: codexSessionInsightsSchemaVersion}
	current := codexSessionInsightsReport{SchemaVersion: codexSessionInsightsSchemaVersion}
	baseline.Summary = codexSessionInsightsSummary{
		codexAggregateMetrics: codexAggregateMetrics{
			Sessions:          10,
			CompletedSessions: 8,
			ToolCalls:         200,
			FailedToolCalls:   20,
		},
	}
	current.Summary = codexSessionInsightsSummary{
		codexAggregateMetrics: codexAggregateMetrics{
			Sessions:          10,
			CompletedSessions: 10,
			ToolCalls:         300,
			FailedToolCalls:   3,
		},
	}
	baseline.Outcomes.ToolUsingCompleted = 10
	current.Outcomes.ToolUsingCompleted = 10

	out, err := captureStdout(t, func() error {
		printSessionTrend(baseline, current, "adequate", false)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertTrendRowDirection(t, out, "completion ratio", "improved")
	assertTrendRowDirection(t, out, "tool roundtrips / session", "regressed")
	assertTrendRowDirection(t, out, "failures / 1k calls", "improved")
}

func assertTrendRowDirection(t *testing.T, output, name, direction string) {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, name) {
			if !strings.HasSuffix(strings.TrimSpace(line), direction) {
				t.Fatalf("%s row=%q want direction %q", name, line, direction)
			}
			return
		}
	}
	t.Fatalf("missing trend row %q:\n%s", name, output)
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
