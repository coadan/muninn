package cli

import (
	"fmt"
	"strings"
)

const verificationEscalationSeparator = " -> "

type verificationEscalationState struct {
	broadCalls  map[int]string
	failedBroad string
	failedRound int
}

func (state *verificationEscalationState) reset() {
	*state = verificationEscalationState{}
}

func (state *verificationEscalationState) observeCall(
	record *codexSessionRecord,
	event normalizedSessionEvent,
	operations []string,
	ownership ownershipCatalog,
) {
	if event.ToolName == "apply_patch" {
		state.failedBroad = ""
		state.failedRound = 0
		return
	}
	if state.failedRound > 0 && event.ToolRound > state.failedRound+5 {
		state.failedBroad = ""
		state.failedRound = 0
	}
	if event.OperationAttributionAmbiguous || len(operations) != 1 {
		return
	}
	operation := operations[0]
	switch ownership.operationKind(operation) {
	case "verification-broad":
		if state.broadCalls == nil {
			state.broadCalls = map[int]string{}
		}
		state.broadCalls[event.ToolRound] = operation
	case "verification-focused":
		if state.failedBroad == "" || !sameOwnedTool(state.failedBroad, operation) {
			return
		}
		pair := state.failedBroad + verificationEscalationSeparator + operation
		record.VerificationEscalations[pair]++
		touchSessionActivity(record.Activity, "verification-escalation", pair, event.OccurredAt)
		state.failedBroad = ""
		state.failedRound = 0
	}
}

func (state *verificationEscalationState) observeOutput(
	event normalizedSessionEvent,
	operations []string,
) {
	broad, exists := state.broadCalls[event.ToolRound]
	if !exists {
		return
	}
	delete(state.broadCalls, event.ToolRound)
	if event.OperationAttributionAmbiguous || len(operations) != 1 ||
		operations[0] != broad || !event.Failed {
		return
	}
	state.failedBroad = broad
	state.failedRound = event.ToolRound
}

func sameOwnedTool(left, right string) bool {
	leftTool, _, leftOK := strings.Cut(left, "/")
	rightTool, _, rightOK := strings.Cut(right, "/")
	return leftOK && rightOK && leftTool == rightTool
}

func buildVerificationEscalationFindings(report codexSessionInsightsReport) []sessionFinding {
	var findings []sessionFinding
	for pair, metrics := range report.Summary.VerificationEscalations {
		if metrics.Count < 3 || metrics.Sessions < 2 {
			continue
		}
		broad, focused, ok := strings.Cut(pair, verificationEscalationSeparator)
		if !ok {
			continue
		}
		findings = append(findings, sessionFinding{
			Category: "verification-escalation",
			Control:  "local",
			Title:    "broad verification repeatedly precedes focused diagnosis: " + broad,
			Evidence: fmt.Sprintf(
				"%s failed %s runs were followed by %s before an edit across %s",
				formatCodexCount(int64(metrics.Count)),
				broad,
				focused,
				formatCodexCountNoun(int64(metrics.Sessions), "session"),
			),
			Action: fmt.Sprintf(
				"Run %s during iteration and reserve %s for the coherent verification boundary; if the broad check is required first, make its failure route directly to the focused diagnosis.",
				focused,
				broad,
			),
			Count:      metrics.Count,
			Sessions:   metrics.Sessions,
			Target:     pair,
			LastSeen:   sessionFindingLastSeen(report, "verification-escalation", pair),
			Lever:      "tests/tooling",
			Confidence: "medium",
			Why:        "Repeatedly using a broad failing check to discover which focused check to run spends the expensive verification boundary too early.",
			score:      770 + metrics.Sessions*30 + metrics.Count*10,
		})
	}
	return findings
}
