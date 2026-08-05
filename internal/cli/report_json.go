package cli

import "sort"

const compactInterventionLimit = 5

type compactSessionSummary struct {
	FilesScanned           int                  `json:"filesScanned"`
	FilesUnreadable        int                  `json:"filesUnreadable"`
	Sessions               int                  `json:"sessions"`
	CompletedSessions      int                  `json:"completedSessions"`
	IncompleteSessions     int                  `json:"incompleteSessions"`
	DurationSeconds        int64                `json:"durationSeconds"`
	Compactions            int                  `json:"compactions"`
	SessionsWithCompaction int                  `json:"sessionsWithCompactions"`
	Tokens                 normalizedTokenUsage `json:"tokens"`
	FreshTokens            int64                `json:"freshTokens"`
	ExcludedTokenStreams   int                  `json:"excludedTokenTelemetrySessions"`
	ToolCalls              int                  `json:"toolCalls"`
	FailedToolCalls        int                  `json:"failedToolCalls"`
	TruncatedToolCalls     int                  `json:"truncatedToolCalls"`
	ToolOutputBytes        int64                `json:"toolOutputBytes"`
	ToolOutputTokens       int64                `json:"toolOutputTokens"`
}

type compactOutcomeSummary struct {
	Completed                int                 `json:"completed"`
	FullyObservedCompleted   int                 `json:"fullyObservedCompleted"`
	LeftCensoredCompleted    int                 `json:"leftCensoredCompleted"`
	ToolUsingCompleted       int                 `json:"toolUsingCompleted"`
	ResponseOnlyCompleted    int                 `json:"responseOnlyCompleted"`
	Incomplete               int                 `json:"incomplete"`
	FreshTokens              outcomeDistribution `json:"freshTokens"`
	ToolCalls                outcomeDistribution `json:"toolCalls"`
	VisibleOutputTokens      outcomeDistribution `json:"visibleOutputTokens"`
	DurationSeconds          outcomeDistribution `json:"durationSeconds"`
	FailedCalls              outcomeDistribution `json:"failedCalls"`
	Compactions              outcomeDistribution `json:"compactions"`
	TopDecileFreshTokenShare float64             `json:"topDecileFreshTokenShare"`
}

type compactDiagnosticSummary struct {
	Available           bool `json:"available"`
	FailureFingerprints int  `json:"failureFingerprints"`
	PassedTargets       int  `json:"passedTargets"`
}

type compactSessionInsightsReport struct {
	SchemaVersion      int                            `json:"schemaVersion"`
	DetailLevel        string                         `json:"detailLevel"`
	Provider           string                         `json:"provider"`
	GeneratedAt        string                         `json:"generatedAt"`
	Since              string                         `json:"since"`
	AnalysisScope      sessionAnalysisScope           `json:"analysisScope"`
	Instructions       repositoryInstructionFootprint `json:"instructions"`
	Summary            compactSessionSummary          `json:"summary"`
	Outcomes           compactOutcomeSummary          `json:"outcomes"`
	Profiles           modelEffortAnalysis            `json:"profiles"`
	Delegation         delegationAnalysis             `json:"delegation"`
	Diagnostics        compactDiagnosticSummary       `json:"diagnostics"`
	TotalInterventions int                            `json:"totalInterventions"`
	Interventions      []sessionIntervention          `json:"interventions"`
	FocusEvidence      *discoveryFocusEvidence        `json:"focusEvidence,omitempty"`
}

type comparisonSessionInsightsReport struct {
	SchemaVersion     int                       `json:"schemaVersion"`
	DetailLevel       string                    `json:"detailLevel"`
	Comparison        string                    `json:"comparison"`
	BaselineLabel     string                    `json:"baselineLabel"`
	Baseline          any                       `json:"baseline"`
	Current           any                       `json:"current"`
	Cohorts           comparisonCohortsJSON     `json:"cohorts"`
	Trends            comparisonTrendsJSON      `json:"trends"`
	QualityVerdict    string                    `json:"qualityAdjustedVerdict"`
	Diagnostics       comparisonDiagnosticsJSON `json:"diagnostics"`
	InterventionTrend interventionTrendJSON     `json:"interventionTrend"`
}

type comparisonTrendsJSON struct {
	CompletedTasks     trendSummaryJSON  `json:"completedTasks"`
	MatchedPerformance trendSummaryJSON  `json:"matchedPerformance"`
	MatchedQuality     trendSummaryJSON  `json:"matchedQuality"`
	Rates              []trendMetricJSON `json:"rates"`
}

type trendSummaryJSON struct {
	SufficientEvidence bool              `json:"sufficientEvidence"`
	Direction          string            `json:"direction"`
	Improved           int               `json:"improved"`
	Regressed          int               `json:"regressed"`
	Unchanged          int               `json:"unchanged"`
	Insufficient       int               `json:"insufficient"`
	Metrics            []trendMetricJSON `json:"metrics"`
}

type trendMetricJSON struct {
	Name           string  `json:"name"`
	Baseline       float64 `json:"baseline"`
	Current        float64 `json:"current"`
	Direction      string  `json:"direction"`
	LowerIsBetter  bool    `json:"lowerIsBetter"`
	Percentage     bool    `json:"percentage"`
	BaselineSample int     `json:"baselineSample"`
	CurrentSample  int     `json:"currentSample"`
	MinimumSample  int     `json:"minimumSample"`
}

type comparisonCohortsJSON struct {
	TotalPerformance int                               `json:"totalPerformance"`
	TotalQuality     int                               `json:"totalQuality"`
	Performance      []performanceCohortComparisonJSON `json:"performance"`
	Quality          []qualityCohortComparisonJSON     `json:"quality"`
}

type comparisonDiagnosticsJSON struct {
	Available         bool                                 `json:"available"`
	TotalFingerprints int                                  `json:"totalFingerprints"`
	Fingerprints      []diagnosticEffectivenessFingerprint `json:"fingerprints"`
}

type performanceCohortComparisonJSON struct {
	Baseline taskPerformanceCohort `json:"baseline"`
	Current  taskPerformanceCohort `json:"current"`
}

type qualityCohortComparisonJSON struct {
	Baseline taskQualityCohort `json:"baseline"`
	Current  taskQualityCohort `json:"current"`
}

type interventionTrendJSON struct {
	SufficientEvidence bool                  `json:"sufficientEvidence"`
	MinimumSessions    int                   `json:"minimumSessions"`
	BaselineSessions   int                   `json:"baselineSessions"`
	CurrentSessions    int                   `json:"currentSessions"`
	Resolved           []sessionIntervention `json:"resolved"`
	Persistent         []sessionIntervention `json:"persistent"`
	New                []sessionIntervention `json:"new"`
}

func analysisJSONPayload(report codexSessionInsightsReport, detailed bool) any {
	if detailed {
		report.DetailLevel = "full"
		return report
	}
	summary := report.Summary
	outcomes := report.Outcomes
	compact := compactSessionInsightsReport{
		SchemaVersion: report.SchemaVersion,
		DetailLevel:   "summary",
		Provider:      report.Provider,
		GeneratedAt:   report.GeneratedAt,
		Since:         report.Since,
		AnalysisScope: report.AnalysisScope,
		Instructions:  report.Instructions,
		Summary: compactSessionSummary{
			FilesScanned:           summary.FilesScanned,
			FilesUnreadable:        summary.FilesUnreadable,
			Sessions:               summary.Sessions,
			CompletedSessions:      summary.CompletedSessions,
			IncompleteSessions:     summary.IncompleteSessions,
			DurationSeconds:        summary.DurationSeconds,
			Compactions:            summary.Compactions,
			SessionsWithCompaction: summary.SessionsWithCompactions,
			Tokens:                 summary.Tokens,
			FreshTokens:            summary.FreshTokens,
			ExcludedTokenStreams:   summary.ExcludedTokenStreams,
			ToolCalls:              summary.ToolCalls,
			FailedToolCalls:        summary.FailedToolCalls,
			TruncatedToolCalls:     summary.TruncatedToolCalls,
			ToolOutputBytes:        summary.ToolOutputBytes,
			ToolOutputTokens:       summary.ToolOutputTokens,
		},
		Outcomes: compactOutcomeSummary{
			Completed:                outcomes.Completed,
			FullyObservedCompleted:   outcomes.FullyObservedCompleted,
			LeftCensoredCompleted:    outcomes.LeftCensoredCompleted,
			ToolUsingCompleted:       outcomes.ToolUsingCompleted,
			ResponseOnlyCompleted:    outcomes.ResponseOnlyCompleted,
			Incomplete:               outcomes.Incomplete,
			FreshTokens:              outcomes.FreshTokens,
			ToolCalls:                outcomes.ToolCalls,
			VisibleOutputTokens:      outcomes.VisibleOutputTokens,
			DurationSeconds:          outcomes.DurationSeconds,
			FailedCalls:              outcomes.FailedCalls,
			Compactions:              outcomes.Compactions,
			TopDecileFreshTokenShare: outcomes.TopDecileFreshTokenShare,
		},
		Profiles:   report.Profiles,
		Delegation: report.Delegation,
		Diagnostics: compactDiagnosticSummary{
			Available:           report.Diagnostics.Available,
			FailureFingerprints: len(report.Diagnostics.Failures),
			PassedTargets:       len(report.Diagnostics.Passes),
		},
		TotalInterventions: len(report.Interventions),
		Interventions:      boundedInterventions(report.Interventions, compactInterventionLimit),
	}
	if report.AnalysisScope.Focus == "discovery" {
		evidence := buildDiscoveryFocusEvidence(summary, report.WorkspaceRoot, 5)
		compact.FocusEvidence = &evidence
	}
	return compact
}

func comparisonJSONPayload(
	baseline,
	current codexSessionInsightsReport,
	detailed bool,
) comparisonSessionInsightsReport {
	detailLevel := "summary"
	if detailed {
		detailLevel = "full"
	}
	performance := matchedPerformanceCohorts(
		baseline.Outcomes.PerformanceCohorts,
		current.Outcomes.PerformanceCohorts,
		3,
	)
	quality := matchedQualityCohorts(
		baseline.Outcomes.QualityCohorts,
		current.Outcomes.QualityCohorts,
		performance,
		5,
	)
	performanceMetrics := matchedPerformanceTrendMetrics(performance)
	qualityMetrics := matchedQualityTrendMetrics(quality)
	completedMetrics := completedTaskTrendMetrics(baseline, current)
	for index := range completedMetrics {
		completedMetrics[index].BaselineSample = baseline.Outcomes.ToolUsingCompleted
		completedMetrics[index].CurrentSample = current.Outcomes.ToolUsingCompleted
		completedMetrics[index].MinimumSample = minimumTrendTasks
	}
	return comparisonSessionInsightsReport{
		SchemaVersion: current.SchemaVersion,
		DetailLevel:   detailLevel,
		Comparison:    "previous",
		BaselineLabel: "previous non-overlapping " + formatTrendLookback(current.AnalysisScope.LookbackSeconds),
		Baseline:      analysisJSONPayload(baseline, detailed),
		Current:       analysisJSONPayload(current, detailed),
		Cohorts:       comparisonCohortPayload(performance, quality, detailed),
		Trends: comparisonTrendsJSON{
			CompletedTasks:     buildTrendSummaryJSON(completedMetrics),
			MatchedPerformance: buildTrendSummaryJSON(performanceMetrics),
			MatchedQuality:     buildTrendSummaryJSON(qualityMetrics),
			Rates:              trendMetricPayload(sessionRateTrendMetrics(baseline, current)),
		},
		QualityVerdict: qualityAdjustedPerformanceVerdict(
			baseline,
			current,
			performanceMetrics,
			qualityMetrics,
		),
		Diagnostics:       comparisonDiagnosticPayload(baseline.Diagnostics, current.Diagnostics, detailed),
		InterventionTrend: buildInterventionTrendJSON(baseline, current, detailed),
	}
}

func buildTrendSummaryJSON(metrics []trendMetric) trendSummaryJSON {
	label, improved, regressed, unchanged := summarizeTrendDirections(metrics)
	insufficient := 0
	for _, metric := range metrics {
		if materialTrendDirection(metric) == "insufficient" {
			insufficient++
		}
	}
	unchanged -= insufficient
	sufficient := len(metrics) > 0 && insufficient == 0
	if !sufficient {
		label = "insufficient"
	}
	return trendSummaryJSON{
		SufficientEvidence: sufficient,
		Direction:          label,
		Improved:           improved,
		Regressed:          regressed,
		Unchanged:          unchanged,
		Insufficient:       insufficient,
		Metrics:            trendMetricPayload(metrics),
	}
}

func trendMetricPayload(metrics []trendMetric) []trendMetricJSON {
	rows := make([]trendMetricJSON, 0, len(metrics))
	for _, metric := range metrics {
		rows = append(rows, trendMetricJSON{
			Name:           metric.Name,
			Baseline:       metric.Baseline,
			Current:        metric.Current,
			Direction:      materialTrendDirection(metric),
			LowerIsBetter:  metric.LowerIsBetter,
			Percentage:     metric.PercentageDisplay,
			BaselineSample: metric.BaselineSample,
			CurrentSample:  metric.CurrentSample,
			MinimumSample:  metric.MinimumSample,
		})
	}
	return rows
}

func comparisonCohortPayload(
	performance []matchedPerformanceCohort,
	quality []matchedQualityCohort,
	detailed bool,
) comparisonCohortsJSON {
	totalPerformance := len(performance)
	totalQuality := len(quality)
	if !detailed {
		if len(performance) > compactInterventionLimit {
			performance = performance[:compactInterventionLimit]
		}
		if len(quality) > compactInterventionLimit {
			quality = quality[:compactInterventionLimit]
		}
	}
	payload := comparisonCohortsJSON{
		TotalPerformance: totalPerformance,
		TotalQuality:     totalQuality,
		Performance:      make([]performanceCohortComparisonJSON, 0, len(performance)),
		Quality:          make([]qualityCohortComparisonJSON, 0, len(quality)),
	}
	for _, cohort := range performance {
		payload.Performance = append(payload.Performance, performanceCohortComparisonJSON{
			Baseline: cohort.Baseline,
			Current:  cohort.Current,
		})
	}
	for _, cohort := range quality {
		payload.Quality = append(payload.Quality, qualityCohortComparisonJSON{
			Baseline: cohort.Baseline,
			Current:  cohort.Current,
		})
	}
	return payload
}

func comparisonDiagnosticPayload(
	baseline,
	current diagnosticFailureAnalysis,
	detailed bool,
) comparisonDiagnosticsJSON {
	analysis := compareDiagnosticEffectiveness(baseline, current)
	fingerprints := analysis.Fingerprints
	if !detailed && len(fingerprints) > compactInterventionLimit {
		fingerprints = fingerprints[:compactInterventionLimit]
	}
	if fingerprints == nil {
		fingerprints = []diagnosticEffectivenessFingerprint{}
	}
	return comparisonDiagnosticsJSON{
		Available:         analysis.Available,
		TotalFingerprints: len(analysis.Fingerprints),
		Fingerprints:      fingerprints,
	}
}

func buildInterventionTrendJSON(
	baseline,
	current codexSessionInsightsReport,
	detailed bool,
) interventionTrendJSON {
	trend := interventionTrendJSON{
		SufficientEvidence: baseline.Summary.Sessions >= minimumTrendSessions &&
			current.Summary.Sessions >= minimumTrendSessions,
		MinimumSessions:  minimumTrendSessions,
		BaselineSessions: baseline.Summary.Sessions,
		CurrentSessions:  current.Summary.Sessions,
		Resolved:         []sessionIntervention{},
		Persistent:       []sessionIntervention{},
		New:              []sessionIntervention{},
	}
	if !trend.SufficientEvidence {
		return trend
	}
	baselineByID := map[string]sessionIntervention{}
	currentByID := map[string]sessionIntervention{}
	for _, intervention := range baseline.Interventions {
		baselineByID[intervention.ID] = intervention
	}
	for _, intervention := range current.Interventions {
		currentByID[intervention.ID] = intervention
	}
	for id, intervention := range baselineByID {
		if currentIntervention, exists := currentByID[id]; exists {
			trend.Persistent = append(trend.Persistent, currentIntervention)
		} else {
			trend.Resolved = append(trend.Resolved, intervention)
		}
	}
	for id, intervention := range currentByID {
		if _, exists := baselineByID[id]; !exists {
			trend.New = append(trend.New, intervention)
		}
	}
	trend.Resolved = sortInterventionTrendJSON(trend.Resolved, detailed)
	trend.Persistent = sortInterventionTrendJSON(trend.Persistent, detailed)
	trend.New = sortInterventionTrendJSON(trend.New, detailed)
	return trend
}

func sortInterventionTrendJSON(
	interventions []sessionIntervention,
	detailed bool,
) []sessionIntervention {
	sort.Slice(interventions, func(i, j int) bool {
		leftPriority := interventionTrendPriority(interventions[i])
		rightPriority := interventionTrendPriority(interventions[j])
		if leftPriority != rightPriority {
			return leftPriority > rightPriority
		}
		if interventions[i].FindingCount != interventions[j].FindingCount {
			return interventions[i].FindingCount > interventions[j].FindingCount
		}
		if interventions[i].score != interventions[j].score {
			return interventions[i].score > interventions[j].score
		}
		return interventions[i].ID < interventions[j].ID
	})
	if detailed {
		return interventions
	}
	return boundedInterventions(interventions, compactInterventionLimit)
}

func boundedInterventions(
	interventions []sessionIntervention,
	limit int,
) []sessionIntervention {
	if limit <= 0 || len(interventions) <= limit {
		return interventions
	}
	return interventions[:limit]
}
