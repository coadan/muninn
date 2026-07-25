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

type ownedOperationTaskDrilldownRow struct {
	Operation string                     `json:"operation"`
	Task      string                     `json:"task"`
	Metrics   codexOwnedOperationMetrics `json:"metrics"`
}

type ownedOperationsDrilldown struct {
	SchemaVersion  int                              `json:"schemaVersion"`
	Provider       string                           `json:"provider"`
	Since          string                           `json:"since"`
	Tool           string                           `json:"tool"`
	Operation      string                           `json:"operation,omitempty"`
	Sessions       int                              `json:"sessions"`
	ToolCalls      int                              `json:"toolCalls"`
	Operations     []ownedOperationDrilldownRow     `json:"operations"`
	TaskCohorts    []ownedOperationTaskDrilldownRow `json:"taskCohorts,omitempty"`
	Recommendation string                           `json:"recommendation,omitempty"`
}

func buildOwnedOperationsDrilldown(
	report codexSessionInsightsReport,
	config repositoryConfig,
	selector string,
	limit int,
) (ownedOperationsDrilldown, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return ownedOperationsDrilldown{}, errors.New("--operations requires a locally owned tool ID")
	}
	toolID := selector
	operationFilter := ""
	if candidateTool, candidateOperation, found := strings.Cut(selector, "/"); found {
		toolID = candidateTool
		operationFilter = selector
		if strings.TrimSpace(candidateOperation) == "" {
			return ownedOperationsDrilldown{}, fmt.Errorf("invalid --operations selector %q", selector)
		}
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
	if operationFilter != "" {
		found := false
		for _, operation := range selected.Operations {
			if toolID+"/"+strings.TrimSpace(operation.ID) == operationFilter {
				found = true
				break
			}
		}
		if !found {
			return ownedOperationsDrilldown{}, fmt.Errorf(
				"unknown --operations operation %q for tool %q",
				operationFilter,
				toolID,
			)
		}
	}

	prefix := toolID + "/"
	rows := make([]ownedOperationDrilldownRow, 0)
	for operation, metrics := range report.Summary.OwnedOperations {
		if !strings.HasPrefix(operation, prefix) ||
			(operationFilter != "" && operation != operationFilter) {
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
	included := map[string]bool{}
	for _, row := range rows {
		included[row.Operation] = true
	}
	taskRows := make([]ownedOperationTaskDrilldownRow, 0)
	for operation, tasks := range report.operationTasks {
		if !included[operation] {
			continue
		}
		for task, metrics := range tasks {
			taskRows = append(taskRows, ownedOperationTaskDrilldownRow{
				Operation: operation,
				Task:      task,
				Metrics:   metrics,
			})
		}
	}
	sort.Slice(taskRows, func(i, j int) bool {
		if taskRows[i].Metrics.Calls != taskRows[j].Metrics.Calls {
			return taskRows[i].Metrics.Calls > taskRows[j].Metrics.Calls
		}
		if taskRows[i].Metrics.FailedCalls != taskRows[j].Metrics.FailedCalls {
			return taskRows[i].Metrics.FailedCalls > taskRows[j].Metrics.FailedCalls
		}
		if taskRows[i].Metrics.OutputBytes != taskRows[j].Metrics.OutputBytes {
			return taskRows[i].Metrics.OutputBytes > taskRows[j].Metrics.OutputBytes
		}
		if taskRows[i].Operation != taskRows[j].Operation {
			return taskRows[i].Operation < taskRows[j].Operation
		}
		return taskRows[i].Task < taskRows[j].Task
	})
	taskCohortLimit := limit
	if taskCohortLimit <= 0 || taskCohortLimit > 20 {
		taskCohortLimit = 20
	}
	if len(taskRows) > taskCohortLimit {
		taskRows = taskRows[:taskCohortLimit]
	}
	return ownedOperationsDrilldown{
		SchemaVersion:  codexSessionInsightsSchemaVersion,
		Provider:       report.Provider,
		Since:          report.Since,
		Tool:           toolID,
		Operation:      operationFilter,
		Sessions:       report.Summary.Sessions,
		ToolCalls:      report.Summary.OwnedTooling[toolID].Calls,
		Operations:     rows,
		TaskCohorts:    taskRows,
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
	if report.Operation != "" {
		fmt.Printf("Operation: %s\n", report.Operation)
	}
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
	printOwnedOperationTaskCohorts(report.TaskCohorts)
	if report.Recommendation != "" {
		fmt.Printf("\nNext: %s\n", report.Recommendation)
	}
}

func printOwnedOperationTaskCohorts(rows []ownedOperationTaskDrilldownRow) {
	if len(rows) == 0 {
		return
	}
	fmt.Println("\nLogical task cohorts:")
	fmt.Printf(
		"%-30s %-38s %7s %7s %8s %7s %7s %10s %10s\n",
		"OPERATION",
		"TASK",
		"CALLS",
		"AMBIG",
		"SESSIONS",
		"FAILED",
		"TRUNC",
		"OUTPUT",
		"AMBIG OUT",
	)
	for _, row := range rows {
		fmt.Printf(
			"%-30s %-38s %7s %7s %8s %7s %7s %10s %10s\n",
			truncateCodexLabel(row.Operation, 30),
			truncateCodexLabel(row.Task, 38),
			formatCodexCount(int64(row.Metrics.Calls)),
			formatCodexCount(int64(row.Metrics.AmbiguousCalls)),
			formatCodexCount(int64(row.Metrics.Sessions)),
			formatCodexCount(int64(row.Metrics.FailedCalls)),
			formatCodexCount(int64(row.Metrics.TruncatedCalls)),
			"~"+formatCodexCount(row.Metrics.EstimatedOutputTokens),
			"~"+formatCodexCount(row.Metrics.EstimatedAmbiguousOutputTokens),
		)
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
