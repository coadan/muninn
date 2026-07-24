package main

import (
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
	Tokens                        codexTokenUsage
	SelectorDigests               []string
	CommandCandidates             []ownedCommandInvocation
	OwnedOperations               []string
	OperationAttributionAmbiguous bool
	OperationContinues            bool
	TargetCandidates              []string
	Targets                       []string
	InlineBytes                   int64
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
	if err != nil || !inside {
		return codexSessionRecord{}, nil
	}

	previousCommandRound := 0
	previousCommand := normalizedSessionEvent{}
	previousTokens := codexTokenUsage{}
	hasPreviousTokens := false
	episode := codexTaskEpisode{}
	deliveryTracker := deliveryReworkTracker{}
	downstreamTracker := downstreamQualityTracker{}
	for _, event := range session.Events {
		event = withoutContinuationCallAttribution(event)
		if event.OccurredAt.Before(since) {
			if event.Kind == sessionEventToken {
				previousTokens = event.Tokens
				hasPreviousTokens = true
			}
			episode.LeftCensored = event.Kind != sessionEventComplete
			continue
		}
		if event.OccurredAt.After(generatedAt) {
			continue
		}
		tokenIncrement := codexTokenUsage{}
		if event.Kind == sessionEventToken {
			tokenIncrement = codexTokenUsageIncrement(event.Tokens, previousTokens, hasPreviousTokens)
			addCodexTokenUsage(&record.Tokens, tokenIncrement)
			previousTokens = event.Tokens
			hasPreviousTokens = true
		}
		if record.StartedAt.IsZero() || event.OccurredAt.Before(record.StartedAt) {
			record.StartedAt = event.OccurredAt
		}
		if record.EndedAt.IsZero() || event.OccurredAt.After(record.EndedAt) {
			record.EndedAt = event.OccurredAt
		}
		if event.Kind == sessionEventToolCall && len(event.Targets) == 0 {
			if event.ToolName == "apply_patch" {
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
			episode = codexTaskEpisode{}
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
				metrics := record.InlineOrchestrationByTool[event.ToolName]
				metrics.Calls++
				metrics.Bytes += event.InlineBytes
				metrics.MaxBytes = max(metrics.MaxBytes, event.InlineBytes)
				record.InlineOrchestrationByTool[event.ToolName] = metrics
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
				touchSessionActivity(record.Activity, "owned-operation", operation, event.OccurredAt)
			}
			for _, target := range event.Targets {
				metrics := record.ReadTargets[target]
				if event.ToolName == "apply_patch" {
					record.EditTargets[target]++
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
			episode.observe(event, codexTokenUsage{}, eventOperations)
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
			recordProgressWait(record, event, ownedOperations)
			recordOversizedOutput(record, event, ownedOperations)
			for _, operation := range ownedOperations {
				target := record.OwnedOperations
				if event.OperationAttributionAmbiguous {
					target = record.OwnedOperationAmbiguous
				}
				addCodexToolMetrics(target, operation, 0, event.Failed, event.Truncated, event.OutputBytes)
				touchSessionActivity(record.Activity, "owned-operation", operation, event.OccurredAt)
				if !event.OperationAttributionAmbiguous &&
					(event.Truncated || (event.Failed && !ownedOperationExpectedFailure(operation, event.FailureReason))) {
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
	record.DeliveryRework = deliveryTracker.metrics
	if record.DeliveryRework.Deliveries > 0 ||
		record.DeliveryRework.PostDeliveryReviewChecks > 0 {
		record.DeliveryRework.Sessions = 1
	}
	record.DownstreamQuality = downstreamTracker.metrics
	finalizeDownstreamQualityMetrics(&record.DownstreamQuality)
	if record.DownstreamQuality.Deliveries > 0 ||
		record.DownstreamQuality.DeliveriesWithFailure > 0 ||
		record.DownstreamQuality.Reverts > 0 {
		record.DownstreamQuality.Sessions = 1
	}
	if record.StartedAt.IsZero() {
		return codexSessionRecord{}, nil
	}
	record.Task = codexTaskName(workspaceRoot, record.CWD)
	touchSessionActivity(record.Activity, "task", record.Task, record.EndedAt)
	return record, nil
}

func codexTokenUsageIncrement(current, previous codexTokenUsage, hasPrevious bool) codexTokenUsage {
	if !hasPrevious || current.TotalTokens < previous.TotalTokens {
		return current
	}
	return codexTokenUsage{
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
		OwnedOperationFailureReasons: map[string]map[string]int{},
		ReadTargets:                  map[string]codexTargetMetrics{},
		EditTargets:                  map[string]int{},
		InlineOrchestrationByTool:    map[string]codexInlineMetrics{},
		FailureReasons:               map[string]int{},
		FailureContexts:              map[string]map[string]int{},
		ProgressStalls:               map[string]codexWaitMetrics{},
		ExpectedWaits:                map[string]codexWaitMetrics{},
		OversizedOutputs:             map[string]codexOversizedOutputMetrics{},
		Activity:                     map[string]time.Time{},
	}
}

const oversizedOutputMinimumBytes int64 = 30_000

func recordOversizedOutput(record codexSessionRecord, event normalizedSessionEvent, ownedOperations []string) {
	if event.OutputBytes < oversizedOutputMinimumBytes {
		return
	}
	if event.OperationAttributionAmbiguous {
		ownedOperations = nil
	}
	context := oversizedOutputContext(event, ownedOperations)
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

func recordProgressWait(record codexSessionRecord, event normalizedSessionEvent, ownedOperations []string) {
	if event.CallOccurredAt.IsZero() || !event.OccurredAt.After(event.CallOccurredAt) {
		return
	}
	duration := event.OccurredAt.Sub(event.CallOccurredAt)
	if duration < progressStallMinimum || event.OutputBytes > progressStallMaximumOutputBytes {
		return
	}
	context := progressWaitContext(event, ownedOperations)
	target := record.ProgressStalls
	if expectedProgressWait(event, ownedOperations) {
		target = record.ExpectedWaits
	}
	metrics := target[context]
	metrics.Calls++
	metrics.Seconds += int64(duration.Seconds())
	target[context] = metrics
	kind := "progress-stall"
	if expectedProgressWait(event, ownedOperations) {
		kind = "expected-wait"
	}
	touchSessionActivity(record.Activity, kind, context, event.OccurredAt)
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

func expectedProgressWait(event normalizedSessionEvent, ownedOperations []string) bool {
	switch event.Family {
	case "tests", "build, lint, or install", "review":
		return true
	}
	for _, operation := range ownedOperations {
		if operation == "bwb/comments" ||
			strings.HasPrefix(operation, "bwb/comments-") ||
			operation == "bwb/test" ||
			strings.HasPrefix(operation, "bwb/test-") {
			return true
		}
	}
	return false
}
