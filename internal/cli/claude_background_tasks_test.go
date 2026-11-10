package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Background-task fixtures are synthetic. Task IDs and output paths mirror
// only the shape Claude Code uses.

func claudeFixtureBackgroundResult(second int, toolUseID, taskID string, structured bool) map[string]any {
	record := claudeFixtureToolResult(second, toolUseID, false,
		"Command running in background with ID: "+taskID+". Output is being written to: /tmp/SECRET-tasks/"+taskID+".output")
	if structured {
		record["toolUseResult"] = map[string]any{"stdout": "", "stderr": "", "backgroundTaskId": taskID}
	}
	return record
}

func claudeFixtureNotification(second int, taskID, status string) map[string]any {
	return claudeFixtureRecord("user", second, map[string]any{
		"origin": map[string]any{"kind": "task-notification"},
		"message": map[string]any{"role": "user", "content": "<task-notification>\n<task-id>" + taskID +
			"</task-id>\n<tool-use-id>toolu_SECRET</tool-use-id>\n<status>" + status +
			"</status>\n<summary>NOTIFICATION-SECRET summary</summary>\n</task-notification>"},
	})
}

// claudeFixtureQueuedNotification is the same notification delivered mid-turn
// as a queued_command attachment, the form Claude Code uses most.
func claudeFixtureQueuedNotification(second int, taskID, status string) map[string]any {
	notification := claudeFixtureNotification(second, taskID, status)
	prompt := notification["message"].(map[string]any)["content"]
	return claudeFixtureRecord("attachment", second, map[string]any{
		"attachment": map[string]any{"type": "queued_command", "prompt": prompt, "commandMode": "task-notification"},
	})
}

func claudeEventLabels(session normalizedSession) []string {
	var labels []string
	for _, event := range session.Events {
		if event.Kind != sessionEventToolCall && event.Kind != sessionEventToolOutput {
			continue
		}
		label := event.Kind + ":" + event.ToolName
		if event.Family != "" {
			label += "/" + event.Family
		}
		if event.OperationContinues {
			label += "+continues"
		}
		if event.Failed {
			label += "+failed"
		}
		labels = append(labels, label)
	}
	return labels
}

func TestParseClaudeSessionMapsBackgroundShellPollsAndCompletion(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	path := writeClaudeSessionFixture(t, t.TempDir(), "background", []any{
		claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("t1", "Bash", map[string]any{
			"command": "go test ./...", "run_in_background": true,
		})),
		claudeFixtureBackgroundResult(1, "t1", "bgtask001", true),
		// A poll of the task's output file continues the yielded command.
		claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("t2", "Bash", map[string]any{
			"command": "tail -5 /tmp/SECRET-tasks/bgtask001.output",
		})),
		claudeFixtureToolResult(4, "t2", true, "Exit code 1\nstill running"),
		claudeFixtureAssistant(5, "m3", "tool_use", usage, claudeFixtureToolUse("t3", "Read", map[string]any{
			"file_path": "/tmp/SECRET-tasks/bgtask001.output",
		})),
		claudeFixtureToolResult(6, "t3", false, "ok"),
		// A prompt the user queues mid-turn is never read as a notification.
		claudeFixtureRecord("attachment", 7, map[string]any{"attachment": map[string]any{
			"type": "queued_command", "prompt": "PROMPT-SECRET <task-notification><task-id>bgtask001</task-id><status>completed</status>",
		}}),
		claudeFixtureNotification(40, "bgtask001", "completed"),
		// A repeated notification and a read after completion are ordinary.
		claudeFixtureNotification(41, "bgtask001", "completed"),
		claudeFixtureAssistant(42, "m4", "tool_use", usage, claudeFixtureToolUse("t4", "Bash", map[string]any{
			"command": "cat /tmp/SECRET-tasks/bgtask001.output",
		})),
		claudeFixtureToolResult(43, "t4", false, "ok"),
	})
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tool_call:exec_command/tests",
		"tool_output:exec_command/tests+continues",
		"tool_call:wait/tests",
		"tool_output:wait/tests+continues+failed",
		"tool_call:wait/tests",
		"tool_output:wait/tests+continues",
		"tool_output:wait/tests",
		"tool_call:exec_command/file reads",
		"tool_output:exec_command/file reads",
	}
	if got := claudeEventLabels(session); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events=\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	poll := session.Events[3]
	if poll.FirstFamily != "" || poll.LastFamily != "" || len(poll.TargetCandidates) != 0 {
		t.Fatalf("poll call kept its own command identity: %#v", poll)
	}
	var completion normalizedSessionEvent
	for _, event := range session.Events {
		if event.Kind == sessionEventToolOutput && event.ToolName == "wait" && !event.OperationContinues {
			completion = event
		}
	}
	if completion.CallOccurredAt != completion.OccurredAt || completion.OutputBytes != 0 {
		t.Fatalf("completion must not time a wait or count output: %#v", completion)
	}
}

func TestParseClaudeSessionMapsTaskOutputAndTaskStop(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	agentResult := claudeFixtureToolResult(2, "a1", false, "Async agent launched successfully.")
	agentResult["toolUseResult"] = map[string]any{"agentId": "agent0001", "isAsync": true, "status": "async_launched"}
	path := writeClaudeSessionFixture(t, t.TempDir(), "tasks", []any{
		claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("a1", "Agent", map[string]any{
			"prompt": "PROMPT-SECRET", "run_in_background": true,
		})),
		agentResult,
		claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("a2", "TaskOutput", map[string]any{"task_id": "agent0001"})),
		claudeFixtureToolResult(4, "a2", false, "done"),
		claudeFixtureAssistant(5, "m3", "tool_use", usage, claudeFixtureToolUse("a3", "TaskStop", map[string]any{"task_id": "agent0001"})),
		claudeFixtureToolResult(6, "a3", false, "stopped"),
		// A shell auto-backgrounded after its timeout; the ID is only in text.
		claudeFixtureAssistant(7, "m4", "tool_use", usage, claudeFixtureToolUse("b1", "Bash", map[string]any{"command": "make build"})),
		claudeFixtureToolResult(130, "b1", false, "Command was moved to the background (ID: bgtask002). Output is being written to: /tmp/x/bgtask002.output"),
		claudeFixtureAssistant(131, "m5", "tool_use", usage, claudeFixtureToolUse("b2", "TaskOutput", map[string]any{"task_id": "bgtask002", "block": false})),
		claudeFixtureToolResult(132, "b2", false, "running"),
		claudeFixtureAssistant(133, "m6", "tool_use", usage, claudeFixtureToolUse("b3", "TaskStop", map[string]any{"task_id": "bgtask002"})),
		claudeFixtureToolResult(134, "b3", false, "stopped"),
		// The stop already ended the task; its notification adds nothing.
		claudeFixtureNotification(135, "bgtask002", "stopped"),
		claudeFixtureAssistant(136, "m7", "tool_use", usage, claudeFixtureToolUse("x1", "TaskStop", map[string]any{"task_id": "unknown01"})),
		claudeFixtureToolResult(137, "x1", true, "No task found"),
	})
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tool_call:spawn_agent", "tool_output:spawn_agent",
		"tool_call:wait_agent", "tool_output:wait_agent",
		"tool_call:interrupt_agent", "tool_output:interrupt_agent",
		"tool_call:exec_command/build, lint, or install",
		"tool_output:exec_command/build, lint, or install+continues",
		"tool_call:wait/build, lint, or install",
		"tool_output:wait/build, lint, or install+continues",
		"tool_call:write_stdin/build, lint, or install",
		"tool_output:write_stdin/build, lint, or install+failed",
		"tool_call:TaskStop", "tool_output:TaskStop+failed",
	}
	if got := claudeEventLabels(session); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events=\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, event := range session.Events {
		if event.ToolName == "wait_agent" || event.ToolName == "interrupt_agent" {
			if !delegationOperation(event) {
				t.Fatalf("%s is not delegation", event.ToolName)
			}
		}
	}
}

func TestParseClaudeSessionFailedNotificationFailsTheBackgroundCommand(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	path := writeClaudeSessionFixture(t, t.TempDir(), "failed", []any{
		claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("t1", "Bash", map[string]any{
			"command": "go test ./...", "run_in_background": true,
		})),
		claudeFixtureBackgroundResult(1, "t1", "bgtask003", false),
		claudeFixtureNotification(30, "bgtask003", "failed"),
	})
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	last := session.Events[len(session.Events)-1]
	if last.ToolName != "wait" || !last.Failed || last.OperationContinues ||
		last.FailureReason != "other non-zero exit" || last.FailureContext != "tests" {
		t.Fatalf("failed completion=%#v", last)
	}
}

func TestClaudeTaskOutputCompletionAndFailedStop(t *testing.T) {
	for _, tc := range []struct {
		name, tool, output        string
		isError, resolved, failed bool
	}{
		{"completed", "TaskOutput", "<retrieval_status>success</retrieval_status>\n<status>completed</status>\n<output>SECRET</output>", false, true, false},
		{"failed", "TaskOutput", "<retrieval_status>success</retrieval_status>\n<status>failed</status>\n<output>SECRET</output>", false, true, true},
		{"stopped", "TaskOutput", "<retrieval_status>success</retrieval_status>\n<status>stopped</status>\n<output>SECRET</output>", false, true, true},
		{"still running", "TaskOutput", "<retrieval_status>success</retrieval_status>\n<status>running</status>\n<output><status>completed</status></output>", false, false, false},
		{"stop failed", "TaskStop", "Unable to stop task", true, false, true},
		{"stop refused", "TaskStop", "Permission to use TaskStop has been denied", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
			path := writeClaudeSessionFixture(t, t.TempDir(), "status", []any{
				claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("b1", "Bash", map[string]any{"command": "go test ./...", "run_in_background": true})),
				claudeFixtureBackgroundResult(2, "b1", "bgtask009", true),
				claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("p1", tc.tool, map[string]any{"task_id": "bgtask009"})),
				claudeFixtureToolResult(4, "p1", tc.isError, tc.output),
			})
			session, err := parseClaudeNormalizedSession(path)
			if err != nil {
				t.Fatal(err)
			}
			last := session.Events[len(session.Events)-1]
			if last.OperationContinues == tc.resolved || last.Failed != tc.failed {
				t.Fatalf("completion=%#v, want resolved=%v failed=%v", last, tc.resolved, tc.failed)
			}
			if tc.name == "stop refused" && last.Refusal != "denied-by-permission" {
				t.Fatalf("refusal=%q", last.Refusal)
			}
		})
	}
}

func TestClaudeBackgroundReferenceDoesNotHideOtherWork(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	path := writeClaudeSessionFixture(t, t.TempDir(), "mixed", []any{
		claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("b1", "Bash", map[string]any{"command": "go test ./...", "run_in_background": true})),
		claudeFixtureBackgroundResult(2, "b1", "bgtask010", true),
		claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("p1", "Bash", map[string]any{"command": "cat /tmp/tasks/bgtask010.output && git push"})),
		claudeFixtureToolResult(4, "p1", true, "push failed"),
	})
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	last := session.Events[len(session.Events)-1]
	if last.ToolName != "exec_command" || !last.Failed || last.OperationContinues {
		t.Fatalf("mixed command was hidden as a poll: %#v", last)
	}
}

func TestParseClaudeSessionNeverDecodesPromptsAsNotifications(t *testing.T) {
	tasks := newClaudeBackgroundTasks()
	tasks.shells["bgtask004"] = &claudeBackgroundTask{}
	at := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	for _, content := range []string{
		"please explain <task-notification><task-id>bgtask004</task-id><status>completed</status>",
		" <task-id>bgtask004</task-id><status>completed</status>",
	} {
		raw, _ := json.Marshal(content)
		if _, ok := tasks.notificationEvent(raw, at); ok {
			t.Fatalf("prompt %q was treated as a task notification", content)
		}
	}
	if tasks.shells["bgtask004"].resolved {
		t.Fatal("a prompt resolved a background task")
	}
	// Claude Code writes the tag literally; the fixtures' encoder escapes it.
	literal := []byte(`"<task-notification>\n<task-id>bgtask004</task-id>\n<status>completed</status>\n</task-notification>"`)
	if _, ok := tasks.notificationEvent(literal, at); !ok || !tasks.shells["bgtask004"].resolved {
		t.Fatal("a literal task notification was not recognized")
	}
}

// Background shells polled in short succession across sessions must reach the
// shared rapid-poll metrics, and a task that never ends must be abandoned.
func TestClaudeBackgroundPollingReachesContinuationMetrics(t *testing.T) {
	root := t.TempDir()
	sessionsDir := t.TempDir()
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	for _, name := range []string{"one", "two"} {
		taskID := "bg" + name + "0001"
		records := []any{
			claudeFixtureAssistant(1, "m0", "tool_use", usage, claudeFixtureToolUse("t0", "Bash", map[string]any{
				"command": "go test ./...", "run_in_background": true,
			})),
			claudeFixtureBackgroundResult(1, "t0", taskID, true),
		}
		for poll := 1; poll <= 3; poll++ {
			id := "p" + string(rune('0'+poll))
			second := poll * 10
			records = append(records,
				claudeFixtureAssistant(second, "m"+id, "tool_use", usage, claudeFixtureToolUse(id, "Bash", map[string]any{
					"command": "tail -3 /tmp/tasks/" + taskID + ".output",
				})),
				claudeFixtureToolResult(second+2, id, false, "PASS"),
			)
		}
		if name == "one" {
			records = append(records, claudeFixtureQueuedNotification(50, taskID, "completed"))
		}
		records = append(records, claudeFixtureAssistant(51, "mz", "end_turn", usage, map[string]any{"type": "text", "text": "done"}))
		for _, record := range records {
			record.(map[string]any)["cwd"] = root
			record.(map[string]any)["sessionId"] = "session-" + name
		}
		writeClaudeSessionFixture(t, sessionsDir, name, records)
	}
	provider, err := resolveSessionSource("claude")
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := provider.Discover(sessionsDir, false)
	if err != nil || len(discovery.Files) != 2 {
		t.Fatalf("discovery=%#v err=%v", discovery, err)
	}
	generatedAt := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	report, err := analyzeProviderSessions(provider, discovery, root, generatedAt.Add(-24*time.Hour), generatedAt, "", ownershipCatalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	polls := report.Summary.RapidPolls["wait"]
	if polls.Calls != 6 || polls.Sessions != 2 {
		t.Fatalf("rapid polls=%#v (all %#v)", polls, report.Summary.RapidPolls)
	}
	abandoned := report.Summary.AbandonedContinuations["tests"]
	if abandoned.Count != 1 || abandoned.Sessions != 1 {
		t.Fatalf("abandoned=%#v (all %#v)", abandoned, report.Summary.AbandonedContinuations)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"bgone0001", "bgtwo0001", "NOTIFICATION-SECRET", "toolu_SECRET", "/tmp/tasks"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("report exposes %q", forbidden)
		}
	}
}
