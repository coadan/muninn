package cli

import "sort"

const compactDecretionCandidateLimit = 10

func buildDecretionCandidates(report codexSessionInsightsReport) []decretionCandidate {
	var candidates []decretionCandidate
	if report.Instructions.RootEstimatedTokens >= largeGuidanceArtifactTokens {
		target := "root instructions"
		if report.Instructions.RootFiles == 1 {
			for _, file := range report.Instructions.Files {
				if file.Scope == "root" {
					target = file.Path
					break
				}
			}
		}
		candidates = append(candidates, decretionCandidate{
			Kind:            "instructions",
			Target:          target,
			Signals:         []string{"large-context-footprint"},
			EstimatedTokens: report.Instructions.RootEstimatedTokens,
			CostBasis:       "always-on-per-session",
			ConsumerEvidence: decretionConsumerEvidence{
				Kind: "injected", Exposures: report.Summary.Sessions,
			},
		})
	}
	for id, metrics := range report.Summary.SkillLoads {
		var signals []string
		if metrics.EstimatedTokens >= largeGuidanceArtifactTokens {
			signals = append(signals, "large-context-footprint")
		}
		if metrics.Reloads >= 3 && metrics.ReloadTasks >= 3 {
			signals = append(signals, "same-context-reloads")
		}
		if len(signals) == 0 {
			continue
		}
		candidates = append(candidates, decretionCandidate{
			Kind:            "skill",
			Target:          id,
			Signals:         signals,
			EstimatedTokens: metrics.EstimatedTokens,
			CostBasis:       "conditional-per-load",
			ConsumerEvidence: decretionConsumerEvidence{
				Kind: "explicit-load", Exposures: metrics.Loads, Tasks: metrics.Tasks,
				Reloads: metrics.Reloads,
			},
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].EstimatedTokens != candidates[j].EstimatedTokens {
			return candidates[i].EstimatedTokens > candidates[j].EstimatedTokens
		}
		return candidates[i].Target < candidates[j].Target
	})
	if len(candidates) > compactDecretionCandidateLimit {
		return candidates[:compactDecretionCandidateLimit]
	}
	return candidates
}

func comparisonBaselineLabel(scope sessionAnalysisScope) string {
	lookback := formatTrendLookback(scope.LookbackSeconds)
	if scope.WindowKind == "since-commit" {
		return "equal-duration pre-commit " + lookback
	}
	return "previous non-overlapping " + lookback
}
