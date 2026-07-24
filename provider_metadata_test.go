package main

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProviderSessionMetadataJoinsPrivacySafeModelAndLineage(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	rootRollout := filepath.Join(sessionsDir, "root.jsonl")
	childRollout := filepath.Join(sessionsDir, "child.jsonl")
	db, err := sql.Open("sqlite", filepath.Join(root, "state_5.sqlite"))
	if err != nil {
		t.Fatalf("open state db: %v", err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE threads (
			id TEXT PRIMARY KEY,
			rollout_path TEXT,
			model TEXT,
			reasoning_effort TEXT,
			thread_source TEXT,
			source TEXT
		)`,
		`CREATE TABLE thread_spawn_edges (
			parent_thread_id TEXT,
			child_thread_id TEXT,
			status TEXT
		)`,
		`INSERT INTO threads VALUES
			('root-secret-id', ?, 'gpt-5.6-sol', 'xhigh', 'user', 'cli'),
			('child-secret-id', ?, 'gpt-5.6-luna', 'max', 'subagent', 'cli')`,
		`INSERT INTO thread_spawn_edges VALUES
			('root-secret-id', 'child-secret-id', 'completed')`,
	} {
		var execErr error
		if strings.Contains(statement, "INSERT INTO threads") {
			_, execErr = db.Exec(statement, rootRollout, childRollout)
		} else {
			_, execErr = db.Exec(statement)
		}
		if execErr != nil {
			t.Fatalf("execute state schema: %v", execErr)
		}
	}

	metadata := loadProviderSessionMetadata("codex", []string{sessionsDir})
	rootEntry := metadata[filepath.Clean(rootRollout)]
	childEntry := metadata[filepath.Clean(childRollout)]
	if rootEntry.Model != "gpt-5.6-sol" || rootEntry.ReasoningEffort != "xhigh" ||
		rootEntry.AgentKind != "root" || rootEntry.LineageKey == "" {
		t.Fatalf("root metadata mismatch: %#v", rootEntry)
	}
	if childEntry.Model != "gpt-5.6-luna" || childEntry.ReasoningEffort != "max" ||
		childEntry.AgentKind != "subagent" || childEntry.SpawnStatus != "completed" ||
		childEntry.ParentLineageKey != rootEntry.LineageKey {
		t.Fatalf("child metadata mismatch: %#v", childEntry)
	}
	encoded, _ := json.Marshal(metadata)
	if strings.Contains(string(encoded), "root-secret-id") ||
		strings.Contains(string(encoded), "child-secret-id") {
		t.Fatalf("provider identifiers leaked into normalized metadata: %s", encoded)
	}
}

func TestNormalizedProviderLabelRejectsUnboundedValues(t *testing.T) {
	if got := normalizedProviderLabel("gpt-5.6-luna"); got != "gpt-5.6-luna" {
		t.Fatalf("valid model label changed: %q", got)
	}
	if got := normalizedProviderLabel(strings.Repeat("x", 65)); got != "(unknown)" {
		t.Fatalf("unbounded model label retained: %q", got)
	}
	if got := normalizedProviderLabel("model with prompt text"); got != "(unknown)" {
		t.Fatalf("unsafe model label retained: %q", got)
	}
}
