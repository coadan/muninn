package main

import (
	"sort"
	"strings"
	"time"
)

const (
	sessionEventToolCall   = "tool_call"
	sessionEventToolOutput = "tool_output"
	sessionEventToken      = "token"
	sessionEventComplete   = "complete"
	sessionEventCompaction = "compaction"
)

// normalizedSession is the provider-neutral boundary between ingestion and
// storage/reporting. SourcePath and CWD are local index metadata only and must
// never be emitted by reports.
type normalizedSession struct {
	Provider         string
	SourcePath       string
	CWD              string
	Model            string
	ReasoningEffort  string
	AgentKind        string
	LineageKey       string
	ParentLineageKey string
	SpawnStatus      string
	Events           []normalizedSessionEvent
}

type normalizedSessionEvent struct {
	Sequence                      int
	OccurredAt                    time.Time
	Kind                          string
	ToolName                      string
	Family                        string
	Shape                         string
	FirstFamily                   string
	LastFamily                    string
	ToolRound                     int
	CallOccurredAt                time.Time
	Failed                        bool
	Truncated                     bool
	OutputBytes                   int64
	FailureReason                 string
	FailureContext                string
	Tokens                        normalizedTokenUsage
	SelectorDigests               []string
	CommandCandidates             []ownedCommandInvocation
	OwnedOperations               []string
	OperationTask                 string
	OperationAttributionAmbiguous bool
	OperationContinues            bool
	ConcurrentBatch               bool
	TargetCandidates              []string
	Targets                       []string
	InlineBytes                   int64
	Diagnostic                    *normalizedDiagnosticObservation
	WorkingDirectories            []string
	InRepositoryScope             bool
}

func sessionRecordFromNormalized(session normalizedSession, workspaceRoot string, since, generatedAt time.Time, ownership ownershipCatalog) (codexSessionRecord, error) {
	record := newCodexSessionRecord()
	record.SourcePath = session.SourcePath
	record.CWD = session.CWD
	record.Model = session.Model
	record.ReasoningEffort = session.ReasoningEffort
	record.AgentKind = session.AgentKind
	record.LineageKey = session.LineageKey
	record.ParentLineageKey = session.ParentLineageKey
	record.SpawnStatus = session.SpawnStatus
	if record.CWD == "" {
		return codexSessionRecord{}, nil
	}
	inside, err := pathInsideRoot(workspaceRoot, record.CWD)
	if err != nil {
		return codexSessionRecord{}, nil
	}
	if !inside && !normalizedSessionTouchesRepository(session, workspaceRoot) {
		return codexSessionRecord{}, nil
	}
	if !inside {
		record.CWD = workspaceRoot
	}
	record.Task = codexTaskName(workspaceRoot, record.CWD)

	previousCommandRound := 0
	previousCommand := normalizedSessionEvent{}
	previousTokens := normalizedTokenUsage{}
	hasPreviousTokens := false
	episode := newTaskEpisode(record)
	deliveryTrackers := map[string]*deliveryReworkTracker{}
	downstreamTrackers := map[string]*downstreamQualityTracker{}
	var activeDiagnostic *diagnosticFailureEpisode
	pendingContinuations := map[string]int{}
	inRepositoryScope := inside
	lastEventKind := ""
	for _, event := range session.Events {
		event = withoutContinuationCallAttribution(event)
		if inside, explicit := eventRepositoryScope(event, session.CWD, workspaceRoot); explicit {
			inRepositoryScope = inside
		}
		if event.OccurredAt.Before(since) {
			if event.Kind == sessionEventToken {
				previousTokens = event.Tokens
				hasPreviousTokens = true
			}
			if inRepositoryScope {
				episode.LeftCensored = event.Kind != sessionEventComplete
			}
			continue
		}
		if event.OccurredAt.After(generatedAt) {
			continue
		}
		tokenIncrement := normalizedTokenUsage{}
		if event.Kind == sessionEventToken {
			tokenIncrement = normalizedTokenUsageIncrement(event.Tokens, previousTokens, hasPreviousTokens)
			previousTokens = event.Tokens
			hasPreviousTokens = true
		}
		if !inRepositoryScope {
			continue
		}
		if event.Kind == sessionEventToken {
			addNormalizedTokenUsage(&record.Tokens, tokenIncrement)
		}
		if activeDiagnostic != nil &&
			event.Kind != sessionEventToolOutput &&
			event.OccurredAt.Sub(activeDiagnostic.EndedAt) > 30*time.Minute {
			record.DiagnosticFailures = append(record.DiagnosticFailures, *activeDiagnostic)
			activeDiagnostic = nil
		}
		if activeDiagnostic != nil {
			if event.Diagnostic != nil {
				record.DiagnosticFailures = append(record.DiagnosticFailures, *activeDiagnostic)
				activeDiagnostic = nil
			} else {
				activeDiagnostic.EndedAt = event.OccurredAt
				switch event.Kind {
				case sessionEventToken:
					addNormalizedTokenUsage(&activeDiagnostic.Tokens, tokenIncrement)
				case sessionEventToolCall:
					activeDiagnostic.ToolCalls++
				case sessionEventToolOutput:
					activeDiagnostic.OutputBytes += event.OutputBytes
					if event.Failed {
						activeDiagnostic.FailedCalls++
					}
				}
				if event.Kind == sessionEventComplete {
					record.DiagnosticFailures = append(record.DiagnosticFailures, *activeDiagnostic)
					activeDiagnostic = nil
				}
			}
		}
		if event.Diagnostic != nil && event.Diagnostic.Status == "passed" {
			record.DiagnosticPasses = append(record.DiagnosticPasses, *event.Diagnostic)
			touchSessionActivity(record.Activity, "diagnostic-pass", event.Diagnostic.Target, event.OccurredAt)
		}
		if event.Diagnostic != nil && event.Diagnostic.Failure != nil {
			observedAt := event.OccurredAt
			if observedAt.IsZero() {
				observedAt = event.Diagnostic.Failure.FailedAt
			}
			activeDiagnostic = &diagnosticFailureEpisode{
				normalizedDiagnosticFailure: *event.Diagnostic.Failure,
				Target:                      event.Diagnostic.Target,
				Model:                       record.Model,
				ReasoningEffort:             record.ReasoningEffort,
				AgentKind:                   record.AgentKind,
				EndedAt:                     event.OccurredAt,
			}
			// Recovery cost starts when the agent observes the report. The
			// provider's failure timestamp may describe a stale artifact.
			activeDiagnostic.FailedAt = observedAt
			touchSessionActivity(record.Activity, "diagnostic-failure", event.Diagnostic.Failure.Fingerprint, event.OccurredAt)
		}
		if record.StartedAt.IsZero() || event.OccurredAt.Before(record.StartedAt) {
			record.StartedAt = event.OccurredAt
		}
		if record.EndedAt.IsZero() || event.OccurredAt.After(record.EndedAt) {
			record.EndedAt = event.OccurredAt
		}
		lastEventKind = event.Kind
		if event.Kind == sessionEventToolCall && len(event.Targets) == 0 {
			if event.ToolName == "apply_patch" {
				if event.OperationTask == "" {
					event.OperationTask = repositoryTaskForTargetCandidates(
						event.TargetCandidates,
						session.CWD,
						workspaceRoot,
					)
				}
				event.Targets = normalizeRepositoryEditTargets(event.TargetCandidates, session.CWD, workspaceRoot)
			} else {
				event.Targets = normalizeRepositoryTargets(event.TargetCandidates, session.CWD, workspaceRoot)
			}
		}
		eventOperations := event.OwnedOperations
		if len(eventOperations) == 0 {
			eventOperations = ownership.classifyOperations(event.CommandCandidates)
		}
		if event.Kind != sessionEventToolOutput {
			episode.observe(event, tokenIncrement, eventOperations)
		}
		eventTask := ownedOperationTask(event, ownership, record.Task)
		deliveryTracker := deliveryTrackers[eventTask]
		if deliveryTracker == nil {
			deliveryTracker = &deliveryReworkTracker{}
			deliveryTrackers[eventTask] = deliveryTracker
		}
		downstreamTracker := downstreamTrackers[eventTask]
		if downstreamTracker == nil {
			downstreamTracker = &downstreamQualityTracker{}
			downstreamTrackers[eventTask] = downstreamTracker
		}
		postDeliveryEdits := deliveryTracker.metrics.PostDeliveryEditCalls
		postDeliveryReviews := deliveryTracker.metrics.PostDeliveryReviewChecks
		downstreamFailures := downstreamTracker.metrics.DeliveriesWithFailure
		downstreamRecoveries := downstreamTracker.metrics.RecoveredDeliveries
		downstreamReverts := downstreamTracker.metrics.Reverts
		deliveryTracker.observe(event, eventOperations)
		downstreamTracker.observe(event, eventOperations)
		if deliveryTracker.metrics.PostDeliveryEditCalls > postDeliveryEdits {
			touchSessionActivity(record.Activity, "delivery-rework", "", event.OccurredAt)
		}
		if deliveryTracker.metrics.PostDeliveryReviewChecks > postDeliveryReviews {
			touchSessionActivity(record.Activity, "delivery-review", "", event.OccurredAt)
		}
		if downstreamTracker.metrics.DeliveriesWithFailure > downstreamFailures {
			touchSessionActivity(record.Activity, "downstream-failure", "", event.OccurredAt)
		}
		if downstreamTracker.metrics.RecoveredDeliveries > downstreamRecoveries {
			touchSessionActivity(record.Activity, "downstream-recovery", "", event.OccurredAt)
		}
		if downstreamTracker.metrics.Reverts > downstreamReverts {
			touchSessionActivity(record.Activity, "downstream-revert", "", event.OccurredAt)
		}
		switch event.Kind {
		case sessionEventComplete:
			record.Completed = true
			touchSessionActivity(record.Activity, "completion", "", event.OccurredAt)
			record.TaskEpisodes = append(record.TaskEpisodes, episode)
			episode = newTaskEpisode(record)
			previousCommand = normalizedSessionEvent{}
			previousCommandRound = 0
		case sessionEventCompaction:
			record.Compactions++
			touchSessionActivity(record.Activity, "compaction", "", event.OccurredAt)
		case sessionEventToolCall:
			record.ToolCalls++
			if delegationOperation(event) {
				touchSessionActivity(record.Activity, "delegation", event.ToolName, event.OccurredAt)
			}
			if event.InlineBytes > 0 {
				record.InlineOrchestrationCalls++
				record.InlineOrchestrationBytes += event.InlineBytes
				record.InlineOrchestrationMaxBytes = max(record.InlineOrchestrationMaxBytes, event.InlineBytes)
				recordInlineMetric(record.InlineOrchestrationByTool, event.ToolName, event.InlineBytes)
				family := event.Family
				if family == "" {
					family = "other CLI"
				}
				recordInlineMetric(record.InlineOrchestrationByFamily, family, event.InlineBytes)
				recordInlineMetric(
					record.InlineOrchestrationByOwner,
					inlineOrchestrationOwner(event, eventOperations, ownership),
					event.InlineBytes,
				)
				touchSessionActivity(record.Activity, "inline", "", event.OccurredAt)
			}
			record.ToolCallsByName[event.ToolName]++
			addCodexToolMetrics(record.ToolMetricsByName, event.ToolName, 1, false, false, 0)
			if event.Family != "" {
				addCodexToolMetrics(record.ShellCommandsByFamily, event.Family, 1, false, false, 0)
			}
			if event.Shape != "" {
				addCodexToolMetrics(record.MixedShellShapes, event.Shape, 1, false, false, 0)
				touchSessionActivity(record.Activity, "shape", event.Shape, event.OccurredAt)
			}
			for _, ownedTool := range ownership.match(event.SelectorDigests) {
				addCodexToolMetrics(record.OwnedTooling, ownedTool, 1, false, false, 0)
				touchSessionActivity(record.Activity, "owned-tool", ownedTool, event.OccurredAt)
			}
			ownedOperations := eventOperations
			for _, operation := range ownedOperations {
				target := record.OwnedOperations
				if event.OperationAttributionAmbiguous {
					target = record.OwnedOperationAmbiguous
				}
				addCodexToolMetrics(target, operation, 1, false, false, 0)
				recordOwnedOperationTask(
					record.OwnedOperationTasks,
					operation,
					ownedOperationTask(event, ownership, record.Task),
					event.OperationAttributionAmbiguous,
					1,
					false,
					false,
					0,
				)
				touchSessionActivity(record.Activity, "owned-operation", operation, event.OccurredAt)
			}
			for _, target := range event.Targets {
				metrics := record.ReadTargets[target]
				if event.ToolName == "apply_patch" {
					record.EditTargets[target]++
					touchSessionActivity(record.Activity, "edit", target, event.OccurredAt)
				} else {
					metrics.Reads++
				}
				if previousCommand.LastFamily == "search" && previousCommandRound == event.ToolRound-1 {
					metrics.SearchReadLoops++
				}
				if event.ToolName != "apply_patch" {
					record.ReadTargets[target] = metrics
					touchSessionActivity(record.Activity, "read", target, event.OccurredAt)
				}
			}
			if event.FirstFamily != "" {
				if previousCommand.LastFamily != "" && previousCommandRound == event.ToolRound-1 {
					transition := previousCommand.LastFamily + " -> " + event.FirstFamily
					record.CrossCallTransitions[transition]++
					touchSessionActivity(record.Activity, "transition", transition, event.OccurredAt)
				}
				previousCommand = event
				previousCommandRound = event.ToolRound
			}
		case sessionEventToolOutput:
			// Match direct analysis semantics: output is attributable only when
			// both the originating call and its output are inside the window.
			if event.CallOccurredAt.Before(since) || event.CallOccurredAt.After(generatedAt) {
				continue
			}
			episode.observe(event, normalizedTokenUsage{}, eventOperations)
			record.ToolOutputBytes += event.OutputBytes
			if event.Truncated {
				record.TruncatedToolCalls++
			}
			if event.Failed {
				record.FailedToolCalls++
				record.FailureReasons[event.FailureReason]++
				addCodexFailureContext(record.FailureContexts, event.FailureReason, event.FailureContext)
				touchSessionActivity(record.Activity, "failure", event.FailureReason+"\x00"+event.FailureContext, event.OccurredAt)
			}
			if event.ToolName != "" {
				addCodexToolMetrics(record.ToolMetricsByName, event.ToolName, 0, event.Failed, event.Truncated, event.OutputBytes)
			}
			if event.Family != "" {
				addCodexToolMetrics(record.ShellCommandsByFamily, event.Family, 0, event.Failed, event.Truncated, event.OutputBytes)
			}
			if event.Shape != "" {
				addCodexToolMetrics(record.MixedShellShapes, event.Shape, 0, event.Failed, event.Truncated, event.OutputBytes)
				touchSessionActivity(record.Activity, "shape", event.Shape, event.OccurredAt)
			}
			for _, ownedTool := range ownership.match(event.SelectorDigests) {
				addCodexToolMetrics(record.OwnedTooling, ownedTool, 0, event.Failed, event.Truncated, event.OutputBytes)
				touchSessionActivity(record.Activity, "owned-tool", ownedTool, event.OccurredAt)
			}
			ownedOperations := eventOperations
			recordContinuationState(pendingContinuations, event, ownedOperations)
			recordProgressWait(record, event, ownedOperations, ownership)
			recordRapidContinuationPoll(record, event, ownedOperations)
			recordOversizedOutput(record, event, ownedOperations)
			for _, operation := range ownedOperations {
				target := record.OwnedOperations
				if event.OperationAttributionAmbiguous {
					target = record.OwnedOperationAmbiguous
				}
				addCodexToolMetrics(target, operation, 0, event.Failed, event.Truncated, event.OutputBytes)
				recordOwnedOperationTask(
					record.OwnedOperationTasks,
					operation,
					ownedOperationTask(event, ownership, record.Task),
					event.OperationAttributionAmbiguous,
					0,
					event.Failed,
					event.Truncated,
					event.OutputBytes,
				)
				touchSessionActivity(record.Activity, "owned-operation", operation, event.OccurredAt)
				if !event.OperationAttributionAmbiguous &&
					(event.Truncated || (event.Failed && !ownership.operationFailureExpected(operation, event.FailureReason))) {
					touchSessionActivity(record.Activity, "owned-operation-friction", operation, event.OccurredAt)
				}
				if !event.Failed {
					continue
				}
				if !event.OperationAttributionAmbiguous {
					addCodexFailureContext(record.OwnedOperationFailureReasons, operation, event.FailureReason)
				}
			}
		}
	}
	if !episode.StartedAt.IsZero() {
		record.TaskEpisodes = append(record.TaskEpisodes, episode)
	}
	if activeDiagnostic != nil {
		record.DiagnosticFailures = append(record.DiagnosticFailures, *activeDiagnostic)
	}
	for _, tracker := range deliveryTrackers {
		addDeliveryReworkMetrics(&record.DeliveryRework, tracker.metrics)
	}
	if record.DeliveryRework.Deliveries > 0 ||
		record.DeliveryRework.PostDeliveryReviewChecks > 0 {
		record.DeliveryRework.Sessions = 1
	}
	for _, tracker := range downstreamTrackers {
		addDownstreamQualityMetrics(&record.DownstreamQuality, tracker.metrics)
	}
	if record.DownstreamQuality.Deliveries > 0 ||
		record.DownstreamQuality.DeliveriesWithFailure > 0 ||
		record.DownstreamQuality.Reverts > 0 {
		record.DownstreamQuality.Sessions = 1
	}
	if record.DownstreamQuality.DeliveriesWithFailure > 0 ||
		record.DownstreamQuality.Reverts > 0 {
		record.DownstreamQuality.FailureSessions = 1
	}
	if record.StartedAt.IsZero() {
		return codexSessionRecord{}, nil
	}
	if lastEventKind == sessionEventComplete || generatedAt.Sub(record.EndedAt) >= 30*time.Minute {
		// ponytail: context-level accounting can undercount concurrent yielded
		// operations with the same attribution; add provider operation IDs if
		// this becomes material in real session evidence.
		for context, count := range pendingContinuations {
			if count > 0 {
				record.AbandonedContinuations[context] += count
				touchSessionActivity(record.Activity, "abandoned-continuation", context, record.EndedAt)
			}
		}
	}
	for operation, tasks := range record.OwnedOperationTasks {
		for task, metrics := range tasks {
			if metrics.Calls > 0 {
				metrics.Sessions = 1
				record.OwnedOperationTasks[operation][task] = metrics
			}
		}
	}
	touchSessionActivity(record.Activity, "task", record.Task, record.EndedAt)
	return record, nil
}

func newTaskEpisode(record codexSessionRecord) codexTaskEpisode {
	return codexTaskEpisode{
		AgentKind:       record.AgentKind,
		Model:           record.Model,
		ReasoningEffort: record.ReasoningEffort,
	}
}

func normalizedTokenUsageIncrement(current, previous normalizedTokenUsage, hasPrevious bool) normalizedTokenUsage {
	if !hasPrevious || current.TotalTokens < previous.TotalTokens {
		return current
	}
	return normalizedTokenUsage{
		InputTokens:         max(int64(0), current.InputTokens-previous.InputTokens),
		CachedInputTokens:   max(int64(0), current.CachedInputTokens-previous.CachedInputTokens),
		UncachedInputTokens: max(int64(0), current.UncachedInputTokens-previous.UncachedInputTokens),
		OutputTokens:        max(int64(0), current.OutputTokens-previous.OutputTokens),
		ReasoningTokens:     max(int64(0), current.ReasoningTokens-previous.ReasoningTokens),
		TotalTokens:         max(int64(0), current.TotalTokens-previous.TotalTokens),
	}
}

func newCodexSessionRecord() codexSessionRecord {
	return codexSessionRecord{
		ToolCallsByName:              map[string]int{},
		ToolMetricsByName:            map[string]codexToolMetrics{},
		ShellCommandsByFamily:        map[string]codexToolMetrics{},
		MixedShellShapes:             map[string]codexToolMetrics{},
		CrossCallTransitions:         map[string]int{},
		OwnedTooling:                 map[string]codexToolMetrics{},
		OwnedOperations:              map[string]codexToolMetrics{},
		OwnedOperationAmbiguous:      map[string]codexToolMetrics{},
		OwnedOperationTasks:          map[string]map[string]codexOwnedOperationMetrics{},
		OwnedOperationFailureReasons: map[string]map[string]int{},
		ReadTargets:                  map[string]codexTargetMetrics{},
		EditTargets:                  map[string]int{},
		InlineOrchestrationByTool:    map[string]codexInlineMetrics{},
		InlineOrchestrationByFamily:  map[string]codexInlineMetrics{},
		InlineOrchestrationByOwner:   map[string]codexInlineMetrics{},
		FailureReasons:               map[string]int{},
		FailureContexts:              map[string]map[string]int{},
		ProgressStalls:               map[string]codexWaitMetrics{},
		ExpectedWaits:                map[string]codexWaitMetrics{},
		RapidPolls:                   map[string]codexWaitMetrics{},
		AbandonedContinuations:       map[string]int{},
		OversizedOutputs:             map[string]codexOversizedOutputMetrics{},
		Activity:                     map[string]time.Time{},
	}
}

func recordInlineMetric(target map[string]codexInlineMetrics, key string, bytes int64) {
	metrics := target[key]
	metrics.Calls++
	metrics.Bytes += bytes
	metrics.MaxBytes = max(metrics.MaxBytes, bytes)
	target[key] = metrics
}

func inlineOrchestrationOwner(
	event normalizedSessionEvent,
	operations []string,
	ownership ownershipCatalog,
) string {
	owner := ""
	for _, operation := range operations {
		if len(operation) > len(owner) || len(operation) == len(owner) && operation < owner {
			owner = operation
		}
	}
	if owner != "" {
		return owner
	}
	tools := ownership.match(event.SelectorDigests)
	sort.Strings(tools)
	if len(tools) > 0 {
		return tools[0]
	}
	return "(unowned)"
}

func recordContinuationState(pending map[string]int, event normalizedSessionEvent, ownedOperations []string) {
	context := progressWaitContext(event, ownedOperations)
	if event.OperationContinues {
		if pending[context] == 0 || !continuedOperationToolName(event.ToolName) {
			pending[context]++
		}
		return
	}
	if !continuedOperationToolName(event.ToolName) || pending[context] == 0 {
		return
	}
	pending[context]--
}

func continuedOperationToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "exec", "wait", "write_stdin":
		return true
	default:
		return false
	}
}

func ownedOperationTask(event normalizedSessionEvent, ownership ownershipCatalog, fallback string) string {
	if event.OperationAttributionAmbiguous {
		if fallback = strings.TrimSpace(fallback); fallback != "" {
			return fallback
		}
		return "(root)"
	}
	if task := strings.TrimSpace(event.OperationTask); task != "" {
		return task
	}
	if task := strings.TrimSpace(ownership.taskForInvocations(event.CommandCandidates)); task != "" {
		return task
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		return fallback
	}
	return "(root)"
}

func recordOwnedOperationTask(
	target map[string]map[string]codexOwnedOperationMetrics,
	operation string,
	task string,
	ambiguous bool,
	calls int,
	failed bool,
	truncated bool,
	outputBytes int64,
) {
	if target[operation] == nil {
		target[operation] = map[string]codexOwnedOperationMetrics{}
	}
	metrics := target[operation][task]
	metrics.Calls += calls
	if ambiguous {
		metrics.AmbiguousCalls += calls
		if failed {
			metrics.AmbiguousFailedCalls++
		}
		if truncated {
			metrics.AmbiguousTruncatedCalls++
		}
		metrics.AmbiguousOutputBytes += outputBytes
		metrics.EstimatedAmbiguousOutputTokens = estimatedTokens(metrics.AmbiguousOutputBytes)
	} else {
		if failed {
			metrics.FailedCalls++
		}
		if truncated {
			metrics.TruncatedCalls++
		}
		metrics.OutputBytes += outputBytes
		metrics.EstimatedOutputTokens = estimatedTokens(metrics.OutputBytes)
	}
	target[operation][task] = metrics
}

const oversizedOutputMinimumBytes int64 = 30_000

func recordOversizedOutput(record codexSessionRecord, event normalizedSessionEvent, ownedOperations []string) {
	if event.OutputBytes < oversizedOutputMinimumBytes {
		return
	}
	if event.OperationAttributionAmbiguous {
		ownedOperations = nil
	}
	context := "concurrent tool batch"
	if !event.ConcurrentBatch {
		context = oversizedOutputContext(event, ownedOperations)
	}
	metrics := record.OversizedOutputs[context]
	metrics.Calls++
	metrics.OutputBytes += event.OutputBytes
	metrics.MaxOutputBytes = max(metrics.MaxOutputBytes, event.OutputBytes)
	record.OversizedOutputs[context] = metrics
	touchSessionActivity(record.Activity, "oversized-output", context, event.OccurredAt)
}

func oversizedOutputContext(event normalizedSessionEvent, ownedOperations []string) string {
	if len(ownedOperations) > 0 {
		return progressWaitContext(event, ownedOperations)
	}
	if event.Shape != "" {
		return event.Shape
	}
	return progressWaitContext(event, nil)
}

const progressStallMinimum = 20 * time.Second
const progressStallMaximumOutputBytes = 256
const rapidContinuationPollMaximum = 10 * time.Second
const rapidContinuationPollMaximumOutputBytes = 512

func recordProgressWait(record codexSessionRecord, event normalizedSessionEvent, ownedOperations []string, ownership ownershipCatalog) {
	if event.CallOccurredAt.IsZero() || !event.OccurredAt.After(event.CallOccurredAt) {
		return
	}
	duration := event.OccurredAt.Sub(event.CallOccurredAt)
	if duration < progressStallMinimum || event.OutputBytes > progressStallMaximumOutputBytes {
		return
	}
	context := progressWaitContext(event, ownedOperations)
	target := record.ProgressStalls
	expected := expectedProgressWait(event, ownedOperations, ownership)
	if expected {
		target = record.ExpectedWaits
	}
	metrics := target[context]
	metrics.Calls++
	metrics.Seconds += int64(duration.Seconds())
	target[context] = metrics
	kind := "progress-stall"
	if expected {
		kind = "expected-wait"
	}
	touchSessionActivity(record.Activity, kind, context, event.OccurredAt)
}

func recordRapidContinuationPoll(record codexSessionRecord, event normalizedSessionEvent, ownedOperations []string) {
	if !event.OperationContinues ||
		!continuationToolName(event.ToolName) ||
		event.CallOccurredAt.IsZero() ||
		!event.OccurredAt.After(event.CallOccurredAt) {
		return
	}
	duration := event.OccurredAt.Sub(event.CallOccurredAt)
	if duration > rapidContinuationPollMaximum ||
		event.OutputBytes > rapidContinuationPollMaximumOutputBytes {
		return
	}
	context := progressWaitContext(event, ownedOperations)
	metrics := record.RapidPolls[context]
	metrics.Calls++
	metrics.Seconds += int64(duration.Seconds())
	record.RapidPolls[context] = metrics
	touchSessionActivity(record.Activity, "rapid-poll", context, event.OccurredAt)
}

func continuationToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "wait", "write_stdin":
		return true
	default:
		return false
	}
}

func progressWaitContext(event normalizedSessionEvent, ownedOperations []string) string {
	specific := ""
	for _, operation := range ownedOperations {
		if len(operation) > len(specific) {
			specific = operation
		}
	}
	if specific != "" {
		return specific
	}
	if event.Family != "" {
		return event.Family
	}
	if event.ToolName != "" {
		return "tool " + event.ToolName
	}
	return "(unknown)"
}

func expectedProgressWait(event normalizedSessionEvent, ownedOperations []string, ownership ownershipCatalog) bool {
	switch event.Family {
	case "tests", "build, lint, or install", "review":
		return true
	}
	for _, operation := range ownedOperations {
		if ownership.operationWaitExpected(operation) ||
			operation == "bwb/comments" ||
			strings.HasPrefix(operation, "bwb/comments-") ||
			operation == "bwb/test" ||
			strings.HasPrefix(operation, "bwb/test-") {
			return true
		}
	}
	return false
}
