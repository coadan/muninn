package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const sessionStoreSchemaVersion = 24
const sessionStoreBusyTimeout = 30 * time.Second

type sessionStore struct {
	db *sql.DB
}

func defaultSessionStorePath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory: %w", err)
	}
	return filepath.Join(cacheDir, "muninn", "muninn.db"), nil
}

func openSessionStore(path string) (*sessionStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("SQLite store path cannot be empty")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create Muninn cache directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open Muninn SQLite store: %w", err)
	}
	// SQLite pragmas such as busy_timeout are connection-local. Keep one
	// configured connection per Muninn process so concurrent analyses sharing
	// the cache wait for short writer transactions instead of intermittently
	// failing with SQLITE_BUSY on an unconfigured pooled connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &sessionStore{db: db}
	if err := store.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if path != ":memory:" {
		_ = os.Chmod(path, 0o600)
	}
	return store, nil
}

func (store *sessionStore) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	return store.db.Close()
}

func (store *sessionStore) initialize(ctx context.Context) error {
	bootstrap := []string{
		fmt.Sprintf(`PRAGMA busy_timeout = %d`, sessionStoreBusyTimeout.Milliseconds()),
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	for _, statement := range bootstrap {
		if _, err := store.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize Muninn SQLite store: %w", err)
		}
	}
	var storedVersion string
	err := store.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key = 'schema_version'`).Scan(&storedVersion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read Muninn store schema version: %w", err)
	}
	if storedVersion != "" && storedVersion != fmt.Sprint(sessionStoreSchemaVersion) {
		for _, statement := range []string{
			`DROP TABLE IF EXISTS events`,
			`DROP TABLE IF EXISTS sessions`,
			`DROP TABLE IF EXISTS sources`,
		} {
			if _, err := store.db.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("rebuild Muninn session index: %w", err)
			}
		}
		if _, err := store.db.ExecContext(
			ctx,
			`UPDATE metadata SET value = ? WHERE key = 'schema_version'`,
			fmt.Sprint(sessionStoreSchemaVersion),
		); err != nil {
			return fmt.Errorf("reset Muninn store schema version: %w", err)
		}
	}
	statements := []string{
		fmt.Sprintf(`PRAGMA busy_timeout = %d`, sessionStoreBusyTimeout.Milliseconds()),
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sources (
			id INTEGER PRIMARY KEY,
			provider TEXT NOT NULL,
			scope_key TEXT NOT NULL,
			source_path TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			mtime_ns INTEGER NOT NULL,
			indexed_at_ns INTEGER NOT NULL,
			UNIQUE(provider, scope_key, source_path)
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY,
			source_id INTEGER NOT NULL UNIQUE REFERENCES sources(id) ON DELETE CASCADE,
			cwd TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY,
			session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			sequence INTEGER NOT NULL,
			occurred_at_ns INTEGER NOT NULL,
			kind TEXT NOT NULL,
			tool_name TEXT NOT NULL DEFAULT '',
			family TEXT NOT NULL DEFAULT '',
			shape TEXT NOT NULL DEFAULT '',
			first_family TEXT NOT NULL DEFAULT '',
			last_family TEXT NOT NULL DEFAULT '',
			tool_round INTEGER NOT NULL DEFAULT 0,
			call_occurred_at_ns INTEGER NOT NULL DEFAULT 0,
			failed INTEGER NOT NULL DEFAULT 0,
			truncated INTEGER NOT NULL DEFAULT 0,
			output_bytes INTEGER NOT NULL DEFAULT 0,
			failure_reason TEXT NOT NULL DEFAULT '',
			failure_context TEXT NOT NULL DEFAULT '',
			input_tokens INTEGER NOT NULL DEFAULT 0,
			cached_input_tokens INTEGER NOT NULL DEFAULT 0,
			uncached_input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			reasoning_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			selector_digests TEXT NOT NULL DEFAULT '[]',
			owned_operations TEXT NOT NULL DEFAULT '[]',
			owned_flags TEXT NOT NULL DEFAULT '[]',
			owned_flag_tools TEXT NOT NULL DEFAULT '[]',
			operation_task TEXT NOT NULL DEFAULT '',
			operation_attribution_ambiguous INTEGER NOT NULL DEFAULT 0,
			operation_continues INTEGER NOT NULL DEFAULT 0,
			targets TEXT NOT NULL DEFAULT '[]',
			inline_bytes INTEGER NOT NULL DEFAULT 0,
			concurrent_batch INTEGER NOT NULL DEFAULT 0,
			diagnostic_json TEXT NOT NULL DEFAULT '',
			working_directories TEXT NOT NULL DEFAULT '[]',
			in_repository_scope INTEGER NOT NULL DEFAULT 0,
			UNIQUE(session_id, sequence)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sources_provider_scope_path ON sources(provider, scope_key, source_path)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_cwd ON sessions(cwd)`,
		`CREATE INDEX IF NOT EXISTS idx_events_session_time ON events(session_id, occurred_at_ns, sequence)`,
		`CREATE INDEX IF NOT EXISTS idx_events_time_kind ON events(occurred_at_ns, kind)`,
	}
	for _, statement := range statements {
		if _, err := store.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize Muninn SQLite store: %w", err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `DROP TABLE IF EXISTS feedback`); err != nil {
		return fmt.Errorf("remove legacy Muninn feedback storage: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO metadata(key, value)
		VALUES('schema_version', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, fmt.Sprint(sessionStoreSchemaVersion)); err != nil {
		return fmt.Errorf("write Muninn store schema version: %w", err)
	}
	return nil
}
