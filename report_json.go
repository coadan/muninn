package main

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
	SchemaVersion int                            `json:"schemaVersion"`
	DetailLevel   string                         `json:"detailLevel"`
	Provider      string                         `json:"provider"`
	GeneratedAt   string                         `json:"generatedAt"`
	Since         string                         `json:"since"`
	AnalysisScope sessionAnalysisScope           `json:"analysisScope"`
	Instructions  repositoryInstructionFootprint `json:"instructions"`
	Summary       compactSessionSummary          `json:"summary"`
	Outcomes      compactOutcomeSummary          `json:"outcomes"`
	Profiles      modelEffortAnalysis            `json:"profiles"`
	Delegation    delegationAnalysis             `json:"delegation"`
	Diagnostics   compactDiagnosticSummary       `json:"diagnostics"`
	Interventions []sessionIntervention          `json:"interventions"`
	Findings      []sessionFinding               `json:"findings"`
}

func analysisJSONPayload(report codexSessionInsightsReport, detailed bool) any {
	if detailed {
		report.DetailLevel = "full"
		return report
	}
	summary := report.Summary
	outcomes := report.Outcomes
	return compactSessionInsightsReport{
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
		Interventions: report.Interventions,
		Findings:      report.Findings,
	}
}
