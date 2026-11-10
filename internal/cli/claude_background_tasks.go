package cli

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// Claude Code runs a shell command in the background when the agent asks for
// it (run_in_background) or when a foreground command outlives its timeout.
// The result then reports a background task ID instead of the command's
// output. The agent learns the outcome by polling the task's output file, by
// TaskOutput, or from the harness's <task-notification> message when the task
// ends; TaskStop ends it early.
//
// This maps onto the shared continuation vocabulary: the backgrounded command
// yields (OperationContinues), a poll is a "wait" continuation that inherits
// the command's attribution, stopping a shell task is a "write_stdin"
// interrupt, and the task's end resolves the yielded operation exactly once.
// Waiting on or stopping a subagent is "wait_agent" or "interrupt_agent".
// Task IDs and tool-use IDs are matched transiently and never stored.

var claudeBackgroundStatusPattern = regexp.MustCompile(`^Command (?:running in background|was moved to the background|was manually backgrounded|did not complete)[^\n]*?ID: ([A-Za-z0-9_-]{4,64})`)
var claudeNotificationTaskPattern = regexp.MustCompile(`<task-id>([A-Za-z0-9_-]{4,64})</task-id>`)
var claudeNotificationStatusPattern = regexp.MustCompile(`<status>([a-z_]{1,32})</status>`)

// A notification is a JSON string that opens with the tag, written literally
// or with HTML-escaped angle brackets.
var claudeNotificationPrefixes = [][]byte{[]byte(`"<task-notification>`), []byte(`"\u003ctask-notification\u003e`)}

type claudeToolUseResult struct {
	BackgroundTaskID string `json:"backgroundTaskId"`
	AgentID          string `json:"agentId"`
}

type claudeBackgroundTask struct {
	call     claudeCall
	resolved bool
}

type claudeBackgroundTasks struct {
	shells map[string]*claudeBackgroundTask
	agents map[string]bool
	// continuations maps a continuation call's tool-use ID to the shell
	// task it continues.
	continuations map[string]string
}

func newClaudeBackgroundTasks() *claudeBackgroundTasks {
	return &claudeBackgroundTasks{
		shells:        map[string]*claudeBackgroundTask{},
		agents:        map[string]bool{},
		continuations: map[string]string{},
	}
}

type claudeTaskReferenceInput struct {
	Command  string `json:"command"`
	FilePath string `json:"file_path"`
	TaskID   string `json:"task_id"`
}

// rewriteCall turns a tool call that continues, waits on, or stops a
// background task into the shared vocabulary. Other calls are left as they
// are.
func (tasks *claudeBackgroundTasks) rewriteCall(block claudeContentBlock, call *claudeCall, event *normalizedSessionEvent) {
	var input claudeTaskReferenceInput
	_ = json.Unmarshal(block.Input, &input)
	name := ""
	taskID := ""
	switch block.Name {
	case "Bash":
		// A command that also edits, pushes, or runs checks is not a
		// poll merely because it mentions an output file.
		if call.Family != "file reads" {
			return
		}
		taskID = tasks.pendingShellReferencedBy(input.Command)
		name = "wait"
	case "Read":
		taskID = tasks.pendingShellReferencedBy(input.FilePath)
		name = "wait"
	case "TaskOutput", "TaskStop":
		if tasks.agents[input.TaskID] {
			name = "wait_agent"
			if block.Name == "TaskStop" {
				name = "interrupt_agent"
			}
			tasks.rename(call, event, name)
			return
		}
		if task := tasks.shells[input.TaskID]; task != nil && !task.resolved {
			taskID = input.TaskID
		}
		name = "wait"
		if block.Name == "TaskStop" {
			name = "write_stdin"
		}
	}
	if taskID == "" {
		return
	}
	shell := tasks.shells[taskID].call
	continued := claudeCall{
		codexToolCallDescriptor: shell.codexToolCallDescriptor,
		TaskOutput:              block.Name == "TaskOutput",
		OriginalToolName:        call.Name,
		OccurredAt:              call.OccurredAt,
		ToolRound:               call.ToolRound,
		SelectorDigests:         shell.SelectorDigests,
		CommandCandidates:       shell.CommandCandidates,
		WorkingDirectories:      shell.WorkingDirectories,
	}
	continued.First, continued.Last = "", ""
	continued.Name = name
	*call = continued
	*event = normalizedSessionEvent{
		OccurredAt:                    event.OccurredAt,
		Kind:                          sessionEventToolCall,
		ToolName:                      name,
		Family:                        shell.Family,
		Shape:                         shell.Shape,
		ToolRound:                     event.ToolRound,
		SelectorDigests:               shell.SelectorDigests,
		CommandCandidates:             shell.CommandCandidates,
		OperationAttributionAmbiguous: len(shell.CommandCandidates) > 1,
		WorkingDirectories:            shell.WorkingDirectories,
	}
	if block.ID != "" {
		tasks.continuations[block.ID] = taskID
	}
}

func (tasks *claudeBackgroundTasks) rename(call *claudeCall, event *normalizedSessionEvent, name string) {
	call.Name = name
	event.ToolName = name
	call.SelectorDigests = []string{ownershipSelectorDigest("tool", name)}
	event.SelectorDigests = call.SelectorDigests
}

// pendingShellReferencedBy finds an unresolved shell task whose ID appears in
// a command or path, such as a poll of the task's output file.
func (tasks *claudeBackgroundTasks) pendingShellReferencedBy(text string) string {
	if text == "" {
		return ""
	}
	match := ""
	for id, task := range tasks.shells {
		if !task.resolved && strings.Contains(text, id+".output") && (match == "" || id < match) {
			match = id
		}
	}
	return match
}

// observeOutput records tasks a result starts and settles the output of a
// continuation call.
func (tasks *claudeBackgroundTasks) observeOutput(block claudeContentBlock, rawResult json.RawMessage, call claudeCall, event *normalizedSessionEvent) {
	var result claudeToolUseResult
	if len(rawResult) > 0 && rawResult[0] == '{' {
		_ = json.Unmarshal(rawResult, &result)
	}
	if taskID, ok := tasks.continuations[block.ToolUseID]; ok {
		// Retain the lookup so a replayed result stays settled too.
		task := tasks.shells[taskID]
		if task.resolved {
			// A notification or another outstanding continuation already
			// settled this task. Keep this physical result without another
			// terminal result attributed to the background command.
			physical := claudeCall{codexToolCallDescriptor: codexToolCallDescriptor{Name: call.OriginalToolName},
				OccurredAt: call.OccurredAt, ToolRound: call.ToolRound, WorkingDirectories: call.WorkingDirectories}
			*event = claudeToolOutputEvent(block, physical, event.OccurredAt, event.Sequence)
			return
		}
		if event.Refusal != "" || event.Failed {
			event.OperationContinues = !task.resolved
			return
		}
		if call.Name == "write_stdin" {
			task.resolved = true
			event.Failed, event.FailureReason = true, "interrupted process"
			event.FailureContext = codexFailureContextLabel(task.call.codexToolCallDescriptor)
		}
		if call.TaskOutput {
			// Legacy TaskOutput reports a fixed status header before the
			// task's output. Never interpret status-looking output text.
			header, _, _ := strings.Cut(claudeToolResultText(block.Content), "<output>")
			if len(header) <= 512 && strings.HasPrefix(header, "<retrieval_status>success</retrieval_status>") {
				if status := claudeNotificationStatusPattern.FindStringSubmatch(header); status != nil {
					switch status[1] {
					case "completed", "failed", "stopped":
						task.resolved = true
						if status[1] == "failed" || status[1] == "stopped" {
							event.Failed = true
							event.FailureReason = codexToolFailureReasonForDescriptor("", task.call.codexToolCallDescriptor)
							if status[1] == "stopped" {
								event.FailureReason = "interrupted process"
							}
							event.FailureContext = codexFailureContextLabel(task.call.codexToolCallDescriptor)
						}
					}
				}
			}
		}
		event.OperationContinues = !task.resolved
		return
	}
	switch call.Name {
	case "spawn_agent":
		if result.AgentID != "" {
			tasks.agents[result.AgentID] = true
		}
	case "exec_command":
		if event.Failed || event.Refusal != "" {
			return
		}
		taskID := result.BackgroundTaskID
		if taskID == "" {
			text := claudeToolResultText(block.Content)
			if len(text) > 512 {
				text = text[:512]
			}
			if match := claudeBackgroundStatusPattern.FindStringSubmatch(text); match != nil {
				taskID = match[1]
			}
		}
		if taskID == "" {
			return
		}
		tasks.shells[taskID] = &claudeBackgroundTask{call: call}
		event.OperationContinues = true
	}
}

// notificationEvent resolves the shell task a <task-notification> reports as
// ended. The notification is harness text in a user record; only its task ID
// and status are read, and only when the record is such a notification, so a
// prompt is never decoded.
func (tasks *claudeBackgroundTasks) notificationEvent(content json.RawMessage, at time.Time) (normalizedSessionEvent, bool) {
	content = bytes.TrimSpace(content)
	if !bytes.HasPrefix(content, claudeNotificationPrefixes[0]) && !bytes.HasPrefix(content, claudeNotificationPrefixes[1]) {
		return normalizedSessionEvent{}, false
	}
	var text string
	if json.Unmarshal(content, &text) != nil {
		return normalizedSessionEvent{}, false
	}
	if len(text) > 4096 {
		text = text[:4096]
	}
	idMatch := claudeNotificationTaskPattern.FindStringSubmatch(text)
	statusMatch := claudeNotificationStatusPattern.FindStringSubmatch(text)
	if idMatch == nil || statusMatch == nil {
		return normalizedSessionEvent{}, false
	}
	task := tasks.shells[idMatch[1]]
	if task == nil || task.resolved {
		return normalizedSessionEvent{}, false
	}
	switch statusMatch[1] {
	case "completed", "failed", "stopped":
	default:
		return normalizedSessionEvent{}, false
	}
	task.resolved = true
	shell := task.call
	event := normalizedSessionEvent{
		OccurredAt: at,
		Kind:       sessionEventToolOutput,
		ToolName:   "wait",
		Family:     shell.Family,
		Shape:      shell.Shape,
		ToolRound:  shell.ToolRound,
		// The agent was not blocked on this result, so no wait is timed.
		CallOccurredAt:                at,
		SelectorDigests:               shell.SelectorDigests,
		CommandCandidates:             shell.CommandCandidates,
		OperationAttributionAmbiguous: len(shell.CommandCandidates) > 1,
		WorkingDirectories:            shell.WorkingDirectories,
	}
	if statusMatch[1] == "failed" || statusMatch[1] == "stopped" {
		event.Failed = true
		event.FailureReason = codexToolFailureReasonForDescriptor("", shell.codexToolCallDescriptor)
		if statusMatch[1] == "stopped" {
			event.FailureReason = "interrupted process"
		}
		event.FailureContext = codexFailureContextLabel(shell.codexToolCallDescriptor)
	}
	return event, true
}
