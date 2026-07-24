package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type ownedOperationDrilldownRow struct {
	Operation      string                            `json:"operation"`
	Metrics        codexOwnedOperationMetrics        `json:"metrics"`
	FailureReasons map[string]codexOccurrenceMetrics `json:"failureReasons,omitempty"`
}

type ownedOperationsDrilldown struct {
	SchemaVersion  int                          `json:"schemaVersion"`
	Provider       string                       `json:"provider"`
	Since          string                       `json:"since"`
	Tool           string                       `json:"tool"`
	Sessions       int                          `json:"sessions"`
	ToolCalls      int                          `json:"toolCalls"`
	Operations     []ownedOperationDrilldownRow `json:"operations"`
	Recommendation string                       `json:"recommendation,omitempty"`
}

func buildOwnedOperationsDrilldown(
	report codexSessionInsightsReport,
	config repositoryConfig,
	toolID string,
	limit int,
) (ownedOperationsDrilldown, error) {
	toolID = strings.TrimSpace(toolID)
	if toolID == "" {
		return ownedOperationsDrilldown{}, errors.New("--operations requires a locally owned tool ID")
	}
	var selected *ownedToolConfig
	for index := range config.OwnedTools {
		if config.OwnedTools[index].ID == toolID {
			selected = &config.OwnedTools[index]
			break
		}
	}
	if selected == nil {
		available := make([]string, 0, len(config.OwnedTools))
		for _, tool := range config.OwnedTools {
			available = append(available, tool.ID)
		}
		sort.Strings(available)
		if len(available) == 0 {
			return ownedOperationsDrilldown{}, errors.New("repository config has no locally owned tools")
		}
		return ownedOperationsDrilldown{}, fmt.Errorf(
			"unknown --operations tool %q (available: %s)",
			toolID,
			strings.Join(available, ", "),
		)
	}

	prefix := toolID + "/"
	rows := make([]ownedOperationDrilldownRow, 0)
	for operation, metrics := range report.Summary.OwnedOperations {
		if !strings.HasPrefix(operation, prefix) {
			continue
		}
		rows = append(rows, ownedOperationDrilldownRow{
			Operation:      operation,
			Metrics:        metrics,
			FailureReasons: report.Summary.OwnedOperationFailureReasons[operation],
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.FailedCalls != rows[j].Metrics.FailedCalls {
			return rows[i].Metrics.FailedCalls > rows[j].Metrics.FailedCalls
		}
		if rows[i].Metrics.TruncatedCalls != rows[j].Metrics.TruncatedCalls {
			return rows[i].Metrics.TruncatedCalls > rows[j].Metrics.TruncatedCalls
		}
		if rows[i].Metrics.OutputBytes != rows[j].Metrics.OutputBytes {
			return rows[i].Metrics.OutputBytes > rows[j].Metrics.OutputBytes
		}
		if rows[i].Metrics.Calls != rows[j].Metrics.Calls {
			return rows[i].Metrics.Calls > rows[j].Metrics.Calls
		}
		return rows[i].Operation < rows[j].Operation
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return ownedOperationsDrilldown{
		SchemaVersion:  codexSessionInsightsSchemaVersion,
		Provider:       report.Provider,
		Since:          report.Since,
		Tool:           toolID,
		Sessions:       report.Summary.Sessions,
		ToolCalls:      report.Summary.OwnedTooling[toolID].Calls,
		Operations:     rows,
		Recommendation: strings.TrimSpace(selected.Recommendation),
	}, nil
}

func printOwnedOperationsDrilldown(report ownedOperationsDrilldown) {
	fmt.Printf("Muninn locally owned operations since %s\n", report.Since)
	fmt.Printf("Provider: %s\n", report.Provider)
	fmt.Printf(
		"Tool: %s · scope %s sessions / %s tool calls\n",
		report.Tool,
		formatCodexCount(int64(report.Sessions)),
		formatCodexCount(int64(report.ToolCalls)),
	)
	metrics := make(map[string]codexOwnedOperationMetrics, len(report.Operations))
	for _, row := range report.Operations {
		metrics[row.Operation] = row.Metrics
	}
	if len(metrics) == 0 {
		fmt.Println("\nNo configured operations were observed for this tool.")
	} else {
		printOwnedOperations(metrics, 0)
	}
	printOwnedOperationFailureLabels(report.Operations)
	if report.Recommendation != "" {
		fmt.Printf("\nNext: %s\n", report.Recommendation)
	}
}

func printOwnedOperationFailureLabels(rows []ownedOperationDrilldownRow) {
	printedHeader := false
	for _, row := range rows {
		type reasonRow struct {
			label    string
			count    int
			sessions int
		}
		reasons := make([]reasonRow, 0, len(row.FailureReasons))
		for label, metrics := range row.FailureReasons {
			if metrics.Count <= 0 {
				continue
			}
			reasons = append(reasons, reasonRow{
				label:    label,
				count:    metrics.Count,
				sessions: metrics.Sessions,
			})
		}
		sort.Slice(reasons, func(i, j int) bool {
			if reasons[i].count != reasons[j].count {
				return reasons[i].count > reasons[j].count
			}
			if reasons[i].sessions != reasons[j].sessions {
				return reasons[i].sessions > reasons[j].sessions
			}
			return reasons[i].label < reasons[j].label
		})
		if len(reasons) > 3 {
			reasons = reasons[:3]
		}
		if len(reasons) == 0 {
			continue
		}
		if !printedHeader {
			fmt.Println("\nObserved fixed failure labels:")
			printedHeader = true
		}
		for _, reason := range reasons {
			callLabel := "calls"
			if reason.count == 1 {
				callLabel = "call"
			}
			sessionLabel := "sessions"
			if reason.sessions == 1 {
				sessionLabel = "session"
			}
			fmt.Printf(
				"- %s: %s (%s %s / %s %s)\n",
				row.Operation,
				reason.label,
				formatCodexCount(int64(reason.count)),
				callLabel,
				formatCodexCount(int64(reason.sessions)),
				sessionLabel,
			)
		}
	}
}
