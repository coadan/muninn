package cli

import (
	"fmt"
	"strings"
)

type pendingHelpLookup struct {
	operation string
	round     int
	// superseded is the earlier pending lookup this one marked "repeated", so
	// a refused lookup can restore it.
	superseded *pendingHelpLookup
}

type pendingHelpUse struct {
	helpOperation string
	operation     string
	round         int
	// lookup is the pending lookup this use consumed, restored if the use is
	// refused.
	lookup pendingHelpLookup
}

type helpEffectivenessState struct {
	lookups map[string]pendingHelpLookup
	uses    map[int]pendingHelpUse
}

func (state *helpEffectivenessState) reset() {
	*state = helpEffectivenessState{}
}

func (state *helpEffectivenessState) observeCall(
	record *codexSessionRecord,
	event normalizedSessionEvent,
	operations []string,
	ownership ownershipCatalog,
) {
	if state.lookups == nil {
		state.lookups = map[string]pendingHelpLookup{}
	}
	if state.uses == nil {
		state.uses = map[int]pendingHelpUse{}
	}
	for tool, lookup := range state.lookups {
		if event.ToolRound > lookup.round+3 {
			recordHelpOutcome(record, lookup.operation, "abandoned")
			delete(state.lookups, tool)
		}
	}
	if event.OperationAttributionAmbiguous || len(operations) != 1 {
		return
	}
	operation := operations[0]
	tool, _, ok := strings.Cut(operation, "/")
	if !ok {
		return
	}
	if ownership.operationKind(operation) == "help" {
		lookup := pendingHelpLookup{operation: operation, round: event.ToolRound}
		if pending, exists := state.lookups[tool]; exists {
			recordHelpOutcome(record, pending.operation, "repeated")
			lookup.superseded = &pending
		}
		metrics := record.HelpEffectiveness[operation]
		metrics.Lookups++
		record.HelpEffectiveness[operation] = metrics
		touchSessionActivity(record.Activity, "help-lookup", operation, event.OccurredAt)
		state.lookups[tool] = lookup
		return
	}
	if pending, exists := state.lookups[tool]; exists {
		state.uses[event.ToolRound] = pendingHelpUse{
			helpOperation: pending.operation,
			operation:     operation,
			round:         event.ToolRound,
			lookup:        pending,
		}
		delete(state.lookups, tool)
	}
}

func (state *helpEffectivenessState) observeOutput(
	record *codexSessionRecord,
	event normalizedSessionEvent,
	operations []string,
	ownership ownershipCatalog,
) {
	if event.OperationAttributionAmbiguous || len(operations) != 1 {
		return
	}
	operation := operations[0]
	if ownership.operationKind(operation) == "help" && event.Failed {
		tool, _, _ := strings.Cut(operation, "/")
		if pending, exists := state.lookups[tool]; exists && pending.round == event.ToolRound {
			recordHelpOutcome(record, pending.operation, "lookup-failed")
			delete(state.lookups, tool)
		}
		return
	}
	use, exists := state.uses[event.ToolRound]
	if !exists || use.operation != operation {
		return
	}
	if event.Failed {
		recordHelpOutcome(record, use.helpOperation, "use-failed")
	} else {
		recordHelpOutcome(record, use.helpOperation, "success")
	}
	delete(state.uses, event.ToolRound)
}

// observeRefusal undoes the call-side bookkeeping of a call that never ran (see
// tool_refusals.go): a refused lookup is no lookup and un-repeats the lookup it
// superseded, and a refused use puts its lookup back as it was.
func (state *helpEffectivenessState) observeRefusal(
	record *codexSessionRecord,
	event normalizedSessionEvent,
	operations []string,
	ownership ownershipCatalog,
) {
	if event.OperationAttributionAmbiguous || len(operations) != 1 {
		return
	}
	operation := operations[0]
	tool, _, ok := strings.Cut(operation, "/")
	if !ok {
		return
	}
	if ownership.operationKind(operation) != "help" {
		use, exists := state.uses[event.ToolRound]
		if !exists || use.operation != operation {
			return
		}
		delete(state.uses, event.ToolRound)
		restored := use.lookup
		current, newer := state.lookups[tool]
		if !newer {
			state.lookups[tool] = restored
			return
		}
		// A newer lookup was issued before this refusal came back: it repeats
		// the restored one, which becomes the oldest in its chain.
		recordHelpOutcome(record, restored.operation, "repeated")
		node := &current
		for node.superseded != nil {
			node = node.superseded
		}
		node.superseded = &restored
		state.lookups[tool] = current
		return
	}
	head, exists := state.lookups[tool]
	if !exists {
		return
	}
	if head.round == event.ToolRound {
		uncountHelpLookup(record, head.operation, false)
		delete(state.lookups, tool)
		if previous := head.superseded; previous != nil {
			adjustHelpMetrics(record, previous.operation, func(metrics *helpEffectivenessMetrics) { metrics.RepeatedLookups-- })
			state.lookups[tool] = *previous
		}
		return
	}
	// Lookups issued together: the refused one was superseded by a later one,
	// which still repeats whatever the refused one superseded.
	for node := &head; node.superseded != nil; node = node.superseded {
		refused := node.superseded
		if refused.round != event.ToolRound {
			continue
		}
		uncountHelpLookup(record, refused.operation, true)
		node.superseded = refused.superseded
		state.lookups[tool] = head
		return
	}
}

// uncountHelpLookup removes a lookup that never ran, and its own "repeated"
// outcome when a later lookup superseded it.
func uncountHelpLookup(record *codexSessionRecord, operation string, repeated bool) {
	adjustHelpMetrics(record, operation, func(metrics *helpEffectivenessMetrics) {
		metrics.Lookups--
		if repeated {
			metrics.RepeatedLookups--
		}
	})
}

func adjustHelpMetrics(record *codexSessionRecord, operation string, adjust func(*helpEffectivenessMetrics)) {
	metrics := record.HelpEffectiveness[operation]
	adjust(&metrics)
	if metrics == (helpEffectivenessMetrics{}) {
		delete(record.HelpEffectiveness, operation)
		return
	}
	record.HelpEffectiveness[operation] = metrics
}

func (state *helpEffectivenessState) finish(record *codexSessionRecord, terminal bool) {
	if terminal {
		for tool, lookup := range state.lookups {
			recordHelpOutcome(record, lookup.operation, "abandoned")
			delete(state.lookups, tool)
		}
		for round, use := range state.uses {
			recordHelpOutcome(record, use.helpOperation, "abandoned")
			delete(state.uses, round)
		}
	}
	for operation, metrics := range record.HelpEffectiveness {
		if metrics.Lookups == 0 {
			continue
		}
		metrics.Sessions = 1
		if ineffectiveHelpLookups(metrics) > 0 {
			metrics.IneffectiveSessions = 1
		}
		record.HelpEffectiveness[operation] = metrics
	}
}

func recordHelpOutcome(record *codexSessionRecord, operation, outcome string) {
	metrics := record.HelpEffectiveness[operation]
	switch outcome {
	case "success":
		metrics.SuccessfulUses++
	case "use-failed":
		metrics.FailedUses++
	case "lookup-failed":
		metrics.FailedLookups++
	case "repeated":
		metrics.RepeatedLookups++
	case "abandoned":
		metrics.AbandonedLookups++
	}
	record.HelpEffectiveness[operation] = metrics
}

func ineffectiveHelpLookups(metrics helpEffectivenessMetrics) int {
	return metrics.FailedLookups + metrics.FailedUses +
		metrics.RepeatedLookups + metrics.AbandonedLookups
}

func buildHelpEffectivenessFindings(report codexSessionInsightsReport) []sessionFinding {
	var findings []sessionFinding
	for operation, metrics := range report.Summary.HelpEffectiveness {
		ineffective := ineffectiveHelpLookups(metrics)
		if metrics.Lookups < 5 || ineffective < 3 ||
			metrics.IneffectiveSessions < 2 ||
			ratio(float64(ineffective), float64(metrics.Lookups)) < 0.5 {
			continue
		}
		findings = append(findings, sessionFinding{
			Category: "help-effectiveness",
			Control:  "local",
			Title:    "help does not reliably unblock the next operation: " + operation,
			Evidence: fmt.Sprintf(
				"%s lookups across %s; %s successful next uses, %s failed uses, %s failed lookups, %s repeated lookups, and %s abandonments",
				formatCodexCount(int64(metrics.Lookups)),
				formatCodexCountNoun(int64(metrics.Sessions), "session"),
				formatCodexCount(int64(metrics.SuccessfulUses)),
				formatCodexCount(int64(metrics.FailedUses)),
				formatCodexCount(int64(metrics.FailedLookups)),
				formatCodexCount(int64(metrics.RepeatedLookups)),
				formatCodexCount(int64(metrics.AbandonedLookups)),
			),
			Action:     "Make this help surface answer the observed next-step question with exact supported syntax and a concise failure contract; if agents still need another lookup, route directly to the authoritative topic.",
			Count:      ineffective,
			Sessions:   metrics.IneffectiveSessions,
			Target:     operation,
			LastSeen:   sessionFindingLastSeen(report, "help-lookup", operation),
			Lever:      "tooling/docs",
			Confidence: "medium",
			Why:        "Help is useful only when it enables a successful owned operation without another lookup or abandonment.",
			score:      720 + metrics.IneffectiveSessions*30 + ineffective*10,
		})
	}
	return findings
}
