package cli

import (
	"fmt"
	"strings"
)

const ownedOperationChainSeparator = " -> "

type ownedOperationChainState struct {
	operations [2]string
	rounds     [2]int
	length     int
}

func (state *ownedOperationChainState) reset() {
	*state = ownedOperationChainState{}
}

func (state *ownedOperationChainState) observe(
	record *codexSessionRecord,
	event normalizedSessionEvent,
	operations []string,
) {
	if event.OperationAttributionAmbiguous || len(operations) != 1 {
		state.reset()
		return
	}
	operation := strings.TrimSpace(operations[0])
	if operation == "" {
		state.reset()
		return
	}
	if state.length > 0 && event.ToolRound != state.rounds[state.length-1]+1 {
		state.reset()
	}
	if state.length == 2 {
		chain := strings.Join([]string{state.operations[0], state.operations[1], operation}, ownedOperationChainSeparator)
		if state.operations[0] != state.operations[1] || state.operations[1] != operation {
			record.OwnedOperationChains[chain]++
			touchSessionActivity(record.Activity, "owned-operation-chain", chain, event.OccurredAt)
		}
		state.operations[0] = state.operations[1]
		state.rounds[0] = state.rounds[1]
		state.operations[1] = operation
		state.rounds[1] = event.ToolRound
		return
	}
	state.operations[state.length] = operation
	state.rounds[state.length] = event.ToolRound
	state.length++
}

func buildOwnedOperationChainFindings(report codexSessionInsightsReport) []sessionFinding {
	var findings []sessionFinding
	for chain, metrics := range report.Summary.OwnedOperationChains {
		if metrics.Sessions < 2 || metrics.Count < max(6, metrics.Sessions*3) {
			continue
		}
		operations := strings.Split(chain, ownedOperationChainSeparator)
		if len(operations) != 3 || distinctOwnedOperations(operations) < 2 {
			continue
		}
		tool := sharedOwnedOperationTool(operations)
		action := "Provide one repository-owned operation that owns this repeated three-step workflow, with bounded progress and focused follow-ups."
		if tool != "" {
			action = fmt.Sprintf(
				"Add or route agents to one %s operation that owns this repeated three-step workflow, with bounded progress and focused follow-ups.",
				tool,
			)
		}
		findings = append(findings, sessionFinding{
			Category:   "operation-chain",
			Control:    "local",
			Title:      "recurring owned-operation chain: " + chain,
			Evidence:   fmt.Sprintf("%s strict three-step chains across %s; each step was one definitely attributed configured operation in an adjacent outer call", formatCodexCount(int64(metrics.Count)), formatCodexCountNoun(int64(metrics.Sessions), "session")),
			Action:     action,
			Count:      metrics.Count,
			Sessions:   metrics.Sessions,
			Target:     chain,
			LastSeen:   sessionFindingLastSeen(report, "owned-operation-chain", chain),
			Lever:      "tooling",
			Confidence: "medium",
			Why:        "A recurring exact operation chain is evidence that agents are reconstructing a stable workflow across multiple roundtrips.",
			score:      760 + metrics.Sessions*25 + metrics.Count,
		})
	}
	return findings
}

func distinctOwnedOperations(operations []string) int {
	distinct := map[string]struct{}{}
	for _, operation := range operations {
		distinct[operation] = struct{}{}
	}
	return len(distinct)
}

func sharedOwnedOperationTool(operations []string) string {
	shared := ""
	for _, operation := range operations {
		tool, _, found := strings.Cut(operation, "/")
		if !found || tool == "" {
			return ""
		}
		if shared == "" {
			shared = tool
			continue
		}
		if shared != tool {
			return ""
		}
	}
	return shared
}
