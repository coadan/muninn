package cli

import "testing"

func TestRefreshReportGuidanceSizesOverlaysCurrentSizeAndPreservesMissing(t *testing.T) {
	report := codexSessionInsightsReport{
		Summary: codexSessionInsightsSummary{codexAggregateMetrics: newCodexAggregateMetrics()},
		Tasks: []codexTaskInsights{{
			codexAggregateMetrics: newCodexAggregateMetrics(),
		}},
	}
	report.Summary.SkillLoads["changed"] = guidanceLoadMetrics{
		Loads: 3, Reloads: 1, EstimatedTokens: 900,
		EstimatedLoadedTokens: 2_700, EstimatedRedundantTokens: 900,
	}
	report.Summary.SkillLoads["missing"] = guidanceLoadMetrics{
		Loads: 2, EstimatedTokens: 700, EstimatedLoadedTokens: 1_400,
	}
	report.Tasks[0].SkillLoads["changed"] = guidanceLoadMetrics{
		Loads: 2, Reloads: 1, EstimatedTokens: 900,
	}
	provider := sessionProviderAdapter{
		name: "test", guidanceSizes: func(string, []string) map[string]int64 {
			return map[string]int64{"changed": 400}
		},
	}

	refreshReportGuidanceSizes(&report, provider, t.TempDir())
	changed := report.Summary.SkillLoads["changed"]
	if changed.EstimatedTokens != 400 || changed.EstimatedLoadedTokens != 1_200 ||
		changed.EstimatedRedundantTokens != 400 {
		t.Fatalf("summary guidance overlay=%#v", changed)
	}
	task := report.Tasks[0].SkillLoads["changed"]
	if task.EstimatedTokens != 400 || task.EstimatedLoadedTokens != 800 ||
		task.EstimatedRedundantTokens != 400 {
		t.Fatalf("task guidance overlay=%#v", task)
	}
	missing := report.Summary.SkillLoads["missing"]
	if missing.EstimatedTokens != 700 || missing.EstimatedLoadedTokens != 1_400 {
		t.Fatalf("unresolved guidance size was not preserved: %#v", missing)
	}
}
