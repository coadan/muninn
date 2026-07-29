package cli

import (
	"fmt"
	"strings"
)

func buildCausalFindings(
	report codexSessionInsightsReport,
	config repositoryConfig,
) []sessionFinding {
	summary := report.Summary
	var findings []sessionFinding

	wasteName, waste := dominantVerificationWaste(
		summary.DeliveryRework.VerificationChecks,
		config.OwnedTools,
	)
	if wasteName != "" {
		findings = append(findings, sessionFinding{
			Category: "verification-loop",
			Control:  "repository",
			Title:    "verification repeats without intervening edits: " + wasteName,
			Evidence: fmt.Sprintf(
				"%s of %s runs repeated the same check after no intervening edit",
				formatCodexCount(int64(waste.RepeatedRuns)),
				formatCodexCount(int64(waste.Runs)),
			),
			Action:     "Run this check once after the latest relevant edit, reuse its terminal result, and reserve another run for a new edit or an explicitly changed environment.",
			Count:      waste.RepeatedRuns,
			Sessions:   summary.DeliveryRework.Sessions,
			Target:     wasteName,
			LastSeen:   report.GeneratedAt,
			Lever:      "tooling",
			Confidence: "high",
			score:      640 + waste.RepeatedRuns*20,
		})
	}

	escapeCheck, withCheck, withoutDeliveries, withoutFailures :=
		dominantDeliveryEscapeCheck(summary.DownstreamQuality)
	if escapeCheck != "" {
		findings = append(findings, sessionFinding{
			Category: "delivery-quality",
			Control:  "repository",
			Title:    "downstream failures escape when a pre-delivery check is absent: " + escapeCheck,
			Evidence: fmt.Sprintf(
				"%s/%s deliveries failed with the check versus %s/%s without it",
				formatCodexCount(int64(withCheck.DeliveriesWithFailure)),
				formatCodexCount(int64(withCheck.Deliveries)),
				formatCodexCount(int64(withoutFailures)),
				formatCodexCount(int64(withoutDeliveries)),
			),
			Action:     "Run this check after the latest relevant edit and before delivery, then compare the same escape rate in the next matched period.",
			Count:      withoutFailures,
			Sessions:   summary.DownstreamQuality.Sessions,
			Target:     escapeCheck,
			LastSeen:   report.GeneratedAt,
			Lever:      "tests",
			Confidence: deliveryEscapeConfidence(withCheck.Deliveries, withoutDeliveries),
			score:      760 + withoutFailures*40,
		})
	}

	compactionPhase, compactionMetrics, observedCompactions :=
		dominantCompactionPhase(report.Outcomes.Phases)
	if compactionPhase != "" {
		findings = append(findings, sessionFinding{
			Category: "session-loop",
			Control:  "repository",
			Title:    "context compactions concentrate in phase: " + compactionPhase,
			Evidence: fmt.Sprintf(
				"%s/%s compactions in completed tool tasks occurred during %s across %s; that phase used %s fresh tokens, ~%s visible output tokens, and %s tool calls",
				formatCodexCount(compactionMetrics.TotalCompactions),
				formatCodexCount(observedCompactions),
				compactionPhase,
				formatCodexCountNoun(int64(compactionMetrics.CompactionSessions), "session"),
				formatCodexCount(compactionMetrics.TotalFreshTokens),
				formatCodexCount(compactionMetrics.TotalOutputTokens),
				formatCodexCount(compactionMetrics.TotalToolCalls),
			),
			Action:     compactionPhaseAction(compactionPhase),
			Count:      int(compactionMetrics.TotalCompactions),
			Sessions:   compactionMetrics.CompactionSessions,
			Target:     compactionPhase,
			LastSeen:   report.GeneratedAt,
			Lever:      "repository workflow",
			Confidence: "medium",
			score:      620 + int(compactionMetrics.TotalCompactions)*15,
		})
	}

	return findings
}

func dominantVerificationWaste(
	checks map[string]verificationMetrics,
	ownedTools []ownedToolConfig,
) (string, verificationMetrics) {
	name := ""
	metrics := verificationMetrics{}
	for candidate, value := range checks {
		if !fixedOwnedOperation(candidate, ownedTools) ||
			value.Runs < 6 || value.RepeatedRuns < 4 ||
			ratio(float64(value.RepeatedRuns), float64(value.Runs)) < 0.25 {
			continue
		}
		if value.RepeatedRuns > metrics.RepeatedRuns ||
			(value.RepeatedRuns == metrics.RepeatedRuns && candidate < name) {
			name = candidate
			metrics = value
		}
	}
	return name, metrics
}

func fixedOwnedOperation(operation string, ownedTools []ownedToolConfig) bool {
	toolID, operationID, ok := strings.Cut(operation, "/")
	if !ok {
		return false
	}
	for _, tool := range ownedTools {
		if tool.ID != toolID {
			continue
		}
		for _, configured := range tool.Operations {
			if configured.ID != operationID {
				continue
			}
			for _, arg := range configured.Args {
				if strings.Contains(arg, "*") {
					return false
				}
			}
			return true
		}
	}
	return false
}

func dominantDeliveryEscapeCheck(
	metrics downstreamQualityMetrics,
) (string, downstreamCheckMetrics, int, int) {
	if metrics.Deliveries < 6 || metrics.DeliveriesWithFailure < 2 {
		return "", downstreamCheckMetrics{}, 0, 0
	}
	name := ""
	withCheck := downstreamCheckMetrics{}
	bestWithoutDeliveries := 0
	bestWithoutFailures := 0
	bestDelta := 0.0
	for candidate, value := range metrics.PreDeliveryChecks {
		withoutDeliveries := metrics.Deliveries - value.Deliveries
		withoutFailures := metrics.DeliveriesWithFailure - value.DeliveriesWithFailure
		if value.Deliveries < 3 || withoutDeliveries < 3 || withoutFailures < 2 {
			continue
		}
		withRate := ratio(float64(value.DeliveriesWithFailure), float64(value.Deliveries))
		withoutRate := ratio(float64(withoutFailures), float64(withoutDeliveries))
		delta := withoutRate - withRate
		if delta < 0.20 {
			continue
		}
		if delta > bestDelta || (delta == bestDelta && candidate < name) {
			name = candidate
			withCheck = value
			bestWithoutDeliveries = withoutDeliveries
			bestWithoutFailures = withoutFailures
			bestDelta = delta
		}
	}
	return name, withCheck, bestWithoutDeliveries, bestWithoutFailures
}

func deliveryEscapeConfidence(withDeliveries, withoutDeliveries int) string {
	if withDeliveries >= 10 && withoutDeliveries >= 10 {
		return "high"
	}
	return "medium"
}

func dominantCompactionPhase(
	phases map[string]taskPhaseAnalysis,
) (string, taskPhaseAnalysis, int64) {
	var total int64
	for _, metrics := range phases {
		total += metrics.TotalCompactions
	}
	if total < 5 {
		return "", taskPhaseAnalysis{}, total
	}
	name := ""
	dominant := taskPhaseAnalysis{}
	for candidate, metrics := range phases {
		if metrics.TotalCompactions > dominant.TotalCompactions ||
			(metrics.TotalCompactions == dominant.TotalCompactions && candidate < name) {
			name = candidate
			dominant = metrics
		}
	}
	if dominant.TotalCompactions < 4 ||
		dominant.CompactionSessions < 2 ||
		ratio(float64(dominant.TotalCompactions), float64(total)) < 0.40 {
		return "", taskPhaseAnalysis{}, total
	}
	return name, dominant, total
}

func compactionPhaseAction(phase string) string {
	switch phase {
	case "discovery":
		return "Reduce repeated reads and oversized discovery output; add or improve one bounded route from entry point to owner and focused verification."
	case "verification":
		return "Keep verification output bounded, run focused checks during iteration, and reserve the broad gate for the latest coherent state."
	case "rework":
		return "Strengthen the pre-delivery invariant or focused check that should catch these corrections before review or downstream failure."
	case "delegation":
		return "Keep delegated scopes bounded and return compact terminal handoffs so the root session does not repeatedly reload child context."
	default:
		return "Inspect the dominant fresh-token and output contributors in this phase, then reduce repeated context carried between substantive steps."
	}
}
