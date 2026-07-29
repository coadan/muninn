package cli

import (
	"strings"
	"testing"
)

func TestBuildCausalFindingsFlagsVerificationWithoutInterveningEdit(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.DeliveryRework = deliveryReworkMetrics{
		Sessions: 3,
		VerificationChecks: map[string]verificationMetrics{
			"repo/test-unit": {Runs: 12, RepeatedRuns: 5},
		},
	}
	config := defaultRepositoryConfig()
	config.OwnedTools = []ownedToolConfig{{
		ID: "repo",
		Operations: []ownedOperationConfig{{
			ID:   "test-unit",
			Args: []string{"test"},
		}},
	}}
	findings := buildCausalFindings(report, config)
	if len(findings) != 1 ||
		findings[0].Title != "verification repeats without intervening edits: repo/test-unit" ||
		!strings.Contains(findings[0].Evidence, "5 of 12 runs") {
		t.Fatalf("verification-waste finding mismatch: %#v", findings)
	}

	report.Summary.DeliveryRework.VerificationChecks = map[string]verificationMetrics{
		"tests": {Runs: 12, RepeatedRuns: 11},
	}
	if generic := buildCausalFindings(report, config); len(generic) != 0 {
		t.Fatalf("generic test family produced a verification-waste finding: %#v", generic)
	}

	config.OwnedTools[0].Operations[0].Args = []string{"test", "**", "--ns"}
	report.Summary.DeliveryRework.VerificationChecks = map[string]verificationMetrics{
		"repo/test-unit": {Runs: 12, RepeatedRuns: 11},
	}
	if variable := buildCausalFindings(report, config); len(variable) != 0 {
		t.Fatalf("variable-selector operation produced a verification-waste finding: %#v", variable)
	}
}

func TestBuildCausalFindingsIdentifiesEscapedCheck(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.DownstreamQuality = downstreamQualityMetrics{
		Deliveries:            10,
		DeliveriesWithFailure: 3,
		FailureRuns:           3,
		Sessions:              3,
		FailureChecks:         map[string]int{"repo/test-unit": 3},
		PreDeliveryChecks: map[string]downstreamCheckMetrics{
			"repo/test-unit": {Deliveries: 5, DeliveriesWithFailure: 0},
		},
	}
	findings := buildCausalFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 ||
		findings[0].Title != "downstream failures escape when a pre-delivery check is absent: repo/test-unit" ||
		!strings.Contains(findings[0].Evidence, "0/5 deliveries failed with the check versus 3/5 without it") {
		t.Fatalf("delivery-escape finding mismatch: %#v", findings)
	}
}

func TestBuildCausalFindingsAttributesCompactionPressureToPhase(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Outcomes.Phases = map[string]taskPhaseAnalysis{
		"discovery": {
			Episodes:           8,
			Sessions:           3,
			CompactionSessions: 2,
			TotalCompactions:   6,
			TotalFreshTokens:   90_000,
			TotalOutputTokens:  20_000,
			TotalToolCalls:     80,
		},
		"editing": {Episodes: 8, Sessions: 3, CompactionSessions: 2, TotalCompactions: 2},
	}
	findings := buildCausalFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 ||
		findings[0].Title != "context compactions concentrate in phase: discovery" ||
		!strings.Contains(findings[0].Evidence, "6/8 compactions") ||
		!strings.Contains(findings[0].Evidence, "across 2 sessions") {
		t.Fatalf("compaction finding mismatch: %#v", findings)
	}
}

func TestBuildCausalFindingsRequiresCrossSessionCompactionPressure(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Outcomes.Phases = map[string]taskPhaseAnalysis{
		"discovery": {
			Episodes:           12,
			Sessions:           1,
			CompactionSessions: 1,
			TotalCompactions:   8,
		},
	}
	if findings := buildCausalFindings(report, defaultRepositoryConfig()); len(findings) != 0 {
		t.Fatalf("one long session became cross-session compaction friction: %#v", findings)
	}
}
