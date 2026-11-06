package cli

import "time"

const (
	sessionEventToolCall   = "tool_call"
	sessionEventToolOutput = "tool_output"
	sessionEventToken      = "token"
	sessionEventComplete   = "complete"
	sessionEventCompaction = "compaction"
	// sessionEventProviderCost carries a provider's cumulative cost. It is
	// telemetry, not task activity: it never opens, extends, or ends a turn.
	sessionEventProviderCost = "provider_cost"
)

// normalizedSession is the provider-neutral boundary between ingestion and
// storage/reporting. SourcePath and CWD are local index metadata only and must
// never be emitted by reports.
type normalizedSession struct {
	Provider         string
	SourcePath       string
	CWD              string
	Model            string
	ReasoningEffort  string
	AgentKind        string
	LineageKey       string
	ParentLineageKey string
	SpawnStatus      string
	// UnrecognizedRecords and UnrecognizedContentBlocks count source records
	// and message content blocks whose type the adapter does not know. They
	// make provider format drift visible as numbers; type names are never kept.
	UnrecognizedRecords       int
	UnrecognizedContentBlocks int
	Events                    []normalizedSessionEvent
}

type normalizedSessionEvent struct {
	Sequence                      int
	OccurredAt                    time.Time
	Kind                          string
	ToolName                      string
	Family                        string
	Shape                         string
	NestedToolContext             string
	FirstFamily                   string
	LastFamily                    string
	ToolRound                     int
	CallOccurredAt                time.Time
	Failed                        bool
	Truncated                     bool
	OutputBytes                   int64
	FailureReason                 string
	FailureContext                string
	Tokens                        normalizedTokenUsage
	Cost                          normalizedProviderCost // provider_cost events only
	SelectorDigests               []string
	CommandCandidates             []ownedCommandInvocation
	OwnedOperations               []string
	BypassedOperations            []string
	OwnedFlags                    []string
	OwnedFlagScopes               []string
	OperationTask                 string
	OperationAttributionAmbiguous bool
	OperationContinues            bool
	ConcurrentBatch               bool
	ConcurrentBatchSize           int
	TargetCandidates              []string
	Targets                       []string
	GuidanceArtifacts             []normalizedGuidanceArtifact
	InlineBytes                   int64
	Diagnostic                    *normalizedDiagnosticObservation
	WorkingDirectories            []string
	InRepositoryScope             bool
	RepositoryScopeKnown          bool
}

// normalizedGuidanceArtifact is a privacy-safe identity for context loaded by
// an agent. ID is a validated logical name, never a source path or contents.
type normalizedGuidanceArtifact struct {
	Kind            string `json:"kind"`
	ID              string `json:"id"`
	EstimatedTokens int64  `json:"estimatedTokens,omitempty"`
}
