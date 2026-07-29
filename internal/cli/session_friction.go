package cli

import (
	"strings"
	"time"
)

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
