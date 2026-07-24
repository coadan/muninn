package main

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
	Provider   string
	SourcePath string
	CWD        string
	Events     []normalizedSessionEvent
}

type normalizedSessionEvent struct {
	Sequence         int
	OccurredAt       time.Time
	Kind             string
	ToolName         string
	Family           string
	Shape            string
	FirstFamily      string
	LastFamily       string
	ToolRound        int
	CallOccurredAt   time.Time
	Failed           bool
	Truncated        bool
	OutputBytes      int64
	FailureReason    string
	FailureContext   string
	Tokens           codexTokenUsage
	SelectorDigests  []string
	TargetCandidates []string
	Targets          []string
	InlineBytes      int64
}

func sessionRecordFromNormalized(session normalizedSession, workspaceRoot string, since, generatedAt time.Time, ownership ownershipCatalog) (codexSessionRecord, error) {
	record := newCodexSessionRecord()
	record.CWD = session.CWD
	if record.CWD == "" {
		return codexSessionRecord{}, nil
	}
	inside, err := pathInsideRoot(workspaceRoot, record.CWD)
	if err != nil || !inside {
		return codexSessionRecord{}, nil
	}

	previousCommandRound := 0
	previousCommand := normalizedSessionEvent{}
	for _, event := range session.Events {
		active := !event.OccurredAt.Before(since) && !event.OccurredAt.After(generatedAt)
		if !active {
			continue
		}
		if record.StartedAt.IsZero() || event.OccurredAt.Before(record.StartedAt) {
			record.StartedAt = event.OccurredAt
		}
		if record.EndedAt.IsZero() || event.OccurredAt.After(record.EndedAt) {
			record.EndedAt = event.OccurredAt
		}
		switch event.Kind {
		case sessionEventComplete:
			record.Completed = true
		case sessionEventCompaction:
			record.Compactions++
		case sessionEventToken:
			if event.Tokens.TotalTokens >= record.Tokens.TotalTokens {
				record.Tokens = event.Tokens
			}
		case sessionEventToolCall:
			record.ToolCalls++
			if event.InlineBytes > 0 {
				record.InlineOrchestrationCalls++
				record.InlineOrchestrationBytes += event.InlineBytes
			}
			record.ToolCallsByName[event.ToolName]++
			addCodexToolMetrics(record.ToolMetricsByName, event.ToolName, 1, false, false, 0)
			if event.Family != "" {
				addCodexToolMetrics(record.ShellCommandsByFamily, event.Family, 1, false, false, 0)
			}
			if event.Shape != "" {
				addCodexToolMetrics(record.MixedShellShapes, event.Shape, 1, false, false, 0)
			}
			for _, ownedTool := range ownership.match(event.SelectorDigests) {
				addCodexToolMetrics(record.OwnedTooling, ownedTool, 1, false, false, 0)
			}
			targets := event.Targets
			if len(targets) == 0 {
				targets = normalizeRepositoryTargets(event.TargetCandidates, session.CWD, workspaceRoot)
			}
			for _, target := range targets {
				metrics := record.ReadTargets[target]
				metrics.Reads++
				if previousCommand.LastFamily == "search" && previousCommandRound == event.ToolRound-1 {
					metrics.SearchReadLoops++
				}
				record.ReadTargets[target] = metrics
			}
			if event.FirstFamily != "" {
				if previousCommand.LastFamily != "" && previousCommandRound == event.ToolRound-1 {
					record.CrossCallTransitions[previousCommand.LastFamily+" -> "+event.FirstFamily]++
				}
				previousCommand = event
				previousCommandRound = event.ToolRound
			}
		case sessionEventToolOutput:
			// Match direct analysis semantics: output is attributable only when
			// both the originating call and its output are inside the window.
			if event.CallOccurredAt.Before(since) || event.CallOccurredAt.After(generatedAt) {
				continue
			}
			record.ToolOutputBytes += event.OutputBytes
			if event.Truncated {
				record.TruncatedToolCalls++
			}
			if event.Failed {
				record.FailedToolCalls++
				record.FailureReasons[event.FailureReason]++
				addCodexFailureContext(record.FailureContexts, event.FailureReason, event.FailureContext)
			}
			if event.ToolName != "" {
				addCodexToolMetrics(record.ToolMetricsByName, event.ToolName, 0, event.Failed, event.Truncated, event.OutputBytes)
			}
			if event.Family != "" {
				addCodexToolMetrics(record.ShellCommandsByFamily, event.Family, 0, event.Failed, event.Truncated, event.OutputBytes)
			}
			if event.Shape != "" {
				addCodexToolMetrics(record.MixedShellShapes, event.Shape, 0, event.Failed, event.Truncated, event.OutputBytes)
			}
			for _, ownedTool := range ownership.match(event.SelectorDigests) {
				addCodexToolMetrics(record.OwnedTooling, ownedTool, 0, event.Failed, event.Truncated, event.OutputBytes)
			}
		}
	}
	if record.StartedAt.IsZero() {
		return codexSessionRecord{}, nil
	}
	record.Task = codexTaskName(workspaceRoot, record.CWD)
	return record, nil
}

func newCodexSessionRecord() codexSessionRecord {
	return codexSessionRecord{
		ToolCallsByName:       map[string]int{},
		ToolMetricsByName:     map[string]codexToolMetrics{},
		ShellCommandsByFamily: map[string]codexToolMetrics{},
		MixedShellShapes:      map[string]codexToolMetrics{},
		CrossCallTransitions:  map[string]int{},
		OwnedTooling:          map[string]codexToolMetrics{},
		ReadTargets:           map[string]codexTargetMetrics{},
		FailureReasons:        map[string]int{},
		FailureContexts:       map[string]map[string]int{},
	}
}
