package cli

import "testing"

func TestNormalizedTokenUsageIncrementHandlesCounterReset(t *testing.T) {
	previous := normalizedTokenUsage{
		InputTokens:         1_000,
		CachedInputTokens:   800,
		UncachedInputTokens: 200,
		OutputTokens:        100,
		TotalTokens:         1_100,
	}
	reset := normalizedTokenUsage{
		InputTokens:         80,
		CachedInputTokens:   50,
		UncachedInputTokens: 30,
		OutputTokens:        10,
		TotalTokens:         90,
	}
	if got := normalizedTokenUsageIncrement(reset, previous, true); got != reset {
		t.Fatalf("counter reset should start a new token epoch: %#v", got)
	}
}

func TestAddCodexTargetMetricsCountsSessionsWithRediscovery(t *testing.T) {
	target := map[string]codexTargetMetrics{}
	addCodexTargetMetrics(target, map[string]codexTargetMetrics{
		"deps.edn": {Reads: 1},
	}, nil)
	addCodexTargetMetrics(target, map[string]codexTargetMetrics{
		"deps.edn": {Reads: 2},
	}, map[string]int{"deps.edn": 1})
	addCodexTargetMetrics(target, map[string]codexTargetMetrics{
		"deps.edn": {Reads: 1, SearchReadLoops: 1},
	}, nil)
	got := target["deps.edn"]
	if got.Sessions != 3 || got.Reads != 4 || got.SearchReadLoops != 1 ||
		got.RediscoverySessions != 2 || got.EditedSessions != 1 ||
		got.UneditedRediscoverySessions != 1 {
		t.Fatalf("target metrics=%#v want edited and unedited rediscovery separated", got)
	}
}

func TestAddCodexOwnedToolMetricsRetainsBundledSubset(t *testing.T) {
	metrics := map[string]codexToolMetrics{}
	addCodexOwnedToolMetrics(metrics, "repo", false, 1, false, false, 400)
	addCodexOwnedToolMetrics(metrics, "repo", true, 1, true, true, 800)

	got := metrics["repo"]
	if got.Calls != 2 || got.AmbiguousCalls != 1 ||
		got.FailedCalls != 1 || got.AmbiguousFailedCalls != 1 ||
		got.TruncatedCalls != 1 || got.AmbiguousTruncatedCalls != 1 ||
		got.OutputBytes != 1_200 || got.AmbiguousOutputBytes != 800 {
		t.Fatalf("owned tool metrics did not retain bundled subset: %#v", got)
	}
}
