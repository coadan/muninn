package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const sessionStoreSchemaVersion = 12
const sessionStoreBusyTimeout = 30 * time.Second

type sessionNormalizer interface {
	NormalizeSession(path string) (normalizedSession, error)
	SessionCWD(path string) (string, error)
}

type sessionStore struct {
	db *sql.DB
}

type sessionRefreshStats struct {
	FilesScanned    int
	FilesUnreadable int
	FilesIndexed    int
	FilesReused     int
}

func (store *sessionStore) ownedOperationFailures(
	ctx context.Context,
	provider string,
	repositoryRoot string,
	since time.Time,
	operation string,
	reason string,
	task string,
	limit int,
) ([]ownedOperationFailureEvent, error) {
	repositoryRoot = filepath.Clean(repositoryRoot)
	repositoryPrefix := repositoryRoot + string(filepath.Separator)
	query := `SELECT
		events.occurred_at_ns,
		operation.value,
		events.failure_reason,
		events.family,
		events.output_bytes,
		events.operation_attribution_ambiguous,
		events.operation_task,
		sessions.cwd
	FROM events
	JOIN sessions ON sessions.id = events.session_id
	JOIN sources ON sources.id = sessions.source_id
	JOIN json_each(events.owned_operations) AS operation
	WHERE sources.provider = ?
	  AND (sessions.cwd = ? OR substr(sessions.cwd, 1, length(?)) = ?)
	  AND events.failed = 1
	  AND events.occurred_at_ns >= ?
	  AND operation.value = ?`
	args := []any{
		provider,
		repositoryRoot,
		repositoryPrefix,
		repositoryPrefix,
		since.UnixNano(),
		operation,
	}
	if reason != "" {
		query += "\n  AND events.failure_reason = ?"
		args = append(args, reason)
	}
	query += "\nORDER BY events.occurred_at_ns DESC, events.sequence DESC"
	if task == "" {
		query += "\nLIMIT ?"
		args = append(args, limit)
	}
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query owned-operation failures: %w", err)
	}
	defer rows.Close()
	events := make([]ownedOperationFailureEvent, 0)
	for rows.Next() {
		var occurredAtNS int64
		var ambiguous int
		var operationTask string
		var cwd string
		var event ownedOperationFailureEvent
		if err := rows.Scan(
			&occurredAtNS,
			&event.Operation,
			&event.Reason,
			&event.Family,
			&event.OutputBytes,
			&ambiguous,
			&operationTask,
			&cwd,
		); err != nil {
			return nil, fmt.Errorf("scan owned-operation failure: %w", err)
		}
		event.OccurredAt = time.Unix(0, occurredAtNS).UTC()
		event.AttributionAmbiguous = ambiguous != 0
		event.Task = operationTask
		if event.Task == "" {
			event.Task = codexTaskName(repositoryRoot, cwd)
		}
		if task != "" && event.Task != task {
			continue
		}
		events = append(events, event)
		if len(events) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read owned-operation failures: %w", err)
	}
	return events, nil
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
			source_path TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			mtime_ns INTEGER NOT NULL,
			indexed_at_ns INTEGER NOT NULL,
			UNIQUE(provider, source_path)
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
			operation_task TEXT NOT NULL DEFAULT '',
			operation_attribution_ambiguous INTEGER NOT NULL DEFAULT 0,
			operation_continues INTEGER NOT NULL DEFAULT 0,
			targets TEXT NOT NULL DEFAULT '[]',
			inline_bytes INTEGER NOT NULL DEFAULT 0,
			UNIQUE(session_id, sequence)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sources_provider_path ON sources(provider, source_path)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_cwd ON sessions(cwd)`,
		`CREATE INDEX IF NOT EXISTS idx_events_session_time ON events(session_id, occurred_at_ns, sequence)`,
		`CREATE INDEX IF NOT EXISTS idx_events_time_kind ON events(occurred_at_ns, kind)`,
		`CREATE TABLE IF NOT EXISTS checkpoints (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			provider TEXT NOT NULL,
			repository_key TEXT NOT NULL,
			created_at_ns INTEGER NOT NULL,
			since TEXT NOT NULL,
			report_json TEXT NOT NULL,
			UNIQUE(name, provider, repository_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_checkpoints_repository ON checkpoints(provider, repository_key, created_at_ns)`,
		`CREATE TABLE IF NOT EXISTS feedback (
			id INTEGER PRIMARY KEY,
			repository_key TEXT NOT NULL,
			source TEXT NOT NULL,
			control TEXT NOT NULL,
			category TEXT NOT NULL,
			target TEXT NOT NULL,
			signal TEXT NOT NULL,
			occurrences INTEGER NOT NULL DEFAULT 1,
			status TEXT NOT NULL DEFAULT 'open',
			first_seen_ns INTEGER NOT NULL,
			last_seen_ns INTEGER NOT NULL,
			resolved_at_ns INTEGER NOT NULL DEFAULT 0,
			UNIQUE(repository_key, source, control, category, target, signal)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_feedback_repository_status ON feedback(repository_key, status, last_seen_ns)`,
	}
	for _, statement := range statements {
		if _, err := store.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize Muninn SQLite store: %w", err)
		}
	}
	var existing string
	err := store.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key = 'schema_version'`).Scan(&existing)
	reindexSources := err == nil && existing != fmt.Sprint(sessionStoreSchemaVersion)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := store.db.ExecContext(ctx, `INSERT INTO metadata(key, value) VALUES('schema_version', ?)`, fmt.Sprint(sessionStoreSchemaVersion)); err != nil {
			return fmt.Errorf("write Muninn store schema version: %w", err)
		}
	case err != nil:
		return fmt.Errorf("read Muninn store schema version: %w", err)
	case existing == "1":
		if _, err := store.db.ExecContext(ctx, `ALTER TABLE events ADD COLUMN targets TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return fmt.Errorf("migrate Muninn store targets: %w", err)
		}
		if _, err := store.db.ExecContext(ctx, `ALTER TABLE events ADD COLUMN inline_bytes INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate Muninn store inline orchestration: %w", err)
		}
		if _, err := store.db.ExecContext(ctx, `ALTER TABLE events ADD COLUMN owned_operations TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return fmt.Errorf("migrate Muninn store owned operations: %w", err)
		}
		if _, err := store.db.ExecContext(ctx, `ALTER TABLE events ADD COLUMN operation_attribution_ambiguous INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate Muninn store operation attribution: %w", err)
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE metadata SET value = ? WHERE key = 'schema_version'`, fmt.Sprint(sessionStoreSchemaVersion)); err != nil {
			return fmt.Errorf("finish Muninn store migration: %w", err)
		}
	case existing == "2":
		if _, err := store.db.ExecContext(ctx, `ALTER TABLE events ADD COLUMN owned_operations TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return fmt.Errorf("migrate Muninn store owned operations: %w", err)
		}
		if _, err := store.db.ExecContext(ctx, `ALTER TABLE events ADD COLUMN operation_attribution_ambiguous INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate Muninn store operation attribution: %w", err)
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE metadata SET value = ? WHERE key = 'schema_version'`, fmt.Sprint(sessionStoreSchemaVersion)); err != nil {
			return fmt.Errorf("finish Muninn store migration: %w", err)
		}
	case existing == "3":
		if _, err := store.db.ExecContext(ctx, `ALTER TABLE events ADD COLUMN operation_attribution_ambiguous INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate Muninn store operation attribution: %w", err)
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE metadata SET value = ? WHERE key = 'schema_version'`, fmt.Sprint(sessionStoreSchemaVersion)); err != nil {
			return fmt.Errorf("finish Muninn store migration: %w", err)
		}
	case existing == "4":
		if _, err := store.db.ExecContext(ctx, `UPDATE metadata SET value = ? WHERE key = 'schema_version'`, fmt.Sprint(sessionStoreSchemaVersion)); err != nil {
			return fmt.Errorf("finish Muninn store migration: %w", err)
		}
	case existing == "5":
		if _, err := store.db.ExecContext(ctx, `UPDATE metadata SET value = ? WHERE key = 'schema_version'`, fmt.Sprint(sessionStoreSchemaVersion)); err != nil {
			return fmt.Errorf("finish Muninn store migration: %w", err)
		}
	case existing == "6":
		if _, err := store.db.ExecContext(ctx, `UPDATE metadata SET value = ? WHERE key = 'schema_version'`, fmt.Sprint(sessionStoreSchemaVersion)); err != nil {
			return fmt.Errorf("finish Muninn store migration: %w", err)
		}
	case existing == "7":
		if _, err := store.db.ExecContext(ctx, `UPDATE metadata SET value = ? WHERE key = 'schema_version'`, fmt.Sprint(sessionStoreSchemaVersion)); err != nil {
			return fmt.Errorf("finish Muninn store migration: %w", err)
		}
	case existing == "8":
		if _, err := store.db.ExecContext(ctx, `UPDATE metadata SET value = ? WHERE key = 'schema_version'`, fmt.Sprint(sessionStoreSchemaVersion)); err != nil {
			return fmt.Errorf("finish Muninn store migration: %w", err)
		}
	case existing == "9":
		if _, err := store.db.ExecContext(ctx, `UPDATE metadata SET value = ? WHERE key = 'schema_version'`, fmt.Sprint(sessionStoreSchemaVersion)); err != nil {
			return fmt.Errorf("finish Muninn store migration: %w", err)
		}
	case existing == "10":
		if _, err := store.db.ExecContext(ctx, `UPDATE metadata SET value = ? WHERE key = 'schema_version'`, fmt.Sprint(sessionStoreSchemaVersion)); err != nil {
			return fmt.Errorf("finish Muninn store migration: %w", err)
		}
	case existing == "11":
		if _, err := store.db.ExecContext(ctx, `UPDATE metadata SET value = ? WHERE key = 'schema_version'`, fmt.Sprint(sessionStoreSchemaVersion)); err != nil {
			return fmt.Errorf("finish Muninn store migration: %w", err)
		}
	case existing != fmt.Sprint(sessionStoreSchemaVersion):
		return fmt.Errorf("unsupported Muninn store schema version %s (expected %d); remove the local cache to rebuild it", existing, sessionStoreSchemaVersion)
	}
	if reindexSources {
		var continuationColumn int
		if err := store.db.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = 'operation_continues'`,
		).Scan(&continuationColumn); err != nil {
			return fmt.Errorf("inspect Muninn operation continuation state: %w", err)
		}
		if continuationColumn == 0 {
			_, err := store.db.ExecContext(ctx, `ALTER TABLE events ADD COLUMN operation_continues INTEGER NOT NULL DEFAULT 0`)
			if err != nil {
				return fmt.Errorf("migrate Muninn operation continuation state: %w", err)
			}
		}
		var operationTaskColumn int
		if err := store.db.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = 'operation_task'`,
		).Scan(&operationTaskColumn); err != nil {
			return fmt.Errorf("inspect Muninn operation task state: %w", err)
		}
		if operationTaskColumn == 0 {
			_, err := store.db.ExecContext(ctx, `ALTER TABLE events ADD COLUMN operation_task TEXT NOT NULL DEFAULT ''`)
			if err != nil {
				return fmt.Errorf("migrate Muninn operation task state: %w", err)
			}
		}
		if _, err := store.db.ExecContext(ctx, `DELETE FROM sources`); err != nil {
			return fmt.Errorf("invalidate Muninn sources for normalizer update: %w", err)
		}
	}
	return nil
}

func (store *sessionStore) refresh(ctx context.Context, provider string, sessionDirs []string, repositoryRoot string, normalizer sessionNormalizer, ownership ownershipCatalog, force bool) (sessionRefreshStats, error) {
	var stats sessionRefreshStats
	for _, sessionDir := range sessionDirs {
		err := filepath.WalkDir(sessionDir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				stats.FilesUnreadable++
				return nil
			}
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
				return nil
			}
			stats.FilesScanned++
			info, err := entry.Info()
			if err != nil {
				stats.FilesUnreadable++
				return nil
			}
			unchanged, err := store.sourceUnchanged(ctx, provider, path, info.Size(), info.ModTime().UnixNano())
			if err != nil {
				return err
			}
			if unchanged && !force {
				stats.FilesReused++
				return nil
			}
			cwd, err := normalizer.SessionCWD(path)
			if err != nil {
				stats.FilesUnreadable++
				return nil
			}
			inside, err := pathInsideRoot(repositoryRoot, cwd)
			if err != nil || !inside {
				return nil
			}
			session, err := normalizer.NormalizeSession(path)
			if err != nil {
				stats.FilesUnreadable++
				return nil
			}
			session.Provider = provider
			session.SourcePath = path
			for index := range session.Events {
				if session.Events[index].ToolName == "apply_patch" {
					session.Events[index].Targets = normalizeRepositoryEditTargets(session.Events[index].TargetCandidates, session.CWD, repositoryRoot)
				} else {
					session.Events[index].Targets = normalizeRepositoryTargets(session.Events[index].TargetCandidates, session.CWD, repositoryRoot)
				}
				session.Events[index].TargetCandidates = nil
				session.Events[index].OwnedOperations = ownership.classifyOperations(session.Events[index].CommandCandidates)
				session.Events[index].OperationTask = ownership.taskForInvocations(session.Events[index].CommandCandidates)
				session.Events[index].CommandCandidates = nil
			}
			if err := store.replaceSession(ctx, session, info.Size(), info.ModTime().UnixNano()); err != nil {
				return err
			}
			stats.FilesIndexed++
			return nil
		})
		if err != nil {
			return stats, fmt.Errorf("refresh %s sessions in %s: %w", provider, sessionDir, err)
		}
	}
	return stats, nil
}

func (store *sessionStore) sourceUnchanged(ctx context.Context, provider, path string, size, mtimeNS int64) (bool, error) {
	var existingSize, existingMTime int64
	err := store.db.QueryRowContext(
		ctx,
		`SELECT size_bytes, mtime_ns FROM sources WHERE provider = ? AND source_path = ?`,
		provider,
		path,
	).Scan(&existingSize, &existingMTime)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect indexed session source: %w", err)
	}
	return existingSize == size && existingMTime == mtimeNS, nil
}

func (store *sessionStore) replaceSession(ctx context.Context, session normalizedSession, size, mtimeNS int64) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session index transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var sourceID int64
	err = tx.QueryRowContext(
		ctx,
		`INSERT INTO sources(provider, source_path, size_bytes, mtime_ns, indexed_at_ns)
		 VALUES(?, ?, ?, ?, ?)
		 ON CONFLICT(provider, source_path) DO UPDATE SET
		   size_bytes = excluded.size_bytes,
		   mtime_ns = excluded.mtime_ns,
		   indexed_at_ns = excluded.indexed_at_ns
		 RETURNING id`,
		session.Provider,
		session.SourcePath,
		size,
		mtimeNS,
		time.Now().UTC().UnixNano(),
	).Scan(&sourceID)
	if err != nil {
		return fmt.Errorf("upsert indexed session source: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE source_id = ?`, sourceID); err != nil {
		return fmt.Errorf("replace indexed session: %w", err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO sessions(source_id, cwd) VALUES(?, ?)`, sourceID, session.CWD)
	if err != nil {
		return fmt.Errorf("insert indexed session: %w", err)
	}
	sessionID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("resolve indexed session ID: %w", err)
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO events(
		session_id, sequence, occurred_at_ns, kind, tool_name, family, shape,
		first_family, last_family, tool_round, call_occurred_at_ns, failed,
		truncated, output_bytes, failure_reason, failure_context, input_tokens,
		cached_input_tokens, uncached_input_tokens, output_tokens,
		reasoning_tokens, total_tokens, selector_digests, owned_operations,
		operation_task,
		operation_attribution_ambiguous,
		operation_continues, targets, inline_bytes
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare indexed session events: %w", err)
	}
	defer statement.Close()
	for _, event := range session.Events {
		selectorDigests, err := json.Marshal(event.SelectorDigests)
		if err != nil {
			return fmt.Errorf("encode selector digests: %w", err)
		}
		ownedOperations, err := json.Marshal(event.OwnedOperations)
		if err != nil {
			return fmt.Errorf("encode owned operations: %w", err)
		}
		targets, err := json.Marshal(event.Targets)
		if err != nil {
			return fmt.Errorf("encode repository targets: %w", err)
		}
		callOccurredAt := int64(0)
		if !event.CallOccurredAt.IsZero() {
			callOccurredAt = event.CallOccurredAt.UnixNano()
		}
		if _, err := statement.ExecContext(
			ctx,
			sessionID,
			event.Sequence,
			event.OccurredAt.UnixNano(),
			event.Kind,
			event.ToolName,
			event.Family,
			event.Shape,
			event.FirstFamily,
			event.LastFamily,
			event.ToolRound,
			callOccurredAt,
			boolInt(event.Failed),
			boolInt(event.Truncated),
			event.OutputBytes,
			event.FailureReason,
			event.FailureContext,
			event.Tokens.InputTokens,
			event.Tokens.CachedInputTokens,
			event.Tokens.UncachedInputTokens,
			event.Tokens.OutputTokens,
			event.Tokens.ReasoningTokens,
			event.Tokens.TotalTokens,
			string(selectorDigests),
			string(ownedOperations),
			event.OperationTask,
			boolInt(event.OperationAttributionAmbiguous),
			boolInt(event.OperationContinues),
			string(targets),
			event.InlineBytes,
		); err != nil {
			return fmt.Errorf("insert indexed session event: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit indexed session: %w", err)
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (store *sessionStore) analyze(ctx context.Context, provider string, sessionDirs []string, workspaceRoot string, since, generatedAt time.Time, taskFilter string, ownership ownershipCatalog, stats sessionRefreshStats, metadata ...map[string]normalizedSessionMetadata) (codexSessionInsightsReport, error) {
	report := newSessionInsightsReport(provider, sessionDirs, workspaceRoot, since, generatedAt)
	report.Summary.FilesScanned = stats.FilesScanned
	report.Summary.FilesUnreadable = stats.FilesUnreadable

	rows, err := store.db.QueryContext(ctx, `SELECT
		sessions.id, sources.source_path, sessions.cwd, events.sequence,
		events.occurred_at_ns, events.kind, events.tool_name, events.family,
		events.shape, events.first_family, events.last_family, events.tool_round,
		events.call_occurred_at_ns, events.failed, events.truncated,
		events.output_bytes, events.failure_reason, events.failure_context,
		events.input_tokens, events.cached_input_tokens,
		events.uncached_input_tokens, events.output_tokens,
		events.reasoning_tokens, events.total_tokens, events.selector_digests,
		events.owned_operations, events.operation_task,
		events.operation_attribution_ambiguous,
		events.operation_continues, events.targets, events.inline_bytes
		FROM sessions
		JOIN sources ON sources.id = sessions.source_id
		JOIN events ON events.session_id = sessions.id
		WHERE sources.provider = ?
		  AND events.occurred_at_ns >= ?
		  AND events.occurred_at_ns <= ?
		ORDER BY sessions.id, events.sequence`,
		provider,
		since.UnixNano(),
		generatedAt.UnixNano(),
	)
	if err != nil {
		return report, fmt.Errorf("query indexed sessions: %w", err)
	}
	defer rows.Close()

	sessions := map[int64]*normalizedSession{}
	var sessionOrder []int64
	for rows.Next() {
		var (
			sessionID                     int64
			sourcePath                    string
			cwd                           string
			event                         normalizedSessionEvent
			occurredAtNS                  int64
			callOccurredAtNS              int64
			failed                        int
			truncated                     int
			selectorDigests               string
			ownedOperations               string
			operationTask                 string
			operationAttributionAmbiguous int
			operationContinues            int
			targets                       string
		)
		err := rows.Scan(
			&sessionID,
			&sourcePath,
			&cwd,
			&event.Sequence,
			&occurredAtNS,
			&event.Kind,
			&event.ToolName,
			&event.Family,
			&event.Shape,
			&event.FirstFamily,
			&event.LastFamily,
			&event.ToolRound,
			&callOccurredAtNS,
			&failed,
			&truncated,
			&event.OutputBytes,
			&event.FailureReason,
			&event.FailureContext,
			&event.Tokens.InputTokens,
			&event.Tokens.CachedInputTokens,
			&event.Tokens.UncachedInputTokens,
			&event.Tokens.OutputTokens,
			&event.Tokens.ReasoningTokens,
			&event.Tokens.TotalTokens,
			&selectorDigests,
			&ownedOperations,
			&operationTask,
			&operationAttributionAmbiguous,
			&operationContinues,
			&targets,
			&event.InlineBytes,
		)
		if err != nil {
			return report, fmt.Errorf("read indexed session event: %w", err)
		}
		if !pathInsideAnyRoot(sessionDirs, sourcePath) {
			continue
		}
		session := sessions[sessionID]
		if session == nil {
			session = &normalizedSession{Provider: provider, SourcePath: sourcePath, CWD: cwd}
			sessions[sessionID] = session
			sessionOrder = append(sessionOrder, sessionID)
		}
		event.OccurredAt = time.Unix(0, occurredAtNS).UTC()
		if callOccurredAtNS != 0 {
			event.CallOccurredAt = time.Unix(0, callOccurredAtNS).UTC()
		}
		event.Failed = failed != 0
		event.Truncated = truncated != 0
		event.OperationAttributionAmbiguous = operationAttributionAmbiguous != 0
		event.OperationContinues = operationContinues != 0
		_ = json.Unmarshal([]byte(selectorDigests), &event.SelectorDigests)
		_ = json.Unmarshal([]byte(ownedOperations), &event.OwnedOperations)
		event.OperationTask = operationTask
		_ = json.Unmarshal([]byte(targets), &event.Targets)
		session.Events = append(session.Events, event)
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("iterate indexed session events: %w", err)
	}
	if err := rows.Close(); err != nil {
		return report, fmt.Errorf("close indexed session events: %w", err)
	}
	for sessionID, session := range sessions {
		boundaries, err := store.analysisBoundaries(ctx, sessionID, since)
		if err != nil {
			return report, err
		}
		session.Events = append(boundaries, session.Events...)
	}
	sort.Slice(sessionOrder, func(i, j int) bool { return sessionOrder[i] < sessionOrder[j] })
	taskMap := map[string]*codexTaskInsights{}
	for _, sessionID := range sessionOrder {
		session := sessions[sessionID]
		if len(metadata) > 0 {
			enrichNormalizedSession(session, metadata[0])
		}
		record, err := sessionRecordFromNormalized(*session, workspaceRoot, since, generatedAt, ownership)
		if err != nil || record.CWD == "" || record.StartedAt.IsZero() {
			continue
		}
		if taskFilter != "" && record.Task != taskFilter {
			continue
		}
		addCodexSessionToReport(&report, taskMap, record)
	}
	finishSessionInsightsReport(&report, taskMap)
	return report, nil
}

func (store *sessionStore) analysisBoundaries(ctx context.Context, sessionID int64, since time.Time) ([]normalizedSessionEvent, error) {
	bySequence := map[int]normalizedSessionEvent{}
	queries := []string{
		`SELECT sequence, occurred_at_ns, kind, input_tokens, cached_input_tokens,
			uncached_input_tokens, output_tokens, reasoning_tokens, total_tokens
		 FROM events
		 WHERE session_id = ? AND kind = 'token' AND occurred_at_ns < ?
		 ORDER BY sequence DESC LIMIT 1`,
		`SELECT sequence, occurred_at_ns, kind, input_tokens, cached_input_tokens,
			uncached_input_tokens, output_tokens, reasoning_tokens, total_tokens
		 FROM events
		 WHERE session_id = ? AND occurred_at_ns < ?
		 ORDER BY sequence DESC LIMIT 1`,
	}
	for _, query := range queries {
		event := normalizedSessionEvent{}
		var occurredAtNS int64
		err := store.db.QueryRowContext(ctx, query, sessionID, since.UnixNano()).Scan(
			&event.Sequence,
			&occurredAtNS,
			&event.Kind,
			&event.Tokens.InputTokens,
			&event.Tokens.CachedInputTokens,
			&event.Tokens.UncachedInputTokens,
			&event.Tokens.OutputTokens,
			&event.Tokens.ReasoningTokens,
			&event.Tokens.TotalTokens,
		)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read indexed analysis boundary: %w", err)
		}
		event.OccurredAt = time.Unix(0, occurredAtNS).UTC()
		bySequence[event.Sequence] = event
	}
	boundaries := make([]normalizedSessionEvent, 0, len(bySequence))
	for _, event := range bySequence {
		boundaries = append(boundaries, event)
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].Sequence < boundaries[j].Sequence })
	return boundaries, nil
}

func pathInsideAnyRoot(roots []string, path string) bool {
	for _, root := range roots {
		inside, err := pathInsideRoot(root, path)
		if err == nil && inside {
			return true
		}
	}
	return false
}

func (store *sessionStore) saveCheckpoint(ctx context.Context, name, provider, repositoryKey string, report codexSessionInsightsReport) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("checkpoint name cannot be empty")
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode checkpoint: %w", err)
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO checkpoints(
		name, provider, repository_key, created_at_ns, since, report_json
	) VALUES(?, ?, ?, ?, ?, ?)
	ON CONFLICT(name, provider, repository_key) DO UPDATE SET
	  created_at_ns = excluded.created_at_ns,
	  since = excluded.since,
	  report_json = excluded.report_json`,
		name,
		provider,
		repositoryKey,
		time.Now().UTC().UnixNano(),
		report.Since,
		string(raw),
	)
	if err != nil {
		return fmt.Errorf("save Muninn checkpoint: %w", err)
	}
	return nil
}

func (store *sessionStore) loadCheckpoint(ctx context.Context, name, provider, repositoryKey string) (codexSessionInsightsReport, error) {
	var raw string
	err := store.db.QueryRowContext(
		ctx,
		`SELECT report_json FROM checkpoints WHERE name = ? AND provider = ? AND repository_key = ?`,
		name,
		provider,
		repositoryKey,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return codexSessionInsightsReport{}, fmt.Errorf("Muninn checkpoint %q does not exist for this repository", name)
	}
	if err != nil {
		return codexSessionInsightsReport{}, fmt.Errorf("load Muninn checkpoint: %w", err)
	}
	var report codexSessionInsightsReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return codexSessionInsightsReport{}, fmt.Errorf("decode Muninn checkpoint: %w", err)
	}
	return report, nil
}
