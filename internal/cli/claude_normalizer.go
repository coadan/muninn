package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Claude Code transcripts are JSONL files with one record per content block.
// Assistant records of one API message repeat that message's usage, so usage is
// counted once per message ID. The normalizer reads only structure: tool names,
// tool results' size and status, usage, and timestamps. Prompt and assistant
// text, thinking, and attachments are never decoded.
//
// Tool names are mapped onto the provider-neutral vocabulary the shared
// analysis already understands: shell commands are "exec_command", file edits
// are "apply_patch", and subagent launch and messaging are "spawn_agent" and
// "send_message". Other tools keep their (validated) names.

var claudeToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

type claudeRecord struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	Timestamp string `json:"timestamp"`
	SessionID string `json:"sessionId"`
	AgentID   string `json:"agentId"`
	CWD       string `json:"cwd"`
	Effort    string `json:"effort"`
	Message   struct {
		ID         string          `json:"id"`
		Model      string          `json:"model"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
		Usage      claudeUsage     `json:"usage"`
	} `json:"message"`
}

type claudeUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
}

type claudeContentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

type claudeToolInput struct {
	Command      string `json:"command"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Skill        string `json:"skill"`
}

type claudeCall struct {
	codexToolCallDescriptor
	OccurredAt         time.Time
	ToolRound          int
	SelectorDigests    []string
	CommandCandidates  []ownedCommandInvocation
	WorkingDirectories []string
}

func claudeRecordLineNeeded(line []byte) bool {
	return bytes.Contains(line, []byte(`"type":"assistant"`)) ||
		bytes.Contains(line, []byte(`"type":"user"`)) ||
		bytes.Contains(line, []byte(`"compact_boundary"`))
}

func parseClaudeNormalizedSession(path string) (normalizedSession, error) {
	file, err := os.Open(path)
	if err != nil {
		return normalizedSession{}, err
	}
	defer file.Close()

	session := normalizedSession{Provider: "claude", SourcePath: path}
	calls := map[string]claudeCall{}
	usageByMessage := map[string]claudeUsage{}
	completed := map[string]bool{}
	seenMessages := map[string]bool{}
	models := map[string]int{}
	var total claudeUsage
	toolRound, sequence := 0, 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !claudeRecordLineNeeded(line) {
			continue
		}
		var record claudeRecord
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		timestamp, err := time.Parse(time.RFC3339Nano, record.Timestamp)
		if err != nil {
			continue
		}
		applyClaudeRecordIdentity(&session, record)
		switch {
		case record.Type == "system" && record.Subtype == "compact_boundary":
			sequence++
			session.Events = append(session.Events, normalizedSessionEvent{
				Sequence: sequence, OccurredAt: timestamp, Kind: sessionEventCompaction,
			})
		case record.Type == "assistant":
			messageID := record.Message.ID
			if model := record.Message.Model; model != "" && model != "<synthetic>" && !seenMessages[messageID] {
				models[model]++
			}
			seenMessages[messageID] = true
			var blocks []claudeContentBlock
			_ = json.Unmarshal(record.Message.Content, &blocks)
			for _, block := range blocks {
				if block.Type != "tool_use" {
					continue
				}
				toolRound++
				call, event := claudeToolCallEvent(block, record.CWD, timestamp, toolRound)
				sequence++
				event.Sequence = sequence
				session.Events = append(session.Events, event)
				if block.ID != "" {
					calls[block.ID] = call
				}
			}
			if messageID != "" && record.Message.Usage != (claudeUsage{}) {
				previous := usageByMessage[messageID]
				usageByMessage[messageID] = record.Message.Usage
				if record.Message.Usage != previous {
					total.InputTokens += record.Message.Usage.InputTokens - previous.InputTokens
					total.CacheCreationTokens += record.Message.Usage.CacheCreationTokens - previous.CacheCreationTokens
					total.CacheReadTokens += record.Message.Usage.CacheReadTokens - previous.CacheReadTokens
					total.OutputTokens += record.Message.Usage.OutputTokens - previous.OutputTokens
					sequence++
					session.Events = append(session.Events, normalizedSessionEvent{
						Sequence: sequence, OccurredAt: timestamp, Kind: sessionEventToken,
						Tokens: claudeTokenUsage(total),
					})
				}
			}
			if record.Message.StopReason == "end_turn" && messageID != "" && !completed[messageID] {
				completed[messageID] = true
				sequence++
				session.Events = append(session.Events, normalizedSessionEvent{
					Sequence: sequence, OccurredAt: timestamp, Kind: sessionEventComplete,
				})
			}
			if session.ReasoningEffort == "" && record.Effort != "" {
				session.ReasoningEffort = normalizedProviderLabel(record.Effort)
			}
		case record.Type == "user":
			var blocks []claudeContentBlock
			if json.Unmarshal(record.Message.Content, &blocks) != nil {
				continue // a plain prompt: never decoded or retained
			}
			for _, block := range blocks {
				if block.Type != "tool_result" {
					continue
				}
				sequence++
				session.Events = append(session.Events, claudeToolOutputEvent(block, calls[block.ToolUseID], timestamp, sequence))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return normalizedSession{}, err
	}
	session.Model = claudeDominantModel(models)
	return session, nil
}

// applyClaudeRecordIdentity takes the working directory and lineage from the
// first record that carries them. Subagent transcripts share their parent's
// session ID and are told apart by agent ID. Provider IDs are digested at once.
func applyClaudeRecordIdentity(session *normalizedSession, record claudeRecord) {
	if session.CWD == "" && record.CWD != "" {
		session.CWD = record.CWD
	}
	if session.LineageKey != "" || record.SessionID == "" {
		return
	}
	if record.AgentID != "" {
		session.AgentKind = "subagent"
		session.LineageKey = ownershipSelectorDigest("provider-thread", record.SessionID+"/"+record.AgentID)
		session.ParentLineageKey = ownershipSelectorDigest("provider-thread", record.SessionID)
		return
	}
	session.AgentKind = "root"
	session.LineageKey = ownershipSelectorDigest("provider-thread", record.SessionID)
}

func claudeTokenUsage(total claudeUsage) normalizedTokenUsage {
	usage := normalizedTokenUsage{
		InputTokens:         total.InputTokens + total.CacheCreationTokens + total.CacheReadTokens,
		CachedInputTokens:   total.CacheReadTokens,
		UncachedInputTokens: total.InputTokens + total.CacheCreationTokens,
		OutputTokens:        total.OutputTokens,
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	return usage
}

func claudeDominantModel(models map[string]int) string {
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)
	best := ""
	for _, name := range names {
		if best == "" || models[name] > models[best] {
			best = name
		}
	}
	if best == "" || !providerLabelPattern.MatchString(best) {
		return ""
	}
	return best
}

func claudeToolCallEvent(block claudeContentBlock, cwd string, at time.Time, round int) (claudeCall, normalizedSessionEvent) {
	var input claudeToolInput
	_ = json.Unmarshal(block.Input, &input)
	name := block.Name
	if !claudeToolNamePattern.MatchString(name) {
		name = "(unknown)"
	}
	call := claudeCall{OccurredAt: at, ToolRound: round}
	event := normalizedSessionEvent{OccurredAt: at, Kind: sessionEventToolCall, ToolRound: round}
	switch name {
	case "Bash":
		name = "exec_command"
		arguments, _ := json.Marshal(map[string]string{"cmd": input.Command})
		argumentText := string(arguments)
		call.Family, call.Shape, call.First, call.Last = codexShellCommandDetails(name, argumentText, "")
		call.SelectorDigests = codexSelectorDigests(name, argumentText, "")
		call.CommandCandidates = codexCommandInvocations(name, argumentText, "")
		if strings.TrimSpace(cwd) != "" {
			call.WorkingDirectories = appendWorkingDirectory(nil, cwd)
		}
		event.TargetCandidates = codexReadTargetCandidates(name, argumentText, "")
		event.GuidanceArtifacts = codexGuidanceArtifacts(event.TargetCandidates, call.WorkingDirectories...)
		event.InlineBytes = codexInlineOrchestrationBytes(name, argumentText, "")
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		name = "apply_patch"
		event.TargetCandidates = appendNonEmpty(nil, input.FilePath, input.NotebookPath)
	case "Read":
		call.First, call.Last = "file reads", "file reads"
		event.TargetCandidates = appendNonEmpty(nil, input.FilePath)
		event.GuidanceArtifacts = codexGuidanceArtifacts(event.TargetCandidates)
	case "Agent", "Task":
		name = "spawn_agent"
	case "SendMessage":
		name = "send_message"
	case "Grep":
		call.First, call.Last = "search", "search"
	case "Glob":
		call.First, call.Last = "file reads", "file reads"
	case "Skill":
		if guidanceArtifactIDPattern.MatchString(input.Skill) {
			event.GuidanceArtifacts = []normalizedGuidanceArtifact{{Kind: "skill", ID: input.Skill}}
		}
	}
	if call.SelectorDigests == nil {
		call.SelectorDigests = []string{ownershipSelectorDigest("tool", name)}
	}
	call.Name = name
	event.ToolName = name
	event.Family, event.Shape = call.Family, call.Shape
	event.FirstFamily, event.LastFamily = call.First, call.Last
	event.SelectorDigests = call.SelectorDigests
	event.CommandCandidates = call.CommandCandidates
	event.OperationAttributionAmbiguous = len(call.CommandCandidates) > 1
	event.WorkingDirectories = call.WorkingDirectories
	return call, event
}

func appendNonEmpty(values []string, candidates ...string) []string {
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) != "" {
			values = appendUniqueString(values, candidate)
		}
	}
	return values
}

func claudeToolOutputEvent(block claudeContentBlock, call claudeCall, at time.Time, sequence int) normalizedSessionEvent {
	text := claudeToolResultText(block.Content)
	failed := block.IsError && !claudeUserDeclined(text)
	event := normalizedSessionEvent{
		Sequence:                      sequence,
		OccurredAt:                    at,
		Kind:                          sessionEventToolOutput,
		ToolName:                      call.Name,
		Family:                        call.Family,
		Shape:                         call.Shape,
		ToolRound:                     call.ToolRound,
		CallOccurredAt:                call.OccurredAt,
		Failed:                        failed,
		Truncated:                     codexToolOutputTruncated(text) || strings.Contains(text, "<persisted-output>"),
		OutputBytes:                   int64(len(text)),
		SelectorDigests:               call.SelectorDigests,
		CommandCandidates:             call.CommandCandidates,
		OperationAttributionAmbiguous: len(call.CommandCandidates) > 1,
		WorkingDirectories:            call.WorkingDirectories,
	}
	if failed {
		event.FailureReason = "tool error"
		if call.Name == "exec_command" {
			event.FailureReason = codexToolFailureReasonForDescriptor(claudeShellStatusText(text), call.codexToolCallDescriptor)
		}
		event.FailureContext = codexFailureContextLabel(call.codexToolCallDescriptor)
	}
	return event
}

// claudeShellStatusText rewrites Claude Code's leading "Exit code N" status into
// the "Exit code: N" form the shared failure classifier recognizes, so a search
// with no match is still told apart from a real failure.
func claudeShellStatusText(text string) string {
	rest, ok := strings.CutPrefix(text, "Exit code ")
	if !ok || rest == "" || rest[0] < '0' || rest[0] > '9' {
		return text
	}
	return "Exit code: " + rest
}

// claudeUserDeclined recognizes a tool call the user or a permission rule
// declined. That is a workflow decision, not a tool failure.
func claudeUserDeclined(text string) bool {
	for _, prefix := range []string{"The user doesn't want to proceed", "Permission for this "} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// claudeToolResultText flattens a tool result for transient classification and
// size. It is never stored.
func claudeToolResultText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, part := range parts {
		if part.Type == "text" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}
