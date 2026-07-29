package cli

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type ownedOperationEffect struct {
	Operation          string  `json:"operation"`
	MatchedCohorts     int     `json:"matchedCohorts"`
	TasksWith          int     `json:"tasksWith"`
	TasksWithout       int     `json:"tasksWithout"`
	FreshTokenDelta    float64 `json:"freshTokenDelta"`
	ToolRoundtripDelta float64 `json:"toolRoundtripDelta"`
	DurationDelta      float64 `json:"durationDelta"`
	Direction          string  `json:"direction"`
}

func analyzeOwnedOperationEffects(episodes []codexTaskEpisode) []ownedOperationEffect {
	cohorts := map[string][]codexTaskEpisode{}
	operations := map[string]struct{}{}
	for _, episode := range episodes {
		family := taskPerformanceFamily(episode)
		if family == "(unknown)" {
			continue
		}
		key := strings.Join([]string{
			nonemptyProfileLabel(episode.AgentKind, "(unknown)"),
			nonemptyProfileLabel(episode.Model, "(unknown)"),
			nonemptyProfileLabel(episode.ReasoningEffort, "(unknown)"),
			family,
		}, "\x00")
		cohorts[key] = append(cohorts[key], episode)
		for operation := range taskCostDiagnosticOperations(episode.OwnedOperations) {
			operations[operation] = struct{}{}
		}
	}

	effects := make([]ownedOperationEffect, 0, len(operations))
	for operation := range operations {
		effect := ownedOperationEffect{Operation: operation}
		var freshDeltas, roundtripDeltas, durationDeltas []float64
		for _, cohort := range cohorts {
			var with, without []codexTaskEpisode
			for _, episode := range cohort {
				if episode.OwnedOperations[operation] > 0 {
					with = append(with, episode)
				} else {
					without = append(without, episode)
				}
			}
			if len(with) < 3 || len(without) < 3 {
				continue
			}
			effect.MatchedCohorts++
			effect.TasksWith += len(with)
			effect.TasksWithout += len(without)
			freshDeltas = append(freshDeltas, relativeEpisodeMedianDelta(with, without, episodeFreshTokens))
			roundtripDeltas = append(roundtripDeltas, relativeEpisodeMedianDelta(
				with,
				without,
				func(episode codexTaskEpisode) int64 { return int64(episode.ToolCalls) },
			))
			durationDeltas = append(durationDeltas, relativeEpisodeMedianDelta(with, without, taskEpisodeDuration))
		}
		if effect.MatchedCohorts == 0 {
			continue
		}
		effect.FreshTokenDelta = averageFloat64(freshDeltas)
		effect.ToolRoundtripDelta = averageFloat64(roundtripDeltas)
		effect.DurationDelta = averageFloat64(durationDeltas)
		effect.Direction = ownedOperationEffectDirection(effect)
		effects = append(effects, effect)
	}
	sort.Slice(effects, func(i, j int) bool {
		left := ownedOperationEffectMagnitude(effects[i])
		right := ownedOperationEffectMagnitude(effects[j])
		if left != right {
			return left > right
		}
		return effects[i].Operation < effects[j].Operation
	})
	return effects
}

func relativeEpisodeMedianDelta(
	with,
	without []codexTaskEpisode,
	value func(codexTaskEpisode) int64,
) float64 {
	withValues := make([]int64, 0, len(with))
	withoutValues := make([]int64, 0, len(without))
	for _, episode := range with {
		withValues = append(withValues, value(episode))
	}
	for _, episode := range without {
		withoutValues = append(withoutValues, value(episode))
	}
	withMedian := summarizeOutcomeDistribution(withValues).P50
	withoutMedian := summarizeOutcomeDistribution(withoutValues).P50
	return ratio(float64(withMedian-withoutMedian), math.Max(1, math.Abs(float64(withoutMedian))))
}

func averageFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func ownedOperationEffectDirection(effect ownedOperationEffect) string {
	deltas := []float64{effect.FreshTokenDelta, effect.ToolRoundtripDelta, effect.DurationDelta}
	higher := 0
	lower := 0
	for _, delta := range deltas {
		if delta >= 0.15 {
			higher++
		}
		if delta <= -0.15 {
			lower++
		}
	}
	switch {
	case higher >= 2 && lower == 0:
		return "higher-cost"
	case lower >= 2 && higher == 0:
		return "lower-cost"
	case higher == 0 && lower == 0:
		return "unchanged"
	default:
		return "mixed"
	}
}

func ownedOperationEffectMagnitude(effect ownedOperationEffect) float64 {
	return math.Max(
		math.Abs(effect.FreshTokenDelta),
		math.Max(math.Abs(effect.ToolRoundtripDelta), math.Abs(effect.DurationDelta)),
	)
}

func printOwnedOperationEffects(effects []ownedOperationEffect, limit int) {
	if len(effects) == 0 {
		return
	}
	rows := effects
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println("\nMatched owned-operation associations:")
	fmt.Printf("%-36s %8s %13s %10s %10s %10s %12s\n", "OPERATION", "COHORTS", "TASKS W/WO", "FRESH", "CALLS", "TIME", "DIRECTION")
	for _, effect := range rows {
		fmt.Printf(
			"%-36s %8d %6d/%-6d %9.0f%% %9.0f%% %9.0f%% %12s\n",
			truncateCodexLabel(effect.Operation, 36),
			effect.MatchedCohorts,
			effect.TasksWith,
			effect.TasksWithout,
			100*effect.FreshTokenDelta,
			100*effect.ToolRoundtripDelta,
			100*effect.DurationDelta,
			effect.Direction,
		)
	}
	fmt.Println("Observed within matched agent/model/effort/task-family cohorts; association is not causal.")
}
