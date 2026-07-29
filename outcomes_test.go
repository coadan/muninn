package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAnalyzeCompletionEpisodesUsesDistributionsNotAverages(t *testing.T) {
	var episodes []codexTaskEpisode
	for _, fresh := range []int64{10, 20, 30, 40, 100} {
		episodes = append(episodes, codexTaskEpisode{
			StartedAt: time.Unix(0, 0),
			EndedAt:   time.Unix(10, 0),
			Completed: true,
			Tokens: codexTokenUsage{
				CachedInputTokens:   2 * fresh,
				UncachedInputTokens: fresh,
				OutputTokens:        fresh / 10,
			},
			ToolCalls: int(fresh / 10),
		})
	}
	analysis := analyzeCompletionEpisodes(episodes)
	if analysis.FullyObservedCompleted != 5 {
		t.Fatalf("completed episode count mismatch: %#v", analysis)
	}
	if analysis.ToolUsingCompleted != 5 || analysis.ResponseOnlyCompleted != 0 {
		t.Fatalf("tool-using episode cohort mismatch: %#v", analysis)
	}
	if analysis.CachedInputTokens.P50 != 60 || analysis.CachedInputTokens.P90 != 200 {
		t.Fatalf("cached-input distribution mismatch: %#v", analysis.CachedInputTokens)
	}
	if analysis.UncachedInputTokens.P50 != 30 || analysis.UncachedInputTokens.P90 != 100 {
		t.Fatalf("uncached-input distribution mismatch: %#v", analysis.UncachedInputTokens)
	}
	if analysis.ModelOutputTokens.P50 != 3 || analysis.ModelOutputTokens.P90 != 10 {
		t.Fatalf("model-output distribution mismatch: %#v", analysis.ModelOutputTokens)
	}
	if analysis.FreshTokens.P50 != 33 || analysis.FreshTokens.P75 != 44 ||
		analysis.FreshTokens.P90 != 110 || analysis.FreshTokens.Max != 110 {
		t.Fatalf("fresh-token distribution mismatch: %#v", analysis.FreshTokens)
	}
	if analysis.TopDecileFreshTokenShare != 0.5 {
		t.Fatalf("top-decile share mismatch: %f", analysis.TopDecileFreshTokenShare)
	}
}

func TestAnalyzeCompletionEpisodesLocalizesFreshTokenTailDrivers(t *testing.T) {
	var episodes []codexTaskEpisode
	for index := range 30 {
		fresh := int64(100)
		episode := codexTaskEpisode{
			StartedAt: time.Unix(0, 0),
			EndedAt:   time.Unix(10, 0),
			Completed: true,
			Tokens: codexTokenUsage{
				UncachedInputTokens: fresh,
			},
			ToolCalls:     2,
			Families:      map[string]int{"file reads": 1},
			TargetCohorts: map[string]int{"packages/common": 1},
		}
		if index < 3 {
			episode.Tokens.UncachedInputTokens = 10_000
			episode.Families["tests"] = 4
			episode.OwnedOperations = map[string]int{"bwb/test": 3, "bwb/git-add": 1}
			episode.Targets = map[string]int{"packages/runtime/worker.ts": 5}
			episode.TargetCohorts["packages/runtime"] = 5
		}
		if index >= 3 && index < 5 {
			episode.Families["tests"] = 1
			episode.OwnedOperations = map[string]int{"bwb/test": 1}
			episode.Targets = map[string]int{"packages/runtime/worker.ts": 1}
			episode.TargetCohorts["packages/runtime"] = 1
		}
		episodes = append(episodes, episode)
	}

	analysis := analyzeCompletionEpisodes(episodes)
	if analysis.TailDrivers.TailEpisodes != 3 || analysis.TailDrivers.OrdinaryEpisodes != 27 {
		t.Fatalf("tail cohort size mismatch: %#v", analysis.TailDrivers)
	}
	if len(analysis.TailDrivers.OwnedOperations) != 1 ||
		analysis.TailDrivers.OwnedOperations[0].Name != "bwb/test" ||
		analysis.TailDrivers.OwnedOperations[0].TailEpisodes != 3 ||
		analysis.TailDrivers.OwnedOperations[0].OrdinaryEpisodes != 2 {
		t.Fatalf("owned operation driver mismatch: %#v", analysis.TailDrivers.OwnedOperations)
	}
	if len(analysis.TailDrivers.TargetCohorts) != 1 ||
		analysis.TailDrivers.TargetCohorts[0].Name != "packages/runtime" {
		t.Fatalf("target cohort driver mismatch: %#v", analysis.TailDrivers.TargetCohorts)
	}
	if len(analysis.TailDrivers.Targets) != 1 ||
		analysis.TailDrivers.Targets[0].Name != "packages/runtime/worker.ts" {
		t.Fatalf("exact target driver mismatch: %#v", analysis.TailDrivers.Targets)
	}
	if formatted := formatTaskCostTailDrivers(analysis.TailDrivers); !strings.Contains(formatted, "cohort targets packages/runtime/worker.ts") {
		t.Fatalf("formatted tail drivers omit exact target: %q", formatted)
	}
}

func TestAnalyzeFileHotspotsCorrelatesDemandCostAndReviewFixes(t *testing.T) {
	start := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	episodes := []codexTaskEpisode{
		{
			StartedAt: start,
			EndedAt:   start.Add(2 * time.Minute),
			Completed: true,
			ToolCalls: 8,
			Targets:   map[string]int{"src/runtime.ts": 2, "src/one-off.ts": 1},
		},
		{
			StartedAt: start,
			EndedAt:   start.Add(10 * time.Minute),
			Completed: true,
			ToolCalls: 40,
			Targets:   map[string]int{"src/runtime.ts": 3},
		},
		{
			StartedAt: start,
			EndedAt:   start.Add(time.Minute),
			Completed: true,
			ToolCalls: 4,
			Targets:   map[string]int{"src/other.ts": 1},
		},
	}
	hotspots := analyzeFileHotspots(
		episodes,
		map[string]int{"src/runtime.ts": 2},
		downstreamQualityMetrics{
			FailureTargets: map[string]int{"src/runtime.ts": 1},
		},
	)
	if len(hotspots) != 1 {
		t.Fatalf("hotspots=%#v want one repeated target", hotspots)
	}
	got := hotspots[0]
	if got.Target != "src/runtime.ts" || got.CompletedTasks != 2 || got.EditCalls != 5 {
		t.Fatalf("hotspot identity mismatch: %#v", got)
	}
	if got.TaskShare != 2.0/3.0 || got.ToolRoundtrips.P50 != 8 ||
		got.ToolRoundtrips.P90 != 40 || got.DurationSeconds.P90 != 600 ||
		got.PostReviewEditCalls != 2 || got.DownstreamFailures != 1 ||
		got.Classification != "review/rework" ||
		got.LastSeen != start.Add(10*time.Minute).Format(time.RFC3339) {
		t.Fatalf("hotspot correlation mismatch: %#v", got)
	}
}

func TestAnalyzeTaskQualityCohortsUsesProfileAndTaskFamily(t *testing.T) {
	record := codexSessionRecord{
		AgentKind:       "root",
		Model:           "gpt-5.6-sol",
		ReasoningEffort: "high",
		DeliveryRework: deliveryReworkMetrics{Cohorts: map[string]deliveryCohortMetrics{
			"src/app": {Deliveries: 8, DeliveriesWithRework: 2},
		}},
		DownstreamQuality: downstreamQualityMetrics{Cohorts: map[string]downstreamCohortMetrics{
			"src/app": {Deliveries: 8, DeliveriesWithFailure: 1, FollowUpEditCycles: 1},
		}},
	}
	cohorts := analyzeTaskQualityCohorts([]codexSessionRecord{record})
	if len(cohorts) != 1 {
		t.Fatalf("quality cohorts=%#v want one", cohorts)
	}
	got := cohorts[0]
	if got.AgentKind != "root" || got.Model != "gpt-5.6-sol" ||
		got.ReasoningEffort != "high" || got.TaskFamily != "src/app" ||
		got.Deliveries != 8 || got.ReviewFixes != 2 ||
		got.DownstreamFailure != 1 || got.FollowUpEdits != 1 {
		t.Fatalf("quality cohort mismatch: %#v", got)
	}
}

func TestAnalyzeTaskPerformanceCohortsUsesModelEffortAndTaskFamily(t *testing.T) {
	start := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	var episodes []codexTaskEpisode
	for index, calls := range []int{8, 12, 20} {
		episodes = append(episodes, codexTaskEpisode{
			AgentKind:       "root",
			Model:           "gpt-5.6-sol",
			ReasoningEffort: "high",
			StartedAt:       start,
			EndedAt:         start.Add(time.Duration(index+1) * time.Minute),
			Completed:       true,
			ToolCalls:       calls,
			Families:        map[string]int{"browser QA": 1},
			TargetCohorts:   map[string]int{"src/app": 2},
		})
	}
	cohorts := analyzeTaskPerformanceCohorts(episodes)
	if len(cohorts) != 1 {
		t.Fatalf("cohorts=%#v want one", cohorts)
	}
	got := cohorts[0]
	if got.AgentKind != "root" || got.Model != "gpt-5.6-sol" ||
		got.ReasoningEffort != "high" || got.TaskFamily != "browser-qa" ||
		got.CompletedTasks != 3 || got.ToolRoundtrips.P50 != 12 ||
		got.DurationSeconds.P90 != 180 {
		t.Fatalf("performance cohort mismatch: %#v", got)
	}
}

func TestTaskCostDiagnosticOperationsExcludesDeliveryBookkeeping(t *testing.T) {
	got := taskCostDiagnosticOperations(map[string]int{
		"bwb/git-add":          1,
		"bwb/git-commit":       1,
		"bwb/git-push":         1,
		"bwb/pr":               1,
		"bwb/publish":          1,
		"bwb/publish-here":     1,
		"bwb/pr-here":          1,
		"bwb/git-push-here":    1,
		"repo/worktree-create": 1,
		"repo/worktree-land":   1,
		"repo/comments":        1,
		"repo/comments-wait":   1,
		"repo/review":          1,
		"repo/update":          1,
		"repo/publish-preview": 3,
		"bwb/test":             2,
	})
	if len(got) != 2 || got["bwb/test"] != 2 || got["repo/publish-preview"] != 3 {
		t.Fatalf("diagnostic operations=%#v want test and non-delivery preview", got)
	}
}

func TestTaskPhaseSequencingTracksDeliveryAndRework(t *testing.T) {
	episode := codexTaskEpisode{}
	at := func(second int) time.Time { return time.Unix(int64(second), 0) }
	observe := func(second int, event normalizedSessionEvent, operations ...string) {
		event.OccurredAt = at(second)
		episode.observe(event, event.Tokens, operations)
	}
	observe(0, normalizedSessionEvent{Kind: sessionEventToolCall, Family: "search"})
	observe(1, normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "search", OutputBytes: 400})
	observe(2, normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "apply_patch"})
	observe(3, normalizedSessionEvent{Kind: sessionEventToolCall, Family: "tests"})
	observe(4, normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "tests", Failed: true})
	observe(5, normalizedSessionEvent{Kind: sessionEventToolCall, Family: "delivery"})
	observe(6, normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "delivery"})
	observe(7, normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "exec", Family: "review"})
	observe(8, normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "apply_patch"})
	observe(9, normalizedSessionEvent{
		Kind: sessionEventToken,
		Tokens: codexTokenUsage{
			UncachedInputTokens: 80,
			OutputTokens:        20,
		},
	})
	observe(10, normalizedSessionEvent{Kind: sessionEventComplete})

	for phase, wantCalls := range map[string]int{
		"discovery":    1,
		"editing":      1,
		"verification": 1,
		"delivery":     1,
		"rework":       2,
	} {
		if got := episode.Phases[phase].ToolCalls; got != wantCalls {
			t.Fatalf("%s calls=%d want %d; phases=%#v", phase, got, wantCalls, episode.Phases)
		}
	}
	if episode.Phases["verification"].FailedCalls != 1 {
		t.Fatalf("verification failure missing: %#v", episode.Phases["verification"])
	}
	if got := phaseFreshTokens(episode.Phases["rework"]); got != 100 {
		t.Fatalf("rework fresh tokens=%d want 100", got)
	}
	var duration int64
	for _, metrics := range episode.Phases {
		duration += metrics.DurationSeconds
	}
	if duration != 10 {
		t.Fatalf("phase duration=%d want 10: %#v", duration, episode.Phases)
	}
}

func TestTaskPhaseSequencingTracksDelegation(t *testing.T) {
	episode := codexTaskEpisode{}
	episode.observe(normalizedSessionEvent{
		Kind:       sessionEventToolCall,
		ToolName:   "spawn_agent",
		OccurredAt: time.Unix(0, 0),
	}, codexTokenUsage{}, nil)
	episode.observe(normalizedSessionEvent{
		Kind:       sessionEventToken,
		OccurredAt: time.Unix(2, 0),
		Tokens: codexTokenUsage{
			UncachedInputTokens: 20,
			OutputTokens:        5,
		},
	}, codexTokenUsage{UncachedInputTokens: 20, OutputTokens: 5}, nil)
	phase := episode.Phases["delegation"]
	if phase.ToolCalls != 1 || phaseFreshTokens(phase) != 25 ||
		phase.DurationSeconds != 2 {
		t.Fatalf("delegation phase mismatch: %#v", phase)
	}
}

func TestTaskPhaseKeepsNewPostDeliveryWorkOutOfDownstreamRework(t *testing.T) {
	episode := codexTaskEpisode{}
	at := func(second int) time.Time { return time.Unix(int64(second), 0) }
	observe := func(second int, event normalizedSessionEvent) {
		event.OccurredAt = at(second)
		episode.observe(event, event.Tokens, nil)
	}
	observe(0, normalizedSessionEvent{Kind: sessionEventToolCall, Family: "delivery"})
	observe(1, normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "delivery"})
	observe(2, normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "apply_patch"})
	observe(3, normalizedSessionEvent{Kind: sessionEventToolCall, Family: "tests"})
	observe(4, normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "tests", Failed: true})
	observe(5, normalizedSessionEvent{
		Kind: sessionEventToken,
		Tokens: codexTokenUsage{
			UncachedInputTokens: 20,
		},
	})

	if _, exists := episode.Phases["rework"]; exists {
		t.Fatalf("new post-delivery task was classified as downstream rework: %#v", episode.Phases)
	}
	if got := episode.Phases["verification"]; got.FailedCalls != 1 || phaseFreshTokens(got) != 20 {
		t.Fatalf("new task verification phase mismatch: %#v", got)
	}
}

func TestAnalyzeTaskPhaseTailAssociationsComparesPhaseMix(t *testing.T) {
	var episodes []codexTaskEpisode
	for index := range 20 {
		episode := codexTaskEpisode{
			Completed: true,
			ToolCalls: 1,
			Phases: map[string]taskPhaseCost{
				"discovery": {
					Tokens:    codexTokenUsage{UncachedInputTokens: 90},
					ToolCalls: 1,
				},
				"editing": {
					Tokens: codexTokenUsage{UncachedInputTokens: 10},
				},
			},
			Tokens: codexTokenUsage{UncachedInputTokens: 100},
		}
		if index < 2 {
			episode.Phases = map[string]taskPhaseCost{
				"discovery": {
					Tokens:    codexTokenUsage{UncachedInputTokens: 200},
					ToolCalls: 1,
				},
				"rework": {
					Tokens: codexTokenUsage{UncachedInputTokens: 800},
				},
			}
			episode.Tokens.UncachedInputTokens = 1_000
		}
		episodes = append(episodes, episode)
	}
	associations := analyzeTaskPhaseTailAssociations(episodes)
	if len(associations) == 0 || associations[0].Phase != "rework" ||
		associations[0].TailShare != 0.8 || associations[0].OrdinaryShare != 0 {
		t.Fatalf("phase tail association mismatch: %#v", associations)
	}
	phases := analyzeTaskPhases(episodes)
	if phases["rework"].Episodes != 2 || phases["discovery"].Episodes != 20 {
		t.Fatalf("phase outcome aggregation mismatch: %#v", phases)
	}
}

func TestDeliveryReworkTrackerCountsReviewToEditCyclesAfterDelivery(t *testing.T) {
	tracker := deliveryReworkTracker{}
	target := ".workbench/repos/engine/packages/runtime/src/runtime.go"
	tracker.observe(normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "apply_patch",
		Targets:  []string{target},
	}, nil)
	tracker.observe(normalizedSessionEvent{
		Kind:   sessionEventToolOutput,
		Family: "tests",
	}, nil)
	tracker.observe(normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "exec",
		Family:   "review",
	}, nil)
	tracker.observe(normalizedSessionEvent{
		Kind:   sessionEventToolOutput,
		Family: "review",
	}, nil)
	tracker.observe(normalizedSessionEvent{
		Kind:            sessionEventToolOutput,
		OwnedOperations: []string{"bwb/pr"},
	}, []string{"bwb/pr"})
	for range 2 {
		tracker.observe(normalizedSessionEvent{
			Kind:            sessionEventToolCall,
			ToolName:        "exec",
			OwnedOperations: []string{"bwb/comments"},
		}, []string{"bwb/comments"})
		tracker.observe(normalizedSessionEvent{
			Kind:            sessionEventToolCall,
			ToolName:        "write_stdin",
			OwnedOperations: []string{"bwb/comments"},
		}, []string{"bwb/comments"})
		tracker.observe(normalizedSessionEvent{
			Kind:     sessionEventToolCall,
			ToolName: "apply_patch",
			Targets:  []string{target},
		}, nil)
		tracker.observe(normalizedSessionEvent{
			Kind:            sessionEventToolCall,
			ToolName:        "exec",
			OwnedOperations: []string{"bwb/comments", "bwb/comments-resolve"},
		}, []string{"bwb/comments", "bwb/comments-resolve"})
	}
	got := tracker.metrics
	if got.Deliveries != 1 || got.PostDeliveryReviewChecks != 2 ||
		got.ReviewToEditCycles != 2 || got.DeliveriesWithRework != 1 ||
		got.PostDeliveryEditCalls != 2 {
		t.Fatalf("delivery rework mismatch: %#v", got)
	}
	if got.DeliveriesWithPreTests != 1 || got.DeliveriesWithPreReview != 1 ||
		got.ReworkedDeliveriesWithPreTests != 1 || got.ReworkedDeliveriesWithPreReview != 1 {
		t.Fatalf("pre-delivery verification mismatch: %#v", got)
	}
	if got.ReworkLevers["source code"] != 2 || got.ReworkScopes["engine"] != 2 {
		t.Fatalf("delivery rework attribution mismatch: %#v", got)
	}
	if got.ReworkTargets[target] != 2 {
		t.Fatalf("delivery rework target mismatch: %#v", got.ReworkTargets)
	}
	cohort := got.Cohorts["engine/packages/runtime"]
	if cohort.Deliveries != 1 || cohort.DeliveriesWithRework != 1 ||
		cohort.ReviewToEditCycles != 2 || cohort.DeliveriesWithPreTests != 1 ||
		cohort.DeliveriesWithPreReview != 1 {
		t.Fatalf("delivery cohort mismatch: %#v", cohort)
	}
	if check := got.VerificationChecks["tests"]; check.Deliveries != 1 ||
		check.DeliveriesWithRework != 1 {
		t.Fatalf("global verification effectiveness mismatch: %#v", check)
	}
	if check := cohort.VerificationChecks["tests"]; check.Deliveries != 1 ||
		check.DeliveriesWithRework != 1 {
		t.Fatalf("cohort verification effectiveness mismatch: %#v", check)
	}
}

func TestDeliveryVerificationTracksFailFixPassByConfiguredCheck(t *testing.T) {
	tracker := deliveryReworkTracker{}
	target := "packages/runtime/src/runtime.go"
	edit := normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "apply_patch",
		Targets:  []string{target},
	}
	check := normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "tests"}
	operations := []string{"repo/test-unit"}

	tracker.observe(edit, nil)
	check.Failed = true
	check.OperationContinues = true
	tracker.observe(check, operations)
	check.OperationContinues = false
	tracker.observe(check, operations)
	tracker.observe(edit, nil)
	check.Failed = false
	tracker.observe(check, operations)
	tracker.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "delivery"}, nil)

	got := tracker.metrics.VerificationChecks["repo/test-unit"]
	if got.Deliveries != 1 || got.FailedRuns != 1 || got.FailFixPassDeliveries != 1 ||
		got.DeliveriesWithRework != 0 {
		t.Fatalf("fail-fix-pass verification mismatch: %#v", got)
	}
	cohort := tracker.metrics.Cohorts["packages/runtime"].VerificationChecks["repo/test-unit"]
	if cohort != got {
		t.Fatalf("cohort verification mismatch: got %#v want %#v", cohort, got)
	}
}

func TestDeliveryVerificationDoesNotCallRetryWithoutEditAFix(t *testing.T) {
	tracker := deliveryReworkTracker{}
	edit := normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "apply_patch",
		Targets:  []string{"src/runtime.go"},
	}
	check := normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "tests", Failed: true}
	operations := []string{"repo/test-unit"}
	tracker.observe(edit, nil)
	tracker.observe(check, operations)
	check.Failed = false
	tracker.observe(check, operations)
	tracker.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "delivery"}, nil)

	if got := tracker.metrics.VerificationChecks["repo/test-unit"]; got.FailedRuns != 1 ||
		got.Deliveries != 1 || got.FailFixPassDeliveries != 0 ||
		got.Runs != 2 || got.RepeatedRuns != 1 {
		t.Fatalf("retry without edit was classified as fail-fix-pass: %#v", got)
	}
}

func TestDeliveryVerificationMustFollowLatestEdit(t *testing.T) {
	tracker := deliveryReworkTracker{}
	edit := normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "apply_patch",
		Targets:  []string{"src/parser/token.go"},
	}
	tracker.observe(edit, nil)
	tracker.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "tests"}, nil)
	tracker.observe(edit, nil)
	tracker.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "delivery"}, nil)
	if tracker.metrics.DeliveriesWithPreTests != 0 {
		t.Fatalf("test before latest edit counted as pre-delivery verification: %#v", tracker.metrics)
	}
	if got := tracker.metrics.VerificationChecks["tests"]; got.Deliveries != 0 || got.Runs != 1 {
		t.Fatalf("stale check attribution mismatch: %#v", tracker.metrics.VerificationChecks)
	}
	if got := tracker.metrics.Cohorts["src/parser"]; got.Deliveries != 1 || got.DeliveriesWithPreTests != 0 {
		t.Fatalf("ordinary repository cohort mismatch: %#v", got)
	}
}

func TestDeliveryReworkIgnoresEditOutsideDeliveredTargets(t *testing.T) {
	tracker := deliveryReworkTracker{}
	tracker.observe(normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "apply_patch",
		Targets:  []string{"packages/web/src/resource_ui.ts"},
	}, nil)
	tracker.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "delivery"}, nil)
	tracker.observe(normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "exec",
		Family:   "review",
	}, nil)
	tracker.observe(normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "apply_patch",
		Targets:  []string{"packages/web/src/billing.ts"},
	}, nil)

	got := tracker.metrics
	if got.PostDeliveryReviewChecks != 1 {
		t.Fatalf("review check missing: %#v", got)
	}
	if got.ReviewToEditCycles != 0 || got.DeliveriesWithRework != 0 || got.PostDeliveryEditCalls != 0 {
		t.Fatalf("unrelated feature edit counted as review-driven rework: %#v", got)
	}
}

func TestDeliveryCohortIgnoresPostReviewEditWithoutDeliveredTarget(t *testing.T) {
	tracker := deliveryReworkTracker{}
	tracker.observe(normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "apply_patch",
		Targets:  []string{"src/parser/token.go"},
	}, nil)
	tracker.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "delivery"}, nil)
	tracker.observe(normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "exec", Family: "review"}, nil)
	tracker.observe(normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "apply_patch",
		Targets:  []string{"docs/parser.md"},
	}, nil)

	if _, exists := tracker.metrics.Cohorts["docs"]; exists {
		t.Fatalf("unrelated post-review cohort was attributed: %#v", tracker.metrics.Cohorts)
	}
	if got := tracker.metrics.Cohorts["src/parser"]; got.Deliveries != 1 ||
		got.DeliveriesWithRework != 0 {
		t.Fatalf("original delivery cohort was misattributed: %#v", got)
	}
}

func TestDeliveryTargetCohortIsRepositoryAgnostic(t *testing.T) {
	tests := map[string]string{
		"README.md":                                   "(root)",
		"src/runtime.go":                              "src",
		"src/parser/token.go":                         "src/parser",
		"packages/web/src/index.ts":                   "packages/web",
		"services/account/internal/handler.go":        "services/account",
		".workbench/repos/engine/modules/ai/model.rs": "engine/modules/ai",
		".worktrees/task/src/parser/token.go":         "src/parser",
	}
	for target, want := range tests {
		if got := deliveryTargetCohort(target); got != want {
			t.Fatalf("deliveryTargetCohort(%q)=%q want %q", target, got, want)
		}
	}
}

func TestDeliveryTargetLabelRemovesTaskInfrastructure(t *testing.T) {
	tests := map[string]string{
		"src/parser/token.go":                                  "src/parser/token.go",
		".workbench/repos/engine/src/parser/token.go":          "engine/src/parser/token.go",
		".worktrees/task/engine/src/parser/token.go":           "engine/src/parser/token.go",
		".workbench/worktrees/task/engine/src/parser/token.go": "engine/src/parser/token.go",
	}
	for target, want := range tests {
		if got := deliveryTargetLabel(target); got != want {
			t.Fatalf("deliveryTargetLabel(%q)=%q want %q", target, got, want)
		}
	}
}

func TestDownstreamQualityTracksFailureFixPassAndRedelivery(t *testing.T) {
	tracker := downstreamQualityTracker{}
	at := func(second int) time.Time { return time.Unix(int64(second), 0) }
	target := "packages/runtime/src/runtime.go"
	edit := normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "apply_patch",
		Targets:  []string{target},
	}
	check := normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "tests"}
	operations := []string{"repo/test-unit"}

	edit.OccurredAt = at(1)
	tracker.observe(edit, nil)
	check.OccurredAt = at(2)
	tracker.observe(check, operations)
	tracker.observe(normalizedSessionEvent{
		Kind:       sessionEventToolOutput,
		Family:     "delivery",
		OccurredAt: at(3),
	}, nil)
	check.OccurredAt = at(13)
	check.Failed = true
	check.OperationContinues = true
	tracker.observe(check, operations)
	check.OperationContinues = false
	tracker.observe(check, operations)
	edit.OccurredAt = at(20)
	tracker.observe(edit, nil)
	check.OccurredAt = at(25)
	check.Failed = false
	tracker.observe(check, operations)
	tracker.observe(normalizedSessionEvent{
		Kind:       sessionEventToolOutput,
		Family:     "delivery",
		OccurredAt: at(33),
	}, nil)

	got := tracker.metrics
	finalizeDownstreamQualityMetrics(&got)
	if got.Deliveries != 2 || got.DeliveriesWithFailure != 1 || got.FailureRuns != 1 ||
		got.FollowUpEditCycles != 1 || got.RedeliveryAttempts != 1 ||
		got.RecoveredDeliveries != 1 || got.Reverts != 0 {
		t.Fatalf("downstream recovery mismatch: %#v", got)
	}
	if got.DeliveriesWithPreTests != 2 || got.FailedDeliveriesWithPreTests != 1 {
		t.Fatalf("fresh verification attribution mismatch: %#v", got)
	}
	if got.FailureChecks["repo/test-unit"] != 1 {
		t.Fatalf("failure check mismatch: %#v", got.FailureChecks)
	}
	if got.TimeToFailureSeconds.P50 != 10 || got.TimeToRecoverySeconds.P50 != 20 {
		t.Fatalf("downstream timing mismatch: %#v %#v", got.TimeToFailureSeconds, got.TimeToRecoverySeconds)
	}
	cohort := got.Cohorts["packages/runtime"]
	if cohort.Deliveries != 2 || cohort.DeliveriesWithFailure != 1 ||
		cohort.FollowUpEditCycles != 1 || cohort.RecoveredDeliveries != 1 {
		t.Fatalf("downstream cohort mismatch: %#v", cohort)
	}
	preCheck := got.PreDeliveryChecks["repo/test-unit"]
	if preCheck.Deliveries != 2 || preCheck.DeliveriesWithFailure != 1 {
		t.Fatalf("pre-delivery check rate mismatch: %#v", preCheck)
	}
}

func TestDownstreamQualitySeparatesReviewCleanupAndUnrelatedEdits(t *testing.T) {
	tracker := downstreamQualityTracker{}
	deliveredTarget := "src/runtime.go"
	tracker.observe(normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "apply_patch",
		Targets:  []string{deliveredTarget},
	}, nil)
	tracker.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "delivery"}, nil)
	tracker.observe(normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "exec",
		Family:   "review",
	}, nil)
	tracker.observe(normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "apply_patch",
		Targets:  []string{"docs/runtime.md"},
	}, nil)
	tracker.observe(normalizedSessionEvent{
		Kind:   sessionEventToolOutput,
		Family: "tests",
		Failed: true,
	}, nil)

	if got := tracker.metrics; got.DeliveriesWithFailure != 0 ||
		got.FailureRuns != 0 || got.FollowUpEditCycles != 0 {
		t.Fatalf("review cleanup or unrelated edit became downstream failure: %#v", got)
	}
}

func TestDownstreamQualityRequiresExactTargetAndMatchingRecoveryCheck(t *testing.T) {
	tracker := downstreamQualityTracker{}
	target := "src/runtime.go"
	tracker.observe(normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "apply_patch",
		Targets:  []string{target},
	}, nil)
	tracker.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "delivery"}, nil)
	tracker.observe(normalizedSessionEvent{
		Kind:   sessionEventToolOutput,
		Family: "tests",
		Failed: true,
	}, []string{"repo/test-unit"})
	tracker.observe(normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "apply_patch",
		Targets:  []string{"src/unrelated.go"},
	}, nil)
	tracker.observe(normalizedSessionEvent{
		Kind:   sessionEventToolOutput,
		Family: "tests",
	}, []string{"repo/test-integration"})
	tracker.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "delivery"}, nil)

	got := tracker.metrics
	if got.DeliveriesWithFailure != 1 || got.FollowUpEditCycles != 0 ||
		got.RedeliveryAttempts != 0 || got.RecoveredDeliveries != 0 {
		t.Fatalf("unrelated recovery was attributed: %#v", got)
	}
}

func TestDownstreamQualityCountsSuccessfulRevertAsBrokenDelivery(t *testing.T) {
	tracker := downstreamQualityTracker{}
	tracker.observe(normalizedSessionEvent{
		Kind:     sessionEventToolCall,
		ToolName: "apply_patch",
		Targets:  []string{"src/runtime.go"},
	}, nil)
	tracker.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, Family: "delivery"}, nil)
	tracker.observe(normalizedSessionEvent{
		Kind:   sessionEventToolOutput,
		Family: "revert",
	}, nil)

	if got := tracker.metrics; got.Reverts != 1 || got.DeliveriesWithFailure != 1 ||
		got.Cohorts["src"].Reverts != 1 {
		t.Fatalf("revert quality mismatch: %#v", got)
	}
}

func TestSessionRecordDoesNotPairReviewWithEditFromAnotherWorktreeTask(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".workbench", "repos", "breyta"), 0o755); err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return since.Add(time.Duration(seconds) * time.Second) }
	target := func(task string) string {
		return filepath.Join(root, ".worktrees", task, "breyta", "src", "shared.clj")
	}
	session := normalizedSession{
		CWD: root,
		Events: []normalizedSessionEvent{
			{
				OccurredAt:       at(1),
				Kind:             sessionEventToolCall,
				ToolName:         "apply_patch",
				TargetCandidates: []string{target("task-a")},
			},
			{
				OccurredAt:     at(2),
				CallOccurredAt: at(1),
				Kind:           sessionEventToolOutput,
				Family:         "delivery",
				OperationTask:  "task-a",
			},
			{
				OccurredAt:    at(3),
				Kind:          sessionEventToolCall,
				ToolName:      "exec_command",
				Family:        "review",
				OperationTask: "task-a",
			},
			{
				OccurredAt:       at(4),
				Kind:             sessionEventToolCall,
				ToolName:         "apply_patch",
				TargetCandidates: []string{target("task-b")},
			},
		},
	}

	record, err := sessionRecordFromNormalized(session, root, since, at(10), ownershipCatalog{})
	if err != nil {
		t.Fatalf("session record: %v", err)
	}
	got := record.DeliveryRework
	if got.Deliveries != 1 || got.PostDeliveryReviewChecks != 1 {
		t.Fatalf("delivery review metrics mismatch: %#v", got)
	}
	if got.ReviewToEditCycles != 0 || got.PostDeliveryEditCalls != 0 || got.DeliveriesWithRework != 0 {
		t.Fatalf("cross-task edit was attributed as delivery rework: %#v", got)
	}
}

func TestSessionRecordSegmentsCompletionEpisodesAndCensoring(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "work")
	since := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return since.Add(time.Duration(seconds) * time.Second) }
	session := normalizedSession{
		CWD: cwd,
		Events: []normalizedSessionEvent{
			{
				OccurredAt: since.Add(-time.Second),
				Kind:       sessionEventToken,
				Tokens: codexTokenUsage{
					InputTokens: 100, UncachedInputTokens: 20, OutputTokens: 10, TotalTokens: 110,
				},
			},
			{OccurredAt: at(1), Kind: sessionEventToolCall, ToolName: "exec", ToolRound: 1},
			{
				OccurredAt: at(2),
				Kind:       sessionEventToken,
				Tokens: codexTokenUsage{
					InputTokens: 150, UncachedInputTokens: 30, OutputTokens: 15, TotalTokens: 165,
				},
			},
			{OccurredAt: at(3), Kind: sessionEventComplete},
			{OccurredAt: at(4), Kind: sessionEventToolCall, ToolName: "exec", ToolRound: 2},
			{
				OccurredAt:     at(5),
				CallOccurredAt: at(4),
				Kind:           sessionEventToolOutput,
				ToolName:       "exec",
				Failed:         true,
				OutputBytes:    400,
			},
			{
				OccurredAt: at(6),
				Kind:       sessionEventToken,
				Tokens: codexTokenUsage{
					InputTokens: 200, UncachedInputTokens: 40, OutputTokens: 20, TotalTokens: 220,
				},
			},
			{OccurredAt: at(7), Kind: sessionEventComplete},
		},
	}
	record, err := sessionRecordFromNormalized(session, root, since, at(10), ownershipCatalog{})
	if err != nil {
		t.Fatalf("session record: %v", err)
	}
	if len(record.TaskEpisodes) != 2 {
		t.Fatalf("episode segmentation mismatch: %#v", record.TaskEpisodes)
	}
	if !record.TaskEpisodes[0].Completed || !record.TaskEpisodes[0].LeftCensored {
		t.Fatalf("first episode should be a left-censored completion: %#v", record.TaskEpisodes[0])
	}
	second := record.TaskEpisodes[1]
	if !second.Completed || second.LeftCensored || second.ToolCalls != 1 || second.FailedCalls != 1 {
		t.Fatalf("fully observed episode mismatch: %#v", second)
	}
	if fresh := second.Tokens.UncachedInputTokens + second.Tokens.OutputTokens; fresh != 15 {
		t.Fatalf("second episode fresh-token delta=%d, want 15", fresh)
	}
}
