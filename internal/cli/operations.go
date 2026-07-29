package cli

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
	SchemaVersion         int                              `json:"schemaVersion"`
	Provider              string                           `json:"provider"`
	Since                 string                           `json:"since"`
	Tool                  string                           `json:"tool"`
	Operation             string                           `json:"operation,omitempty"`
	AnalysisSessions      int                              `json:"analysisSessions"`
	ToolMetrics           *codexToolMetrics                `json:"toolMetrics,omitempty"`
	UnmatchedMetrics      *codexToolMetrics                `json:"unmatchedMetrics,omitempty"`
	OperationAssociations int                              `json:"operationAssociations"`
	Operations            []ownedOperationDrilldownRow     `json:"operations"`
	TaskCohorts           []ownedOperationTaskDrilldownRow `json:"taskCohorts,omitempty"`
	Recommendation        string                           `json:"recommendation,omitempty"`
}

func buildOwnedOperationsDrilldown(
	report codexSessionInsightsReport,
	config repositoryConfig,
	selector string,
	limit int,
) (ownedOperationsDrilldown, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return ownedOperationsDrilldown{}, errors.New("--operation requires a locally owned tool ID")
	}
	toolID := selector
	operationFilter := ""
	if candidateTool, candidateOperation, found := strings.Cut(selector, "/"); found {
		toolID = candidateTool
		operationFilter = selector
		if strings.TrimSpace(candidateOperation) == "" {
			return ownedOperationsDrilldown{}, fmt.Errorf("invalid --operation selector %q", selector)
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
			"unknown --operation tool %q (available: %s)",
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
				"unknown --operation %q for tool %q",
				operationFilter,
				toolID,
			)
		}
	}

	prefix := toolID + "/"
	rows := make([]ownedOperationDrilldownRow, 0)
	operationAssociations := 0
	for operation, metrics := range report.Summary.OwnedOperations {
		if !strings.HasPrefix(operation, prefix) ||
			(operationFilter != "" && operation != operationFilter) {
			continue
		}
		operationAssociations += metrics.Calls
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
	var toolMetrics, unmatchedMetrics *codexToolMetrics
	if operationFilter == "" {
		toolValue := report.Summary.OwnedTooling[toolID]
		unmatchedValue := report.Summary.OwnedToolUnmatched[toolID]
		toolMetrics = &toolValue
		unmatchedMetrics = &unmatchedValue
	}
	return ownedOperationsDrilldown{
		SchemaVersion:         codexSessionInsightsSchemaVersion,
		Provider:              report.Provider,
		Since:                 report.Since,
		Tool:                  toolID,
		Operation:             operationFilter,
		AnalysisSessions:      report.Summary.Sessions,
		ToolMetrics:           toolMetrics,
		UnmatchedMetrics:      unmatchedMetrics,
		OperationAssociations: operationAssociations,
		Operations:            rows,
		TaskCohorts:           taskRows,
		Recommendation:        strings.TrimSpace(selected.Recommendation),
	}, nil
}
