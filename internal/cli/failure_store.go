package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

func (store *sessionStore) ownedOperationFailures(
	ctx context.Context,
	provider string,
	repositoryRoot string,
	ownership ownershipCatalog,
	since time.Time,
	operation string,
	reason string,
	task string,
	limit int,
) ([]ownedOperationFailureEvent, error) {
	repositoryRoot = filepath.Clean(repositoryRoot)
	scopeKey := repositoryStoreScopeKey(repositoryRoot, ownership)
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
	  AND sources.scope_key = ?
	  AND events.in_repository_scope = 1
	  AND events.failed = 1
	  AND events.occurred_at_ns >= ?
	  AND operation.value = ?`
	args := []any{
		provider,
		scopeKey,
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
