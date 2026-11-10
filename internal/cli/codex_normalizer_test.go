package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexRolloutLineNeededSkipsConversationContent(t *testing.T) {
	for _, line := range []string{
		`{"type":"response_item","payload":{"type":"reasoning","encrypted_content":"large"}}`,
		`{"type":"response_item","payload":{"type":"message","content":[{"text":"large"}]}}`,
		`{"type":"world_state","payload":{"full":true}}`,
	} {
		if codexRolloutLineNeeded([]byte(line)) {
			t.Fatalf("expected content line to be skipped: %s", line)
		}
	}
	for _, line := range []string{
		`{"type":"session_meta","payload":{"cwd":"/workspace"}}`,
		`{"type":"event_msg","payload":{"type":"token_count"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_output"}}`,
	} {
		if !codexRolloutLineNeeded([]byte(line)) {
			t.Fatalf("expected structural line to be parsed: %s", line)
		}
	}
}

func TestCodexContextUsesDistinctLastUsageWithoutCumulativeFallback(t *testing.T) {
	count := func(second int, total int64, last any) any {
		return map[string]any{"type": "event_msg", "timestamp": claudeFixtureTime(second),
			"payload": map[string]any{"type": "token_count", "info": map[string]any{
				"total_token_usage": map[string]any{"input_tokens": total, "total_tokens": total},
				"last_token_usage":  last, "model_context_window": 2000,
			}}}
	}
	last := func(input int64) any { return map[string]any{"input_tokens": input, "cached_input_tokens": 50} }
	path := writeCodexSessionFixture(t, t.TempDir(), "context", []any{
		count(0, 1000, last(100)),
		count(1, 1000, last(100)),
		count(2, 1250, last(250)),
		count(3, 1300, nil), // Legacy cumulative telemetry is not context.
		count(4, 1400, last(100)),
		map[string]any{"timestamp": claudeFixtureTime(5), "type": "compacted"},
		count(6, 1400, last(100)), // A replay after compaction is not a new request.
		count(7, 1600, last(200)),
	})
	session, err := parseCodexNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	var samples []int64
	for _, event := range session.Events {
		if event.Kind == sessionEventToken && event.ContextTokens > 0 {
			samples = append(samples, event.ContextTokens)
			if event.ContextWindowTokens != 2000 {
				t.Fatalf("missing reported window: %+v", event)
			}
		}
	}
	want := []int64{100, 250, 100, 200}
	if len(samples) != len(want) {
		t.Fatalf("samples=%v want=%v", samples, want)
	}
	for i := range want {
		if samples[i] != want[i] {
			t.Fatalf("samples=%v want=%v", samples, want)
		}
	}
}

func TestParseCodexSessionRetainsOnlyValidatedSkillIdentity(t *testing.T) {
	skillPath := filepath.Join(t.TempDir(), "evidence-first-planning", "SKILL.md")
	writeInstructionFixture(t, skillPath, strings.Repeat("x", largeGuidanceArtifactBytes))
	arguments, err := json.Marshal(map[string]string{"cmd": "sed -n '1,200p' " + skillPath})
	if err != nil {
		t.Fatal(err)
	}
	sessionPath := writeCodexSessionFixture(t, t.TempDir(), "skill-load", []any{
		map[string]any{
			"timestamp": "2026-07-29T08:00:00Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "function_call", "call_id": "skill", "name": "exec_command",
				"arguments": string(arguments),
			},
		},
	})
	session, err := parseCodexNormalizedSession(sessionPath)
	if err != nil {
		t.Fatalf("parse skill load: %v", err)
	}
	if len(session.Events) != 1 || len(session.Events[0].GuidanceArtifacts) != 1 {
		t.Fatalf("skill guidance missing: %#v", session.Events)
	}
	got := session.Events[0].GuidanceArtifacts[0]
	if got.Kind != "skill" || got.ID != "evidence-first-planning" || got.EstimatedTokens != largeGuidanceArtifactTokens {
		t.Fatalf("skill guidance=%#v", got)
	}
	if invalid := codexGuidanceArtifacts([]string{"/private/skills/not valid/SKILL.md"}); len(invalid) != 0 {
		t.Fatalf("invalid skill identity retained: %#v", invalid)
	}
}

func TestParseCodexSessionUsesPrivacySafeRolloutLineage(t *testing.T) {
	sessionsDir := t.TempDir()
	childID := "provider-child-secret"
	parentID := "provider-parent-secret"
	sessionPath := writeCodexSessionFixture(t, sessionsDir, "lineage", []any{
		map[string]any{
			"timestamp": "2026-07-24T08:00:00Z",
			"type":      "session_meta",
			"payload": map[string]any{
				"id":               childID,
				"parent_thread_id": parentID,
				"cwd":              "/workspace",
			},
		},
	})

	session, err := parseCodexNormalizedSession(sessionPath)
	if err != nil {
		t.Fatalf("parse Codex lineage: %v", err)
	}
	if session.AgentKind != "subagent" ||
		session.LineageKey != ownershipSelectorDigest("provider-thread", childID) ||
		session.ParentLineageKey != ownershipSelectorDigest("provider-thread", parentID) {
		t.Fatalf("rollout lineage mismatch: %#v", session)
	}
	encoded, _ := json.Marshal(session)
	if strings.Contains(string(encoded), childID) || strings.Contains(string(encoded), parentID) {
		t.Fatalf("provider identifiers escaped privacy-safe lineage: %s", encoded)
	}
}

func TestParseCodexSessionCarriesOnlyNestedToolNames(t *testing.T) {
	sessionPath := writeCodexSessionFixture(t, t.TempDir(), "nested-tool-context", []any{
		map[string]any{
			"timestamp": "2026-07-29T08:00:00Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "custom_tool_call", "call_id": "nested", "name": "exec",
				"input": `const result = await tools.web__run({secret:"private-query"}); text(result);`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-29T08:00:01Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "custom_tool_call_output", "call_id": "nested",
				"output": strings.Repeat("x", 35_000),
			},
		},
	})
	session, err := parseCodexNormalizedSession(sessionPath)
	if err != nil {
		t.Fatalf("parse nested tool context: %v", err)
	}
	if len(session.Events) != 2 ||
		session.Events[0].NestedToolContext != "nested tool web__run" ||
		session.Events[1].NestedToolContext != "nested tool web__run" {
		t.Fatalf("nested tool context was not propagated: %#v", session.Events)
	}
	encoded, _ := json.Marshal(session)
	if strings.Contains(string(encoded), "private-query") {
		t.Fatalf("nested tool arguments escaped normalization: %s", encoded)
	}
}

func TestParseCodexSessionPreservesNestedContinuationTool(t *testing.T) {
	sessionPath := writeCodexSessionFixture(t, t.TempDir(), "nested-continuation", []any{
		map[string]any{
			"timestamp": "2026-07-29T08:00:00Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "custom_tool_call", "call_id": "start", "name": "exec",
				"input": `const result = await tools.exec_command({cmd:"go test ./..."}); text(result);`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-29T08:00:01Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "custom_tool_call_output", "call_id": "start",
				"output": map[string]any{
					"output": "still running", "session_id": 12, "wall_time_seconds": 1,
				},
			},
		},
		map[string]any{
			"timestamp": "2026-07-29T08:00:02Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "custom_tool_call", "call_id": "poll", "name": "exec",
				"input": `const result = await tools.write_stdin({session_id:12,chars:""}); text(result);`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-29T08:00:03Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "custom_tool_call_output", "call_id": "poll",
				"output": map[string]any{
					"output": "still running", "session_id": 12, "wall_time_seconds": 1,
				},
			},
		},
	})
	session, err := parseCodexNormalizedSession(sessionPath)
	if err != nil {
		t.Fatalf("parse nested continuation: %v", err)
	}
	if len(session.Events) != 4 ||
		session.Events[2].NestedToolContext != "nested tool write_stdin" ||
		session.Events[3].NestedToolContext != "nested tool write_stdin" ||
		session.Events[0].ToolRound != 1 ||
		session.Events[1].ToolRound != 1 ||
		session.Events[3].ToolRound != 1 ||
		!session.Events[3].OperationContinues ||
		session.Events[3].Family == "" {
		t.Fatalf("nested continuation attribution mismatch: %#v", session.Events)
	}
}

func TestParseCodexSessionDropsMCPServerNameButKeepsOwnershipSelector(t *testing.T) {
	const tool = "mcp__SECRET-MCP-SERVER__lookup"
	sessionPath := writeCodexSessionFixture(t, t.TempDir(), "mcp", []any{
		map[string]any{
			"timestamp": "2026-07-29T08:00:00Z",
			"type":      "response_item",
			"payload":   map[string]any{"type": "function_call", "call_id": "c1", "name": tool, "arguments": `{}`},
		},
		map[string]any{
			"timestamp": "2026-07-29T08:00:01Z",
			"type":      "response_item",
			"payload":   map[string]any{"type": "function_call_output", "call_id": "c1", "output": "Exit code: 1"},
		},
	})
	session, err := parseCodexNormalizedSession(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Events) != 2 {
		t.Fatalf("events=%#v", session.Events)
	}
	// Code Mode exposes tools as identifiers, so a nested MCP name has no hyphen.
	const nestedTool = "mcp__secret_server__lookup"
	nested := codexNestedToolContext("exec", `await tools.`+nestedTool+`({}); await tools.web__run({});`)
	if nested != "nested tools "+normalizedToolName(nestedTool)+" + web__run" {
		t.Fatalf("nested MCP tool name retained: %q", nested)
	}
	for _, event := range session.Events {
		if event.ToolName != normalizedToolName(tool) ||
			strings.Contains(event.ToolName+event.FailureContext, "SECRET") {
			t.Fatalf("MCP server name retained: %#v", event)
		}
		if len(event.SelectorDigests) == 0 || event.SelectorDigests[0] != ownershipSelectorDigest("tool", tool) {
			t.Fatalf("raw-name ownership selector lost: %#v", event.SelectorDigests)
		}
	}
}
