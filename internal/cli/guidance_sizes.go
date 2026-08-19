package cli

func refreshReportGuidanceSizes(
	report *codexSessionInsightsReport,
	provider sessionProvider,
	workspaceRoot string,
) {
	ids := make([]string, 0, len(report.Summary.SkillLoads))
	for id := range report.Summary.SkillLoads {
		ids = append(ids, id)
	}
	sizes := provider.GuidanceArtifactSizes(workspaceRoot, ids)
	if len(sizes) == 0 {
		return
	}
	overlayGuidanceSizes(report.Summary.SkillLoads, sizes)
	for index := range report.Tasks {
		overlayGuidanceSizes(report.Tasks[index].SkillLoads, sizes)
	}
}

func overlayGuidanceSizes(
	metricsByID map[string]guidanceLoadMetrics,
	sizes map[string]int64,
) {
	for id, metrics := range metricsByID {
		tokens, ok := sizes[id]
		if !ok {
			continue
		}
		metrics.EstimatedTokens = tokens
		metrics.EstimatedLoadedTokens = int64(metrics.Loads) * tokens
		metrics.EstimatedRedundantTokens = int64(metrics.Reloads) * tokens
		metricsByID[id] = metrics
	}
}
