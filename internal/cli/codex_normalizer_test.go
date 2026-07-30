package cli

import (
	"encoding/json"
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
