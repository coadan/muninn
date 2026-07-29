package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type indexedCodexDescriptor struct {
	codexToolCallDescriptor
	OccurredAt                    time.Time
	SelectorDigests               []string
	CommandCandidates             []ownedCommandInvocation
	OwnedOperations               []string
	OperationAttributionAmbiguous bool
	EmitsSessionMarker            bool
	ConcurrentBatch               bool
	WorkingDirectories            []string
}

type codexSessionSource struct{}

func (codexSessionSource) Name() string {
	return "codex"
}

func (codexSessionSource) Discover(explicit string, includeArchived bool) (sessionDiscovery, error) {
	resolved, err := resolveCodexSessionsDir(explicit)
	if err != nil {
		return sessionDiscovery{}, err
	}
	dirs := []string{resolved}
	if includeArchived {
		archivedDir := filepath.Join(filepath.Dir(resolved), "archived_sessions")
		if dirExists(archivedDir) {
			dirs = append(dirs, archivedDir)
		}
	}
	return discoverCodexSessions(dirs)
}

func (codexSessionSource) Metadata(discovery sessionDiscovery) map[string]normalizedSessionMetadata {
	return loadCodexSessionMetadata(discovery.Dirs)
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
				usage := codexTokenUsage{
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
				descriptor := indexedCodexDescriptor{
					codexToolCallDescriptor: codexToolCallDescriptor{Name: name},
					OccurredAt:              timestamp,
					SelectorDigests:         codexSelectorDigests(name, payload.Arguments, payload.Input),
					CommandCandidates:       codexCommandInvocations(name, payload.Arguments, payload.Input),
					EmitsSessionMarker:      codexEmitsExplicitSessionMarker(name, payload.Input),
					ConcurrentBatch:         codexConcurrentToolBatch(name, payload.Input),
					WorkingDirectories:      codexToolWorkingDirectories(name, payload.Arguments, payload.Input),
				}
				descriptor.OperationAttributionAmbiguous = len(descriptor.CommandCandidates) > 1
				continuation := false
				if reference, ok := codexNestedContinuationReference(name, payload.Input); ok {
					continuation = true
					if reference.Type == "session" {
						descriptor = execSessions[reference.ID]
					} else {
						descriptor = execCells[strings.ToLower(reference.ID)]
					}
					descriptor.Name = name
					descriptor.OccurredAt = timestamp
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
					FirstFamily:                   descriptor.First,
					LastFamily:                    descriptor.Last,
					ToolRound:                     toolRound,
					SelectorDigests:               descriptor.SelectorDigests,
					CommandCandidates:             descriptor.CommandCandidates,
					OwnedOperations:               descriptor.OwnedOperations,
					OperationAttributionAmbiguous: descriptor.OperationAttributionAmbiguous,
					ConcurrentBatch:               descriptor.ConcurrentBatch,
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

func readCodexRolloutLineage(path string) (string, string) {
	file, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 16*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte(`"session_meta"`)) {
			continue
		}
		var envelope codexRolloutEnvelope
		if json.Unmarshal(line, &envelope) != nil || envelope.Type != "session_meta" {
			continue
		}
		var payload codexRolloutPayload
		if json.Unmarshal(envelope.Payload, &payload) != nil {
			continue
		}
		lineage := ""
		parent := ""
		if payload.ID != "" {
			lineage = ownershipSelectorDigest("provider-thread", payload.ID)
		}
		if payload.ParentThreadID != "" {
			parent = ownershipSelectorDigest("provider-thread", payload.ParentThreadID)
		}
		return lineage, parent
	}
	return "", ""
}

func (codexSessionSource) NormalizeSession(path string) (normalizedSession, error) {
	return parseCodexNormalizedSession(path)
}

func (codexSessionSource) SessionCWD(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte(`"type":"session_meta"`)) {
			continue
		}
		var envelope codexRolloutEnvelope
		if json.Unmarshal(line, &envelope) != nil || envelope.Type != "session_meta" {
			continue
		}
		var payload codexRolloutPayload
		if json.Unmarshal(envelope.Payload, &payload) == nil {
			return payload.CWD, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", nil
}

func discoverCodexSessions(sessionDirs []string) (sessionDiscovery, error) {
	discovery := sessionDiscovery{Dirs: append([]string(nil), sessionDirs...)}
	for _, sessionDir := range sessionDirs {
		err := filepath.WalkDir(sessionDir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				discovery.FilesUnreadable++
				return nil
			}
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
				return nil
			}
			discovery.Files = append(discovery.Files, path)
			return nil
		})
		if err != nil {
			return sessionDiscovery{}, fmt.Errorf("scan Codex sessions in %s: %w", sessionDir, err)
		}
	}
	sort.Strings(discovery.Files)
	return discovery, nil
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
