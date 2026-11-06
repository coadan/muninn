package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fixtures are synthetic and mirror only the structural shape of Claude Code
// transcripts: one record per content block, usage repeated per message.

const (
	claudeFixtureSession = "11111111-aaaa-bbbb-cccc-000000000001"
	claudeFixturePrompt  = "PROMPT-SECRET please refactor the billing module"
	claudeFixtureCommand = "grep -rn SECRET_TOKEN_VALUE /workspace/app"
)

func claudeFixtureTime(second int) string {
	return time.Date(2026, 7, 29, 8, 0, second, 0, time.UTC).Format(time.RFC3339Nano)
}

func claudeFixtureRecord(kind string, second int, extra map[string]any) map[string]any {
	record := map[string]any{
		"type": kind, "timestamp": claudeFixtureTime(second),
		"sessionId": claudeFixtureSession, "cwd": "/workspace/app",
	}
	for key, value := range extra {
		record[key] = value
	}
	return record
}

func claudeFixtureAssistant(second int, id, stop string, usage map[string]any, blocks ...map[string]any) map[string]any {
	return claudeFixtureRecord("assistant", second, map[string]any{
		"effort": "high",
		"message": map[string]any{
			"id": id, "model": "claude-test-model", "stop_reason": stop,
			"usage": usage, "content": blocks,
		},
	})
}

func claudeFixtureToolUse(id, name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

func claudeFixtureToolResult(second int, id string, isError bool, content any) map[string]any {
	return claudeFixtureRecord("user", second, map[string]any{
		"message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": isError, "content": content},
		}},
	})
}

func writeClaudeSessionFixture(t *testing.T, dir, name string, records []any) string {
	t.Helper()
	path := filepath.Join(dir, "-workspace-app", name+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, record := range records {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(raw))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func claudeFixtureConversation(root string) []any {
	usageA := map[string]any{"input_tokens": 2, "cache_creation_input_tokens": 100, "cache_read_input_tokens": 1000, "output_tokens": 50}
	usageB := map[string]any{"input_tokens": 3, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 1100, "output_tokens": 70}
	return []any{
		claudeFixtureRecord("user", 0, map[string]any{"message": map[string]any{"role": "user", "content": claudeFixturePrompt}}),
		// Two content blocks of one API message repeat the same usage.
		claudeFixtureAssistant(1, "msg_1", "tool_use", usageA, claudeFixtureToolUse("t1", "Bash", map[string]any{"command": claudeFixtureCommand})),
		claudeFixtureAssistant(1, "msg_1", "tool_use", usageA, claudeFixtureToolUse("t2", "Edit", map[string]any{"file_path": filepath.Join(root, "main.go"), "old_string": "SECRET_OLD", "new_string": "SECRET_NEW"})),
		claudeFixtureToolResult(2, "t1", true, "Exit code 1\nbash: nosuchtool: command not found"),
		claudeFixtureToolResult(3, "t2", false, "ok"),
		claudeFixtureAssistant(4, "msg_2", "end_turn", usageB, map[string]any{"type": "text", "text": "ASSISTANT-SECRET done"}),
		claudeFixtureRecord("system", 5, map[string]any{"subtype": "compact_boundary"}),
	}
}

func TestParseClaudeSessionNormalizesEventsAndCountsUsageOncePerMessage(t *testing.T) {
	path := writeClaudeSessionFixture(t, t.TempDir(), "main", claudeFixtureConversation("/workspace/app"))
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.CWD != "/workspace/app" || session.Model != "claude-test-model" || session.ReasoningEffort != "high" {
		t.Fatalf("identity mismatch: %#v", session)
	}
	var kinds []string
	var lastTokens normalizedTokenUsage
	for _, event := range session.Events {
		kinds = append(kinds, event.Kind+":"+event.ToolName)
		if event.Kind == sessionEventToken {
			lastTokens = event.Tokens
		}
	}
	want := "tool_call:exec_command token: tool_call:apply_patch tool_output:exec_command tool_output:apply_patch token: complete: compaction:"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("events=\n%s\nwant\n%s", got, want)
	}
	// msg_1 (2+100+1000 in, 50 out) + msg_2 (3+0+1100 in, 70 out); msg_1's
	// repeated block must not be added twice.
	wantTokens := normalizedTokenUsage{
		InputTokens: 2205, CachedInputTokens: 2100, UncachedInputTokens: 105,
		OutputTokens: 120, TotalTokens: 2325,
	}
	if lastTokens != wantTokens {
		t.Fatalf("tokens=%#v want %#v", lastTokens, wantTokens)
	}
}

func TestParseClaudeSessionMapsToolsAndFailures(t *testing.T) {
	path := writeClaudeSessionFixture(t, t.TempDir(), "tools", claudeFixtureConversation("/workspace/app"))
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	byKind := func(kind, tool string) normalizedSessionEvent {
		for _, event := range session.Events {
			if event.Kind == kind && event.ToolName == tool {
				return event
			}
		}
		t.Fatalf("no %s event for %s", kind, tool)
		return normalizedSessionEvent{}
	}
	shell := byKind(sessionEventToolCall, "exec_command")
	if shell.Family != "search" || shell.FirstFamily != "search" || len(shell.WorkingDirectories) != 1 {
		t.Fatalf("shell call=%#v", shell)
	}
	edit := byKind(sessionEventToolCall, "apply_patch")
	if len(edit.TargetCandidates) != 1 || edit.TargetCandidates[0] != filepath.Join("/workspace/app", "main.go") {
		t.Fatalf("edit targets=%#v", edit.TargetCandidates)
	}
	failed := byKind(sessionEventToolOutput, "exec_command")
	if !failed.Failed || failed.FailureReason != "missing executable" || failed.OutputBytes == 0 {
		t.Fatalf("shell output=%#v", failed)
	}
	if ok := byKind(sessionEventToolOutput, "apply_patch"); ok.Failed {
		t.Fatalf("successful edit marked failed: %#v", ok)
	}
}

func TestParseClaudeSessionDeclinedAndPersistedResults(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	path := writeClaudeSessionFixture(t, t.TempDir(), "declined", []any{
		claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("t1", "Bash", map[string]any{"command": "make deploy"})),
		claudeFixtureToolResult(2, "t1", true, "The user doesn't want to proceed with this tool use."),
		claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("t2", "Read", map[string]any{"file_path": "/workspace/app/big.log"})),
		claudeFixtureToolResult(4, "t2", false, []any{map[string]any{"type": "text", "text": "<persisted-output>Output too large</persisted-output>"}}),
		claudeFixtureAssistant(5, "m3", "tool_use", usage, claudeFixtureToolUse("t3", "bad name with spaces", nil)),
	})
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	var outputs []normalizedSessionEvent
	for _, event := range session.Events {
		if event.Kind == sessionEventToolOutput {
			outputs = append(outputs, event)
		}
		if event.Kind == sessionEventToolCall && event.ToolRound == 3 && event.ToolName != "(unknown)" {
			t.Fatalf("unvalidated tool name retained: %q", event.ToolName)
		}
	}
	if len(outputs) != 2 || outputs[0].Failed {
		t.Fatalf("a declined call must not count as a tool failure: %#v", outputs)
	}
	if !outputs[1].Truncated {
		t.Fatalf("persisted output must count as truncated: %#v", outputs[1])
	}
}

func TestParseClaudeSubagentUsesDigestedLineage(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	sub := claudeFixtureAssistant(1, "m1", "end_turn", usage, map[string]any{"type": "text", "text": "x"})
	sub["agentId"] = "agent-secret-id"
	subPath := writeClaudeSessionFixture(t, t.TempDir(), "agent-sub", []any{sub})
	parentPath := writeClaudeSessionFixture(t, t.TempDir(), "main", []any{
		claudeFixtureAssistant(1, "m1", "end_turn", usage, map[string]any{"type": "text", "text": "x"}),
	})
	child, err := parseClaudeNormalizedSession(subPath)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := parseClaudeNormalizedSession(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	if parent.AgentKind != "root" || child.AgentKind != "subagent" || child.ParentLineageKey != parent.LineageKey || child.LineageKey == parent.LineageKey {
		t.Fatalf("lineage child=%#v parent=%#v", child, parent)
	}
	for _, key := range []string{child.LineageKey, child.ParentLineageKey, parent.LineageKey} {
		if strings.Contains(key, claudeFixtureSession) || strings.Contains(key, "agent-secret-id") || len(key) != 64 {
			t.Fatalf("lineage key is not an irreversible digest: %q", key)
		}
	}
}

func TestClaudeSessionAnalysisReportOmitsSessionContent(t *testing.T) {
	root := t.TempDir()
	sessionsDir := t.TempDir()
	conversation := claudeFixtureConversation(root)
	for _, record := range conversation {
		record.(map[string]any)["cwd"] = root
	}
	writeClaudeSessionFixture(t, sessionsDir, "main", conversation)
	provider, err := resolveSessionSource("claude")
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := provider.Discover(sessionsDir, false)
	if err != nil || len(discovery.Files) != 1 {
		t.Fatalf("discovery=%#v err=%v", discovery, err)
	}
	generatedAt := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	report, err := analyzeProviderSessions(provider, discovery, root, generatedAt.Add(-24*time.Hour), generatedAt, "", ownershipCatalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	summary := report.Summary
	if report.Provider != "claude" || summary.Sessions != 1 || summary.CompletedSessions != 1 ||
		summary.ToolCalls != 2 || summary.FailedToolCalls != 1 || summary.Compactions != 1 ||
		summary.Tokens.TotalTokens != 2325 {
		t.Fatalf("summary mismatch: %#v", summary)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"PROMPT-SECRET", "ASSISTANT-SECRET", "SECRET_TOKEN_VALUE", "SECRET_OLD", "SECRET_NEW",
		claudeFixtureSession, root, "nosuchtool", sessionsDir,
	} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("report exposes %q: %s", forbidden, raw)
		}
	}
}

func TestDiscoverClaudeSessionsIncludesSubagentsAndIgnoresOtherFiles(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "-workspace-app", "s1.jsonl")
	sub := filepath.Join(root, "-workspace-app", "s1", "subagents", "agent-a.jsonl")
	for _, path := range []string{main, sub, filepath.Join(root, "-workspace-app", "s1", "tool-results", "out.txt")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	discovery, err := discoverClaudeSessionSource(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Files) != 2 || discovery.Files[0] != main || discovery.Files[1] != sub {
		t.Fatalf("discovery=%#v", discovery)
	}
	if _, err := discoverClaudeSessionSource(filepath.Join(root, "missing"), false); err == nil {
		t.Fatal("missing sessions directory must be an error")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Dir(root))
	if _, err := resolveClaudeSessionsDir(""); err == nil {
		t.Fatal("config dir without projects/ must be an error")
	}
	configDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configDir, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	if got, err := resolveClaudeSessionsDir(""); err != nil || got != filepath.Join(configDir, "projects") {
		t.Fatalf("default dir=%q err=%v", got, err)
	}
}

func TestParseClaudeSessionToleratesMalformedRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weird.jsonl")
	lines := []string{
		`not json`,
		`{"type":"assistant"`,
		`{"type":"assistant","timestamp":"bad"}`,
		`{"type":"user","timestamp":"2026-07-29T08:00:00Z","cwd":"/x","message":{"content":"a plain prompt"}}`,
		`{"type":"user","timestamp":"2026-07-29T08:00:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"unknown","content":null,"is_error":true}]}}`,
		`{"type":"assistant","timestamp":"2026-07-29T08:00:02Z","message":{"id":"m","content":[{"type":"tool_use","id":"a","name":"Bash","input":null},{"type":"tool_use","id":"b","name":"Skill","input":{"skill":"../../etc"}}],"usage":{"output_tokens":3}}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatalf("malformed records must be skipped, not fatal: %v", err)
	}
	for _, event := range session.Events {
		if len(event.GuidanceArtifacts) != 0 {
			t.Fatalf("invalid skill identity retained: %#v", event.GuidanceArtifacts)
		}
	}
	if _, err := parseClaudeNormalizedSession(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Fatal("an unreadable file must be reported")
	}
}

func TestClaudeSessionIndexOmitsSessionContent(t *testing.T) {
	root := t.TempDir()
	sessionsDir := t.TempDir()
	conversation := claudeFixtureConversation(root)
	for _, record := range conversation {
		record.(map[string]any)["cwd"] = root
	}
	// A subagent transcript exercises the digested lineage keys.
	subagent := claudeFixtureAssistant(1, "msg_sub", "end_turn", map[string]any{"input_tokens": 1, "output_tokens": 1},
		claudeFixtureToolUse("s1", "mcp__internal-server__lookup", map[string]any{"query": "SECRET_QUERY"}))
	subagent["agentId"], subagent["cwd"] = "agent-secret-id", root
	writeClaudeSessionFixture(t, sessionsDir, "main", conversation)
	writeClaudeSessionFixture(t, sessionsDir, "agent-sub", []any{subagent})

	provider, err := resolveSessionSource("claude")
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := provider.Discover(sessionsDir, false)
	if err != nil || len(discovery.Files) != 2 {
		t.Fatalf("discovery=%#v err=%v", discovery, err)
	}
	databasePath := filepath.Join(t.TempDir(), "sessions.sqlite")
	store, err := openSessionStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := store.refresh(t.Context(), provider.Name(), discovery, root, provider, ownershipCatalog{}, false)
	if err != nil || stats.FilesIndexed != 2 {
		t.Fatalf("stats=%#v err=%v", stats, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// SourcePath and the working directory are local index metadata. Nothing
	// from the conversation or any provider identifier may be stored.
	for _, name := range []string{"", "-wal"} {
		raw, err := os.ReadFile(databasePath + name)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			"PROMPT-SECRET", "ASSISTANT-SECRET", "SECRET_TOKEN_VALUE", "SECRET_OLD", "SECRET_NEW",
			"SECRET_QUERY", claudeFixtureSession, "agent-secret-id", "nosuchtool",
		} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("index%s stores %q", name, forbidden)
			}
		}
	}
}

func TestParseClaudeSessionMapsSubagentCoordinationTools(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	path := writeClaudeSessionFixture(t, t.TempDir(), "delegation", []any{
		claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("t1", "Agent", map[string]any{"prompt": "PROMPT-SECRET"})),
		claudeFixtureAssistant(2, "m2", "tool_use", usage, claudeFixtureToolUse("t2", "Task", map[string]any{"prompt": "x"})),
		claudeFixtureAssistant(3, "m3", "tool_use", usage, claudeFixtureToolUse("t3", "SendMessage", map[string]any{"message": "x"})),
	})
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, event := range session.Events {
		if event.Kind == sessionEventToolCall {
			if !delegationOperation(event) {
				t.Fatalf("%q is not recognized as delegation", event.ToolName)
			}
			names = append(names, event.ToolName)
		}
	}
	if strings.Join(names, " ") != "spawn_agent spawn_agent send_message" {
		t.Fatalf("names=%v", names)
	}
}

func TestParseClaudeSessionUsesLastUsageWhenAMessageUpdates(t *testing.T) {
	early := map[string]any{"input_tokens": 5, "cache_read_input_tokens": 10, "output_tokens": 1}
	final := map[string]any{"input_tokens": 5, "cache_read_input_tokens": 10, "output_tokens": 40}
	next := map[string]any{"input_tokens": 2, "cache_read_input_tokens": 20, "output_tokens": 7}
	path := writeClaudeSessionFixture(t, t.TempDir(), "streaming", []any{
		claudeFixtureAssistant(1, "m1", "", early, map[string]any{"type": "text", "text": "x"}),
		claudeFixtureAssistant(2, "m1", "end_turn", final, map[string]any{"type": "text", "text": "x"}),
		claudeFixtureAssistant(3, "m2", "end_turn", next, map[string]any{"type": "text", "text": "x"}),
	})
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	var totals []int64
	for _, event := range session.Events {
		if event.Kind == sessionEventToken {
			totals = append(totals, event.Tokens.OutputTokens)
		}
	}
	// Cumulative output follows the latest usage per message: 1, 40 (not 41), 47.
	if len(totals) != 3 || totals[0] != 1 || totals[1] != 40 || totals[2] != 47 {
		t.Fatalf("cumulative output tokens=%v", totals)
	}
}

func TestParseClaudeSessionClassifiesSearchMissAndNativeSearchTransitions(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	path := writeClaudeSessionFixture(t, t.TempDir(), "search", []any{
		claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("t1", "Grep", map[string]any{"pattern": "x"})),
		claudeFixtureAssistant(2, "m2", "tool_use", usage, claudeFixtureToolUse("t2", "Read", map[string]any{"file_path": "/workspace/app/main.go"})),
		claudeFixtureAssistant(3, "m3", "tool_use", usage, claudeFixtureToolUse("t3", "Bash", map[string]any{"command": "rg nothing-matches ."})),
		claudeFixtureToolResult(4, "t3", true, "Exit code 1\n"),
	})
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	families := map[string]string{}
	for _, event := range session.Events {
		switch {
		case event.Kind == sessionEventToolCall && (event.ToolName == "Grep" || event.ToolName == "Read"):
			families[event.ToolName] = event.FirstFamily + "/" + event.LastFamily
			if !discoveryOperation(event, nil) {
				t.Fatalf("%s must count as discovery: %#v", event.ToolName, event)
			}
		case event.Kind == sessionEventToolOutput:
			if !event.Failed || event.FailureReason != "search no match" {
				t.Fatalf("a search with no match must keep its own reason: %#v", event)
			}
		}
	}
	if families["Grep"] != "search/search" || families["Read"] != "file reads/file reads" {
		t.Fatalf("transition families=%v", families)
	}
}

func TestClaudeToolCallMappingTable(t *testing.T) {
	root := "/workspace/app"
	cases := []struct {
		name     string
		input    map[string]any
		tool     string
		targets  []string
		families string
		skill    string
	}{
		{name: "Write", input: map[string]any{"file_path": root + "/a.go"}, tool: "apply_patch", targets: []string{root + "/a.go"}},
		{name: "MultiEdit", input: map[string]any{"file_path": root + "/b.go"}, tool: "apply_patch", targets: []string{root + "/b.go"}},
		{name: "NotebookEdit", input: map[string]any{"notebook_path": root + "/n.ipynb"}, tool: "apply_patch", targets: []string{root + "/n.ipynb"}},
		{name: "Read", input: map[string]any{"file_path": root + "/r.go"}, tool: "Read", targets: []string{root + "/r.go"}, families: "file reads/file reads"},
		{name: "Glob", input: map[string]any{"pattern": "**/*.go"}, tool: "Glob", families: "file reads/file reads"},
		{name: "Grep", input: map[string]any{"pattern": "x"}, tool: "Grep", families: "search/search"},
		{name: "Skill", input: map[string]any{"skill": "plugin:review-helper"}, tool: "Skill", skill: "plugin:review-helper"},
		{name: "WebFetch", input: map[string]any{"url": "https://example.invalid/SECRET"}, tool: "WebFetch"},
	}
	for _, tc := range cases {
		_, event := claudeToolCallEvent(claudeContentBlock{Type: "tool_use", ID: "x", Name: tc.name, Input: mustJSON(t, tc.input)}, root, time.Time{}, 1)
		if event.ToolName != tc.tool || strings.Join(event.TargetCandidates, ",") != strings.Join(tc.targets, ",") ||
			(event.FirstFamily+"/"+event.LastFamily != tc.families && tc.families != "") {
			t.Fatalf("%s mapped to %#v", tc.name, event)
		}
		skill := ""
		if len(event.GuidanceArtifacts) == 1 && event.GuidanceArtifacts[0].Kind == "skill" {
			skill = event.GuidanceArtifacts[0].ID
		}
		if skill != tc.skill {
			t.Fatalf("%s skill=%q want %q", tc.name, skill, tc.skill)
		}
	}
}

func TestClaudeUserDeclinedPrefixes(t *testing.T) {
	for _, text := range []string{
		"The user doesn't want to proceed with this tool use. The tool use was rejected.",
		"Permission for this command was denied by a rule.",
		"Permission for this action was not granted.",
	} {
		if !claudeUserDeclined(text) {
			t.Fatalf("%q should be treated as declined", text)
		}
	}
	if claudeUserDeclined("Exit code 1\nPermission for this is not a prefix here") {
		t.Fatal("a failing command must not be treated as declined")
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
