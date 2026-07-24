package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionStoreReusesUnchangedSourcesAndMatchesDirectAnalysis(t *testing.T) {
	sessionsDir := t.TempDir()
	repositoryRoot := filepath.Join(t.TempDir(), "repository")
	writeCodexSessionFixture(t, sessionsDir, "indexed", []any{
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
	})

	store, err := openSessionStore(filepath.Join(t.TempDir(), "muninn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	first, err := store.refresh(ctx, "codex", []string{sessionsDir}, repositoryRoot, codexSessionSource{}, ownershipCatalog{}, false)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if first.FilesIndexed != 1 || first.FilesReused != 0 {
		t.Fatalf("unexpected first refresh: %#v", first)
	}
	second, err := store.refresh(ctx, "codex", []string{sessionsDir}, repositoryRoot, codexSessionSource{}, ownershipCatalog{}, false)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if second.FilesIndexed != 0 || second.FilesReused != 1 {
		t.Fatalf("unchanged source was not reused: %#v", second)
	}

	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	since := generatedAt.Add(-24 * time.Hour)
	indexed, err := store.analyze(ctx, "codex", []string{sessionsDir}, repositoryRoot, since, generatedAt, "", ownershipCatalog{}, second)
	if err != nil {
		t.Fatalf("indexed analysis: %v", err)
	}
	direct, err := analyzeCodexSessions([]string{sessionsDir}, repositoryRoot, since, generatedAt)
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
	if got := indexed.Summary.Tokens; got.InputTokens != 20 ||
		got.CachedInputTokens != 10 ||
		got.UncachedInputTokens != 10 ||
		got.OutputTokens != 5 ||
		got.TotalTokens != 25 {
		t.Fatalf("indexed window token delta mismatch: %#v", got)
	}
}

func TestSessionStoreSavesPrivacySafeCheckpoint(t *testing.T) {
	store, err := openSessionStore(filepath.Join(t.TempDir(), "muninn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	report := newSessionInsightsReport(
		"codex",
		[]string{"/private/provider/sessions"},
		"/private/repository",
		time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC),
	)
	report.Summary.Sessions = 2
	if err := store.saveCheckpoint(ctx, "before", "codex", "repository-digest", report); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	loaded, err := store.loadCheckpoint(ctx, "before", "codex", "repository-digest")
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if loaded.Summary.Sessions != 2 {
		t.Fatalf("checkpoint summary missing: %#v", loaded.Summary)
	}
	raw, err := json.Marshal(loaded)
	if err != nil {
		t.Fatalf("marshal loaded checkpoint: %v", err)
	}
	if string(raw) == "" || containsAny(string(raw), "/private/repository", "/private/provider/sessions") {
		t.Fatalf("checkpoint exposed local paths: %s", raw)
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
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
