package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionStoreRemovesLegacyFeedbackStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "muninn.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy store: %v", err)
	}
	if _, err := legacy.Exec(`CREATE TABLE feedback (id INTEGER PRIMARY KEY)`); err != nil {
		legacy.Close()
		t.Fatalf("create legacy feedback table: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	store, err := openSessionStore(path)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer store.Close()

	var tables int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'feedback'`,
	).Scan(&tables); err != nil {
		t.Fatalf("inspect migrated store: %v", err)
	}
	if tables != 0 {
		t.Fatalf("legacy feedback table still exists")
	}
}

func TestSessionStoreReusesUnchangedSourcesAndMatchesDirectAnalysis(t *testing.T) {
	sessionsDir := t.TempDir()
	repositoryRoot := filepath.Join(t.TempDir(), "repository")
	sessionPath := writeCodexSessionFixture(t, sessionsDir, "indexed", []any{
		map[string]any{
			"timestamp": "2026-07-23T08:00:00Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{"total_token_usage": map[string]any{
					"input_tokens": 60, "cached_input_tokens": 40,
					"output_tokens": 5, "total_tokens": 65,
				}},
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:00Z",
			"type":      "session_meta",
			"payload":   map[string]any{"cwd": repositoryRoot},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:01Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "search",
				"name":      "exec_command",
				"arguments": `{"cmd":"rg -n target src"}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:02Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "function_call_output",
				"call_id": "search",
				"output":  "exit code: 1",
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:03Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{"total_token_usage": map[string]any{
					"input_tokens": 80, "cached_input_tokens": 50,
					"output_tokens": 10, "total_tokens": 90,
				}},
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:04Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "delivery",
				"name":      "exec_command",
				"arguments": `{"cmd":"git push origin feature"}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:05Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "function_call_output",
				"call_id": "delivery",
				"output":  "exit code: 0",
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:06Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "downstream-test",
				"name":      "exec_command",
				"arguments": `{"cmd":"go test ./..."}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:07Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "function_call_output",
				"call_id": "downstream-test",
				"output":  "exit code: 1",
			},
		},
	})

	store, err := openSessionStore(filepath.Join(t.TempDir(), "muninn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	discovery, err := discoverCodexSessions([]string{sessionsDir})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.refresh(ctx, "codex", discovery, repositoryRoot, codexSessionProvider, ownershipCatalog{}, false)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if first.FilesIndexed != 1 || first.FilesReused != 0 {
		t.Fatalf("unexpected first refresh: %#v", first)
	}
	second, err := store.refresh(ctx, "codex", discovery, repositoryRoot, codexSessionProvider, ownershipCatalog{}, false)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if second.FilesIndexed != 0 || second.FilesReused != 1 {
		t.Fatalf("unchanged source was not reused: %#v", second)
	}

	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	since := generatedAt.Add(-24 * time.Hour)
	metadata := map[string]normalizedSessionMetadata{
		filepath.Clean(sessionPath): {
			Model:           "gpt-5.6-sol",
			ReasoningEffort: "xhigh",
			AgentKind:       "root",
			LineageKey:      "privacy-safe-lineage",
		},
	}
	indexed, err := store.analyze(ctx, "codex", []string{sessionsDir}, repositoryRoot, since, generatedAt, "", ownershipCatalog{}, second, metadata)
	if err != nil {
		t.Fatalf("indexed analysis: %v", err)
	}
	direct, err := analyzeCodexSessionsFilteredWithMetadata(
		[]string{sessionsDir},
		repositoryRoot,
		since,
		generatedAt,
		"",
		ownershipCatalog{},
		metadata,
	)
	if err != nil {
		t.Fatalf("direct analysis: %v", err)
	}
	indexedJSON, _ := json.Marshal(indexed.Summary)
	directJSON, _ := json.Marshal(direct.Summary)
	if string(indexedJSON) != string(directJSON) {
		t.Fatalf("indexed and direct summaries differ:\nindexed=%s\ndirect=%s", indexedJSON, directJSON)
	}
	indexedOutcomesJSON, _ := json.Marshal(indexed.Outcomes)
	directOutcomesJSON, _ := json.Marshal(direct.Outcomes)
	if string(indexedOutcomesJSON) != string(directOutcomesJSON) {
		t.Fatalf("indexed and direct outcomes differ:\nindexed=%s\ndirect=%s", indexedOutcomesJSON, directOutcomesJSON)
	}
	indexedProfilesJSON, _ := json.Marshal(indexed.Profiles)
	directProfilesJSON, _ := json.Marshal(direct.Profiles)
	if string(indexedProfilesJSON) != string(directProfilesJSON) {
		t.Fatalf("indexed and direct profiles differ:\nindexed=%s\ndirect=%s", indexedProfilesJSON, directProfilesJSON)
	}
	if got := indexed.Summary.DownstreamQuality; got.Deliveries != 1 ||
		got.DeliveriesWithFailure != 1 || got.FailureRuns != 1 {
		t.Fatalf("indexed downstream quality mismatch: %#v", got)
	}
	if got := indexed.Summary.Tokens; got.InputTokens != 20 ||
		got.CachedInputTokens != 10 ||
		got.UncachedInputTokens != 10 ||
		got.OutputTokens != 5 ||
		got.TotalTokens != 25 {
		t.Fatalf("indexed window token delta mismatch: %#v", got)
	}
}

func TestSessionStoreMigrationReindexesNormalizerChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "muninn.db")
	store, err := openSessionStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO sources(provider, source_path, size_bytes, mtime_ns, indexed_at_ns)
		VALUES('codex', '/private/session.jsonl', 1, 1, 1)`); err != nil {
		t.Fatalf("insert stale source: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE metadata SET value = '6' WHERE key = 'schema_version'`); err != nil {
		t.Fatalf("set old schema version: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close old store: %v", err)
	}

	migrated, err := openSessionStore(path)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	defer migrated.Close()
	var sources int
	if err := migrated.db.QueryRow(`SELECT COUNT(*) FROM sources`).Scan(&sources); err != nil {
		t.Fatalf("count sources: %v", err)
	}
	if sources != 0 {
		t.Fatalf("expected stale target index to be invalidated, got %d sources", sources)
	}
	var version string
	if err := migrated.db.QueryRow(`SELECT value FROM metadata WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != fmt.Sprint(sessionStoreSchemaVersion) {
		t.Fatalf("expected schema version %d, got %q", sessionStoreSchemaVersion, version)
	}
	var continuationColumn int
	if err := migrated.db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = 'operation_continues'`,
	).Scan(&continuationColumn); err != nil {
		t.Fatalf("inspect continuation column: %v", err)
	}
	if continuationColumn != 1 {
		t.Fatalf("expected operation continuation column, got %d", continuationColumn)
	}
	var operationTaskColumn int
	if err := migrated.db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = 'operation_task'`,
	).Scan(&operationTaskColumn); err != nil {
		t.Fatalf("inspect operation task column: %v", err)
	}
	if operationTaskColumn != 1 {
		t.Fatalf("expected operation task column, got %d", operationTaskColumn)
	}
	var concurrentBatchColumn int
	if err := migrated.db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = 'concurrent_batch'`,
	).Scan(&concurrentBatchColumn); err != nil {
		t.Fatalf("inspect concurrent batch column: %v", err)
	}
	if concurrentBatchColumn != 1 {
		t.Fatalf("expected concurrent batch column, got %d", concurrentBatchColumn)
	}
}

func TestSessionStoreWaitsForConcurrentWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "muninn.db")
	first, err := openSessionStore(path)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	defer first.Close()
	second, err := openSessionStore(path)
	if err != nil {
		t.Fatalf("open second store: %v", err)
	}
	defer second.Close()

	var busyTimeout int64
	if err := second.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy timeout: %v", err)
	}
	if busyTimeout != sessionStoreBusyTimeout.Milliseconds() {
		t.Fatalf("busy timeout = %d, want %d", busyTimeout, sessionStoreBusyTimeout.Milliseconds())
	}

	tx, err := first.db.Begin()
	if err != nil {
		t.Fatalf("begin first writer: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO metadata(key, value) VALUES('concurrent-writer', 'first')`); err != nil {
		_ = tx.Rollback()
		t.Fatalf("acquire first writer lock: %v", err)
	}

	writeDone := make(chan error, 1)
	go func() {
		_, err := second.db.Exec(`INSERT INTO metadata(key, value) VALUES('waited-writer', 'second')`)
		writeDone <- err
	}()

	select {
	case err := <-writeDone:
		_ = tx.Rollback()
		t.Fatalf("second writer returned while first held the lock: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit first writer: %v", err)
	}
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("second writer did not wait for the lock: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second writer did not resume after the lock was released")
	}
}

func TestSessionStoreOwnedOperationFailuresAreBoundedAndRepositoryScoped(t *testing.T) {
	store, err := openSessionStore(filepath.Join(t.TempDir(), "muninn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	root := filepath.Join(t.TempDir(), "repository")
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	sessions := []normalizedSession{
		{
			Provider:   "codex",
			SourcePath: "inside.jsonl",
			CWD:        filepath.Join(root, "worktree"),
			Events: []normalizedSessionEvent{
				{
					Sequence:        1,
					OccurredAt:      now.Add(-3 * time.Hour),
					Kind:            sessionEventToolOutput,
					Family:          "tests",
					Failed:          true,
					OutputBytes:     120,
					FailureReason:   "test failure",
					OwnedOperations: []string{"bwb/test-nses"},
				},
				{
					Sequence:                      2,
					OccurredAt:                    now.Add(-2 * time.Hour),
					Kind:                          sessionEventToolOutput,
					Family:                        "mixed shell",
					Failed:                        true,
					OutputBytes:                   480,
					FailureReason:                 "test harness protocol",
					OwnedOperations:               []string{"bwb/test-nses"},
					OperationAttributionAmbiguous: true,
				},
				{
					Sequence:        3,
					OccurredAt:      now.Add(-time.Hour),
					Kind:            sessionEventToolOutput,
					Family:          "tests",
					Failed:          false,
					OutputBytes:     80,
					OwnedOperations: []string{"bwb/test-nses"},
				},
			},
		},
		{
			Provider:   "codex",
			SourcePath: "outside.jsonl",
			CWD:        root + "-other",
			Events: []normalizedSessionEvent{{
				Sequence:        1,
				OccurredAt:      now.Add(-30 * time.Minute),
				Kind:            sessionEventToolOutput,
				Family:          "tests",
				Failed:          true,
				OutputBytes:     999,
				FailureReason:   "test failure",
				OwnedOperations: []string{"bwb/test-nses"},
			}},
		},
		{
			Provider:   "codex",
			SourcePath: "task.jsonl",
			CWD:        filepath.Join(root, "worktree"),
			Events: []normalizedSessionEvent{{
				Sequence:        1,
				OccurredAt:      now.Add(-4 * time.Hour),
				Kind:            sessionEventToolOutput,
				Family:          "tests",
				Failed:          true,
				OutputBytes:     240,
				FailureReason:   "test failure",
				OwnedOperations: []string{"bwb/test-nses"},
				OperationTask:   "cost-task",
			}},
		},
	}
	for index, session := range sessions {
		if err := store.replaceSession(context.Background(), session, int64(index+1), int64(index+1)); err != nil {
			t.Fatalf("replace session %d: %v", index, err)
		}
	}

	events, err := store.ownedOperationFailures(
		context.Background(),
		"codex",
		root,
		now.Add(-24*time.Hour),
		"bwb/test-nses",
		"",
		"",
		1,
	)
	if err != nil {
		t.Fatalf("query failures: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%d want 1", len(events))
	}
	if events[0].Reason != "test harness protocol" || events[0].OutputBytes != 480 || !events[0].AttributionAmbiguous {
		t.Fatalf("unexpected event: %#v", events[0])
	}
	if events[0].Operation != "bwb/test-nses" || events[0].Family != "mixed shell" {
		t.Fatalf("missing safe event labels: %#v", events[0])
	}
	if events[0].Task != "(root)" {
		t.Fatalf("task=%q want root attribution", events[0].Task)
	}

	events, err = store.ownedOperationFailures(
		context.Background(),
		"codex",
		root,
		now.Add(-24*time.Hour),
		"bwb/test-nses",
		"test failure",
		"",
		10,
	)
	if err != nil {
		t.Fatalf("query reason-filtered failures: %v", err)
	}
	if len(events) != 2 || events[0].Reason != "test failure" || events[1].Task != "cost-task" {
		t.Fatalf("reason-filtered events=%#v", events)
	}

	events, err = store.ownedOperationFailures(
		context.Background(),
		"codex",
		root,
		now.Add(-24*time.Hour),
		"bwb/test-nses",
		"test failure",
		"cost-task",
		10,
	)
	if err != nil {
		t.Fatalf("query task-filtered failures: %v", err)
	}
	if len(events) != 1 || events[0].Task != "cost-task" || events[0].OutputBytes != 240 {
		t.Fatalf("task-filtered events=%#v", events)
	}
}

func TestSessionStoreRestoresRepositoryScopeAtAnalysisBoundary(t *testing.T) {
	store, err := openSessionStore(filepath.Join(t.TempDir(), "muninn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	root := t.TempDir()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	session := normalizedSession{
		Provider:   "codex",
		SourcePath: "scope.jsonl",
		CWD:        root,
		Events: []normalizedSessionEvent{
			{Sequence: 1, OccurredAt: now.Add(-3 * time.Minute), Kind: sessionEventToolCall, ToolName: "exec_command", WorkingDirectories: []string{filepath.Join(filepath.Dir(root), "muninn")}},
			{Sequence: 2, OccurredAt: now.Add(-2 * time.Minute), Kind: sessionEventToken, Tokens: normalizedTokenUsage{TotalTokens: 500}},
			{Sequence: 3, OccurredAt: now.Add(-time.Minute), Kind: sessionEventToolOutput, ToolName: "exec_command", Failed: true, OutputBytes: 1000},
		},
	}
	if err := store.replaceSession(context.Background(), session, 1, 1); err != nil {
		t.Fatalf("replace session: %v", err)
	}

	boundaries, err := store.analysisBoundaries(context.Background(), 1, now.Add(-90*time.Second))
	if err != nil {
		t.Fatalf("analysis boundaries: %v", err)
	}
	record, err := sessionRecordFromNormalized(
		normalizedSession{
			Provider: "codex",
			CWD:      root,
			Events: append(boundaries, normalizedSessionEvent{
				Sequence:      3,
				OccurredAt:    now.Add(-time.Minute),
				Kind:          sessionEventToolOutput,
				ToolName:      "exec_command",
				Failed:        true,
				OutputBytes:   1000,
				FailureReason: "test failure",
			}),
		},
		root,
		now.Add(-90*time.Second),
		now,
		ownershipCatalog{},
	)
	if err != nil {
		t.Fatalf("normalize bounded session: %v", err)
	}
	if record.FailedToolCalls != 0 || record.ToolOutputBytes != 0 {
		t.Fatalf("outside boundary leaked failures=%d bytes=%d", record.FailedToolCalls, record.ToolOutputBytes)
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
