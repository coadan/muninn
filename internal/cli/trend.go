package cli

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type trendMetric struct {
	Name              string
	Baseline          float64
	Current           float64
	LowerIsBetter     bool
	PercentageDisplay bool
	BaselineSample    int
	CurrentSample     int
	MinimumSample     int
}

func previousLookbackWindow(
	currentSince,
	currentUntil time.Time,
	lookbackSeconds int64,
) (time.Time, time.Time, error) {
	if lookbackSeconds <= 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("previous-period comparison requires a positive lookback")
	}
	if !currentUntil.After(currentSince) {
		return time.Time{}, time.Time{}, fmt.Errorf("current analysis window is not positive")
	}
	lookback := time.Duration(lookbackSeconds) * time.Second
	if currentUntil.Sub(currentSince) != lookback {
		return time.Time{}, time.Time{}, fmt.Errorf("current analysis window does not match its lookback")
	}
	return currentSince.Add(-lookback), currentSince, nil
}

func normalizeTrendFocus(focus string) string {
	focus = strings.ToLower(strings.TrimSpace(focus))
	if focus == "friction" {
		return ""
	}
	return focus
}

func formatTrendLookback(seconds int64) string {
	duration := time.Duration(seconds) * time.Second
	if duration > 0 && duration%(7*24*time.Hour) == 0 {
		return fmt.Sprintf("%dw", duration/(7*24*time.Hour))
	}
	if duration > 0 && duration%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", duration/(24*time.Hour))
	}
	return duration.String()
}

func printSessionTrend(
	baseline,
	current codexSessionInsightsReport,
	baselineLabel string,
	includeFindingTrend bool,
) {
	base := baseline.Summary
	now := current.Summary
	baseRework := base.DeliveryRework
	nowRework := now.DeliveryRework
	baseQuality := base.DownstreamQuality
	nowQuality := now.DownstreamQuality
	metrics := []trendMetric{
		{
			Name:              "completion ratio",
			Baseline:          ratio(float64(base.CompletedSessions), float64(base.Sessions)),
			Current:           ratio(float64(now.CompletedSessions), float64(now.Sessions)),
			PercentageDisplay: true,
			BaselineSample:    base.Sessions,
			CurrentSample:     now.Sessions,
			MinimumSample:     1,
		},
		{
			Name:           "tool roundtrips / session",
			Baseline:       ratio(float64(base.ToolCalls), float64(base.Sessions)),
			Current:        ratio(float64(now.ToolCalls), float64(now.Sessions)),
			LowerIsBetter:  true,
			BaselineSample: base.Sessions,
			CurrentSample:  now.Sessions,
			MinimumSample:  1,
		},
		{
			Name:           "cross-call transitions / task",
			Baseline:       ratio(float64(totalCrossCallTransitions(base.CrossCallTransitions)), float64(baseline.Outcomes.ToolUsingCompleted)),
			Current:        ratio(float64(totalCrossCallTransitions(now.CrossCallTransitions)), float64(current.Outcomes.ToolUsingCompleted)),
			LowerIsBetter:  true,
			BaselineSample: baseline.Outcomes.ToolUsingCompleted,
			CurrentSample:  current.Outcomes.ToolUsingCompleted,
			MinimumSample:  1,
		},
		{
			Name:           "repeated transitions / task",
			Baseline:       ratio(float64(repeatedCrossCallTransitions(base.CrossCallTransitions)), float64(baseline.Outcomes.ToolUsingCompleted)),
			Current:        ratio(float64(repeatedCrossCallTransitions(now.CrossCallTransitions)), float64(current.Outcomes.ToolUsingCompleted)),
			LowerIsBetter:  true,
			BaselineSample: baseline.Outcomes.ToolUsingCompleted,
			CurrentSample:  current.Outcomes.ToolUsingCompleted,
			MinimumSample:  1,
		},
		{
			Name:           "rapid polls / task",
			Baseline:       ratio(float64(totalWaitCalls(base.RapidPolls)), float64(baseline.Outcomes.ToolUsingCompleted)),
			Current:        ratio(float64(totalWaitCalls(now.RapidPolls)), float64(current.Outcomes.ToolUsingCompleted)),
			LowerIsBetter:  true,
			BaselineSample: baseline.Outcomes.ToolUsingCompleted,
			CurrentSample:  current.Outcomes.ToolUsingCompleted,
			MinimumSample:  1,
		},
		{
			Name:           "progress stalls / task",
			Baseline:       ratio(float64(totalWaitCalls(base.ProgressStalls)), float64(baseline.Outcomes.ToolUsingCompleted)),
			Current:        ratio(float64(totalWaitCalls(now.ProgressStalls)), float64(current.Outcomes.ToolUsingCompleted)),
			LowerIsBetter:  true,
			BaselineSample: baseline.Outcomes.ToolUsingCompleted,
			CurrentSample:  current.Outcomes.ToolUsingCompleted,
			MinimumSample:  1,
		},
		{
			Name:           "visible output tokens / call",
			Baseline:       ratio(float64(base.ToolOutputTokens), float64(base.ToolCalls)),
			Current:        ratio(float64(now.ToolOutputTokens), float64(now.ToolCalls)),
			LowerIsBetter:  true,
			BaselineSample: base.ToolCalls,
			CurrentSample:  now.ToolCalls,
			MinimumSample:  1,
		},
		{
			Name:           "failures / 1k calls",
			Baseline:       1000 * ratio(float64(base.FailedToolCalls), float64(base.ToolCalls)),
			Current:        1000 * ratio(float64(now.FailedToolCalls), float64(now.ToolCalls)),
			LowerIsBetter:  true,
			BaselineSample: base.ToolCalls,
			CurrentSample:  now.ToolCalls,
			MinimumSample:  1,
		},
		{
			Name:           "truncations / 1k calls",
			Baseline:       1000 * ratio(float64(base.TruncatedToolCalls), float64(base.ToolCalls)),
			Current:        1000 * ratio(float64(now.TruncatedToolCalls), float64(now.ToolCalls)),
			LowerIsBetter:  true,
			BaselineSample: base.ToolCalls,
			CurrentSample:  now.ToolCalls,
			MinimumSample:  1,
		},
		{
			Name:           "compactions / session",
			Baseline:       ratio(float64(base.Compactions), float64(base.Sessions)),
			Current:        ratio(float64(now.Compactions), float64(now.Sessions)),
			LowerIsBetter:  true,
			BaselineSample: base.Sessions,
			CurrentSample:  now.Sessions,
			MinimumSample:  1,
		},
		{
			Name:              "pre-delivery test evidence",
			Baseline:          ratio(float64(baseRework.DeliveriesWithPreTests), float64(baseRework.Deliveries)),
			Current:           ratio(float64(nowRework.DeliveriesWithPreTests), float64(nowRework.Deliveries)),
			PercentageDisplay: true,
			BaselineSample:    baseRework.Deliveries,
			CurrentSample:     nowRework.Deliveries,
			MinimumSample:     10,
		},
		{
			Name:              "deliveries needing review fixes",
			Baseline:          ratio(float64(baseRework.DeliveriesWithRework), float64(baseRework.Deliveries)),
			Current:           ratio(float64(nowRework.DeliveriesWithRework), float64(nowRework.Deliveries)),
			LowerIsBetter:     true,
			PercentageDisplay: true,
			BaselineSample:    baseRework.Deliveries,
			CurrentSample:     nowRework.Deliveries,
			MinimumSample:     10,
		},
		{
			Name:           "review-to-edit cycles / delivery",
			Baseline:       ratio(float64(baseRework.ReviewToEditCycles), float64(baseRework.Deliveries)),
			Current:        ratio(float64(nowRework.ReviewToEditCycles), float64(nowRework.Deliveries)),
			LowerIsBetter:  true,
			BaselineSample: baseRework.Deliveries,
			CurrentSample:  nowRework.Deliveries,
			MinimumSample:  10,
		},
		{
			Name:              "downstream delivery failures",
			Baseline:          ratio(float64(baseQuality.DeliveriesWithFailure), float64(baseQuality.Deliveries)),
			Current:           ratio(float64(nowQuality.DeliveriesWithFailure), float64(nowQuality.Deliveries)),
			LowerIsBetter:     true,
			PercentageDisplay: true,
			BaselineSample:    baseQuality.Deliveries,
			CurrentSample:     nowQuality.Deliveries,
			MinimumSample:     10,
		},
		{
			Name:           "follow-up edits / delivery",
			Baseline:       ratio(float64(baseQuality.FollowUpEditCycles), float64(baseQuality.Deliveries)),
			Current:        ratio(float64(nowQuality.FollowUpEditCycles), float64(nowQuality.Deliveries)),
			LowerIsBetter:  true,
			BaselineSample: baseQuality.Deliveries,
			CurrentSample:  nowQuality.Deliveries,
			MinimumSample:  10,
		},
		{
			Name:              "reverted deliveries",
			Baseline:          ratio(float64(baseQuality.Reverts), float64(baseQuality.Deliveries)),
			Current:           ratio(float64(nowQuality.Reverts), float64(nowQuality.Deliveries)),
			LowerIsBetter:     true,
			PercentageDisplay: true,
			BaselineSample:    baseQuality.Deliveries,
			CurrentSample:     nowQuality.Deliveries,
			MinimumSample:     10,
		},
		{
			Name:              "successful recovery redeliveries",
			Baseline:          ratio(float64(baseQuality.RecoveredDeliveries), float64(baseQuality.RedeliveryAttempts)),
			Current:           ratio(float64(nowQuality.RecoveredDeliveries), float64(nowQuality.RedeliveryAttempts)),
			PercentageDisplay: true,
			BaselineSample:    baseQuality.RedeliveryAttempts,
			CurrentSample:     nowQuality.RedeliveryAttempts,
			MinimumSample:     10,
		},
	}
	if baseline.SchemaVersion >= 28 && current.SchemaVersion >= 28 {
		metrics = append(metrics, trendMetric{
			Name:          "root instruction tokens",
			Baseline:      float64(baseline.Instructions.RootEstimatedTokens),
			Current:       float64(current.Instructions.RootEstimatedTokens),
			LowerIsBetter: true,
		})
	}
	fmt.Printf("\nPerformance comparison against %q:\n", baselineLabel)
	printCompletedTaskTrend(baseline, current)
	matchedMetrics := printMatchedPerformanceTrend(baseline, current)
	matchedQuality := printMatchedQualityTrend(baseline, current)
	fmt.Printf("\nSession and quality rates:\n")
	fmt.Printf("%-32s %12s %12s %12s\n", "RATE", "BASELINE", "CURRENT", "CHANGE")
	for _, metric := range metrics {
		direction := materialTrendDirection(metric)
		if metric.PercentageDisplay {
			fmt.Printf("%-32s %11.1f%% %11.1f%% %12s\n",
				metric.Name,
				100*metric.Baseline,
				100*metric.Current,
				direction,
			)
			continue
		}
		fmt.Printf("%-32s %12.1f %12.1f %12s\n",
			metric.Name,
			metric.Baseline,
			metric.Current,
			direction,
		)
	}
	fmt.Printf(
		"Quality-adjusted verdict: %s\n",
		qualityAdjustedPerformanceVerdict(baseline, current, matchedMetrics, matchedQuality),
	)
	printDiagnosticEffectiveness(baseline.Diagnostics, current.Diagnostics)
	printInterventionTrend(baseline.Interventions, current.Interventions)
	if includeFindingTrend {
		printFindingTrend(baseline.Findings, current.Findings)
	}
}

type matchedQualityCohort struct {
	Baseline taskQualityCohort
	Current  taskQualityCohort
}

func matchedQualityCohorts(
	baseline,
	current []taskQualityCohort,
	performance []matchedPerformanceCohort,
	minimumDeliveries int,
) []matchedQualityCohort {
	performanceKeys := map[string]struct{}{}
	for _, cohort := range performance {
		performanceKeys[taskPerformanceCohortKey(cohort.Baseline)] = struct{}{}
	}
	currentByKey := map[string]taskQualityCohort{}
	for _, cohort := range current {
		currentByKey[taskQualityCohortKey(cohort)] = cohort
	}
	var matched []matchedQualityCohort
	for _, base := range baseline {
		key := taskQualityCohortKey(base)
		if _, matchedPerformance := performanceKeys[key]; !matchedPerformance {
			continue
		}
		now, ok := currentByKey[key]
		if !ok || base.Deliveries < minimumDeliveries || now.Deliveries < minimumDeliveries {
			continue
		}
		matched = append(matched, matchedQualityCohort{Baseline: base, Current: now})
	}
	sort.Slice(matched, func(i, j int) bool {
		left := min(matched[i].Baseline.Deliveries, matched[i].Current.Deliveries)
		right := min(matched[j].Baseline.Deliveries, matched[j].Current.Deliveries)
		if left != right {
			return left > right
		}
		return taskQualityCohortKey(matched[i].Baseline) <
			taskQualityCohortKey(matched[j].Baseline)
	})
	return matched
}

func matchedQualityTrendMetrics(matched []matchedQualityCohort) []trendMetric {
	if len(matched) == 0 {
		return nil
	}
	type qualityMetric struct {
		name string
		get  func(taskQualityCohort) int
	}
	definitions := []qualityMetric{
		{"matched review-fix rate", func(cohort taskQualityCohort) int { return cohort.ReviewFixes }},
		{"matched downstream-failure rate", func(cohort taskQualityCohort) int { return cohort.DownstreamFailure }},
		{"matched follow-up-edit rate", func(cohort taskQualityCohort) int { return cohort.FollowUpEdits }},
		{"matched revert rate", func(cohort taskQualityCohort) int { return cohort.Reverts }},
	}
	metrics := make([]trendMetric, 0, len(definitions))
	for _, definition := range definitions {
		var baselineRate, currentRate float64
		for _, cohort := range matched {
			baselineRate += ratio(
				float64(definition.get(cohort.Baseline)),
				float64(cohort.Baseline.Deliveries),
			)
			currentRate += ratio(
				float64(definition.get(cohort.Current)),
				float64(cohort.Current.Deliveries),
			)
		}
		metrics = append(metrics, trendMetric{
			Name:              definition.name,
			Baseline:          baselineRate / float64(len(matched)),
			Current:           currentRate / float64(len(matched)),
			LowerIsBetter:     true,
			PercentageDisplay: true,
		})
	}
	return metrics
}

func printMatchedQualityTrend(baseline, current codexSessionInsightsReport) []trendMetric {
	performance := matchedPerformanceCohorts(
		baseline.Outcomes.PerformanceCohorts,
		current.Outcomes.PerformanceCohorts,
		3,
	)
	matched := matchedQualityCohorts(
		baseline.Outcomes.QualityCohorts,
		current.Outcomes.QualityCohorts,
		performance,
		5,
	)
	if len(matched) == 0 {
		fmt.Println("Matched quality cohorts: insufficient shared delivery evidence.")
		return nil
	}
	fmt.Println("\nMatched delivery quality (minimum 5 deliveries in each period):")
	fmt.Printf("%-42s %11s %15s %15s\n", "COHORT", "DELIVERIES", "REVIEW FIX B→C", "FAILURE B→C")
	rows := matched
	if len(rows) > 8 {
		rows = rows[:8]
	}
	for _, row := range rows {
		label := strings.Join([]string{
			row.Baseline.AgentKind,
			row.Baseline.Model + "/" + row.Baseline.ReasoningEffort,
			row.Baseline.TaskFamily,
		}, " ")
		fmt.Printf(
			"%-42s %5d→%-5d %6.1f%%→%-6.1f%% %6.1f%%→%-6.1f%%\n",
			truncateCodexLabel(label, 42),
			row.Baseline.Deliveries,
			row.Current.Deliveries,
			100*ratio(float64(row.Baseline.ReviewFixes), float64(row.Baseline.Deliveries)),
			100*ratio(float64(row.Current.ReviewFixes), float64(row.Current.Deliveries)),
			100*ratio(float64(row.Baseline.DownstreamFailure), float64(row.Baseline.Deliveries)),
			100*ratio(float64(row.Current.DownstreamFailure), float64(row.Current.Deliveries)),
		)
	}
	metrics := matchedQualityTrendMetrics(matched)
	label, improved, regressed, unchanged, _ := summarizeQualityDirections(metrics)
	fmt.Printf(
		"Matched quality direction: %s (%d improved, %d regressed, %d unchanged across equal-weight cohort rates).\n",
		label,
		improved,
		regressed,
		unchanged,
	)
	return metrics
}

type matchedPerformanceCohort struct {
	Baseline taskPerformanceCohort
	Current  taskPerformanceCohort
}

func matchedPerformanceCohorts(
	baseline,
	current []taskPerformanceCohort,
	minimumTasks int,
) []matchedPerformanceCohort {
	currentByKey := map[string]taskPerformanceCohort{}
	for _, cohort := range current {
		currentByKey[taskPerformanceCohortKey(cohort)] = cohort
	}
	var matched []matchedPerformanceCohort
	for _, base := range baseline {
		now, ok := currentByKey[taskPerformanceCohortKey(base)]
		if !ok || base.CompletedTasks < minimumTasks || now.CompletedTasks < minimumTasks {
			continue
		}
		matched = append(matched, matchedPerformanceCohort{Baseline: base, Current: now})
	}
	sort.Slice(matched, func(i, j int) bool {
		left := min(matched[i].Baseline.CompletedTasks, matched[i].Current.CompletedTasks)
		right := min(matched[j].Baseline.CompletedTasks, matched[j].Current.CompletedTasks)
		if left != right {
			return left > right
		}
		return taskPerformanceCohortKey(matched[i].Baseline) <
			taskPerformanceCohortKey(matched[j].Baseline)
	})
	return matched
}

func matchedPerformanceTrendMetrics(matched []matchedPerformanceCohort) []trendMetric {
	if len(matched) == 0 {
		return nil
	}
	var baseRoundtrips, nowRoundtrips, baseDuration, nowDuration []int64
	for _, cohort := range matched {
		baseRoundtrips = append(baseRoundtrips, cohort.Baseline.ToolRoundtrips.P50)
		nowRoundtrips = append(nowRoundtrips, cohort.Current.ToolRoundtrips.P50)
		baseDuration = append(baseDuration, cohort.Baseline.DurationSeconds.P50)
		nowDuration = append(nowDuration, cohort.Current.DurationSeconds.P50)
	}
	return []trendMetric{
		{
			Name:          "matched cohort median roundtrips",
			Baseline:      float64(summarizeOutcomeDistribution(baseRoundtrips).P50),
			Current:       float64(summarizeOutcomeDistribution(nowRoundtrips).P50),
			LowerIsBetter: true,
		},
		{
			Name:          "matched cohort median duration",
			Baseline:      float64(summarizeOutcomeDistribution(baseDuration).P50),
			Current:       float64(summarizeOutcomeDistribution(nowDuration).P50),
			LowerIsBetter: true,
		},
	}
}

func printMatchedPerformanceTrend(
	baseline,
	current codexSessionInsightsReport,
) []trendMetric {
	matched := matchedPerformanceCohorts(
		baseline.Outcomes.PerformanceCohorts,
		current.Outcomes.PerformanceCohorts,
		3,
	)
	if len(matched) == 0 {
		fmt.Println("Matched performance cohorts: insufficient shared model/effort/task-family evidence.")
		return nil
	}
	fmt.Println("\nMatched model/effort/task-family cohorts (minimum 3 completed tasks in each period):")
	fmt.Printf(
		"%-42s %11s %13s %13s\n",
		"COHORT",
		"TASKS B→C",
		"RT P50 B→C",
		"TIME P50 B→C",
	)
	rows := matched
	if len(rows) > 8 {
		rows = rows[:8]
	}
	for _, row := range rows {
		label := strings.Join(
			[]string{
				row.Baseline.AgentKind,
				row.Baseline.Model + "/" + row.Baseline.ReasoningEffort,
				row.Baseline.TaskFamily,
			},
			" ",
		)
		fmt.Printf(
			"%-42s %5d→%-5d %6d→%-6d %7s→%-7s\n",
			truncateCodexLabel(label, 42),
			row.Baseline.CompletedTasks,
			row.Current.CompletedTasks,
			row.Baseline.ToolRoundtrips.P50,
			row.Current.ToolRoundtrips.P50,
			formatDurationSeconds(row.Baseline.DurationSeconds.P50),
			formatDurationSeconds(row.Current.DurationSeconds.P50),
		)
	}
	metrics := matchedPerformanceTrendMetrics(matched)
	label, improved, regressed, unchanged := summarizeTrendDirections(metrics)
	fmt.Printf(
		"Matched efficiency direction: %s (%d improved, %d regressed, %d unchanged across equal-weight cohort medians).\n",
		label,
		improved,
		regressed,
		unchanged,
	)
	return metrics
}

func qualityAdjustedPerformanceVerdict(
	baseline,
	current codexSessionInsightsReport,
	matchedEfficiency []trendMetric,
	matchedQuality []trendMetric,
) string {
	efficiency := matchedEfficiency
	if len(efficiency) == 0 &&
		baseline.Outcomes.ToolUsingCompleted >= 10 &&
		current.Outcomes.ToolUsingCompleted >= 10 {
		efficiency = []trendMetric{
			{
				Name:          "tool roundtrips p50",
				Baseline:      float64(baseline.Outcomes.ToolCalls.P50),
				Current:       float64(current.Outcomes.ToolCalls.P50),
				LowerIsBetter: true,
			},
			{
				Name:          "duration seconds p50",
				Baseline:      float64(baseline.Outcomes.DurationSeconds.P50),
				Current:       float64(current.Outcomes.DurationSeconds.P50),
				LowerIsBetter: true,
			},
		}
	}
	efficiencyLabel, _, efficiencyRegressed, _ := summarizeTrendDirections(efficiency)
	quality := matchedQuality
	if len(quality) == 0 {
		quality = qualityOutcomeTrendMetrics(baseline.Summary, current.Summary)
	}
	qualityLabel, qualityImproved, qualityRegressed, qualityUnchanged, qualityInsufficient :=
		summarizeQualityDirections(quality)
	if len(efficiency) == 0 || qualityInsufficient == len(quality) {
		return "insufficient evidence"
	}
	switch {
	case efficiencyLabel == "improved" && qualityRegressed == 0:
		return "improved — faster with stable or better observed delivery quality"
	case efficiencyLabel == "improved" && qualityRegressed > 0:
		return "shifted downstream — faster execution but worse observed delivery quality"
	case efficiencyRegressed > 0 && qualityLabel == "improved":
		return "quality tradeoff — slower execution with better observed delivery quality"
	case efficiencyRegressed > 0 && qualityRegressed > 0:
		return "regressed — slower execution and worse observed delivery quality"
	case efficiencyLabel == "unchanged" && qualityImproved > 0 && qualityRegressed == 0:
		return "improved quality at stable execution cost"
	case efficiencyLabel == "unchanged" && qualityUnchanged == len(quality)-qualityInsufficient:
		return "unchanged"
	default:
		return "mixed or inconclusive"
	}
}

func qualityOutcomeTrendMetrics(
	baseline,
	current codexSessionInsightsSummary,
) []trendMetric {
	baseRework := baseline.DeliveryRework
	nowRework := current.DeliveryRework
	baseQuality := baseline.DownstreamQuality
	nowQuality := current.DownstreamQuality
	return []trendMetric{
		{
			Name:           "deliveries needing review fixes",
			Baseline:       ratio(float64(baseRework.DeliveriesWithRework), float64(baseRework.Deliveries)),
			Current:        ratio(float64(nowRework.DeliveriesWithRework), float64(nowRework.Deliveries)),
			LowerIsBetter:  true,
			BaselineSample: baseRework.Deliveries,
			CurrentSample:  nowRework.Deliveries,
			MinimumSample:  10,
		},
		{
			Name:           "downstream delivery failures",
			Baseline:       ratio(float64(baseQuality.DeliveriesWithFailure), float64(baseQuality.Deliveries)),
			Current:        ratio(float64(nowQuality.DeliveriesWithFailure), float64(nowQuality.Deliveries)),
			LowerIsBetter:  true,
			BaselineSample: baseQuality.Deliveries,
			CurrentSample:  nowQuality.Deliveries,
			MinimumSample:  10,
		},
		{
			Name:           "follow-up edits",
			Baseline:       ratio(float64(baseQuality.FollowUpEditCycles), float64(baseQuality.Deliveries)),
			Current:        ratio(float64(nowQuality.FollowUpEditCycles), float64(nowQuality.Deliveries)),
			LowerIsBetter:  true,
			BaselineSample: baseQuality.Deliveries,
			CurrentSample:  nowQuality.Deliveries,
			MinimumSample:  10,
		},
		{
			Name:           "reverts",
			Baseline:       ratio(float64(baseQuality.Reverts), float64(baseQuality.Deliveries)),
			Current:        ratio(float64(nowQuality.Reverts), float64(nowQuality.Deliveries)),
			LowerIsBetter:  true,
			BaselineSample: baseQuality.Deliveries,
			CurrentSample:  nowQuality.Deliveries,
			MinimumSample:  10,
		},
	}
}

func summarizeQualityDirections(
	metrics []trendMetric,
) (label string, improved, regressed, unchanged, insufficient int) {
	for _, metric := range metrics {
		switch materialTrendDirection(metric) {
		case "improved":
			improved++
		case "regressed":
			regressed++
		case "insufficient":
			insufficient++
		default:
			unchanged++
		}
	}
	switch {
	case improved > 0 && regressed == 0:
		label = "improved"
	case regressed > 0 && improved == 0:
		label = "regressed"
	case improved > 0 && regressed > 0:
		label = "mixed"
	default:
		label = "unchanged"
	}
	return
}

func completedTaskTrendMetrics(baseline, current codexSessionInsightsReport) []trendMetric {
	base := baseline.Outcomes
	now := current.Outcomes
	if base.ToolUsingCompleted == 0 || now.ToolUsingCompleted == 0 {
		return nil
	}
	var metrics []trendMetric
	if baseline.SchemaVersion >= 30 && current.SchemaVersion >= 30 {
		metrics = append(metrics,
			trendMetric{Name: "cached input p50", Baseline: float64(base.CachedInputTokens.P50), Current: float64(now.CachedInputTokens.P50), LowerIsBetter: true},
			trendMetric{Name: "cached input p90", Baseline: float64(base.CachedInputTokens.P90), Current: float64(now.CachedInputTokens.P90), LowerIsBetter: true},
			trendMetric{Name: "uncached input p50", Baseline: float64(base.UncachedInputTokens.P50), Current: float64(now.UncachedInputTokens.P50), LowerIsBetter: true},
			trendMetric{Name: "uncached input p90", Baseline: float64(base.UncachedInputTokens.P90), Current: float64(now.UncachedInputTokens.P90), LowerIsBetter: true},
			trendMetric{Name: "model output p50", Baseline: float64(base.ModelOutputTokens.P50), Current: float64(now.ModelOutputTokens.P50), LowerIsBetter: true},
			trendMetric{Name: "model output p90", Baseline: float64(base.ModelOutputTokens.P90), Current: float64(now.ModelOutputTokens.P90), LowerIsBetter: true},
		)
	} else {
		metrics = append(metrics,
			trendMetric{Name: "fresh tokens p50", Baseline: float64(base.FreshTokens.P50), Current: float64(now.FreshTokens.P50), LowerIsBetter: true},
			trendMetric{Name: "fresh tokens p90", Baseline: float64(base.FreshTokens.P90), Current: float64(now.FreshTokens.P90), LowerIsBetter: true},
		)
	}
	return append(metrics,
		trendMetric{Name: "tool roundtrips p50", Baseline: float64(base.ToolCalls.P50), Current: float64(now.ToolCalls.P50), LowerIsBetter: true},
		trendMetric{Name: "tool roundtrips p90", Baseline: float64(base.ToolCalls.P90), Current: float64(now.ToolCalls.P90), LowerIsBetter: true},
		trendMetric{Name: "duration seconds p50", Baseline: float64(base.DurationSeconds.P50), Current: float64(now.DurationSeconds.P50), LowerIsBetter: true},
		trendMetric{Name: "duration seconds p90", Baseline: float64(base.DurationSeconds.P90), Current: float64(now.DurationSeconds.P90), LowerIsBetter: true},
		trendMetric{Name: "failed calls p90", Baseline: float64(base.FailedCalls.P90), Current: float64(now.FailedCalls.P90), LowerIsBetter: true},
		trendMetric{Name: "compactions p90", Baseline: float64(base.Compactions.P90), Current: float64(now.Compactions.P90), LowerIsBetter: true},
	)
}

func totalCrossCallTransitions(transitions map[string]codexTransitionMetrics) int {
	total := 0
	for _, metrics := range transitions {
		total += metrics.Count
	}
	return total
}

func repeatedCrossCallTransitions(transitions map[string]codexTransitionMetrics) int {
	total := 0
	for transition, metrics := range transitions {
		from, to, ok := strings.Cut(transition, " -> ")
		if ok && from == to {
			total += metrics.Count
		}
	}
	return total
}

func totalWaitCalls(waits map[string]codexWaitMetrics) int {
	total := 0
	for _, metrics := range waits {
		total += metrics.Calls
	}
	return total
}

func materialTrendDirection(metric trendMetric) string {
	if metric.MinimumSample > 0 &&
		(metric.BaselineSample < metric.MinimumSample || metric.CurrentSample < metric.MinimumSample) {
		return "insufficient"
	}
	change := metric.Current - metric.Baseline
	scale := math.Max(math.Abs(metric.Baseline), 1)
	if math.Abs(change)/scale < 0.01 {
		return "unchanged"
	}
	improved := change > 0
	if metric.LowerIsBetter {
		improved = change < 0
	}
	if improved {
		return "improved"
	}
	return "regressed"
}

func summarizeTrendDirections(metrics []trendMetric) (label string, improved, regressed, unchanged int) {
	for _, metric := range metrics {
		switch materialTrendDirection(metric) {
		case "improved":
			improved++
		case "regressed":
			regressed++
		default:
			unchanged++
		}
	}
	switch {
	case improved > 0 && regressed == 0:
		label = "improved"
	case regressed > 0 && improved == 0:
		label = "regressed"
	case improved > 0 && regressed > 0:
		label = "mixed"
	default:
		label = "unchanged"
	}
	return label, improved, regressed, unchanged
}

func printCompletedTaskTrend(baseline, current codexSessionInsightsReport) {
	metrics := completedTaskTrendMetrics(baseline, current)
	if len(metrics) == 0 {
		fmt.Printf("Completed-task direction: insufficient completed tool-task evidence.\n")
		return
	}
	if baseline.Outcomes.ToolUsingCompleted < 10 || current.Outcomes.ToolUsingCompleted < 10 {
		fmt.Printf(
			"Completed-task direction: insufficient evidence (%s baseline, %s current; need at least 10 each).\n",
			formatCodexCount(int64(baseline.Outcomes.ToolUsingCompleted)),
			formatCodexCount(int64(current.Outcomes.ToolUsingCompleted)),
		)
		return
	}
	fmt.Printf(
		"Completed-task cost (%s baseline → %s current; lower is better):\n",
		formatCodexCount(int64(baseline.Outcomes.ToolUsingCompleted)),
		formatCodexCount(int64(current.Outcomes.ToolUsingCompleted)),
	)
	fmt.Printf("%-32s %12s %12s %12s\n", "METRIC", "BASELINE", "CURRENT", "CHANGE")
	for _, metric := range metrics {
		fmt.Printf(
			"%-32s %12.0f %12.0f %12s\n",
			metric.Name,
			metric.Baseline,
			metric.Current,
			materialTrendDirection(metric),
		)
	}
	label, improved, regressed, unchanged := summarizeTrendDirections(metrics)
	fmt.Printf(
		"Observed completed-task direction: %s (%d improved, %d regressed, %d unchanged). Rolling task mix is observational, not causal.\n",
		label,
		improved,
		regressed,
		unchanged,
	)
	if baseline.SchemaVersion < 30 && current.SchemaVersion >= 30 {
		fmt.Printf("The older comparison schema cannot separate cached input, uncached input, and model output.\n")
	}
}

func ratio(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	return numerator / denominator
}

func printInterventionTrend(baseline, current []sessionIntervention) {
	baselineByID := map[string]sessionIntervention{}
	currentByID := map[string]sessionIntervention{}
	for _, intervention := range baseline {
		baselineByID[intervention.ID] = intervention
	}
	for _, intervention := range current {
		currentByID[intervention.ID] = intervention
	}
	var resolved, introduced, persistent []sessionIntervention
	for id, intervention := range baselineByID {
		if _, exists := currentByID[id]; exists {
			persistent = append(persistent, intervention)
		} else {
			resolved = append(resolved, intervention)
		}
	}
	for id, intervention := range currentByID {
		if _, exists := baselineByID[id]; !exists {
			introduced = append(introduced, intervention)
		}
	}
	fmt.Printf("\nIntervention trend: %s resolved, %s persistent, %s new.\n",
		formatCodexCount(int64(len(resolved))),
		formatCodexCount(int64(len(persistent))),
		formatCodexCount(int64(len(introduced))),
	)
	printInterventionTrendRows("Resolved", resolved, 4)
	printInterventionTrendRows("New", introduced, 4)
}

func printInterventionTrendRows(label string, interventions []sessionIntervention, limit int) {
	if len(interventions) == 0 {
		return
	}
	sort.Slice(interventions, func(i, j int) bool {
		leftPriority := interventionTrendPriority(interventions[i])
		rightPriority := interventionTrendPriority(interventions[j])
		if leftPriority != rightPriority {
			return leftPriority > rightPriority
		}
		if interventions[i].FindingCount != interventions[j].FindingCount {
			return interventions[i].FindingCount > interventions[j].FindingCount
		}
		if interventions[i].score != interventions[j].score {
			return interventions[i].score > interventions[j].score
		}
		return interventions[i].ID < interventions[j].ID
	})
	rows := interventions
	if len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Printf("%s interventions:\n", label)
	for _, intervention := range rows {
		fmt.Printf("- [%s priority] %s · %s\n",
			intervention.Priority,
			intervention.ID,
			intervention.Title,
		)
	}
	if len(rows) < len(interventions) {
		fmt.Printf("- ... %d more\n", len(interventions)-len(rows))
	}
}

func interventionTrendPriority(intervention sessionIntervention) int {
	if intervention.priority > 0 {
		return intervention.priority
	}
	switch intervention.Priority {
	case "highest":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	default:
		return 1
	}
}

func printFindingTrend(baseline, current []sessionFinding) {
	baselineByKey := map[string]sessionFinding{}
	currentByKey := map[string]sessionFinding{}
	for _, finding := range baseline {
		baselineByKey[findingTrendKey(finding)] = finding
	}
	for _, finding := range current {
		currentByKey[findingTrendKey(finding)] = finding
	}
	var resolved, introduced, persistent []sessionFinding
	for key, finding := range baselineByKey {
		if _, exists := currentByKey[key]; exists {
			persistent = append(persistent, finding)
		} else {
			resolved = append(resolved, finding)
		}
	}
	for key, finding := range currentByKey {
		if _, exists := baselineByKey[key]; !exists {
			introduced = append(introduced, finding)
		}
	}
	fmt.Printf("\nFinding trend: %s resolved, %s persistent, %s new.\n",
		formatCodexCount(int64(len(resolved))),
		formatCodexCount(int64(len(persistent))),
		formatCodexCount(int64(len(introduced))),
	)
	printFindingTrendRows("Resolved", resolved, 4)
	printFindingTrendRows("New", introduced, 4)
}

func findingTrendKey(finding sessionFinding) string {
	return finding.Category + "\x00" + finding.Title + "\x00" + finding.Target
}

func printFindingTrendRows(label string, findings []sessionFinding, limit int) {
	if len(findings) == 0 {
		return
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Category != findings[j].Category {
			return findings[i].Category < findings[j].Category
		}
		if findings[i].Target != findings[j].Target {
			return findings[i].Target < findings[j].Target
		}
		return findings[i].Title < findings[j].Title
	})
	rows := findings
	if len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Printf("%s findings:\n", label)
	for _, finding := range rows {
		target := ""
		if finding.Target != "" {
			target = " · " + finding.Target
		}
		fmt.Printf("- [%s] %s%s\n", finding.Category, finding.Title, target)
	}
	if len(rows) < len(findings) {
		fmt.Printf("- ... %d more\n", len(findings)-len(rows))
	}
}
