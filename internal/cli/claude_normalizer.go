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
// counted once per message ID. Thinking tokens are part of output tokens, as
// reasoning tokens are in Codex. A root transcript's cost-state records carry
// the process's cumulative provider cost and API and tool time, subagents
// included; they become provider_cost events. The normalizer reads
// only structure: tool names, tool results' size and status, usage, cost
// totals, and timestamps. Assistant text and thinking are never decoded.
// User strings and queued attachments are decoded only when they open with
// the harness's task-notification tag; only task ID and status are read.
//
// Tool names are mapped onto the provider-neutral vocabulary the shared
// analysis already understands: shell commands are "exec_command", file edits
// are "apply_patch", and subagent launch and messaging are "spawn_agent" and
// "send_message". MCP tools become "mcp:<digest>" (see normalizedToolName).
// Other tools keep their (validated) names. Background tasks
// map onto continuations (claude_background_tasks.go) and pull-request creation
// onto delivery events (claude_delivery.go).

var claudeToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

var claudePreToolHookPattern = regexp.MustCompile(`^PreToolUse:[A-Za-z0-9_.:-]+ hook error`)

type claudeRecord struct {
	Type          string          `json:"type"`
	Subtype       string          `json:"subtype"`
	Timestamp     string          `json:"timestamp"`
	SessionID     string          `json:"sessionId"`
	AgentID       string          `json:"agentId"`
	CWD           string          `json:"cwd"`
	Effort        string          `json:"effort"`
	PerTurnEffort string          `json:"perTurnEffort"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	// Attachment is read only for a queued task notification.
	Attachment struct {
		Type   string          `json:"type"`
		Prompt json.RawMessage `json:"prompt"`
	} `json:"attachment"`
	CompactMetadata struct {
		Trigger   string `json:"trigger"`
		PreTokens int64  `json:"preTokens"`
	} `json:"compactMetadata"`
	Message struct {
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
	OutputTokensDetails struct {
		ThinkingTokens int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

type claudeCostState struct {
	TotalCostUSD      float64 `json:"totalCostUSD"`
	TotalAPIDuration  int64   `json:"totalAPIDuration"`
	TotalToolDuration int64   `json:"totalToolDuration"`
}

// claudeKnownRecordTypes are the record types Claude Code writes: those the
// adapter reads and those it deliberately ignores. Most were observed in real
// transcripts; summary and progress are written by other builds. Any other
// type is counted as format drift.
var claudeKnownRecordTypes = map[string]bool{
	"progress": true, "summary": true,
	"agent-name": true, "api-request": true, "api-request-blob": true,
	"api-request-shape": true, "artifact-autoreact-ledger": true,
	"artifact-comment-monitor": true, "assistant": true, "atis-latch": true,
	"attachment": true, "bridge-session": true, "cost-state": true,
	"custom-title": true, "file-history-delta": true,
	"file-history-snapshot": true, "fork-context-ref": true, "frame-link": true,
	"isolation-latch": true, "last-prompt": true, "mode": true, "pr-link": true,
	"queue-operation": true, "relocated": true, "system": true,
	"tools-used": true, "user": true, "worktree-state": true,
}

// Message content-block types: those observed in transcripts plus the
// Messages API's server-tool, redacted-thinking, and document blocks. Text,
// thinking, and documents are recognized but never decoded.
var (
	claudeKnownAssistantBlocks = map[string]bool{
		"redacted_thinking": true, "server_tool_use": true, "text": true,
		"thinking": true, "tool_use": true, "web_fetch_tool_result": true,
		"web_search_tool_result": true,
	}
	claudeKnownUserBlocks = map[string]bool{"document": true, "image": true, "text": true, "tool_result": true}
)

type claudeContentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   json.RawMessage `json:"is_error"`
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
	TaskOutput         bool
	OriginalToolName   string
	PRCreate           bool
	OccurredAt         time.Time
	ToolRound          int
	SelectorDigests    []string
	CommandCandidates  []ownedCommandInvocation
	WorkingDirectories []string
}

func claudeRecordLineNeeded(line []byte) bool {
	return bytes.Contains(line, []byte(`"type":"assistant"`)) ||
		bytes.Contains(line, []byte(`"type":"user"`)) ||
		bytes.Contains(line, []byte(`"compact_boundary"`)) ||
		bytes.Contains(line, []byte(`"type":"attachment"`)) && bytes.Contains(line, []byte(`task-notification`))
}

// claudeSkippedRecordType reads the type of a record the adapter does not
// otherwise decode. Most such records lead with their type; large attachment
// records are recognized by their type key without a full decode.
func claudeSkippedRecordType(line []byte) (string, bool) {
	if rest, ok := bytes.CutPrefix(line, []byte(`{"type":"`)); ok {
		if end := bytes.IndexByte(rest, '"'); end >= 0 {
			return string(rest[:end]), true
		}
	}
	if bytes.Contains(line, []byte(`"type":"attachment"`)) {
		return "attachment", true
	}
	var head struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &head) != nil {
		return "", false
	}
	return head.Type, true
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
	contextByMessage := map[string]int{}
	completed := map[string]bool{}
	seenMessages := map[string]bool{}
	models := map[string]int{}
	efforts := map[string]int{}
	var total claudeUsage
	var cost normalizedProviderCost
	var lastTimestamp time.Time
	toolRound, sequence := 0, 0
	background := newClaudeBackgroundTasks()
	emit := func(event normalizedSessionEvent) {
		sequence++
		event.Sequence = sequence
		session.Events = append(session.Events, event)
	}
	delivery := claudeDeliveryState{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !claudeRecordLineNeeded(line) {
			recordType, ok := claudeSkippedRecordType(line)
			if ok && !claudeKnownRecordTypes[recordType] {
				session.UnrecognizedRecords++
			}
			if recordType != "cost-state" || lastTimestamp.IsZero() {
				continue
			}
			var state claudeCostState
			if json.Unmarshal(line, &state) != nil {
				continue
			}
			next := normalizedProviderCost{
				CostUSD:        state.TotalCostUSD,
				APIDurationMs:  state.TotalAPIDuration,
				ToolDurationMs: state.TotalToolDuration,
			}
			if next != cost {
				cost = next
				sequence++
				session.Events = append(session.Events, normalizedSessionEvent{
					Sequence: sequence, OccurredAt: lastTimestamp, Kind: sessionEventProviderCost, Cost: cost,
				})
			}
			continue
		}
		var record claudeRecord
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		if !claudeKnownRecordTypes[record.Type] {
			session.UnrecognizedRecords++
		}
		timestamp, err := time.Parse(time.RFC3339Nano, record.Timestamp)
		if err != nil {
			continue
		}
		lastTimestamp = timestamp
		applyClaudeRecordIdentity(&session, record)
		switch {
		case record.Type == "system" && record.Subtype == "compact_boundary":
			sequence++
			session.Events = append(session.Events, normalizedSessionEvent{
				Sequence: sequence, OccurredAt: timestamp, Kind: sessionEventCompaction,
				ContextTokens:     max(int64(0), record.CompactMetadata.PreTokens),
				CompactionTrigger: normalizedCompactionTrigger(record.CompactMetadata.Trigger),
			})
		case record.Type == "assistant":
			messageID := record.Message.ID
			if model := record.Message.Model; model != "" && model != "<synthetic>" && !seenMessages[messageID] {
				models[model]++
				if effort := claudeRecordEffort(record); effort != "" {
					efforts[normalizedProviderLabel(effort)]++
				}
			}
			seenMessages[messageID] = true
			var blocks []claudeContentBlock
			_ = json.Unmarshal(record.Message.Content, &blocks)
			for _, block := range blocks {
				if !claudeKnownAssistantBlocks[block.Type] {
					session.UnrecognizedContentBlocks++
				}
				if block.Type != "tool_use" {
					continue
				}
				toolRound++
				call, event := claudeToolCallEvent(block, record.CWD, timestamp, toolRound)
				background.rewriteCall(block, &call, &event)
				sequence++
				event.Sequence = sequence
				session.Events = append(session.Events, event)
				if block.ID != "" {
					calls[block.ID] = call
				}
			}
			if usage := record.Message.Usage; messageID != "" && claudeCountedUsage(usage) != (claudeUsage{}) {
				previous := usageByMessage[messageID]
				usageByMessage[messageID] = usage
				// A thinking-only change rides along with the next counted
				// change, so it never adds a token event of its own.
				total.OutputTokensDetails.ThinkingTokens += usage.OutputTokensDetails.ThinkingTokens - previous.OutputTokensDetails.ThinkingTokens
				if claudeCountedUsage(usage) != claudeCountedUsage(previous) {
					total.InputTokens += usage.InputTokens - previous.InputTokens
					total.CacheCreationTokens += usage.CacheCreationTokens - previous.CacheCreationTokens
					total.CacheReadTokens += usage.CacheReadTokens - previous.CacheReadTokens
					total.OutputTokens += usage.OutputTokens - previous.OutputTokens
					sequence++
					contextTokens := int64(0)
					input := usage.InputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
					if input > 0 && record.Message.Model != "<synthetic>" {
						if index, ok := contextByMessage[messageID]; ok {
							// Streaming corrections update the original observation;
							// output-only updates must not create more requests.
							session.Events[index].ContextTokens = input
						} else {
							contextByMessage[messageID] = len(session.Events)
							contextTokens = input
						}
					}
					session.Events = append(session.Events, normalizedSessionEvent{
						Sequence: sequence, OccurredAt: timestamp, Kind: sessionEventToken,
						Tokens: claudeTokenUsage(total), ContextTokens: contextTokens,
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
		case record.Type == "attachment" && record.Attachment.Type == "queued_command":
			if event, ok := background.notificationEvent(record.Attachment.Prompt, timestamp); ok {
				emit(event)
			}
		case record.Type == "user":
			if event, ok := background.notificationEvent(record.Message.Content, timestamp); ok {
				emit(event)
				continue
			}
			var blocks []claudeContentBlock
			if json.Unmarshal(record.Message.Content, &blocks) != nil {
				continue // a plain prompt: never decoded or retained
			}
			for _, block := range blocks {
				if !claudeKnownUserBlocks[block.Type] {
					session.UnrecognizedContentBlocks++
				}
				if block.Type != "tool_result" {
					continue
				}
				sequence++
				call := calls[block.ToolUseID]
				event := claudeToolOutputEvent(block, call, timestamp, sequence)
				background.observeOutput(block, record.ToolUseResult, call, &event)
				if event.Failed || event.Refusal != "" || event.OperationContinues {
					event.GuidanceReadSucceeded = false
				}
				session.Events = append(session.Events, event)
				if deliveryEvent, ok := delivery.observeOutput(block, call, event); ok {
					emit(deliveryEvent)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return normalizedSession{}, err
	}
	session.Model = claudeDominantLabel(models)
	session.ReasoningEffort = claudeDominantLabel(efforts)
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

// claudeCountedUsage is the part of a message's usage whose change makes a
// token event: input, cache, and output counts, without the thinking detail.
func claudeCountedUsage(usage claudeUsage) claudeUsage {
	usage.OutputTokensDetails.ThinkingTokens = 0
	return usage
}

// claudeRecordEffort is the effort the turn ran at: perTurnEffort, or the
// session effort when a record carries no per-turn value.
func claudeRecordEffort(record claudeRecord) string {
	if record.PerTurnEffort != "" {
		return record.PerTurnEffort
	}
	return record.Effort
}

func claudeTokenUsage(total claudeUsage) normalizedTokenUsage {
	usage := normalizedTokenUsage{
		InputTokens:         total.InputTokens + total.CacheCreationTokens + total.CacheReadTokens,
		CachedInputTokens:   total.CacheReadTokens,
		UncachedInputTokens: total.InputTokens + total.CacheCreationTokens,
		OutputTokens:        total.OutputTokens,
		ReasoningTokens:     min(total.OutputTokensDetails.ThinkingTokens, total.OutputTokens),
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	return usage
}

// claudeDominantLabel returns the label counted on the most API messages, the
// alphabetically first on a tie, or "" when it is neither a safe provider
// label nor the "(unknown)" placeholder for an unsafe one.
func claudeDominantLabel(counts map[string]int) string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	best := ""
	for _, name := range names {
		if best == "" || counts[name] > counts[best] {
			best = name
		}
	}
	if best == "" || (best != "(unknown)" && !providerLabelPattern.MatchString(best)) {
		return ""
	}
	return best
}

func claudeToolCallEvent(block claudeContentBlock, cwd string, at time.Time, round int) (claudeCall, normalizedSessionEvent) {
	var input claudeToolInput
	_ = json.Unmarshal(block.Input, &input)
	rawName := block.Name
	if !claudeToolNamePattern.MatchString(rawName) {
		rawName = "(unknown)"
	}
	name := normalizedToolName(rawName)
	call := claudeCall{OccurredAt: at, ToolRound: round}
	event := normalizedSessionEvent{OccurredAt: at, Kind: sessionEventToolCall, ToolRound: round}
	switch name {
	case "Bash":
		call.PRCreate = claudeStandalonePRCreate(input.Command)
		name = "exec_command"
		arguments, _ := json.Marshal(map[string]string{"cmd": input.Command})
		argumentText := string(arguments)
		call.Family, call.Shape, call.First, call.Last = codexShellCommandDetails(name, argumentText, "")
		event.ReadSearch = codexShellCommandReadsOrSearches(name, argumentText, "")
		call.SelectorDigests = codexSelectorDigests(name, argumentText, "")
		call.CommandCandidates = codexCommandInvocations(name, argumentText, "")
		if strings.TrimSpace(cwd) != "" {
			call.WorkingDirectories = appendWorkingDirectory(nil, cwd)
		}
		event.TargetCandidates = codexReadTargetCandidates(name, argumentText, "")
		event.GuidanceArtifacts = codexGuidanceArtifacts(event.TargetCandidates, call.WorkingDirectories...)
		if len(event.GuidanceArtifacts) > 0 && guidanceReadStatusUnconfirmed(name, argumentText, "") {
			for i := range event.GuidanceArtifacts {
				event.GuidanceArtifacts[i].OutcomeUnconfirmed = true
			}
		}
		event.InlineBytes = codexInlineOrchestrationBytes(name, argumentText, "")
		event.InlineFileEdit = event.InlineBytes > 0 && codexInlineFileEdit(name, argumentText, "")
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
		call.SelectorDigests = []string{ownershipSelectorDigest("tool", rawName)}
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
	refusal := ""
	errorStatus := bytes.TrimSpace(block.IsError)
	isError := bytes.Equal(errorStatus, []byte("true"))
	// The protocol's successful example omits optional is_error; explicit
	// null is not a Boolean status. See Anthropic's handle-tool-calls docs.
	knownSuccess := len(errorStatus) == 0 || bytes.Equal(errorStatus, []byte("false"))
	if isError {
		refusal = claudeToolRefusal(text)
	}
	failed := isError && refusal == ""
	content := bytes.TrimSpace(block.Content)
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
		GuidanceReadSucceeded:         knownSuccess && len(content) > 0 && (content[0] == '"' || content[0] == '['),
		Refusal:                       refusal,
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

// claudeToolRefusal maps the fixed result text Claude Code writes when a tool
// call never ran onto a provider-neutral refusal label (see tool_refusals.go):
//   - "The user doesn't want to proceed with this tool use..." and "The user
//     doesn't want to take this action right now..." when the user declines;
//   - "Permission for this action|command was denied by ..." (auto mode or a
//     safety check) and "Permission to use <Tool> ... has been denied" (a
//     permission rule);
//   - "PreToolUse:<Tool> hook error: ..." when a pre-tool hook blocks the call.
//
// Only the label is kept; the text is never stored.
func claudeToolRefusal(text string) string {
	switch {
	case strings.HasPrefix(text, "The user doesn't want to "):
		return toolRefusalDeclinedByUser
	case strings.HasPrefix(text, "Permission for this "),
		strings.HasPrefix(text, "Permission to use ") && strings.Contains(text, " has been denied"):
		return toolRefusalDeniedByPermission
	case claudePreToolHookPattern.MatchString(text):
		return toolRefusalBlockedByHook
	}
	return ""
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
