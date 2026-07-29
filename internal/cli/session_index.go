package cli

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type sessionRefreshStats struct {
	FilesScanned    int
	FilesUnreadable int
	FilesIndexed    int
	FilesReused     int
	FilesPruned     int
}

func (store *sessionStore) refresh(ctx context.Context, provider string, discovery sessionDiscovery, repositoryRoot string, normalizer sessionNormalizer, ownership ownershipCatalog, force bool) (sessionRefreshStats, error) {
	stats := sessionRefreshStats{
		FilesScanned:    len(discovery.Files),
		FilesUnreadable: discovery.FilesUnreadable,
	}
	scopeKey := repositoryStoreScopeKey(repositoryRoot, ownership)
	for _, path := range discovery.Files {
		info, err := os.Stat(path)
		if err != nil {
			stats.FilesUnreadable++
			continue
		}
		unchanged, err := store.sourceUnchanged(ctx, provider, scopeKey, path, info.Size(), info.ModTime().UnixNano())
		if err != nil {
			return stats, err
		}
		if unchanged && !force {
			stats.FilesReused++
			continue
		}
		session, err := normalizer.NormalizeSession(path)
		if err != nil {
			stats.FilesUnreadable++
			continue
		}
		session.Provider = provider
		session.SourcePath = path
		if !normalizedSessionTouchesRepository(session, repositoryRoot) {
			if err := store.replaceSession(ctx, scopeKey, session, info.Size(), info.ModTime().UnixNano(), false); err != nil {
				return stats, err
			}
			continue
		}
		markRepositoryEventScope(&session, repositoryRoot)
		for index := range session.Events {
			eventCWD := eventRepositoryWorkingDirectory(session.Events[index], session.CWD, repositoryRoot)
			if session.Events[index].ToolName == "apply_patch" {
				if session.Events[index].OperationTask == "" {
					session.Events[index].OperationTask = repositoryTaskForTargetCandidates(
						session.Events[index].TargetCandidates,
						eventCWD,
						repositoryRoot,
					)
				}
				session.Events[index].Targets = normalizeRepositoryEditTargets(session.Events[index].TargetCandidates, eventCWD, repositoryRoot)
			} else {
				session.Events[index].Targets = normalizeRepositoryTargets(session.Events[index].TargetCandidates, eventCWD, repositoryRoot)
			}
			session.Events[index].TargetCandidates = nil
			session.Events[index].OwnedOperations = ownership.classifyOperations(session.Events[index].CommandCandidates)
			if session.Events[index].OperationTask == "" {
				session.Events[index].OperationTask = ownership.taskForInvocations(session.Events[index].CommandCandidates)
			}
			session.Events[index].CommandCandidates = nil
		}
		if err := store.replaceSession(ctx, scopeKey, session, info.Size(), info.ModTime().UnixNano(), true); err != nil {
			return stats, err
		}
		stats.FilesIndexed++
	}
	if discovery.FilesUnreadable == 0 {
		pruned, err := store.pruneMissingSources(ctx, provider, scopeKey, discovery.Files)
		if err != nil {
			return stats, err
		}
		stats.FilesPruned = pruned
	}
	return stats, nil
}

func repositoryStoreScopeKey(repositoryRoot string, ownership ownershipCatalog) string {
	sum := sha256.Sum256([]byte(filepath.Clean(repositoryRoot) + "\x00" + ownership.cacheKey))
	return fmt.Sprintf("%x", sum[:8])
}

func (store *sessionStore) sourceUnchanged(ctx context.Context, provider, scopeKey, path string, size, mtimeNS int64) (bool, error) {
	var existingSize, existingMTime int64
	err := store.db.QueryRowContext(
		ctx,
		`SELECT size_bytes, mtime_ns FROM sources WHERE provider = ? AND scope_key = ? AND source_path = ?`,
		provider,
		scopeKey,
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

func (store *sessionStore) pruneMissingSources(
	ctx context.Context,
	provider,
	scopeKey string,
	discoveredFiles []string,
) (int, error) {
	discovered := make(map[string]struct{}, len(discoveredFiles))
	for _, path := range discoveredFiles {
		discovered[filepath.Clean(path)] = struct{}{}
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin stale session pruning: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(
		ctx,
		`SELECT source_path FROM sources WHERE provider = ? AND scope_key = ?`,
		provider,
		scopeKey,
	)
	if err != nil {
		return 0, fmt.Errorf("query indexed session sources: %w", err)
	}
	var missing []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan indexed session source: %w", err)
		}
		if _, exists := discovered[filepath.Clean(path)]; !exists {
			missing = append(missing, path)
		}
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close indexed session sources: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read indexed session sources: %w", err)
	}
	pruned := 0
	for _, path := range missing {
		result, err := tx.ExecContext(
			ctx,
			`DELETE FROM sources WHERE provider = ? AND scope_key = ? AND source_path = ?`,
			provider,
			scopeKey,
			path,
		)
		if err != nil {
			return 0, fmt.Errorf("delete stale indexed session source: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("count pruned session sources: %w", err)
		}
		pruned += int(count)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit stale session pruning: %w", err)
	}
	return pruned, nil
}

func (store *sessionStore) replaceSession(
	ctx context.Context,
	scopeKey string,
	session normalizedSession,
	size,
	mtimeNS int64,
	include bool,
) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session index transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var sourceID int64
	err = tx.QueryRowContext(
		ctx,
		`INSERT INTO sources(provider, scope_key, source_path, size_bytes, mtime_ns, indexed_at_ns)
		 VALUES(?, ?, ?, ?, ?, ?)
		 ON CONFLICT(provider, scope_key, source_path) DO UPDATE SET
		   size_bytes = excluded.size_bytes,
		   mtime_ns = excluded.mtime_ns,
		   indexed_at_ns = excluded.indexed_at_ns
		 RETURNING id`,
		session.Provider,
		scopeKey,
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
	if !include {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit excluded session index: %w", err)
		}
		return nil
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
		operation_continues, targets, inline_bytes, concurrent_batch,
		diagnostic_json, working_directories, in_repository_scope
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
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
		workingDirectories, err := json.Marshal(event.WorkingDirectories)
		if err != nil {
			return fmt.Errorf("encode working directories: %w", err)
		}
		callOccurredAt := int64(0)
		if !event.CallOccurredAt.IsZero() {
			callOccurredAt = event.CallOccurredAt.UnixNano()
		}
		diagnosticJSON := ""
		if event.Diagnostic != nil {
			raw, err := json.Marshal(event.Diagnostic)
			if err != nil {
				return fmt.Errorf("encode diagnostic failure: %w", err)
			}
			diagnosticJSON = string(raw)
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
			boolInt(event.ConcurrentBatch),
			diagnosticJSON,
			string(workingDirectories),
			boolInt(event.InRepositoryScope),
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
