package main

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
}

func validateSessionTrendComparison(
	baseline,
	current codexSessionInsightsReport,
	checkpointName string,
) error {
	baseScope, err := normalizedTrendScope(baseline)
	if err != nil {
		return fmt.Errorf("checkpoint %q has invalid analysis scope: %w", checkpointName, err)
	}
	currentScope, err := normalizedTrendScope(current)
	if err != nil {
		return fmt.Errorf("current analysis has invalid scope: %w", err)
	}
	if baseScope.WindowKind != currentScope.WindowKind {
		return fmt.Errorf(
			"checkpoint %q uses %s, but current analysis uses %s; rerun with the checkpoint's window mode or create a matched checkpoint",
			checkpointName,
			formatTrendWindowKind(baseScope.WindowKind),
			formatTrendWindowKind(currentScope.WindowKind),
		)
	}
	switch baseScope.WindowKind {
	case "lookback":
		if baseScope.LookbackSeconds != currentScope.LookbackSeconds {
			return fmt.Errorf(
				"checkpoint %q uses a %s lookback, but current analysis uses %s; rerun with --since %s or create a matched checkpoint",
				checkpointName,
				formatTrendLookback(baseScope.LookbackSeconds),
				formatTrendLookback(currentScope.LookbackSeconds),
				formatTrendLookback(baseScope.LookbackSeconds),
			)
		}
	case "since-commit":
		if baseline.Since != current.Since {
			return fmt.Errorf(
				"checkpoint %q and current analysis resolve to different --since-commit boundaries; rerun from the checkpoint's commit boundary or create a matched checkpoint",
				checkpointName,
			)
		}
	default:
		return fmt.Errorf("unsupported window kind %q", baseScope.WindowKind)
	}
	if baseScope.Task != currentScope.Task {
		return fmt.Errorf(
			"checkpoint %q uses task scope %s, but current analysis uses %s; rerun with the same --task scope or create a matched checkpoint",
			checkpointName,
			formatTrendScopeValue(baseScope.Task, "all tasks"),
			formatTrendScopeValue(currentScope.Task, "all tasks"),
		)
	}
	if baseScope.IncludeArchived != currentScope.IncludeArchived {
		return fmt.Errorf(
			"checkpoint %q and current analysis differ on --include-archived; rerun with the same archive scope or create a matched checkpoint",
			checkpointName,
		)
	}
	if baseScope.Focus != currentScope.Focus {
		return fmt.Errorf(
			"checkpoint %q uses finding focus %s, but current analysis uses %s; rerun with the same --focus scope or create a matched checkpoint",
			checkpointName,
			formatTrendScopeValue(baseScope.Focus, "all findings"),
			formatTrendScopeValue(currentScope.Focus, "all findings"),
		)
	}
	return nil
}

func normalizedTrendScope(report codexSessionInsightsReport) (sessionAnalysisScope, error) {
	scope := report.AnalysisScope
	if scope.WindowKind != "" {
		if scope.WindowKind == "lookback" && scope.LookbackSeconds <= 0 {
			return sessionAnalysisScope{}, fmt.Errorf("lookback must be positive")
		}
		scope.Focus = normalizeTrendFocus(scope.Focus)
		return scope, nil
	}
	generatedAt, err := time.Parse(time.RFC3339, report.GeneratedAt)
	if err != nil {
		return sessionAnalysisScope{}, fmt.Errorf("parse generated time: %w", err)
	}
	since, err := time.Parse(time.RFC3339, report.Since)
	if err != nil {
		return sessionAnalysisScope{}, fmt.Errorf("parse since time: %w", err)
	}
	if !generatedAt.After(since) {
		return sessionAnalysisScope{}, fmt.Errorf("analysis window is not positive")
	}
	return sessionAnalysisScope{
		WindowKind:      "lookback",
		LookbackSeconds: int64(generatedAt.Sub(since) / time.Second),
	}, nil
}

func normalizeTrendFocus(focus string) string {
	focus = strings.ToLower(strings.TrimSpace(focus))
	if focus == "friction" {
		return ""
	}
	return focus
}

func formatTrendWindowKind(kind string) string {
	if kind == "since-commit" {
		return "--since-commit"
	}
	return "a rolling lookback"
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

func formatTrendScopeValue(value, empty string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = empty
	}
	return fmt.Sprintf("%q", value)
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
	if baseline.SchemaVersion >= 28 && current.SchemaVersion >= 28 {
		metrics = append(metrics, trendMetric{
			Name:          "root instruction tokens",
			Baseline:      float64(baseline.Instructions.RootEstimatedTokens),
			Current:       float64(current.Instructions.RootEstimatedTokens),
			LowerIsBetter: true,
		})
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
