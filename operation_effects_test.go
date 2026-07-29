package main

import (
	"testing"
	"time"
)

func TestAnalyzeOwnedOperationEffectsMatchesComparableTasks(t *testing.T) {
	start := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	var episodes []codexTaskEpisode
	for index := 0; index < 6; index++ {
		used := index < 3
		calls := 20
		fresh := int64(1_000)
		duration := 10 * time.Minute
		operations := map[string]int{}
		if used {
			calls = 10
			fresh = 500
			duration = 5 * time.Minute
			operations["repo/inspect"] = 1
		}
		episodes = append(episodes, codexTaskEpisode{
			AgentKind:       "root",
			Model:           "gpt-5.6-sol",
			ReasoningEffort: "high",
			StartedAt:       start,
			EndedAt:         start.Add(duration),
			Completed:       true,
			ToolCalls:       calls,
			Tokens:          normalizedTokenUsage{UncachedInputTokens: fresh},
			TargetCohorts:   map[string]int{"src/app": 1},
			OwnedOperations: operations,
		})
	}
	effects := analyzeOwnedOperationEffects(episodes)
	if len(effects) != 1 {
		t.Fatalf("effects=%#v want one", effects)
	}
	got := effects[0]
	if got.Operation != "repo/inspect" || got.MatchedCohorts != 1 ||
		got.TasksWith != 3 || got.TasksWithout != 3 ||
		got.Direction != "lower-cost" ||
		got.FreshTokenDelta != -0.5 ||
		got.ToolRoundtripDelta != -0.5 ||
		got.DurationDelta != -0.5 {
		t.Fatalf("effect mismatch: %#v", got)
	}
}
