package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	claudeFixturePRURL  = "https://forge.example/SECRET-owner/SECRET-repo/pull/4242"
	claudeFixturePRRepo = "SECRET-owner/SECRET-repo"
)

func claudeFixturePRLink(second int, url string) map[string]any {
	record := claudeFixtureRecord("pr-link", second, map[string]any{
		"prNumber": 4242, "prRepository": claudeFixturePRRepo, "prUrl": url,
	})
	delete(record, "cwd") // pr-link records carry no working directory.
	return record
}

// claudeFixtureDeliveryConversation edits, tests, and opens a pull request.
// With push, it pushes before opening the pull request.
func claudeFixtureDeliveryConversation(root string, push bool) []any {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	records := []any{
		claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("e1", "Edit", map[string]any{"file_path": filepath.Join(root, "main.go")})),
		claudeFixtureToolResult(2, "e1", false, "ok"),
		claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("v1", "Bash", map[string]any{"command": "go test ./..."})),
		claudeFixtureToolResult(4, "v1", false, "ok"),
	}
	if push {
		records = append(records,
			claudeFixtureAssistant(5, "m3", "tool_use", usage, claudeFixtureToolUse("g1", "Bash", map[string]any{"command": "git push -u origin HEAD"})),
			claudeFixtureToolResult(6, "g1", false, "ok"),
		)
	}
	records = append(records,
		claudeFixtureAssistant(7, "m4", "tool_use", usage, claudeFixtureToolUse("p1", "Bash", map[string]any{"command": "gh pr create --fill"})),
		claudeFixtureToolResult(8, "p1", false, claudeFixturePRURL),
		claudeFixturePRLink(8, claudeFixturePRURL),
		// Claude Code repeats the link for the same pull request.
		claudeFixturePRLink(9, claudeFixturePRURL),
		claudeFixtureAssistant(10, "m5", "tool_use", usage, claudeFixtureToolUse("e2", "Edit", map[string]any{"file_path": filepath.Join(root, "main.go")})),
		claudeFixtureToolResult(11, "e2", false, "ok"),
		claudeFixtureAssistant(12, "m6", "end_turn", usage, map[string]any{"type": "text", "text": "done"}),
	)
	for _, record := range records {
		if _, ok := record.(map[string]any)["cwd"]; ok {
			record.(map[string]any)["cwd"] = root
		}
	}
	return records
}

func TestParseClaudeSessionEmitsOneDeliveryPerPullRequest(t *testing.T) {
	root := "/workspace/app"
	path := writeClaudeSessionFixture(t, t.TempDir(), "pr", claudeFixtureDeliveryConversation(root, false))
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	var deliveries []normalizedSessionEvent
	for _, event := range session.Events {
		if event.Kind == sessionEventDelivery {
			deliveries = append(deliveries, event)
		}
	}
	if len(deliveries) != 1 {
		t.Fatalf("deliveries=%#v", deliveries)
	}
	delivery := deliveries[0]
	if delivery.ToolName != "" || delivery.Family != "" || len(delivery.SelectorDigests) != 0 ||
		len(delivery.WorkingDirectories) != 1 || delivery.WorkingDirectories[0] != root {
		t.Fatalf("delivery must be a fixed event in the last working directory: %#v", delivery)
	}

}

func TestClaudePullRequestReachesDeliveryMetricsWithoutIdentifiers(t *testing.T) {
	root := t.TempDir()
	sessionsDir := t.TempDir()
	writeClaudeSessionFixture(t, sessionsDir, "pr", claudeFixtureDeliveryConversation(root, false))
	writeClaudeSessionFixture(t, sessionsDir, "pushed", claudeFixtureDeliveryConversation(root, true))
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
	rework := report.Summary.DeliveryRework
	// One delivery per session: the pull request in one, the push in the other.
	if rework.Deliveries != 2 || rework.DeliveriesWithPreTests != 2 || rework.Sessions != 2 {
		t.Fatalf("delivery rework=%#v", rework)
	}
	raw := mustJSON(t, report)
	databasePath := filepath.Join(t.TempDir(), "sessions.sqlite")
	store, err := openSessionStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.refresh(t.Context(), provider.Name(), discovery, root, provider, ownershipCatalog{}, false); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	stored := []string{string(raw)}
	for _, name := range []string{"", "-wal"} {
		index, err := os.ReadFile(databasePath + name)
		if err == nil {
			stored = append(stored, string(index))
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	for _, content := range stored {
		for _, forbidden := range []string{"SECRET-owner", "SECRET-repo", "4242", "forge.example"} {
			if strings.Contains(content, forbidden) {
				t.Fatalf("report or index exposes %q", forbidden)
			}
		}
	}
}

func TestClaudeBindingExistingPRAfterEditsDoesNotDeliver(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	for _, afterCompletion := range []bool{false, true} {
		records := []any{
			claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("e1", "Edit", map[string]any{"file_path": "/workspace/app/main.go"})),
			claudeFixtureToolResult(2, "e1", false, "ok"),
		}
		if afterCompletion {
			records = append(records, claudeFixtureAssistant(3, "m2", "end_turn", usage), claudeFixturePRLink(4, claudeFixturePRURL))
		} else {
			records = append(records, claudeFixturePRLink(3, claudeFixturePRURL), claudeFixtureAssistant(4, "m2", "end_turn", usage))
		}
		record := claudeBackgroundRecord(t, records)
		if record.DeliveryRework.Deliveries != 0 || len(record.TaskEpisodes) != 1 {
			t.Fatalf("binding afterCompletion=%v deliveries=%#v episodes=%#v", afterCompletion, record.DeliveryRework, record.TaskEpisodes)
		}
	}
}

func TestClaudeLateCreatedPRLinkDoesNotOpenAnEpisode(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	record := claudeBackgroundRecord(t, []any{
		claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("e1", "Edit", map[string]any{"file_path": "/workspace/app/main.go"})),
		claudeFixtureToolResult(2, "e1", false, "ok"),
		claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("p1", "Bash", map[string]any{"command": "gh pr create --fill"})),
		claudeFixtureToolResult(4, "p1", false, claudeFixturePRURL),
		claudeFixtureAssistant(5, "m3", "end_turn", usage),
		claudeFixturePRLink(6, claudeFixturePRURL),
	})
	if record.DeliveryRework.Deliveries != 1 || len(record.TaskEpisodes) != 1 {
		t.Fatalf("late creation deliveries=%#v episodes=%#v", record.DeliveryRework, record.TaskEpisodes)
	}
}

func TestClaudeDelayedPRLinkCannotDeliverLaterEdits(t *testing.T) {
	for _, push := range []bool{false, true} {
		for _, cwd := range []string{"/workspace/app", "/workspace/other"} {
			records := claudeFixtureDeliveryConversation("/workspace/app", push)
			// The first link arrives after another edit, with a changed directory.
			var delayed []any
			for i, raw := range records {
				record := raw.(map[string]any)
				if record["type"] != "pr-link" && i != len(records)-1 {
					delayed = append(delayed, raw)
				}
			}
			link := claudeFixturePRLink(12, claudeFixturePRURL)
			link["cwd"] = cwd
			delayed = append(delayed, link, records[len(records)-1])
			record := claudeBackgroundRecord(t, delayed)
			if record.DeliveryRework.Deliveries != 1 {
				t.Fatalf("push=%v cwd=%s delayed link deliveries=%#v", push, cwd, record.DeliveryRework)
			}
			path := writeClaudeSessionFixture(t, t.TempDir(), "delayed", delayed)
			session, err := parseClaudeNormalizedSession(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range session.Events {
				if event.Kind == sessionEventDelivery && (event.OccurredAt.Second() != 8 || len(event.WorkingDirectories) != 1 || event.WorkingDirectories[0] != "/workspace/app") {
					t.Fatalf("creation moved to delayed link: %#v", event)
				}
			}
		}
	}
}

func TestClaudePRCreationRequiresUnambiguousSuccessfulCommand(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	for _, command := range []string{
		"gh pr create --fill || printf '%s\\n' " + claudeFixturePRURL,
		"gh pr create --fill; printf '%s\\n' " + claudeFixturePRURL,
		"sh -c 'gh pr create --fill 2>/dev/null || eval \"printf " + claudeFixturePRURL + "\"'",
	} {
		record := claudeBackgroundRecord(t, []any{
			claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("e1", "Edit", map[string]any{"file_path": "/workspace/app/main.go"})),
			claudeFixtureToolResult(2, "e1", false, "ok"),
			claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("p1", "Bash", map[string]any{"command": command})),
			claudeFixtureToolResult(4, "p1", false, claudeFixturePRURL),
			claudeFixturePRLink(5, claudeFixturePRURL),
		})
		if record.DeliveryRework.Deliveries != 0 {
			t.Fatalf("compound command counted as creation: %q", command)
		}
	}
}

func TestClaudeStandalonePRCreateRespectsQuotedArguments(t *testing.T) {
	for _, command := range []string{
		"\n gh pr create --fill\n",
		"gh pr create --body 'Handle empty input; add coverage & tests | checks'",
		"gh pr create --body 'First line\nSecond line'",
		"gh pr create --body 'Literal $(example) and `example`'",
		`gh pr create --body "Handle input; add coverage"`,
		`gh pr create --body "Literal \$(example) and \` + "`" + `example\` + "`" + `"`,
	} {
		if !claudeStandalonePRCreate(command) {
			t.Errorf("standalone command rejected: %q", command)
		}
		records := claudeFixtureDeliveryConversation("/workspace/app", false)
		records[4] = claudeFixtureAssistant(7, "m4", "tool_use", map[string]any{"input_tokens": 1, "output_tokens": 1}, claudeFixtureToolUse("p1", "Bash", map[string]any{"command": command}))
		if got := claudeBackgroundRecord(t, records).DeliveryRework.Deliveries; got != 1 {
			t.Errorf("standalone delivery=%d command=%q", got, command)
		}
	}
	for _, command := range []string{
		"gh pr create --fill &", "gh pr create --fill;",
		"gh pr create --body $(echo example)", "gh pr create --body \"$(echo example)\"",
		"gh pr create --body `echo example`", "gh pr create --body 'unterminated",
		"gh pr create --body <(echo example)",
	} {
		if claudeStandalonePRCreate(command) {
			t.Errorf("ambiguous command accepted: %q", command)
		}
	}
}

func TestClaudeLateResultFromStoppedPRCreationDoesNotDeliver(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	record := claudeBackgroundRecord(t, []any{
		claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("e1", "Edit", map[string]any{"file_path": "/workspace/app/main.go"})),
		claudeFixtureToolResult(2, "e1", false, "ok"),
		claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("p1", "Bash", map[string]any{"command": "gh pr create --fill", "run_in_background": true})),
		claudeFixtureBackgroundResult(4, "p1", "bgtask014", true),
		claudeFixtureAssistant(5, "m3", "tool_use", usage, claudeFixtureToolUse("p2", "TaskOutput", map[string]any{"task_id": "bgtask014"})),
		claudeFixtureNotification(6, "bgtask014", "stopped"),
		claudeFixtureToolResult(7, "p2", false, claudeFixturePRURL),
		claudeFixturePRLink(8, claudeFixturePRURL),
	})
	if record.DeliveryRework.Deliveries != 0 || record.FailureReasons["interrupted process"] != 1 || record.ToolOutputBytes == 0 {
		t.Fatalf("late cancelled creation: delivery=%#v failures=%#v bytes=%d", record.DeliveryRework, record.FailureReasons, record.ToolOutputBytes)
	}
}
