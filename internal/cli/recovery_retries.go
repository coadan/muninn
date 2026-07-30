package cli

import "fmt"

type ownedOperationRetryState struct {
	lastCallOperation string
	lastCallRound     int
	failedOperation   string
	failedRound       int
	retryOperation    string
	retryRound        int
}

func (state *ownedOperationRetryState) reset() {
	*state = ownedOperationRetryState{}
}

func (state *ownedOperationRetryState) observeCall(
	event normalizedSessionEvent,
	operations []string,
) {
	state.retryOperation = ""
	state.retryRound = 0
	if event.OperationAttributionAmbiguous || len(operations) != 1 {
		state.failedOperation = ""
		state.failedRound = 0
		state.lastCallOperation = ""
		state.lastCallRound = event.ToolRound
		return
	}
	operation := operations[0]
	if operation == state.failedOperation && event.ToolRound == state.failedRound+1 {
		state.retryOperation = operation
		state.retryRound = event.ToolRound
	}
	state.failedOperation = ""
	state.failedRound = 0
	state.lastCallOperation = operation
	state.lastCallRound = event.ToolRound
}

func (state *ownedOperationRetryState) observeOutput(
	record *codexSessionRecord,
	event normalizedSessionEvent,
	operations []string,
) {
	if event.OperationContinues ||
		event.OperationAttributionAmbiguous ||
		len(operations) != 1 {
		return
	}
	operation := operations[0]
	if state.retryOperation == operation && state.retryRound == event.ToolRound {
		metrics := record.OwnedOperationRetries[operation]
		metrics.Attempts++
		if event.Failed {
			metrics.RepeatedFailures++
		} else {
			metrics.SuccessfulRetries++
		}
		record.OwnedOperationRetries[operation] = metrics
		touchSessionActivity(record.Activity, "owned-operation-retry", operation, event.OccurredAt)
		state.retryOperation = ""
		state.retryRound = 0
	}
	if event.ToolRound != state.lastCallRound || operation != state.lastCallOperation {
		return
	}
	if event.Failed {
		state.failedOperation = operation
		state.failedRound = event.ToolRound
		return
	}
	state.failedOperation = ""
	state.failedRound = 0
}

func buildOwnedOperationRetryFindings(report codexSessionInsightsReport) []sessionFinding {
	var findings []sessionFinding
	for operation, metrics := range report.Summary.OwnedOperationRetries {
		if metrics.RepeatedFailures < 3 || metrics.RepeatedFailureSessions < 2 {
			continue
		}
		findings = append(findings, sessionFinding{
			Category: "recovery-loop",
			Control:  "local",
			Title:    "owned operation is retried unchanged after failure: " + operation,
			Evidence: fmt.Sprintf(
				"%s immediate unchanged retries across %s; %s failed again and %s succeeded",
				formatCodexCount(int64(metrics.Attempts)),
				formatCodexCountNoun(int64(metrics.Sessions), "session"),
				formatCodexCount(int64(metrics.RepeatedFailures)),
				formatCodexCount(int64(metrics.SuccessfulRetries)),
			),
			Action: fmt.Sprintf(
				"Stop automatically retrying %s without a state change; return the diagnostic or required recovery action first, then retry only after that condition changes.",
				operation,
			),
			Count:      metrics.RepeatedFailures,
			Sessions:   metrics.RepeatedFailureSessions,
			Target:     operation,
			LastSeen:   sessionFindingLastSeen(report, "owned-operation-retry", operation),
			Lever:      "tooling",
			Confidence: "high",
			Why:        "An adjacent retry that fails again spends another roundtrip without changing the condition that produced the first failure.",
			score:      860 + metrics.RepeatedFailureSessions*35 + metrics.RepeatedFailures*15,
		})
	}
	return findings
}
