package main

import (
	"path/filepath"
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
				UncachedInputTokens: fresh,
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
	if analysis.FreshTokens.P50 != 30 || analysis.FreshTokens.P75 != 40 ||
		analysis.FreshTokens.P90 != 100 || analysis.FreshTokens.Max != 100 {
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
			episode.OwnedOperations = map[string]int{"bwb/test": 3}
			episode.TargetCohorts["packages/runtime"] = 5
		}
		if index >= 3 && index < 5 {
			episode.Families["tests"] = 1
			episode.OwnedOperations = map[string]int{"bwb/test": 1}
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
		got.Deliveries != 1 || got.FailFixPassDeliveries != 0 {
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
	if len(tracker.metrics.VerificationChecks) != 0 {
		t.Fatalf("stale check was attached to delivery: %#v", tracker.metrics.VerificationChecks)
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
