package cli

import (
	"testing"
	"time"
)

func claudeBackgroundRecord(t *testing.T, records []any) codexSessionRecord {
	t.Helper()
	path := writeClaudeSessionFixture(t, t.TempDir(), "outcomes", records)
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	record, err := sessionRecordFromNormalized(session, "/workspace/app", at, at.Add(time.Hour), ownershipCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestClaudeStoppedBackgroundCommandIsNeverSuccessfulDeliveryOrVerification(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	for _, command := range []string{"go test ./...", "git push"} {
		for _, surface := range []string{"TaskStop", "TaskOutput", "notification"} {
			t.Run(command+"/"+surface, func(t *testing.T) {
				records := []any{
					claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("e1", "Edit", map[string]any{"file_path": "/workspace/app/main.go"})),
					claudeFixtureToolResult(2, "e1", false, "ok"),
					claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("b1", "Bash", map[string]any{"command": command, "run_in_background": true})),
					claudeFixtureBackgroundResult(4, "b1", "bgtask011", true),
				}
				if surface == "notification" {
					records = append(records, claudeFixtureNotification(6, "bgtask011", "stopped"))
				} else {
					output := "stopped"
					if surface == "TaskOutput" {
						output = "<retrieval_status>success</retrieval_status>\n<status>stopped</status>\n<output>SECRET</output>"
					}
					records = append(records, claudeFixtureAssistant(5, "m3", "tool_use", usage, claudeFixtureToolUse("s1", surface, map[string]any{"task_id": "bgtask011"})), claudeFixtureToolResult(6, "s1", false, output))
					if surface == "TaskStop" {
						records = append(records, claudeFixtureToolResult(6, "s1", false, output))
					}
				}
				if command == "go test ./..." {
					records = append(records, claudeFixtureAssistant(7, "m4", "tool_use", usage, claudeFixtureToolUse("p1", "Bash", map[string]any{"command": "gh pr create --fill"})), claudeFixtureToolResult(8, "p1", false, claudeFixturePRURL), claudeFixturePRLink(8, claudeFixturePRURL))
				}
				records = append(records, claudeFixtureAssistant(9, "m5", "end_turn", usage))
				record := claudeBackgroundRecord(t, records)
				wantDeliveries := 0
				if command == "go test ./..." {
					wantDeliveries = 1
				}
				if record.DeliveryRework.Deliveries != wantDeliveries || record.DeliveryRework.DeliveriesWithPreTests != 0 || record.FailureReasons["interrupted process"] != 1 {
					t.Fatalf("stopped command outcomes: deliveries=%#v failures=%#v", record.DeliveryRework, record.FailureReasons)
				}
			})
		}
	}
}

func TestClaudeOutstandingResultsDoNotResolveAnotherBackgroundTask(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	for _, surface := range []string{"TaskOutput", "TaskStop"} {
		t.Run(surface, func(t *testing.T) {
			records := []any{
				claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("b1", "Bash", map[string]any{"command": "go test ./...", "run_in_background": true})),
				claudeFixtureBackgroundResult(2, "b1", "bgtask012", true),
				claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("b2", "Bash", map[string]any{"command": "go test ./other", "run_in_background": true})),
				claudeFixtureBackgroundResult(4, "b2", "bgtask013", true),
				claudeFixtureAssistant(5, "m3", "tool_use", usage, claudeFixtureToolUse("p1", surface, map[string]any{"task_id": "bgtask012"}), claudeFixtureToolUse("p2", surface, map[string]any{"task_id": "bgtask012"})),
			}
			if surface == "TaskOutput" {
				records = append(records, claudeFixtureNotification(6, "bgtask012", "completed"))
			}
			records = append(records, claudeFixtureToolResult(7, "p1", false, "<retrieval_status>success</retrieval_status>\n<status>completed</status>\n<output>ok</output>"), claudeFixtureToolResult(8, "p2", false, "stopped"), claudeFixtureAssistant(9, "m4", "end_turn", usage))
			record := claudeBackgroundRecord(t, records)
			if record.AbandonedContinuations["tests"] != 1 {
				t.Fatalf("unresolved second task lost: %#v", record.AbandonedContinuations)
			}
		})
	}
}
