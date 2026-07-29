package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func printCodexSessionInsights(report codexSessionInsightsReport, config repositoryConfig, limit int, view string) {
	summary := report.Summary
	fmt.Printf("Muninn session insights since %s\n", report.Since)
	fmt.Printf("Provider: %s\n", report.Provider)
	fmt.Printf("Repository: %s\n", filepath.Base(report.WorkspaceRoot))
	if view == "findings" || view == "focused" {
		fmt.Printf(
			"Scope: %s sessions, %s tool calls, %s failed, ~%s fresh tokens\n",
			formatCodexCount(int64(summary.Sessions)),
			formatCodexCount(int64(summary.ToolCalls)),
			formatCodexCount(int64(summary.FailedToolCalls)),
			formatCodexCount(summary.FreshTokens),
		)
		printSessionInterventions(report.Interventions, limit)
		if view == "focused" && report.AnalysisScope.Focus == "discovery" {
			printDiscoveryFocusEvidence(buildDiscoveryFocusEvidence(summary, limit))
		}
		return
	}
	fmt.Printf("Sessions: %s (%s complete, %s incomplete)\n",
		formatCodexCount(int64(summary.Sessions)),
		formatCodexCount(int64(summary.CompletedSessions)),
		formatCodexCount(int64(summary.IncompleteSessions)),
	)
	fmt.Printf("Model tokens: %s input (%s cached, %s uncached), %s output, %s total\n",
		formatCodexCount(summary.Tokens.InputTokens),
		formatCodexCount(summary.Tokens.CachedInputTokens),
		formatCodexCount(summary.Tokens.UncachedInputTokens),
		formatCodexCount(summary.Tokens.OutputTokens),
		formatCodexCount(summary.Tokens.TotalTokens),
	)
	fmt.Printf("Fresh-token proxy: %s (uncached input + output)\n", formatCodexCount(summary.FreshTokens))
	printRepositoryInstructionFootprint(report.Instructions)
	fmt.Printf("Tool calls: %s (%s failed, %s truncated); visible output: ~%s tokens\n",
		formatCodexCount(int64(summary.ToolCalls)),
		formatCodexCount(int64(summary.FailedToolCalls)),
		formatCodexCount(int64(summary.TruncatedToolCalls)),
		formatCodexCount(summary.ToolOutputTokens),
	)
	printCompletionEpisodeAnalysis(report.Outcomes)
	if view == "details" {
		printFileHotspots(report.Outcomes.FileHotspots, limit)
		printOwnedOperationEffects(report.Outcomes.OwnedOperationEffects, limit)
	}
	printModelEffortAnalysis(report.Profiles)
	printDelegationAnalysis(report.Delegation)
	printDiagnosticFailureAnalysis(report.Diagnostics)
	printDeliveryReworkAnalysis(summary.DeliveryRework)
	printDownstreamQualityAnalysis(summary.DownstreamQuality)
	if summary.FilesUnreadable > 0 {
		fmt.Printf("Files: %s scanned, %s unreadable\n", formatCodexCount(int64(summary.FilesScanned)), formatCodexCount(int64(summary.FilesUnreadable)))
	}
	if len(report.Tasks) == 0 {
		fmt.Println("\nNo matching sessions.")
		return
	}
	rows := report.Tasks
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println("\nTop tasks by fresh-token proxy:")
	fmt.Printf("%-40s %8s %13s %13s %13s %13s %10s %8s\n", "TASK", "SESSIONS", "INPUT", "UNCACHED", "FRESH", "FRESH/SESS", "TOOL OUT", "FAILED")
	for _, task := range rows {
		fmt.Printf("%-40s %8s %13s %13s %13s %13s %10s %8s\n",
			truncateCodexLabel(task.Task, 40),
			formatCodexCount(int64(task.Sessions)),
			formatCodexCount(task.Tokens.InputTokens),
			formatCodexCount(task.Tokens.UncachedInputTokens),
			formatCodexCount(task.FreshTokens),
			formatCodexCount(perSessionTokens(task.FreshTokens, task.Sessions)),
			"~"+formatCodexCount(task.ToolOutputTokens),
			formatCodexCount(int64(task.FailedToolCalls)),
		)
	}
	if len(rows) < len(report.Tasks) {
		fmt.Printf("... %d more tasks; use --limit 0 to show all.\n", len(report.Tasks)-len(rows))
	}

	printCodexToolMetrics("\nTool output by name:", "TOOL", summary.ToolMetricsByName, 8, 32)
	printCodexToolMetrics("\nShell output by command family:", "FAMILY", summary.ShellCommandsByFamily, 12, 32)
	printCodexToolMetrics("\nMixed-shell output by family sequence:", "SEQUENCE", summary.MixedShellShapes, 12, 56)
	printCodexTransitions(summary.CrossCallTransitions, 12)
	printCodexReadTargets(summary.ReadTargets, 12)
	printOwnedTooling(summary.OwnedTooling, config.OwnedTools)
	printOwnedOperations(summary.OwnedOperations, 16)
	printCodexWaitMetrics("\nCandidate progress stalls (long, low-output waits):", summary.ProgressStalls, 12)
	printCodexWaitMetrics("\nExpected long waits excluded from stall findings:", summary.ExpectedWaits, 12)
	printCodexWaitMetrics("\nRapid continuation polling:", summary.RapidPolls, 12)
	printCodexOccurrenceMetrics("\nYielded operations without a terminal result:", summary.AbandonedContinuations, 12)
	printCodexOversizedOutputMetrics(summary.OversizedOutputs, 12)
	printCodexFailureReasons(summary.FailureReasons)
	printCodexFailureContexts(summary.FailureContexts, 12)

	fmt.Println("\nSignals:")
	if summary.InlineOrchestrationCalls > 0 {
		fmt.Printf("- %s large inline orchestration calls carried %s input bytes across %s sessions (largest call: %s bytes). Extract repeated scripts into a tested helper or compact agent-facing command.\n",
			formatCodexCount(int64(summary.InlineOrchestrationCalls)),
			formatCodexCount(summary.InlineOrchestrationBytes),
			formatCodexCount(int64(summary.InlineOrchestrationSessions)),
			formatCodexCount(summary.InlineOrchestrationMaxBytes),
		)
		printCodexInlineTools(summary.InlineOrchestrationByTool)
	}
	if summary.Compactions > 0 {
		cachedRatio := float64(0)
		if summary.Tokens.InputTokens > 0 {
			cachedRatio = 100 * float64(summary.Tokens.CachedInputTokens) / float64(summary.Tokens.InputTokens)
		}
		fmt.Printf("- %s context compactions occurred with %.0f%% cached input. Repeated compactions plus recurring transitions indicate a session loop or stale-context problem; cache hits alone do not.\n",
			formatCodexCount(int64(summary.Compactions)),
			cachedRatio,
		)
	}
	stallCalls, stallSeconds := codexWaitTotals(summary.ProgressStalls)
	expectedWaitCalls, expectedWaitSeconds := codexWaitTotals(summary.ExpectedWaits)
	rapidPollCalls, rapidPollSeconds := codexWaitTotals(summary.RapidPolls)
	abandonedContinuations := codexOccurrenceTotal(summary.AbandonedContinuations)
	oversizedCalls, oversizedBytes := codexOversizedOutputTotals(summary.OversizedOutputs)
	if stallCalls > 0 {
		fmt.Printf("- %s candidate low-output waits consumed %s. Remove redundant polling or add bounded progress for non-essential waits.\n",
			formatCodexCount(int64(stallCalls)),
			formatDurationSeconds(stallSeconds),
		)
	}
	if expectedWaitCalls > 0 {
		fmt.Printf("- %s long waits consuming %s were classified as expected tests, builds, local reviews, or remote GitHub review and excluded from stall findings.\n",
			formatCodexCount(int64(expectedWaitCalls)),
			formatDurationSeconds(expectedWaitSeconds),
		)
	}
	if rapidPollCalls > 0 {
		fmt.Printf("- %s rapid continuation polls consumed %s. Resume yielded commands with a wait aligned to their progress heartbeat instead of repeatedly returning to the model.\n",
			formatCodexCount(int64(rapidPollCalls)),
			formatDurationSeconds(rapidPollSeconds),
		)
	}
	if abandonedContinuations > 0 {
		fmt.Printf("- %s yielded operations never reached a terminal result before session completion or 30 minutes of inactivity. Resume or explicitly terminate yielded work before leaving it.\n",
			formatCodexCount(int64(abandonedContinuations)),
		)
	}
	if oversizedCalls > 0 {
		fmt.Printf("- %s oversized tool outputs returned ~%s visible tokens. Lower default output or provide a bounded follow-up surface.\n",
			formatCodexCount(int64(oversizedCalls)),
			formatCodexCount(estimatedTokens(oversizedBytes)),
		)
	}
	if summary.TruncatedToolCalls > 0 {
		fmt.Printf("- %s tool calls returned truncated output. Narrow file/diff reads or lower command output before retrying.\n", formatCodexCount(int64(summary.TruncatedToolCalls)))
	} else {
		fmt.Println("- No truncated tool output was detected.")
	}
	searchReadMetrics := codexMixedSearchReadMetrics(summary.MixedShellShapes)
	if searchReadMetrics.Calls > 0 {
		fmt.Printf("- Bundled search/read calls returned ~%s visible tokens across %s calls. %s\n",
			formatCodexCount(searchReadMetrics.EstimatedOutputTokens),
			formatCodexCount(int64(searchReadMetrics.Calls)),
			config.Actions.SourceContext,
		)
	}
	multiSessionTasks := 0
	for _, task := range report.Tasks {
		if task.Sessions > 1 {
			multiSessionTasks++
		}
	}
	if multiSessionTasks > 0 {
		fmt.Printf("- %s tasks span multiple sessions; preserve focused findings and validation state in task progress to avoid rediscovery.\n", formatCodexCount(int64(multiSessionTasks)))
	}
	searchMisses := summary.FailureReasons["search no match"]
	if searchMisses > 0 {
		fmt.Printf("- %s non-zero calls were search misses. Prefer a bounded source-context command that returns no matches cleanly.\n", formatCodexCount(int64(searchMisses)))
	}
	remainingFailures := summary.FailedToolCalls - searchMisses
	if remainingFailures > 0 {
		fmt.Printf("- %s remaining tool calls failed or timed out; inspect the reason/context rows before changing shared tooling.\n", formatCodexCount(int64(remainingFailures)))
	}
	fmt.Println("- Token counts are rollout totals, not billing amounts. Fresh-token proxy excludes cached input but does not apply model prices.")
	if view == "details" {
		printSessionFindings(report.Findings, limit)
	}
}

func printDiscoveryFocusEvidence(evidence discoveryFocusEvidence) {
	if len(evidence.ReadTargets) == 0 && len(evidence.SearchReadShapes) == 0 {
		return
	}
	fmt.Println("\nDiscovery evidence:")
	if len(evidence.ReadTargets) > 0 {
		fmt.Println("  Ranked repository read targets:")
		for _, target := range evidence.ReadTargets {
			fmt.Printf(
				"  - %s: %s, %s, %s, %s\n",
				target.Target,
				formatCodexCountNoun(int64(target.Reads), "read"),
				formatCodexCountNoun(int64(target.SearchReadLoops), "search/read loop"),
				formatCodexCountNoun(int64(target.Sessions), "session"),
				formatCodexCountNoun(int64(target.RediscoverySessions), "rediscovery session"),
			)
		}
	}
	if len(evidence.SearchReadShapes) > 0 {
		fmt.Println("  Highest-output bundled search/read shapes:")
		for _, shape := range evidence.SearchReadShapes {
			fmt.Printf(
				"  - %s: %s, %s, ~%s visible output tokens\n",
				shape.Shape,
				formatCodexCountNoun(int64(shape.Calls), "call"),
				formatCodexCountNoun(int64(shape.Sessions), "session"),
				formatCodexCount(shape.EstimatedOutputTokens),
			)
		}
	}
}

func printRepositoryInstructionFootprint(footprint repositoryInstructionFootprint) {
	if footprint.RootFiles == 0 && footprint.ScopedFiles == 0 {
		return
	}
	fmt.Printf(
		"Repository instructions: %s root files, %s bytes (~%s tokens/session baseline); %s scoped files, %s bytes\n",
		formatCodexCount(int64(footprint.RootFiles)),
		formatCodexCount(footprint.RootBytes),
		formatCodexCount(footprint.RootEstimatedTokens),
		formatCodexCount(int64(footprint.ScopedFiles)),
		formatCodexCount(footprint.ScopedBytes),
	)
}

func printCodexWaitMetrics(title string, metrics map[string]codexWaitMetrics, limit int) {
	type row struct {
		Context string
		Metrics codexWaitMetrics
	}
	rows := make([]row, 0, len(metrics))
	for context, value := range metrics {
		rows = append(rows, row{Context: context, Metrics: value})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.Seconds != rows[j].Metrics.Seconds {
			return rows[i].Metrics.Seconds > rows[j].Metrics.Seconds
		}
		if rows[i].Metrics.Calls != rows[j].Metrics.Calls {
			return rows[i].Metrics.Calls > rows[j].Metrics.Calls
		}
		return rows[i].Context < rows[j].Context
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println(title)
	fmt.Printf("%-40s %8s %10s %10s\n", "CONTEXT", "CALLS", "SESSIONS", "WAIT")
	for _, row := range rows {
		fmt.Printf("%-40s %8s %10s %10s\n",
			truncateCodexLabel(row.Context, 40),
			formatCodexCount(int64(row.Metrics.Calls)),
			formatCodexCount(int64(row.Metrics.Sessions)),
			formatDurationSeconds(row.Metrics.Seconds),
		)
	}
}

func printCodexOccurrenceMetrics(title string, metrics map[string]codexOccurrenceMetrics, limit int) {
	type row struct {
		Context string
		Metrics codexOccurrenceMetrics
	}
	rows := make([]row, 0, len(metrics))
	for context, value := range metrics {
		rows = append(rows, row{Context: context, Metrics: value})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.Count != rows[j].Metrics.Count {
			return rows[i].Metrics.Count > rows[j].Metrics.Count
		}
		return rows[i].Context < rows[j].Context
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println(title)
	fmt.Printf("%-40s %8s %10s\n", "CONTEXT", "COUNT", "SESSIONS")
	for _, row := range rows {
		fmt.Printf("%-40s %8s %10s\n",
			truncateCodexLabel(row.Context, 40),
			formatCodexCount(int64(row.Metrics.Count)),
			formatCodexCount(int64(row.Metrics.Sessions)),
		)
	}
}

func codexOccurrenceTotal(metrics map[string]codexOccurrenceMetrics) int {
	total := 0
	for _, value := range metrics {
		total += value.Count
	}
	return total
}

func codexWaitTotals(metrics map[string]codexWaitMetrics) (calls int, seconds int64) {
	for _, value := range metrics {
		calls += value.Calls
		seconds += value.Seconds
	}
	return calls, seconds
}

func printCodexOversizedOutputMetrics(metrics map[string]codexOversizedOutputMetrics, limit int) {
	type row struct {
		Context string
		Metrics codexOversizedOutputMetrics
	}
	rows := make([]row, 0, len(metrics))
	for context, value := range metrics {
		rows = append(rows, row{Context: context, Metrics: value})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.OutputBytes != rows[j].Metrics.OutputBytes {
			return rows[i].Metrics.OutputBytes > rows[j].Metrics.OutputBytes
		}
		if rows[i].Metrics.Calls != rows[j].Metrics.Calls {
			return rows[i].Metrics.Calls > rows[j].Metrics.Calls
		}
		return rows[i].Context < rows[j].Context
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println("\nOversized tool outputs (at least 30,000 bytes in one call):")
	fmt.Printf("%-40s %8s %10s %13s %13s\n", "CONTEXT", "CALLS", "SESSIONS", "OUTPUT", "MAX CALL")
	for _, row := range rows {
		fmt.Printf("%-40s %8s %10s %13s %13s\n",
			truncateCodexLabel(row.Context, 40),
			formatCodexCount(int64(row.Metrics.Calls)),
			formatCodexCount(int64(row.Metrics.Sessions)),
			formatCodexCount(row.Metrics.OutputBytes),
			formatCodexCount(row.Metrics.MaxOutputBytes),
		)
	}
}

func codexOversizedOutputTotals(metrics map[string]codexOversizedOutputMetrics) (calls int, bytes int64) {
	for _, value := range metrics {
		calls += value.Calls
		bytes += value.OutputBytes
	}
	return calls, bytes
}

func printCodexInlineTools(metrics map[string]codexInlineMetrics) {
	type row struct {
		Tool    string
		Metrics codexInlineMetrics
	}
	rows := make([]row, 0, len(metrics))
	for tool, value := range metrics {
		rows = append(rows, row{Tool: tool, Metrics: value})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.Bytes != rows[j].Metrics.Bytes {
			return rows[i].Metrics.Bytes > rows[j].Metrics.Bytes
		}
		return rows[i].Tool < rows[j].Tool
	})
	for _, row := range rows {
		fmt.Printf("  - %s: %s calls, %s bytes, largest %s\n",
			row.Tool,
			formatCodexCount(int64(row.Metrics.Calls)),
			formatCodexCount(row.Metrics.Bytes),
			formatCodexCount(row.Metrics.MaxBytes),
		)
	}
}

func codexMixedSearchReadMetrics(shapes map[string]codexToolMetrics) codexToolMetrics {
	var result codexToolMetrics
	for shape, metrics := range shapes {
		if !strings.Contains(shape, "search") || !strings.Contains(shape, "file reads") {
			continue
		}
		result.Calls += metrics.Calls
		result.Sessions = max(result.Sessions, metrics.Sessions)
		result.FailedCalls += metrics.FailedCalls
		result.TruncatedCalls += metrics.TruncatedCalls
		result.OutputBytes += metrics.OutputBytes
	}
	result.EstimatedOutputTokens = estimatedTokens(result.OutputBytes)
	return result
}

func printCodexFailureReasons(reasons map[string]int) {
	type row struct {
		Name  string
		Count int
	}
	rows := make([]row, 0, len(reasons))
	for name, count := range reasons {
		rows = append(rows, row{Name: name, Count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Name < rows[j].Name
	})
	if len(rows) == 0 {
		return
	}
	fmt.Println("\nFailed tool calls by fixed reason:")
	for _, row := range rows {
		fmt.Printf("- %-30s %s\n", row.Name, formatCodexCount(int64(row.Count)))
	}
}

func printCodexTransitions(transitions map[string]codexTransitionMetrics, limit int) {
	type row struct {
		Name    string
		Metrics codexTransitionMetrics
	}
	rows := make([]row, 0, len(transitions))
	for name, metrics := range transitions {
		rows = append(rows, row{Name: name, Metrics: metrics})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.Count != rows[j].Metrics.Count {
			return rows[i].Metrics.Count > rows[j].Metrics.Count
		}
		if rows[i].Metrics.Sessions != rows[j].Metrics.Sessions {
			return rows[i].Metrics.Sessions > rows[j].Metrics.Sessions
		}
		return rows[i].Name < rows[j].Name
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println("\nCross-call command-family transitions:")
	fmt.Printf("%-52s %10s %10s\n", "TRANSITION", "COUNT", "SESSIONS")
	for _, row := range rows {
		fmt.Printf("%-52s %10s %10s\n",
			truncateCodexLabel(row.Name, 52),
			formatCodexCount(int64(row.Metrics.Count)),
			formatCodexCount(int64(row.Metrics.Sessions)),
		)
	}
}

func printOwnedTooling(metrics map[string]codexToolMetrics, configs []ownedToolConfig) {
	if len(metrics) == 0 {
		return
	}
	configByID := map[string]ownedToolConfig{}
	for _, config := range configs {
		configByID[config.ID] = config
	}
	type row struct {
		ID      string
		Metrics codexToolMetrics
		Config  ownedToolConfig
	}
	rows := make([]row, 0, len(metrics))
	for id, value := range metrics {
		value.EstimatedOutputTokens = estimatedTokens(value.OutputBytes)
		rows = append(rows, row{ID: id, Metrics: value, Config: configByID[id]})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.FailedCalls != rows[j].Metrics.FailedCalls {
			return rows[i].Metrics.FailedCalls > rows[j].Metrics.FailedCalls
		}
		if rows[i].Metrics.OutputBytes != rows[j].Metrics.OutputBytes {
			return rows[i].Metrics.OutputBytes > rows[j].Metrics.OutputBytes
		}
		return rows[i].ID < rows[j].ID
	})
	fmt.Println("\nLocally controlled tooling:")
	fmt.Printf("%-24s %-24s %9s %10s %10s\n", "TOOL", "REPOSITORY", "CALLS", "OUTPUT", "FAILED")
	for _, row := range rows {
		repository := row.Config.Repository
		if repository == "" {
			repository = "(configured locally)"
		}
		fmt.Printf("%-24s %-24s %9s %10s %10s\n",
			truncateCodexLabel(row.ID, 24),
			truncateCodexLabel(repository, 24),
			formatCodexCount(int64(row.Metrics.Calls)),
			"~"+formatCodexCount(row.Metrics.EstimatedOutputTokens),
			formatCodexCount(int64(row.Metrics.FailedCalls)),
		)
		if recommendation := strings.TrimSpace(row.Config.Recommendation); recommendation != "" {
			fmt.Printf("  %s\n", recommendation)
		}
	}
}

func printOwnedOperations(metrics map[string]codexOwnedOperationMetrics, limit int) {
	type row struct {
		Operation string
		Metrics   codexOwnedOperationMetrics
	}
	rows := make([]row, 0, len(metrics))
	for operation, value := range metrics {
		rows = append(rows, row{Operation: operation, Metrics: value})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.FailedCalls != rows[j].Metrics.FailedCalls {
			return rows[i].Metrics.FailedCalls > rows[j].Metrics.FailedCalls
		}
		if rows[i].Metrics.OutputBytes != rows[j].Metrics.OutputBytes {
			return rows[i].Metrics.OutputBytes > rows[j].Metrics.OutputBytes
		}
		if rows[i].Metrics.Calls != rows[j].Metrics.Calls {
			return rows[i].Metrics.Calls > rows[j].Metrics.Calls
		}
		return rows[i].Operation < rows[j].Operation
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println("\nLocally controlled operations:")
	fmt.Printf(
		"%-32s %8s %8s %10s %10s %7s %7s %7s %7s\n",
		"OPERATION",
		"CALLS",
		"SESSIONS",
		"OUTPUT",
		"AMBIG OUT",
		"FAILED",
		"TRUNC",
		"AMB F",
		"AMB T",
	)
	for _, row := range rows {
		fmt.Printf("%-32s %8s %8s %10s %10s %7s %7s %7s %7s\n",
			truncateCodexLabel(row.Operation, 32),
			formatCodexCount(int64(row.Metrics.Calls)),
			formatCodexCount(int64(row.Metrics.Sessions)),
			"~"+formatCodexCount(row.Metrics.EstimatedOutputTokens),
			"~"+formatCodexCount(row.Metrics.EstimatedAmbiguousOutputTokens),
			formatCodexCount(int64(row.Metrics.FailedCalls)),
			formatCodexCount(int64(row.Metrics.TruncatedCalls)),
			formatCodexCount(int64(row.Metrics.AmbiguousFailedCalls)),
			formatCodexCount(int64(row.Metrics.AmbiguousTruncatedCalls)),
		)
	}
}

func printCodexReadTargets(targets map[string]codexTargetMetrics, limit int) {
	type row struct {
		Path    string
		Metrics codexTargetMetrics
	}
	rows := make([]row, 0, len(targets))
	for path, metrics := range targets {
		rows = append(rows, row{Path: path, Metrics: metrics})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.SearchReadLoops != rows[j].Metrics.SearchReadLoops {
			return rows[i].Metrics.SearchReadLoops > rows[j].Metrics.SearchReadLoops
		}
		if rows[i].Metrics.Reads != rows[j].Metrics.Reads {
			return rows[i].Metrics.Reads > rows[j].Metrics.Reads
		}
		return rows[i].Path < rows[j].Path
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println("\nRepository-relative read targets:")
	fmt.Printf("%-72s %8s %10s %9s %10s\n", "TARGET", "READS", "LOOPS", "SESSIONS", "REDISCOVER")
	for _, row := range rows {
		fmt.Printf("%-72s %8s %10s %9s %10s\n",
			truncateCodexLabel(row.Path, 72),
			formatCodexCount(int64(row.Metrics.Reads)),
			formatCodexCount(int64(row.Metrics.SearchReadLoops)),
			formatCodexCount(int64(row.Metrics.Sessions)),
			formatCodexCount(int64(row.Metrics.RediscoverySessions)),
		)
	}
}

func printCodexFailureContexts(contexts map[string]map[string]codexOccurrenceMetrics, limit int) {
	type row struct {
		Reason  string
		Context string
		Metrics codexOccurrenceMetrics
	}
	var rows []row
	for reason, values := range contexts {
		for context, metrics := range values {
			rows = append(rows, row{Reason: reason, Context: context, Metrics: metrics})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.Sessions != rows[j].Metrics.Sessions {
			return rows[i].Metrics.Sessions > rows[j].Metrics.Sessions
		}
		if rows[i].Metrics.Count != rows[j].Metrics.Count {
			return rows[i].Metrics.Count > rows[j].Metrics.Count
		}
		if rows[i].Reason != rows[j].Reason {
			return rows[i].Reason < rows[j].Reason
		}
		return rows[i].Context < rows[j].Context
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println("\nFailed calls by reason and privacy-safe context:")
	fmt.Printf("%-28s %-52s %8s %9s\n", "REASON", "CONTEXT", "CALLS", "SESSIONS")
	for _, row := range rows {
		fmt.Printf("%-28s %-52s %8s %9s\n",
			truncateCodexLabel(row.Reason, 28),
			truncateCodexLabel(row.Context, 52),
			formatCodexCount(int64(row.Metrics.Count)),
			formatCodexCount(int64(row.Metrics.Sessions)),
		)
	}
}

func printCodexToolMetrics(title, label string, metrics map[string]codexToolMetrics, limit, labelWidth int) {
	type row struct {
		Name    string
		Metrics codexToolMetrics
	}
	rows := make([]row, 0, len(metrics))
	for name, value := range metrics {
		rows = append(rows, row{Name: name, Metrics: value})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.OutputBytes != rows[j].Metrics.OutputBytes {
			return rows[i].Metrics.OutputBytes > rows[j].Metrics.OutputBytes
		}
		if rows[i].Metrics.Calls != rows[j].Metrics.Calls {
			return rows[i].Metrics.Calls > rows[j].Metrics.Calls
		}
		return rows[i].Name < rows[j].Name
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println(title)
	fmt.Printf("%-*s %10s %14s %10s %9s\n", labelWidth, label, "CALLS", "OUTPUT", "TRUNC", "FAILED")
	for _, row := range rows {
		fmt.Printf("%-*s %10s %14s %10s %9s\n",
			labelWidth,
			truncateCodexLabel(row.Name, labelWidth),
			formatCodexCount(int64(row.Metrics.Calls)),
			"~"+formatCodexCount(row.Metrics.EstimatedOutputTokens),
			formatCodexCount(int64(row.Metrics.TruncatedCalls)),
			formatCodexCount(int64(row.Metrics.FailedCalls)),
		)
	}
}

func formatCodexCount(value int64) string {
	negative := value < 0
	if negative {
		value = -value
	}
	raw := strconv.FormatInt(value, 10)
	for i := len(raw) - 3; i > 0; i -= 3 {
		raw = raw[:i] + "," + raw[i:]
	}
	if negative {
		return "-" + raw
	}
	return raw
}

func formatCodexCountNoun(value int64, noun string) string {
	if value != 1 {
		noun += "s"
	}
	return formatCodexCount(value) + " " + noun
}

func truncateCodexLabel(value string, max int) string {
	if len(value) <= max {
		return value
	}
	if max <= 1 {
		return value[:max]
	}
	return value[:max-1] + "…"
}
