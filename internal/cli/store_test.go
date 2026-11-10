package cli

import (
	"database/sql"
	"fmt"
	"path/filepath"
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

func TestSessionStoreRebuildsIncompatibleDerivedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "muninn.db")
	store, err := openSessionStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO sources(provider, scope_key, source_path, size_bytes, mtime_ns, indexed_at_ns)
		VALUES('codex', 'stale', '/private/session.jsonl', 1, 1, 1)`); err != nil {
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
	if version != sessionStoreSchemaKey {
		t.Fatalf("expected schema key %q, got %q", sessionStoreSchemaKey, version)
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
	for _, column := range []struct {
		table string
		name  string
	}{
		{"sources", "scope_key"},
		{"events", "in_repository_scope"},
		{"events", "nested_tool_context"},
		{"events", "guidance_artifacts"},
	} {
		var count int
		if err := migrated.db.QueryRow(
			fmt.Sprintf(
				`SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?`,
				column.table,
			),
			column.name,
		).Scan(&count); err != nil {
			t.Fatalf("inspect %s.%s: %v", column.table, column.name, err)
		}
		if count != 1 {
			t.Fatalf("expected %s.%s, got %d columns", column.table, column.name, count)
		}
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

// A build that changes the events table without bumping the version number
// must still rebuild an index another build created at that number.
func TestSessionStoreRebuildsChangedTablesAtSameVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "muninn.db")
	store, err := openSessionStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	for _, statement := range []string{
		`DROP TABLE events`,
		`CREATE TABLE events (id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, sequence INTEGER NOT NULL)`,
		fmt.Sprintf(`UPDATE metadata SET value = '%d' WHERE key = 'schema_version'`, sessionStoreSchemaVersion),
	} {
		if _, err := store.db.Exec(statement); err != nil {
			t.Fatalf("simulate other build: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	reopened, err := openSessionStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	var columns int
	if err := reopened.db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = 'context_tokens'`,
	).Scan(&columns); err != nil {
		t.Fatalf("inspect events: %v", err)
	}
	if columns != 1 {
		t.Fatalf("expected the events table to be rebuilt with context_tokens, got %d", columns)
	}
}
