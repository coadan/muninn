package cli

import (
	"sort"
	"time"
)

func analyzeCodexSessions(sessionDirs []string, workspaceRoot string, since, generatedAt time.Time) (codexSessionInsightsReport, error) {
	return analyzeCodexSessionsFiltered(sessionDirs, workspaceRoot, since, generatedAt, "")
}

func analyzeCodexSessionsFiltered(sessionDirs []string, workspaceRoot string, since, generatedAt time.Time, taskFilter string) (codexSessionInsightsReport, error) {
	return analyzeCodexSessionsFilteredWithOwnership(sessionDirs, workspaceRoot, since, generatedAt, taskFilter, ownershipCatalog{})
}

func analyzeCodexSessionsFilteredWithOwnership(sessionDirs []string, workspaceRoot string, since, generatedAt time.Time, taskFilter string, ownership ownershipCatalog) (codexSessionInsightsReport, error) {
	return analyzeCodexSessionsFilteredWithMetadata(sessionDirs, workspaceRoot, since, generatedAt, taskFilter, ownership, nil)
}

func analyzeCodexSessionsFilteredWithMetadata(sessionDirs []string, workspaceRoot string, since, generatedAt time.Time, taskFilter string, ownership ownershipCatalog, metadata map[string]normalizedSessionMetadata) (codexSessionInsightsReport, error) {
	discovery, err := discoverCodexSessions(sessionDirs)
	if err != nil {
		return codexSessionInsightsReport{}, err
	}
	return analyzeProviderSessions(codexSessionProvider, discovery, workspaceRoot, since, generatedAt, taskFilter, ownership, metadata)
}

func newSessionInsightsReport(provider string, sessionDirs []string, workspaceRoot string, since, generatedAt time.Time) codexSessionInsightsReport {
	return codexSessionInsightsReport{
		SchemaVersion: codexSessionInsightsSchemaVersion,
		DetailLevel:   "full",
		Provider:      provider,
		GeneratedAt:   generatedAt.Format(time.RFC3339),
		Since:         since.Format(time.RFC3339),
		WorkspaceRoot: workspaceRoot,
		SessionDirs:   append([]string(nil), sessionDirs...),
		Summary: codexSessionInsightsSummary{
			ToolCallsByName:       map[string]int{},
			ToolMetricsByName:     map[string]codexToolMetrics{},
			codexAggregateMetrics: newCodexAggregateMetrics(),
		},
		operationTasks: map[string]map[string]codexOwnedOperationMetrics{},
	}
}

func newCodexAggregateMetrics() codexAggregateMetrics {
	return codexAggregateMetrics{
		ShellCommandsByFamily:        map[string]codexToolMetrics{},
		MixedShellShapes:             map[string]codexToolMetrics{},
		CrossCallTransitions:         map[string]codexTransitionMetrics{},
		OwnedOperationChains:         map[string]codexTransitionMetrics{},
		OwnedOperationRetries:        map[string]ownedOperationRetryMetrics{},
		OwnedTooling:                 map[string]codexToolMetrics{},
		OwnedToolUnmatched:           map[string]codexToolMetrics{},
		OwnedOperations:              map[string]codexOwnedOperationMetrics{},
		OwnedFlags:                   map[string]codexOccurrenceMetrics{},
		OwnedFlagEligibleCalls:       map[string]codexOccurrenceMetrics{},
		OwnedOperationFailureReasons: map[string]map[string]codexOccurrenceMetrics{},
		ReadTargets:                  map[string]codexTargetMetrics{},
		InlineOrchestrationByTool:    map[string]codexInlineMetrics{},
		InlineOrchestrationByFamily:  map[string]codexInlineMetrics{},
		InlineOrchestrationByOwner:   map[string]codexInlineMetrics{},
		FailureReasons:               map[string]int{},
		FailureContexts:              map[string]map[string]codexOccurrenceMetrics{},
		ProgressStalls:               map[string]codexWaitMetrics{},
		ExpectedWaits:                map[string]codexWaitMetrics{},
		RapidPolls:                   map[string]codexWaitMetrics{},
		AbandonedContinuations:       map[string]codexOccurrenceMetrics{},
		OversizedOutputs:             map[string]codexOversizedOutputMetrics{},
		Activity:                     map[string]time.Time{},
	}
}

func finishSessionInsightsReport(report *codexSessionInsightsReport, taskMap map[string]*codexTaskInsights) {
	report.Tasks = make([]codexTaskInsights, 0, len(taskMap))
	for _, task := range taskMap {
		report.Tasks = append(report.Tasks, *task)
	}
	sort.Slice(report.Tasks, func(i, j int) bool {
		if report.Tasks[i].FreshTokens != report.Tasks[j].FreshTokens {
			return report.Tasks[i].FreshTokens > report.Tasks[j].FreshTokens
		}
		if report.Tasks[i].Sessions != report.Tasks[j].Sessions {
			return report.Tasks[i].Sessions > report.Tasks[j].Sessions
		}
		return report.Tasks[i].Task < report.Tasks[j].Task
	})
	report.Outcomes = analyzeCompletionEpisodes(report.taskEpisodes)
	report.Outcomes.FileHotspots = analyzeFileHotspots(
		report.taskEpisodes,
		report.Summary.DeliveryRework.ReworkTargets,
		report.Summary.DownstreamQuality,
	)
	report.Outcomes.QualityCohorts = analyzeTaskQualityCohorts(report.sessionRecords)
	report.Profiles = analyzeModelEffortProfiles(report.sessionRecords)
	report.Delegation = analyzeDelegation(report.sessionRecords)
	report.Diagnostics = analyzeDiagnosticFailures(report.sessionRecords)
}

func parseCodexSession(path, workspaceRoot string, since, generatedAt time.Time) (codexSessionRecord, error) {
	return parseCodexSessionWithOwnership(path, workspaceRoot, since, generatedAt, ownershipCatalog{})
}

func parseCodexSessionWithOwnership(path, workspaceRoot string, since, generatedAt time.Time, ownership ownershipCatalog) (codexSessionRecord, error) {
	session, err := parseCodexNormalizedSession(path)
	if err != nil {
		return codexSessionRecord{}, err
	}
	return sessionRecordFromNormalized(session, workspaceRoot, since, generatedAt, ownership)
}

func addCodexSessionToReport(report *codexSessionInsightsReport, taskMap map[string]*codexTaskInsights, record codexSessionRecord) {
	report.sessionRecords = append(report.sessionRecords, record)
	summary := &report.Summary
	addCodexRecordMetrics(&summary.codexAggregateMetrics, record)
	for name, count := range record.ToolCallsByName {
		summary.ToolCallsByName[name] += count
	}
	for name, metrics := range record.ToolMetricsByName {
		addCodexToolMetricsValue(summary.ToolMetricsByName, name, metrics)
	}
	addCodexOwnedOperationTaskMetrics(report.operationTasks, record.OwnedOperationTasks)
	sessionOrdinal := len(report.sessionRecords)
	for _, episode := range record.TaskEpisodes {
		episode.sessionOrdinal = sessionOrdinal
		report.taskEpisodes = append(report.taskEpisodes, episode)
	}

	task := taskMap[record.Task]
	if task == nil {
		task = &codexTaskInsights{
			Task:                  record.Task,
			codexAggregateMetrics: newCodexAggregateMetrics(),
		}
		taskMap[record.Task] = task
	}
	addCodexRecordMetrics(&task.codexAggregateMetrics, record)
}

func addCodexRecordMetrics(target *codexAggregateMetrics, record codexSessionRecord) {
	target.Sessions++
	if record.Completed {
		target.CompletedSessions++
	} else {
		target.IncompleteSessions++
	}
	target.DurationSeconds += codexSessionDuration(record)
	target.Compactions += record.Compactions
	if record.Compactions > 0 {
		target.SessionsWithCompactions++
	}
	addNormalizedTokenUsage(&target.Tokens, record.Tokens)
	target.FreshTokens += record.Tokens.UncachedInputTokens + record.Tokens.OutputTokens
	target.ToolCalls += record.ToolCalls
	target.FailedToolCalls += record.FailedToolCalls
	target.TruncatedToolCalls += record.TruncatedToolCalls
	target.ToolOutputBytes += record.ToolOutputBytes
	target.ToolOutputTokens += estimatedTokens(record.ToolOutputBytes)
	for family, metrics := range record.ShellCommandsByFamily {
		addCodexToolMetricsValue(target.ShellCommandsByFamily, family, metrics)
	}
	for shape, metrics := range record.MixedShellShapes {
		addCodexToolMetricsValue(target.MixedShellShapes, shape, metrics)
	}
	addCodexTransitionMetrics(target.CrossCallTransitions, record.CrossCallTransitions)
	addCodexTransitionMetrics(target.OwnedOperationChains, record.OwnedOperationChains)
	addOwnedOperationRetryMetrics(target.OwnedOperationRetries, record.OwnedOperationRetries)
	for id, metrics := range record.OwnedTooling {
		addCodexToolMetricsValue(target.OwnedTooling, id, metrics)
	}
	for id, metrics := range record.OwnedToolUnmatched {
		addCodexToolMetricsValue(target.OwnedToolUnmatched, id, metrics)
	}
	addCodexOwnedOperationMetrics(target.OwnedOperations, record.OwnedOperations, record.OwnedOperationAmbiguous)
	addCodexOccurrenceMetrics(target.OwnedFlags, record.OwnedFlags)
	addCodexOccurrenceMetrics(target.OwnedFlagEligibleCalls, record.OwnedFlagEligibleCalls)
	addCodexFailureContexts(target.OwnedOperationFailureReasons, record.OwnedOperationFailureReasons)
	addCodexTargetMetrics(target.ReadTargets, record.ReadTargets, record.EditTargets)
	target.InlineOrchestrationCalls += record.InlineOrchestrationCalls
	target.InlineOrchestrationBytes += record.InlineOrchestrationBytes
	target.InlineOrchestrationMaxBytes = max(target.InlineOrchestrationMaxBytes, record.InlineOrchestrationMaxBytes)
	addCodexInlineMetrics(target.InlineOrchestrationByTool, record.InlineOrchestrationByTool)
	addCodexInlineMetrics(target.InlineOrchestrationByFamily, record.InlineOrchestrationByFamily)
	addCodexInlineMetrics(target.InlineOrchestrationByOwner, record.InlineOrchestrationByOwner)
	if record.InlineOrchestrationCalls > 0 {
		target.InlineOrchestrationSessions++
	}
	for reason, count := range record.FailureReasons {
		target.FailureReasons[reason] += count
	}
	addCodexFailureContexts(target.FailureContexts, record.FailureContexts)
	addCodexWaitMetrics(target.ProgressStalls, record.ProgressStalls)
	addCodexWaitMetrics(target.ExpectedWaits, record.ExpectedWaits)
	addCodexWaitMetrics(target.RapidPolls, record.RapidPolls)
	addCodexOccurrenceMetrics(target.AbandonedContinuations, record.AbandonedContinuations)
	addCodexOversizedOutputMetrics(target.OversizedOutputs, record.OversizedOutputs)
	addDeliveryReworkMetrics(&target.DeliveryRework, record.DeliveryRework)
	addDownstreamQualityMetrics(&target.DownstreamQuality, record.DownstreamQuality)
	mergeSessionActivity(target.Activity, record.Activity)
}

func addOwnedOperationRetryMetrics(
	target,
	addition map[string]ownedOperationRetryMetrics,
) {
	for operation, value := range addition {
		metrics := target[operation]
		metrics.Attempts += value.Attempts
		metrics.RepeatedFailures += value.RepeatedFailures
		metrics.SuccessfulRetries += value.SuccessfulRetries
		if value.Attempts > 0 {
			metrics.Sessions++
		}
		if value.RepeatedFailures > 0 {
			metrics.RepeatedFailureSessions++
		}
		target[operation] = metrics
	}
}

func addCodexWaitMetrics(target, addition map[string]codexWaitMetrics) {
	for context, value := range addition {
		metrics := target[context]
		metrics.Calls += value.Calls
		metrics.Seconds += value.Seconds
		if value.Calls > 0 {
			metrics.Sessions++
		}
		target[context] = metrics
	}
}

func addCodexOccurrenceMetrics(target map[string]codexOccurrenceMetrics, addition map[string]int) {
	for context, count := range addition {
		if count <= 0 {
			continue
		}
		metrics := target[context]
		metrics.Count += count
		metrics.Sessions++
		target[context] = metrics
	}
}

func addCodexOversizedOutputMetrics(target, addition map[string]codexOversizedOutputMetrics) {
	for context, value := range addition {
		metrics := target[context]
		metrics.Calls += value.Calls
		metrics.OutputBytes += value.OutputBytes
		metrics.MaxOutputBytes = max(metrics.MaxOutputBytes, value.MaxOutputBytes)
		metrics.NestedCalls += value.NestedCalls
		metrics.MaxNestedCalls = max(metrics.MaxNestedCalls, value.MaxNestedCalls)
		if value.Calls > 0 {
			metrics.Sessions++
		}
		target[context] = metrics
	}
}

func addCodexTransitionMetrics(target map[string]codexTransitionMetrics, additions map[string]int) {
	for transition, count := range additions {
		value := target[transition]
		value.Count += count
		value.Sessions++
		target[transition] = value
	}
}

func addCodexTargetMetrics(
	target,
	additions map[string]codexTargetMetrics,
	editTargets map[string]int,
) {
	for path, addition := range additions {
		metrics := target[path]
		metrics.Reads += addition.Reads
		metrics.SearchReadLoops += addition.SearchReadLoops
		metrics.Sessions++
		rediscovered := addition.Reads > 1 || addition.SearchReadLoops > 0
		if rediscovered {
			metrics.RediscoverySessions++
		}
		if editTargets[path] > 0 {
			metrics.EditedSessions++
		} else if rediscovered {
			metrics.UneditedRediscoverySessions++
		}
		target[path] = metrics
	}
}

func addCodexFailureContexts(target map[string]map[string]codexOccurrenceMetrics, additions map[string]map[string]int) {
	for reason, contexts := range additions {
		for context, count := range contexts {
			if target[reason] == nil {
				target[reason] = map[string]codexOccurrenceMetrics{}
			}
			metrics := target[reason][context]
			metrics.Count += count
			metrics.Sessions++
			target[reason][context] = metrics
		}
	}
}

func addCodexToolMetricsValue(target map[string]codexToolMetrics, key string, addition codexToolMetrics) {
	value := target[key]
	value.Calls += addition.Calls
	if addition.Calls > 0 {
		value.Sessions++
	}
	value.FailedCalls += addition.FailedCalls
	value.AmbiguousFailedCalls += addition.AmbiguousFailedCalls
	value.TruncatedCalls += addition.TruncatedCalls
	value.AmbiguousTruncatedCalls += addition.AmbiguousTruncatedCalls
	value.OutputBytes += addition.OutputBytes
	value.AmbiguousCalls += addition.AmbiguousCalls
	value.AmbiguousOutputBytes += addition.AmbiguousOutputBytes
	value.EstimatedOutputTokens = estimatedTokens(value.OutputBytes)
	value.EstimatedAmbiguousOutputTokens = estimatedTokens(value.AmbiguousOutputBytes)
	target[key] = value
}

func addCodexInlineMetrics(target, additions map[string]codexInlineMetrics) {
	for tool, addition := range additions {
		metrics := target[tool]
		metrics.Calls += addition.Calls
		if addition.Calls > 0 {
			metrics.Sessions++
		}
		metrics.Bytes += addition.Bytes
		metrics.MaxBytes = max(metrics.MaxBytes, addition.MaxBytes)
		target[tool] = metrics
	}
}

func addCodexOwnedOperationMetrics(target map[string]codexOwnedOperationMetrics, additions, ambiguous map[string]codexToolMetrics) {
	operations := map[string]struct{}{}
	for operation := range additions {
		operations[operation] = struct{}{}
	}
	for operation := range ambiguous {
		operations[operation] = struct{}{}
	}
	for operation := range operations {
		addition := additions[operation]
		ambiguousAddition := ambiguous[operation]
		metrics := target[operation]
		metrics.Calls += addition.Calls + ambiguousAddition.Calls
		metrics.AmbiguousCalls += ambiguousAddition.Calls
		if addition.Calls > 0 || ambiguousAddition.Calls > 0 {
			metrics.Sessions++
		}
		metrics.FailedCalls += addition.FailedCalls
		metrics.AmbiguousFailedCalls += ambiguousAddition.FailedCalls
		metrics.TruncatedCalls += addition.TruncatedCalls
		metrics.AmbiguousTruncatedCalls += ambiguousAddition.TruncatedCalls
		metrics.OutputBytes += addition.OutputBytes
		metrics.AmbiguousOutputBytes += ambiguousAddition.OutputBytes
		metrics.EstimatedOutputTokens = estimatedTokens(metrics.OutputBytes)
		metrics.EstimatedAmbiguousOutputTokens = estimatedTokens(metrics.AmbiguousOutputBytes)
		target[operation] = metrics
	}
}

func addCodexOwnedOperationTaskMetrics(
	target map[string]map[string]codexOwnedOperationMetrics,
	additions map[string]map[string]codexOwnedOperationMetrics,
) {
	for operation, tasks := range additions {
		if target[operation] == nil {
			target[operation] = map[string]codexOwnedOperationMetrics{}
		}
		for task, addition := range tasks {
			metrics := target[operation][task]
			metrics.Calls += addition.Calls
			metrics.AmbiguousCalls += addition.AmbiguousCalls
			metrics.Sessions += addition.Sessions
			metrics.FailedCalls += addition.FailedCalls
			metrics.AmbiguousFailedCalls += addition.AmbiguousFailedCalls
			metrics.TruncatedCalls += addition.TruncatedCalls
			metrics.AmbiguousTruncatedCalls += addition.AmbiguousTruncatedCalls
			metrics.OutputBytes += addition.OutputBytes
			metrics.AmbiguousOutputBytes += addition.AmbiguousOutputBytes
			metrics.EstimatedOutputTokens = estimatedTokens(metrics.OutputBytes)
			metrics.EstimatedAmbiguousOutputTokens = estimatedTokens(metrics.AmbiguousOutputBytes)
			target[operation][task] = metrics
		}
	}
}

func codexSessionDuration(record codexSessionRecord) int64 {
	if record.StartedAt.IsZero() || record.EndedAt.IsZero() || record.EndedAt.Before(record.StartedAt) {
		return 0
	}
	return int64(record.EndedAt.Sub(record.StartedAt).Seconds())
}

func addNormalizedTokenUsage(total *normalizedTokenUsage, usage normalizedTokenUsage) {
	total.InputTokens += usage.InputTokens
	total.CachedInputTokens += usage.CachedInputTokens
	total.UncachedInputTokens += usage.UncachedInputTokens
	total.OutputTokens += usage.OutputTokens
	total.ReasoningTokens += usage.ReasoningTokens
	total.TotalTokens += usage.TotalTokens
}

func estimatedTokens(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	return (bytes + 3) / 4
}
