package cli

import "testing"

func TestBuildDecretionCandidatesReportsConsumerEvidenceWithoutClaimingUse(t *testing.T) {
	report := codexSessionInsightsReport{
		Instructions: repositoryInstructionFootprint{
			RootFiles: 1, RootEstimatedTokens: largeGuidanceArtifactTokens,
			Files: []repositoryInstructionFile{{Path: "AGENTS.md", Scope: "root"}},
		},
		Summary: codexSessionInsightsSummary{codexAggregateMetrics: newCodexAggregateMetrics()},
	}
	report.Summary.Sessions = 4
	report.Summary.SkillLoads["large-skill"] = guidanceLoadMetrics{
		Loads: 5, Tasks: 3, Reloads: 2, EstimatedTokens: largeGuidanceArtifactTokens,
	}
	report.Summary.SkillLoads["reloaded-skill"] = guidanceLoadMetrics{
		Loads: 8, Tasks: 4, Reloads: 3, ReloadTasks: 3, EstimatedTokens: 500,
	}

	got := buildDecretionCandidates(report)
	if len(got) != 3 {
		t.Fatalf("decretion candidates=%#v", got)
	}
	byTarget := map[string]decretionCandidate{}
	for _, candidate := range got {
		byTarget[candidate.Target] = candidate
	}
	root := byTarget["AGENTS.md"]
	if root.ConsumerEvidence.Kind != "injected" || root.ConsumerEvidence.Exposures != 4 {
		t.Fatalf("root consumer evidence=%#v", root.ConsumerEvidence)
	}
	skill := byTarget["large-skill"]
	if skill.ConsumerEvidence.Kind != "explicit-load" ||
		skill.ConsumerEvidence.Exposures != 5 || skill.ConsumerEvidence.Tasks != 3 {
		t.Fatalf("skill consumer evidence=%#v", skill.ConsumerEvidence)
	}
	reloaded := byTarget["reloaded-skill"]
	if len(reloaded.Signals) != 1 || reloaded.Signals[0] != "same-context-reloads" {
		t.Fatalf("reload candidate=%#v", reloaded)
	}
}
