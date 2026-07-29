package cli

import "time"

const codexSessionInsightsSchemaVersion = 98

type normalizedTokenUsage struct {
	InputTokens         int64 `json:"inputTokens"`
	CachedInputTokens   int64 `json:"cachedInputTokens"`
	UncachedInputTokens int64 `json:"uncachedInputTokens"`
	OutputTokens        int64 `json:"outputTokens"`
	ReasoningTokens     int64 `json:"reasoningTokens"`
	TotalTokens         int64 `json:"totalTokens"`
}

type codexTaskInsights struct {
	Task string `json:"task"`
	codexAggregateMetrics
}

type codexAggregateMetrics struct {
	Sessions                     int                                          `json:"sessions"`
	CompletedSessions            int                                          `json:"completedSessions"`
	IncompleteSessions           int                                          `json:"incompleteSessions"`
	DurationSeconds              int64                                        `json:"durationSeconds"`
	Compactions                  int                                          `json:"compactions"`
	SessionsWithCompactions      int                                          `json:"sessionsWithCompactions"`
	Tokens                       normalizedTokenUsage                         `json:"tokens"`
	FreshTokens                  int64                                        `json:"freshTokens"`
	ToolCalls                    int                                          `json:"toolCalls"`
	FailedToolCalls              int                                          `json:"failedToolCalls"`
	TruncatedToolCalls           int                                          `json:"truncatedToolCalls"`
	ToolOutputBytes              int64                                        `json:"toolOutputBytes"`
	ToolOutputTokens             int64                                        `json:"toolOutputTokens"`
	ShellCommandsByFamily        map[string]codexToolMetrics                  `json:"shellCommandsByFamily"`
	MixedShellShapes             map[string]codexToolMetrics                  `json:"mixedShellShapes"`
	CrossCallTransitions         map[string]codexTransitionMetrics            `json:"crossCallTransitions"`
	OwnedTooling                 map[string]codexToolMetrics                  `json:"ownedTooling"`
	OwnedToolUnmatched           map[string]codexToolMetrics                  `json:"ownedToolUnmatched,omitempty"`
	OwnedOperations              map[string]codexOwnedOperationMetrics        `json:"ownedOperations"`
	OwnedFlags                   map[string]codexOccurrenceMetrics            `json:"ownedFlags"`
	OwnedFlagEligibleCalls       map[string]codexOccurrenceMetrics            `json:"ownedFlagEligibleCalls"`
	OwnedOperationFailureReasons map[string]map[string]codexOccurrenceMetrics `json:"ownedOperationFailureReasons"`
	ReadTargets                  map[string]codexTargetMetrics                `json:"readTargets"`
	InlineOrchestrationCalls     int                                          `json:"inlineOrchestrationCalls"`
	InlineOrchestrationBytes     int64                                        `json:"inlineOrchestrationBytes"`
	InlineOrchestrationMaxBytes  int64                                        `json:"inlineOrchestrationMaxBytes"`
	InlineOrchestrationSessions  int                                          `json:"inlineOrchestrationSessions"`
	InlineOrchestrationByTool    map[string]codexInlineMetrics                `json:"inlineOrchestrationByTool"`
	InlineOrchestrationByFamily  map[string]codexInlineMetrics                `json:"inlineOrchestrationByFamily"`
	InlineOrchestrationByOwner   map[string]codexInlineMetrics                `json:"inlineOrchestrationByOwner"`
	FailureReasons               map[string]int                               `json:"failureReasons"`
	FailureContexts              map[string]map[string]codexOccurrenceMetrics `json:"failureContexts"`
	ProgressStalls               map[string]codexWaitMetrics                  `json:"progressStalls"`
	ExpectedWaits                map[string]codexWaitMetrics                  `json:"expectedWaits"`
	RapidPolls                   map[string]codexWaitMetrics                  `json:"rapidPolls"`
	AbandonedContinuations       map[string]codexOccurrenceMetrics            `json:"abandonedContinuations"`
	OversizedOutputs             map[string]codexOversizedOutputMetrics       `json:"oversizedOutputs"`
	DeliveryRework               deliveryReworkMetrics                        `json:"deliveryRework"`
	DownstreamQuality            downstreamQualityMetrics                     `json:"downstreamQuality"`
	Activity                     map[string]time.Time                         `json:"-"`
}

type codexToolMetrics struct {
	Calls                          int   `json:"calls"`
	AmbiguousCalls                 int   `json:"ambiguousCalls,omitempty"`
	Sessions                       int   `json:"sessions"`
	FailedCalls                    int   `json:"failedCalls"`
	AmbiguousFailedCalls           int   `json:"ambiguousFailedCalls,omitempty"`
	TruncatedCalls                 int   `json:"truncatedCalls"`
	AmbiguousTruncatedCalls        int   `json:"ambiguousTruncatedCalls,omitempty"`
	OutputBytes                    int64 `json:"outputBytes"`
	AmbiguousOutputBytes           int64 `json:"ambiguousOutputBytes,omitempty"`
	EstimatedOutputTokens          int64 `json:"estimatedOutputTokens"`
	EstimatedAmbiguousOutputTokens int64 `json:"estimatedAmbiguousOutputTokens,omitempty"`
}

type codexOwnedOperationMetrics struct {
	Calls                          int   `json:"calls"`
	AmbiguousCalls                 int   `json:"ambiguousCalls"`
	Sessions                       int   `json:"sessions"`
	FailedCalls                    int   `json:"failedCalls"`
	AmbiguousFailedCalls           int   `json:"ambiguousFailedCalls"`
	TruncatedCalls                 int   `json:"truncatedCalls"`
	AmbiguousTruncatedCalls        int   `json:"ambiguousTruncatedCalls"`
	OutputBytes                    int64 `json:"outputBytes"`
	AmbiguousOutputBytes           int64 `json:"ambiguousOutputBytes"`
	EstimatedOutputTokens          int64 `json:"estimatedOutputTokens"`
	EstimatedAmbiguousOutputTokens int64 `json:"estimatedAmbiguousOutputTokens"`
}

type codexTransitionMetrics struct {
	Count    int `json:"count"`
	Sessions int `json:"sessions"`
}

type codexOccurrenceMetrics struct {
	Count    int `json:"count"`
	Sessions int `json:"sessions"`
}

type codexWaitMetrics struct {
	Calls    int   `json:"calls"`
	Seconds  int64 `json:"seconds"`
	Sessions int   `json:"sessions"`
}

type codexOversizedOutputMetrics struct {
	Calls          int   `json:"calls"`
	OutputBytes    int64 `json:"outputBytes"`
	MaxOutputBytes int64 `json:"maxOutputBytes"`
	Sessions       int   `json:"sessions"`
	NestedCalls    int   `json:"nestedCalls,omitempty"`
	MaxNestedCalls int   `json:"maxNestedCalls,omitempty"`
}

type codexInlineMetrics struct {
	Calls    int   `json:"calls"`
	Sessions int   `json:"sessions"`
	Bytes    int64 `json:"bytes"`
	MaxBytes int64 `json:"maxBytes"`
}

type codexTargetMetrics struct {
	Reads                       int `json:"reads"`
	SearchReadLoops             int `json:"searchReadLoops"`
	Sessions                    int `json:"sessions"`
	RediscoverySessions         int `json:"rediscoverySessions"`
	EditedSessions              int `json:"editedSessions"`
	UneditedRediscoverySessions int `json:"uneditedRediscoverySessions"`
}

type codexSessionInsightsSummary struct {
	FilesScanned      int                         `json:"filesScanned"`
	FilesUnreadable   int                         `json:"filesUnreadable"`
	ToolCallsByName   map[string]int              `json:"toolCallsByName"`
	ToolMetricsByName map[string]codexToolMetrics `json:"toolMetricsByName"`
	codexAggregateMetrics
}

type codexSessionInsightsReport struct {
	SchemaVersion  int                            `json:"schemaVersion"`
	DetailLevel    string                         `json:"detailLevel"`
	Provider       string                         `json:"provider"`
	GeneratedAt    string                         `json:"generatedAt"`
	Since          string                         `json:"since"`
	AnalysisScope  sessionAnalysisScope           `json:"analysisScope"`
	WorkspaceRoot  string                         `json:"-"`
	SessionDirs    []string                       `json:"-"`
	Instructions   repositoryInstructionFootprint `json:"instructions"`
	Summary        codexSessionInsightsSummary    `json:"summary"`
	Tasks          []codexTaskInsights            `json:"tasks"`
	Interventions  []sessionIntervention          `json:"interventions"`
	Findings       []sessionFinding               `json:"findings"`
	Outcomes       completionEpisodeAnalysis      `json:"outcomes"`
	Profiles       modelEffortAnalysis            `json:"profiles"`
	Delegation     delegationAnalysis             `json:"delegation"`
	Diagnostics    diagnosticFailureAnalysis      `json:"diagnostics"`
	taskEpisodes   []codexTaskEpisode
	sessionRecords []codexSessionRecord
	operationTasks map[string]map[string]codexOwnedOperationMetrics
}

type sessionAnalysisScope struct {
	WindowKind      string `json:"windowKind"`
	LookbackSeconds int64  `json:"lookbackSeconds,omitempty"`
	Task            string `json:"task,omitempty"`
	IncludeArchived bool   `json:"includeArchived,omitempty"`
	Focus           string `json:"focus,omitempty"`
}

type codexSessionRecord struct {
	SourcePath                   string
	CWD                          string
	Task                         string
	Model                        string
	ReasoningEffort              string
	AgentKind                    string
	LineageKey                   string
	ParentLineageKey             string
	SpawnStatus                  string
	StartedAt                    time.Time
	EndedAt                      time.Time
	Completed                    bool
	Compactions                  int
	Tokens                       normalizedTokenUsage
	ToolCalls                    int
	FailedToolCalls              int
	TruncatedToolCalls           int
	ToolOutputBytes              int64
	ToolCallsByName              map[string]int
	ToolMetricsByName            map[string]codexToolMetrics
	ShellCommandsByFamily        map[string]codexToolMetrics
	MixedShellShapes             map[string]codexToolMetrics
	CrossCallTransitions         map[string]int
	OwnedTooling                 map[string]codexToolMetrics
	OwnedToolUnmatched           map[string]codexToolMetrics
	OwnedOperations              map[string]codexToolMetrics
	OwnedOperationAmbiguous      map[string]codexToolMetrics
	OwnedFlags                   map[string]int
	OwnedFlagEligibleCalls       map[string]int
	OwnedOperationTasks          map[string]map[string]codexOwnedOperationMetrics
	OwnedOperationFailureReasons map[string]map[string]int
	ReadTargets                  map[string]codexTargetMetrics
	EditTargets                  map[string]int
	InlineOrchestrationCalls     int
	InlineOrchestrationBytes     int64
	InlineOrchestrationMaxBytes  int64
	InlineOrchestrationByTool    map[string]codexInlineMetrics
	InlineOrchestrationByFamily  map[string]codexInlineMetrics
	InlineOrchestrationByOwner   map[string]codexInlineMetrics
	FailureReasons               map[string]int
	FailureContexts              map[string]map[string]int
	ProgressStalls               map[string]codexWaitMetrics
	ExpectedWaits                map[string]codexWaitMetrics
	RapidPolls                   map[string]codexWaitMetrics
	AbandonedContinuations       map[string]int
	OversizedOutputs             map[string]codexOversizedOutputMetrics
	DeliveryRework               deliveryReworkMetrics
	DownstreamQuality            downstreamQualityMetrics
	Activity                     map[string]time.Time
	TaskEpisodes                 []codexTaskEpisode
	DiagnosticFailures           []diagnosticFailureEpisode
	DiagnosticPasses             []normalizedDiagnosticObservation
}
