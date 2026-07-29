package cli

import "time"

const (
	sessionEventToolCall   = "tool_call"
	sessionEventToolOutput = "tool_output"
	sessionEventToken      = "token"
	sessionEventComplete   = "complete"
	sessionEventCompaction = "compaction"
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
	Events           []normalizedSessionEvent
}

type normalizedSessionEvent struct {
	Sequence                      int
	OccurredAt                    time.Time
	Kind                          string
	ToolName                      string
	Family                        string
	Shape                         string
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
	SelectorDigests               []string
	CommandCandidates             []ownedCommandInvocation
	OwnedOperations               []string
	OwnedFlags                    []string
	OwnedFlagTools                []string
	OperationTask                 string
	OperationAttributionAmbiguous bool
	OperationContinues            bool
	ConcurrentBatch               bool
	ConcurrentBatchSize           int
	TargetCandidates              []string
	Targets                       []string
	InlineBytes                   int64
	Diagnostic                    *normalizedDiagnosticObservation
	WorkingDirectories            []string
	InRepositoryScope             bool
	RepositoryScopeKnown          bool
}
