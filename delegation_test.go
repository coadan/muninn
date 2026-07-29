package main

import (
	"strings"
	"testing"
	"time"
)

func TestAnalyzeModelEffortProfilesAndDelegation(t *testing.T) {
	at := func(second int) time.Time { return time.Unix(int64(second), 0) }
	completedEpisode := func(start, end int, fresh int64) codexTaskEpisode {
		return codexTaskEpisode{
			StartedAt: at(start),
			EndedAt:   at(end),
			Completed: true,
			ToolCalls: 2,
			Tokens: normalizedTokenUsage{
				UncachedInputTokens: fresh,
			},
		}
	}
	records := []codexSessionRecord{
		{
			Model:           "gpt-5.6-sol",
			ReasoningEffort: "xhigh",
			AgentKind:       "root",
			LineageKey:      "parent",
			StartedAt:       at(0),
			EndedAt:         at(40),
			Completed:       true,
			Tokens:          normalizedTokenUsage{UncachedInputTokens: 100, OutputTokens: 20},
			ToolCalls:       8,
			ToolCallsByName: map[string]int{"spawn_agent": 2, "wait_agent": 2},
			EditTargets:     map[string]int{"src/shared.go": 1},
			ReadTargets:     map[string]codexTargetMetrics{"src/owner.go": {Reads: 1}},
			TaskEpisodes: []codexTaskEpisode{{
				StartedAt: at(0),
				EndedAt:   at(40),
				Completed: true,
				ToolCalls: 8,
				Phases: map[string]taskPhaseCost{
					"delegation": {
						Tokens:          normalizedTokenUsage{UncachedInputTokens: 10, OutputTokens: 5},
						ToolCalls:       4,
						ToolOutputBytes: 400,
					},
				},
			}},
		},
		{
			Model:            "gpt-5.6-luna",
			ReasoningEffort:  "max",
			AgentKind:        "subagent",
			LineageKey:       "child-1",
			ParentLineageKey: "parent",
			SpawnStatus:      "completed",
			StartedAt:        at(10),
			EndedAt:          at(30),
			Completed:        true,
			Tokens:           normalizedTokenUsage{UncachedInputTokens: 70, OutputTokens: 10},
			ToolCalls:        4,
			EditTargets:      map[string]int{"src/shared.go": 1, "src/child.go": 1},
			ReadTargets:      map[string]codexTargetMetrics{"src/owner.go": {Reads: 1}},
			TaskEpisodes:     []codexTaskEpisode{completedEpisode(10, 30, 80)},
		},
		{
			Model:            "gpt-5.6-luna",
			ReasoningEffort:  "max",
			AgentKind:        "subagent",
			LineageKey:       "child-2",
			ParentLineageKey: "parent",
			StartedAt:        at(15),
			EndedAt:          at(25),
			Tokens:           normalizedTokenUsage{UncachedInputTokens: 45, OutputTokens: 5},
			ToolCalls:        2,
			TaskEpisodes:     []codexTaskEpisode{completedEpisode(15, 25, 50)},
		},
	}

	profiles := analyzeModelEffortProfiles(records)
	if !profiles.Available || len(profiles.Profiles) != 2 {
		t.Fatalf("model profiles mismatch: %#v", profiles)
	}
	var luna modelEffortProfile
	for _, profile := range profiles.Profiles {
		if profile.Model == "gpt-5.6-luna" {
			luna = profile
		}
	}
	if luna.Sessions != 2 || luna.CompletedToolTasks != 2 ||
		luna.FreshTokens != 130 || luna.CompletedTaskFreshTokens != 130 ||
		luna.FreshTokensPerCompletedTask != 65 ||
		luna.CompletedTaskDurationSeconds.P50 != 10 {
		t.Fatalf("luna profile mismatch: %#v", luna)
	}

	delegation := analyzeDelegation(records)
	if !delegation.Available || delegation.SubagentSessions != 2 ||
		delegation.LinkedSubagents != 2 || delegation.DelegatingParents != 1 ||
		!delegation.CoordinationAvailable ||
		delegation.CoordinationCalls != 4 || delegation.CoordinationFreshTokens != 15 ||
		delegation.CoordinationOutputTokens != 100 {
		t.Fatalf("delegation totals mismatch: %#v", delegation)
	}
	if delegation.ChildrenWithUniqueEdits != 1 ||
		delegation.ChildrenWithEditOverlap != 1 ||
		delegation.EditOverlapTargets != 1 ||
		delegation.ChildrenWithReadOverlap != 1 ||
		delegation.SubagentsByWorkMode["implementation"] != 1 ||
		delegation.SubagentsByWorkMode["other tool work"] != 1 ||
		delegation.SubagentFreshTokensByWorkMode["implementation"] != 80 ||
		delegation.SubagentFreshTokensByWorkMode["other tool work"] != 50 {
		t.Fatalf("delegation attribution mismatch: %#v", delegation)
	}
	if delegation.SummedSubagentDurationSeconds != 30 ||
		delegation.DelegatedWallSeconds != 20 ||
		delegation.SubagentConcurrencyFactor != 1.5 {
		t.Fatalf("delegation concurrency mismatch: %#v", delegation)
	}
}

func TestAnalyzeDelegationSeparatesMissingLineageFromParentOutsideScope(t *testing.T) {
	records := []codexSessionRecord{
		{AgentKind: "root", LineageKey: "in-scope-parent"},
		{AgentKind: "subagent", LineageKey: "missing-parent-key"},
		{AgentKind: "subagent", LineageKey: "outside-child", ParentLineageKey: "outside-parent"},
		{AgentKind: "subagent", LineageKey: "linked-child", ParentLineageKey: "in-scope-parent"},
	}
	delegation := analyzeDelegation(records)
	if delegation.LinkedSubagents != 1 ||
		delegation.UnlinkedSubagents != 1 ||
		delegation.ParentsOutsideScope != 1 {
		t.Fatalf("delegation coverage mismatch: %#v", delegation)
	}
}

func TestAnalyzeDelegationDoesNotTreatResearchWorkModeAsWaste(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Delegation = delegationAnalysis{
		Available:                     true,
		SubagentSessions:              8,
		DelegatingParents:             2,
		SubagentFreshTokens:           800_000,
		ParentFreshTokens:             200_000,
		SubagentFreshTokenShare:       0.8,
		SubagentsByWorkMode:           map[string]int{"research/review": 8},
		SubagentFreshTokensByWorkMode: map[string]int64{"research/review": 800_000},
		SubagentConcurrencyFactor:     2,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	for _, finding := range findings {
		if finding.Category == "delegation-cost" {
			t.Fatalf("unattributed research alone produced a delegation finding: %#v", finding)
		}
	}
}

func TestDelegatedWorkModeUsesStrongestObservableOutcome(t *testing.T) {
	verification := codexTaskEpisode{
		Phases: map[string]taskPhaseCost{
			"verification": {ToolCalls: 1},
		},
	}
	tests := []struct {
		name   string
		record codexSessionRecord
		want   string
	}{
		{name: "implementation", record: codexSessionRecord{
			EditTargets:  map[string]int{"src/owner.go": 1},
			TaskEpisodes: []codexTaskEpisode{verification},
		}, want: "implementation"},
		{name: "delivery", record: codexSessionRecord{
			DeliveryRework: deliveryReworkMetrics{Deliveries: 1},
		}, want: "delivery"},
		{name: "verification", record: codexSessionRecord{
			TaskEpisodes: []codexTaskEpisode{verification},
		}, want: "verification"},
		{name: "research", record: codexSessionRecord{
			ReadTargets: map[string]codexTargetMetrics{"src/owner.go": {Reads: 1}},
		}, want: "research/review"},
		{name: "other tools", record: codexSessionRecord{ToolCalls: 1}, want: "other tool work"},
		{name: "response", record: codexSessionRecord{}, want: "response only"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := delegatedWorkMode(test.record); got != test.want {
				t.Fatalf("delegatedWorkMode()=%q want %q", got, test.want)
			}
		})
	}
}

func TestPrintDelegationAnalysisDistinguishesUnavailableCoordination(t *testing.T) {
	out, err := captureStdout(t, func() error {
		printDelegationAnalysis(delegationAnalysis{
			Available:                     true,
			SubagentSessions:              1,
			LinkedSubagents:               1,
			DelegatingParents:             1,
			SubagentsByWorkMode:           map[string]int{"research/review": 1},
			SubagentFreshTokensByWorkMode: map[string]int64{"research/review": 100},
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Delegation coordination: unavailable") ||
		strings.Contains(out, "0 coordination calls") {
		t.Fatalf("unavailable coordination rendered as measured zero:\n%s", out)
	}
}

func TestBuildSessionFindingsFlagsMaterialDelegationCoordination(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Delegation = delegationAnalysis{
		Available:                 true,
		CoordinationAvailable:     true,
		SubagentSessions:          4,
		DelegatingParents:         2,
		ParentFreshTokens:         250_000,
		SubagentFreshTokens:       200_000,
		SubagentFreshTokenShare:   0.44,
		CoordinationCalls:         16,
		CoordinationFreshTokens:   60_000,
		CoordinationCallsByAction: map[string]int{"wait_agent": 10, "send_message": 6},
		SubagentConcurrencyFactor: 2,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Category != "delegation-cost" ||
		findings[0].Lever != "instructions/docs" {
		t.Fatalf("delegation coordination finding mismatch: %#v", findings)
	}
}
