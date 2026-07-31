package cli

import (
	"fmt"
	"strings"
	"time"
)

type searchEffectivenessMetrics struct {
	Bursts               int `json:"bursts"`
	SearchCalls          int `json:"searchCalls"`
	InefficientBursts    int `json:"inefficientBursts"`
	InefficientResolved  int `json:"inefficientResolved"`
	InefficientAbandoned int `json:"inefficientAbandoned"`
	Sessions             int `json:"sessions"`
	InefficientSessions  int `json:"inefficientSessions"`
}

type searchBurstState struct {
	active    bool
	target    string
	searches  int
	lastRound int
}

func (state *searchBurstState) reset() {
	*state = searchBurstState{}
}

func (state *searchBurstState) observeCall(
	record *codexSessionRecord,
	event normalizedSessionEvent,
	operations []string,
	ownership ownershipCatalog,
) {
	if state.active && event.ToolRound != state.lastRound+1 {
		state.finish(record, false, event.OccurredAt)
	}
	search := event.FirstFamily == "search" || event.LastFamily == "search" ||
		oneOperationHasKind(operations, ownership, "search")
	read := event.FirstFamily == "file reads" || event.LastFamily == "file reads"
	edit := event.ToolName == "apply_patch"
	if search {
		target := searchBurstTarget(event, operations, ownership)
		if !state.active {
			state.active = true
			state.target = target
		} else if state.target != target {
			state.target = "search"
		}
		state.searches++
		state.lastRound = event.ToolRound
		if read || edit {
			state.finish(record, true, event.OccurredAt)
		}
		return
	}
	if !state.active {
		return
	}
	if read || edit {
		state.finish(record, true, event.OccurredAt)
		return
	}
	state.finish(record, false, event.OccurredAt)
}

func oneOperationHasKind(operations []string, ownership ownershipCatalog, kind string) bool {
	return len(operations) == 1 && ownership.operationKind(operations[0]) == kind
}

func (state *searchBurstState) finish(
	record *codexSessionRecord,
	resolved bool,
	occurredAt time.Time,
) {
	if !state.active {
		return
	}
	metrics := record.SearchEffectiveness[state.target]
	metrics.Bursts++
	metrics.SearchCalls += state.searches
	if state.searches >= 3 {
		metrics.InefficientBursts++
		if resolved {
			metrics.InefficientResolved++
		} else {
			metrics.InefficientAbandoned++
		}
		touchSessionActivity(record.Activity, "search-inefficiency", state.target, occurredAt)
	}
	record.SearchEffectiveness[state.target] = metrics
	state.reset()
}

func (state *searchBurstState) finishSession(record *codexSessionRecord, terminal bool) {
	if terminal {
		state.finish(record, false, record.EndedAt)
	}
	for target, metrics := range record.SearchEffectiveness {
		if metrics.Bursts == 0 {
			continue
		}
		metrics.Sessions = 1
		if metrics.InefficientBursts > 0 {
			metrics.InefficientSessions = 1
		}
		record.SearchEffectiveness[target] = metrics
	}
}

func searchBurstTarget(
	event normalizedSessionEvent,
	operations []string,
	ownership ownershipCatalog,
) string {
	if !event.OperationAttributionAmbiguous && len(operations) == 1 &&
		ownership.operationKind(operations[0]) == "search" {
		return operations[0]
	}
	return "search"
}

func buildSearchEffectivenessFindings(report codexSessionInsightsReport) []sessionFinding {
	var findings []sessionFinding
	for target, metrics := range report.Summary.SearchEffectiveness {
		if metrics.InefficientBursts < 3 || metrics.InefficientSessions < 2 {
			continue
		}
		control := "repository"
		lever := "repository workflow"
		confidence := "medium"
		action := "Improve the bounded search-to-owner route: rank authoritative owners first, return small source pointers, and make the next focused read explicit."
		if strings.Contains(target, "/") {
			control = "local"
			lever = "tooling"
			confidence = "high"
			action = fmt.Sprintf(
				"Improve %s so one call returns the authoritative owner and a bounded next read; preserve query privacy and measure whether the search burst disappears.",
				target,
			)
		}
		findings = append(findings, sessionFinding{
			Category: "search-inefficiency",
			Control:  control,
			Title:    "search requires repeated roundtrips before reaching an owner: " + target,
			Evidence: fmt.Sprintf(
				"%s bursts used at least 3 separate search calls across %s; %s eventually reached a read or edit and %s were abandoned",
				formatCodexCount(int64(metrics.InefficientBursts)),
				formatCodexCountNoun(int64(metrics.InefficientSessions), "session"),
				formatCodexCount(int64(metrics.InefficientResolved)),
				formatCodexCount(int64(metrics.InefficientAbandoned)),
			),
			Action:     action,
			Count:      metrics.InefficientBursts,
			Sessions:   metrics.InefficientSessions,
			Target:     target,
			LastSeen:   sessionFindingLastSeen(report, "search-inefficiency", target),
			Lever:      lever,
			Confidence: confidence,
			Why:        "Several separate searches before the first bounded read or edit indicate low search-to-owner yield without requiring access to query text.",
			score:      740 + metrics.InefficientSessions*30 + metrics.InefficientBursts*10,
		})
	}
	return findings
}
