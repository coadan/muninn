package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"time"
)

type codexToolCallDescriptor struct {
	Name   string
	Family string
	Shape  string
	Active bool
	First  string
	Last   string
}

type codexRolloutEnvelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexRolloutPayload struct {
	Type           string `json:"type"`
	ID             string `json:"id"`
	ParentThreadID string `json:"parent_thread_id"`
	CWD            string `json:"cwd"`
	CallID         string `json:"call_id"`
	Name           string `json:"name"`
	Arguments      string `json:"arguments"`
	Input          string `json:"input"`
	Info           struct {
		TotalTokenUsage struct {
			InputTokens       int64 `json:"input_tokens"`
			CachedInputTokens int64 `json:"cached_input_tokens"`
			OutputTokens      int64 `json:"output_tokens"`
			ReasoningTokens   int64 `json:"reasoning_output_tokens"`
			TotalTokens       int64 `json:"total_tokens"`
		} `json:"total_token_usage"`
	} `json:"info"`
	Output json.RawMessage `json:"output"`
}

type indexedCodexDescriptor struct {
	codexToolCallDescriptor
	OccurredAt                    time.Time
	ToolRound                     int
	SelectorDigests               []string
	CommandCandidates             []ownedCommandInvocation
	OwnedOperations               []string
	OperationAttributionAmbiguous bool
	EmitsSessionMarker            bool
	ConcurrentBatch               bool
	ConcurrentBatchSize           int
	NestedToolContext             string
	WorkingDirectories            []string
}

func codexRolloutLineNeeded(line []byte) bool {
	return bytes.Contains(line, []byte(`"type":"session_meta"`)) ||
		bytes.Contains(line, []byte(`"type":"compacted"`)) ||
		bytes.Contains(line, []byte(`"type":"context_compacted"`)) ||
		bytes.Contains(line, []byte(`"type":"token_count"`)) ||
		bytes.Contains(line, []byte(`"type":"task_complete"`)) ||
		bytes.Contains(line, []byte(`"type":"task_completed"`)) ||
		bytes.Contains(line, []byte(`"type":"function_call"`)) ||
		bytes.Contains(line, []byte(`"type":"function_call_output"`)) ||
		bytes.Contains(line, []byte(`"type":"custom_tool_call"`)) ||
		bytes.Contains(line, []byte(`"type":"custom_tool_call_output"`))
}

func parseCodexNormalizedSession(path string) (normalizedSession, error) {
	file, err := os.Open(path)
	if err != nil {
		return normalizedSession{}, err
	}
	defer file.Close()

	session := normalizedSession{Provider: "codex", SourcePath: path}
	callDescriptors := map[string]indexedCodexDescriptor{}
	execSessions := map[string]indexedCodexDescriptor{}
	execCells := map[string]indexedCodexDescriptor{}
	toolRound := 0
	sequence := 0
	var lastCompaction time.Time
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !codexRolloutLineNeeded(line) {
			continue
		}
		var envelope codexRolloutEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			continue
		}
		timestamp, err := time.Parse(time.RFC3339Nano, envelope.Timestamp)
		if err != nil {
			continue
		}
		var payload codexRolloutPayload
		if len(envelope.Payload) > 0 {
			_ = json.Unmarshal(envelope.Payload, &payload)
		}
		switch envelope.Type {
		case "compacted":
			appendNormalizedCompaction(&session, &sequence, &lastCompaction, timestamp)
		case "session_meta":
			applyCodexSessionMeta(&session, payload)
		case "event_msg":
			switch payload.Type {
			case "task_complete", "task_completed":
				sequence++
				session.Events = append(session.Events, normalizedSessionEvent{
					Sequence:   sequence,
					OccurredAt: timestamp,
					Kind:       sessionEventComplete,
				})
			case "context_compacted":
				appendNormalizedCompaction(&session, &sequence, &lastCompaction, timestamp)
			case "token_count":
				usage := normalizedTokenUsage{
					InputTokens:       payload.Info.TotalTokenUsage.InputTokens,
					CachedInputTokens: payload.Info.TotalTokenUsage.CachedInputTokens,
					OutputTokens:      payload.Info.TotalTokenUsage.OutputTokens,
					ReasoningTokens:   payload.Info.TotalTokenUsage.ReasoningTokens,
					TotalTokens:       payload.Info.TotalTokenUsage.TotalTokens,
				}
				usage.UncachedInputTokens = usage.InputTokens - usage.CachedInputTokens
				if usage.UncachedInputTokens < 0 {
					usage.UncachedInputTokens = 0
				}
				sequence++
				session.Events = append(session.Events, normalizedSessionEvent{
					Sequence:   sequence,
					OccurredAt: timestamp,
					Kind:       sessionEventToken,
					Tokens:     usage,
				})
			}
		case "response_item":
			switch payload.Type {
			case "function_call", "custom_tool_call":
				toolRound++
				name := strings.TrimSpace(payload.Name)
				if name == "" {
					name = "(unknown)"
				}
				concurrentBatchSize := codexConcurrentToolBatchSize(name, payload.Input)
				descriptor := indexedCodexDescriptor{
					codexToolCallDescriptor: codexToolCallDescriptor{Name: name},
					OccurredAt:              timestamp,
					ToolRound:               toolRound,
					SelectorDigests:         codexSelectorDigests(name, payload.Arguments, payload.Input),
					CommandCandidates:       codexCommandInvocations(name, payload.Arguments, payload.Input),
					EmitsSessionMarker:      codexEmitsExplicitSessionMarker(name, payload.Input),
					ConcurrentBatch:         concurrentBatchSize > 0,
					ConcurrentBatchSize:     concurrentBatchSize,
					NestedToolContext:       codexNestedToolContext(name, payload.Input),
					WorkingDirectories:      codexToolWorkingDirectories(name, payload.Arguments, payload.Input),
				}
				descriptor.OperationAttributionAmbiguous = len(descriptor.CommandCandidates) > 1
				continuation := false
				if reference, ok := codexNestedContinuationReference(name, payload.Input); ok {
					continuation = true
					continuationToolContext := descriptor.NestedToolContext
					if reference.Type == "session" {
						descriptor = execSessions[reference.ID]
					} else {
						descriptor = execCells[strings.ToLower(reference.ID)]
					}
					descriptor.Name = name
					descriptor.OccurredAt = timestamp
					descriptor.NestedToolContext = continuationToolContext
					descriptor.First = ""
					descriptor.Last = ""
				}
				if descriptor.Family == "" {
					descriptor.Family, descriptor.Shape, descriptor.First, descriptor.Last = codexShellCommandDetails(name, payload.Arguments, payload.Input)
				}
				if descriptor.Family == "" {
					if continuationType, continuationID := codexContinuationID(name, payload.Arguments); continuationID != "" {
						continuation = true
						if continuationType == "session" {
							descriptor = execSessions[continuationID]
						} else {
							descriptor = execCells[continuationID]
						}
						descriptor.Name = name
						descriptor.OccurredAt = timestamp
						descriptor.First = ""
						descriptor.Last = ""
					}
				}
				sequence++
				event := normalizedSessionEvent{
					Sequence:                      sequence,
					OccurredAt:                    timestamp,
					Kind:                          sessionEventToolCall,
					ToolName:                      name,
					Family:                        descriptor.Family,
					Shape:                         descriptor.Shape,
					NestedToolContext:             descriptor.NestedToolContext,
					FirstFamily:                   descriptor.First,
					LastFamily:                    descriptor.Last,
					ToolRound:                     toolRound,
					SelectorDigests:               descriptor.SelectorDigests,
					CommandCandidates:             descriptor.CommandCandidates,
					OwnedOperations:               descriptor.OwnedOperations,
					OperationAttributionAmbiguous: descriptor.OperationAttributionAmbiguous,
					ConcurrentBatch:               descriptor.ConcurrentBatch,
					ConcurrentBatchSize:           descriptor.ConcurrentBatchSize,
					TargetCandidates: append(
						codexReadTargetCandidates(name, payload.Arguments, payload.Input),
						codexEditTargetCandidates(name, payload.Input)...,
					),
					InlineBytes:        codexInlineOrchestrationBytes(name, payload.Arguments, payload.Input),
					WorkingDirectories: descriptor.WorkingDirectories,
				}
				if continuation {
					event.FirstFamily = ""
					event.LastFamily = ""
				}
				session.Events = append(session.Events, event)
				if payload.CallID != "" {
					callDescriptors[payload.CallID] = descriptor
				}
			case "function_call_output", "custom_tool_call_output":
				text, statusText, byteCount := codexToolOutputText(payload.Output)
				descriptor := callDescriptors[payload.CallID]
				var continuationReferences []codexContinuationReference
				if descriptor.Family != "" {
					continuationReferences = codexToolContinuationReferences(payload.Output)
					if descriptor.EmitsSessionMarker {
						continuationReferences = append(
							continuationReferences,
							codexExplicitSessionMarkerReferences(payload.Output)...,
						)
					}
				}
				failed := codexToolOutputFailed(statusText, descriptor.Name)
				var diagnostic *normalizedDiagnosticObservation
				if isHeimdalReportInvocation(descriptor.CommandCandidates) {
					diagnostic = parseHeimdalDiagnosticObservation(text)
				}
				reason := ""
				context := ""
				if failed {
					reason = codexToolFailureReasonForDescriptor(statusText, descriptor.codexToolCallDescriptor)
					context = codexFailureContextLabel(descriptor.codexToolCallDescriptor)
				}
				sequence++
				session.Events = append(session.Events, normalizedSessionEvent{
					Sequence:                      sequence,
					OccurredAt:                    timestamp,
					Kind:                          sessionEventToolOutput,
					ToolName:                      descriptor.Name,
					Family:                        descriptor.Family,
					Shape:                         descriptor.Shape,
					NestedToolContext:             descriptor.NestedToolContext,
					ToolRound:                     descriptor.ToolRound,
					CallOccurredAt:                descriptor.OccurredAt,
					Failed:                        failed,
					Truncated:                     codexToolOutputTruncated(text),
					OutputBytes:                   byteCount,
					FailureReason:                 reason,
					FailureContext:                context,
					SelectorDigests:               descriptor.SelectorDigests,
					CommandCandidates:             descriptor.CommandCandidates,
					OwnedOperations:               descriptor.OwnedOperations,
					OperationAttributionAmbiguous: descriptor.OperationAttributionAmbiguous,
					OperationContinues:            len(continuationReferences) > 0,
					ConcurrentBatch:               descriptor.ConcurrentBatch,
					ConcurrentBatchSize:           descriptor.ConcurrentBatchSize,
					Diagnostic:                    diagnostic,
					WorkingDirectories:            descriptor.WorkingDirectories,
				})
				if descriptor.Family != "" {
					for _, reference := range continuationReferences {
						if reference.Type == "session" {
							execSessions[reference.ID] = descriptor
						} else {
							execCells[strings.ToLower(reference.ID)] = descriptor
						}
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return normalizedSession{}, err
	}
	return session, nil
}

func applyCodexSessionMeta(session *normalizedSession, payload codexRolloutPayload) {
	if session == nil {
		return
	}
	if payload.CWD != "" {
		session.CWD = payload.CWD
	}
	if payload.ID != "" {
		session.LineageKey = ownershipSelectorDigest("provider-thread", payload.ID)
	}
	if payload.ParentThreadID != "" {
		session.AgentKind = "subagent"
		session.ParentLineageKey = ownershipSelectorDigest("provider-thread", payload.ParentThreadID)
	}
}

func appendNormalizedCompaction(session *normalizedSession, sequence *int, last *time.Time, timestamp time.Time) {
	if !last.IsZero() {
		delta := timestamp.Sub(*last)
		if delta >= -5*time.Second && delta <= 5*time.Second {
			return
		}
	}
	*sequence++
	session.Events = append(session.Events, normalizedSessionEvent{
		Sequence:   *sequence,
		OccurredAt: timestamp,
		Kind:       sessionEventCompaction,
	})
	*last = timestamp
}
