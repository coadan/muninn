package main

import (
	"fmt"
	"math"
)

type trendMetric struct {
	Name              string
	Baseline          float64
	Current           float64
	LowerIsBetter     bool
	PercentageDisplay bool
}

func printSessionTrend(baseline, current codexSessionInsightsReport, checkpointName string) {
	base := baseline.Summary
	now := current.Summary
	metrics := []trendMetric{
		{
			Name:              "completion ratio",
			Baseline:          ratio(float64(base.CompletedSessions), float64(base.Sessions)),
			Current:           ratio(float64(now.CompletedSessions), float64(now.Sessions)),
			PercentageDisplay: true,
		},
		{
			Name:          "tool calls / session",
			Baseline:      ratio(float64(base.ToolCalls), float64(base.Sessions)),
			Current:       ratio(float64(now.ToolCalls), float64(now.Sessions)),
			LowerIsBetter: true,
		},
		{
			Name:          "visible output tokens / call",
			Baseline:      ratio(float64(base.ToolOutputTokens), float64(base.ToolCalls)),
			Current:       ratio(float64(now.ToolOutputTokens), float64(now.ToolCalls)),
			LowerIsBetter: true,
		},
		{
			Name:          "failures / 1k calls",
			Baseline:      1000 * ratio(float64(base.FailedToolCalls), float64(base.ToolCalls)),
			Current:       1000 * ratio(float64(now.FailedToolCalls), float64(now.ToolCalls)),
			LowerIsBetter: true,
		},
		{
			Name:          "truncations / 1k calls",
			Baseline:      1000 * ratio(float64(base.TruncatedToolCalls), float64(base.ToolCalls)),
			Current:       1000 * ratio(float64(now.TruncatedToolCalls), float64(now.ToolCalls)),
			LowerIsBetter: true,
		},
		{
			Name:          "compactions / session",
			Baseline:      ratio(float64(base.Compactions), float64(base.Sessions)),
			Current:       ratio(float64(now.Compactions), float64(now.Sessions)),
			LowerIsBetter: true,
		},
	}
	fmt.Printf("\nTrend from checkpoint %q:\n", checkpointName)
	fmt.Printf("%-32s %12s %12s %12s\n", "RATE", "BASELINE", "CURRENT", "CHANGE")
	for _, metric := range metrics {
		change := metric.Current - metric.Baseline
		direction := "unchanged"
		const epsilon = 0.0001
		if math.Abs(change) > epsilon {
			improved := change > 0
			if metric.LowerIsBetter {
				improved = change < 0
			}
			if improved {
				direction = "improved"
			} else {
				direction = "regressed"
			}
		}
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
}

func ratio(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	return numerator / denominator
}
