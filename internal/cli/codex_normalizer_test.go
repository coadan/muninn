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
