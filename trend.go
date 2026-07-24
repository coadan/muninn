package main

import (
	"fmt"
	"math"
	"sort"
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
		{
			Name:              "downstream delivery failures",
			Baseline:          ratio(float64(base.DownstreamQuality.DeliveriesWithFailure), float64(base.DownstreamQuality.Deliveries)),
			Current:           ratio(float64(now.DownstreamQuality.DeliveriesWithFailure), float64(now.DownstreamQuality.Deliveries)),
			LowerIsBetter:     true,
			PercentageDisplay: true,
		},
		{
			Name:              "successful recovery redeliveries",
			Baseline:          ratio(float64(base.DownstreamQuality.RecoveredDeliveries), float64(base.DownstreamQuality.RedeliveryAttempts)),
			Current:           ratio(float64(now.DownstreamQuality.RecoveredDeliveries), float64(now.DownstreamQuality.RedeliveryAttempts)),
			PercentageDisplay: true,
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
	printFindingTrend(baseline.Findings, current.Findings)
}

func ratio(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	return numerator / denominator
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
