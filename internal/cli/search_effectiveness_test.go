package cli

import (
	"strings"
	"testing"
	"time"
)

func TestSearchBurstTracksConfiguredSearchToOwnerYield(t *testing.T) {
	record := newCodexSessionRecord()
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "ygg",
		Executables: []string{"ygg"},
		Operations: []ownedOperationConfig{{
			ID: "search", Args: []string{"search"}, Kind: "search",
		}},
	}})
	state := searchBurstState{}
	for round := 1; round <= 3; round++ {
		event := normalizedSessionEvent{
			Kind: sessionEventToolCall, ToolRound: round,
			OwnedOperations: []string{"ygg/search"},
			OccurredAt:      time.Date(2026, 7, 30, 12, 0, round, 0, time.UTC),
		}
		state.observeCall(&record, event, event.OwnedOperations, catalog)
	}
	read := normalizedSessionEvent{
		Kind: sessionEventToolCall, ToolRound: 4,
		FirstFamily: "file reads", LastFamily: "file reads",
		OccurredAt: time.Date(2026, 7, 30, 12, 0, 4, 0, time.UTC),
	}
	state.observeCall(&record, read, nil, catalog)
	state.finishSession(&record, true)
	metrics := record.SearchEffectiveness["ygg/search"]
	if metrics.Bursts != 1 || metrics.SearchCalls != 3 ||
		metrics.InefficientBursts != 1 || metrics.InefficientResolved != 1 ||
		metrics.InefficientAbandoned != 0 {
		t.Fatalf("search effectiveness=%#v", metrics)
	}
}

func TestSearchBurstDoesNotPenalizeBundledSearchAndRead(t *testing.T) {
	record := newCodexSessionRecord()
	state := searchBurstState{}
	event := normalizedSessionEvent{
		Kind: sessionEventToolCall, ToolRound: 1,
		FirstFamily: "search", LastFamily: "file reads",
	}
	state.observeCall(&record, event, nil, ownershipCatalog{})
	state.finishSession(&record, true)
	if metrics := record.SearchEffectiveness["search"]; metrics.InefficientBursts != 0 {
		t.Fatalf("bundled search/read was inefficient: %#v", metrics)
	}
}

func TestGenericSearchBurstRoutesToSingleConfiguredSearchOwner(t *testing.T) {
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "ygg",
		Executables: []string{"ygg"},
		Operations: []ownedOperationConfig{{
			ID: "search", Args: []string{"search"}, Kind: "search",
		}},
	}})
	event := normalizedSessionEvent{FirstFamily: "search", LastFamily: "search"}
	if got := searchBurstTarget(event, nil, catalog); got != "ygg/search" {
		t.Fatalf("search burst target=%q", got)
	}
}

func TestBuildSearchEffectivenessFindingsRoutesOwnedSearchToTooling(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), time.Time{}, time.Now().UTC())
	report.Summary.SearchEffectiveness["ygg/search"] = searchEffectivenessMetrics{
		Bursts: 8, SearchCalls: 29, InefficientBursts: 5,
		InefficientResolved: 4, InefficientAbandoned: 1,
		Sessions: 4, InefficientSessions: 3,
	}
	findings := buildSearchEffectivenessFindings(report)
	if len(findings) != 1 || findings[0].Target != "ygg/search" ||
		findings[0].Control != "local" || findings[0].Confidence != "high" ||
		!strings.Contains(findings[0].Action, "Improve ygg/search") {
		t.Fatalf("search effectiveness findings=%#v", findings)
	}
}
