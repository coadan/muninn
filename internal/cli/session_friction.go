package cli

import (
	"strings"
	"time"
)

const oversizedOutputMinimumBytes int64 = 30_000
const concurrentBatchOutputBudgetTokens int64 = 12_000
const concurrentBatchOutputMinimumBytes = 4 * concurrentBatchOutputBudgetTokens

func recordOversizedOutput(record codexSessionRecord, event normalizedSessionEvent, ownedOperations []string) {
	minimumBytes := oversizedOutputMinimumBytes
	if event.ConcurrentBatch {
		minimumBytes = concurrentBatchOutputMinimumBytes
	}
	if event.OutputBytes < minimumBytes {
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
	if event.ConcurrentBatch {
		metrics.NestedCalls += event.ConcurrentBatchSize
		metrics.MaxNestedCalls = max(metrics.MaxNestedCalls, event.ConcurrentBatchSize)
	}
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
	if event.Family != "" {
		return event.Family
	}
	if event.NestedToolContext != "" {
		return event.NestedToolContext
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
		continuationPollSurface(event) == "" ||
		event.CallOccurredAt.IsZero() ||
		!event.OccurredAt.After(event.CallOccurredAt) {
		return
	}
	duration := event.OccurredAt.Sub(event.CallOccurredAt)
	if duration > rapidContinuationPollMaximum ||
		event.OutputBytes > rapidContinuationPollMaximumOutputBytes {
		return
	}
	context := rapidPollContext(event, ownedOperations)
	metrics := record.RapidPolls[context]
	metrics.Calls++
	metrics.Seconds += int64(duration.Seconds())
	record.RapidPolls[context] = metrics
	touchSessionActivity(record.Activity, "rapid-poll", context, event.OccurredAt)
}

func rapidPollContext(event normalizedSessionEvent, ownedOperations []string) string {
	if operation := mostSpecificOwnedOperation(ownedOperations); operation != "" {
		return operation
	}
	if surface := continuationPollSurface(event); surface != "" {
		return surface
	}
	return progressWaitContext(event, nil)
}

func continuationPollSurface(event normalizedSessionEvent) string {
	switch strings.ToLower(strings.TrimSpace(event.ToolName)) {
	case "wait", "write_stdin":
		return strings.ToLower(strings.TrimSpace(event.ToolName))
	}
	if !strings.EqualFold(strings.TrimSpace(event.ToolName), "exec") {
		return ""
	}
	for _, surface := range []string{"write_stdin", "wait"} {
		if strings.Contains(event.NestedToolContext, surface) {
			return surface
		}
	}
	return ""
}

func progressWaitContext(event normalizedSessionEvent, ownedOperations []string) string {
	if operation := mostSpecificOwnedOperation(ownedOperations); operation != "" {
		return operation
	}
	if event.Family != "" {
		return event.Family
	}
	if event.ToolName != "" {
		return "tool " + event.ToolName
	}
	return "(unknown)"
}

func mostSpecificOwnedOperation(ownedOperations []string) string {
	specific := ""
	for _, operation := range ownedOperations {
		if len(operation) > len(specific) {
			specific = operation
		}
	}
	return specific
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
