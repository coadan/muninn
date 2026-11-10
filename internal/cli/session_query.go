package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

func (store *sessionStore) analyze(ctx context.Context, provider string, sessionDirs []string, workspaceRoot string, since, generatedAt time.Time, taskFilter string, ownership ownershipCatalog, stats sessionRefreshStats, metadata ...map[string]normalizedSessionMetadata) (codexSessionInsightsReport, error) {
	report := newSessionInsightsReport(provider, sessionDirs, workspaceRoot, since, generatedAt)
	report.Summary.FilesScanned = stats.FilesScanned
	report.Summary.FilesUnreadable = stats.FilesUnreadable

	rows, err := store.db.QueryContext(ctx, `SELECT
		sessions.id, sources.source_path, sessions.cwd, sessions.model,
		sessions.reasoning_effort, sessions.agent_kind, sessions.lineage_key,
		sessions.parent_lineage_key, sessions.spawn_status,
		sessions.unrecognized_records, sessions.unrecognized_content_blocks,
		events.sequence,
		events.occurred_at_ns, events.kind, events.tool_name, events.family,
		events.shape, events.nested_tool_context, events.first_family,
		events.last_family, events.tool_round,
		events.call_occurred_at_ns, events.failed, events.refusal, events.truncated,
		events.output_bytes, events.failure_reason, events.failure_context,
		events.input_tokens, events.cached_input_tokens,
		events.uncached_input_tokens, events.output_tokens,
		events.reasoning_tokens, events.total_tokens,
		events.context_tokens, events.context_window_tokens, events.compaction_trigger, events.provider_cost_usd,
		events.provider_api_duration_ms, events.provider_tool_duration_ms,
		events.selector_digests,
		events.owned_operations, events.bypassed_operations,
		events.owned_flags, events.owned_flag_tools,
		events.operation_task,
		events.operation_attribution_ambiguous,
		events.operation_continues, events.targets, events.guidance_artifacts, events.inline_bytes,
		events.concurrent_batch, events.concurrent_batch_size,
		events.diagnostic_json,
		events.working_directories, events.in_repository_scope
		FROM sessions
		JOIN sources ON sources.id = sessions.source_id
		JOIN events ON events.session_id = sessions.id
		WHERE sources.provider = ?
		  AND sources.scope_key = ?
		  AND events.occurred_at_ns >= ?
		  AND events.occurred_at_ns <= ?
		ORDER BY sessions.id, events.sequence`,
		provider,
		repositoryStoreScopeKey(workspaceRoot, ownership),
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
			sessionMetadata               normalizedSession
			event                         normalizedSessionEvent
			occurredAtNS                  int64
			callOccurredAtNS              int64
			failed                        int
			truncated                     int
			selectorDigests               string
			ownedOperations               string
			bypassedOperations            string
			ownedFlags                    string
			ownedFlagScopes               string
			operationTask                 string
			operationAttributionAmbiguous int
			operationContinues            int
			concurrentBatch               int
			concurrentBatchSize           int
			inRepositoryScope             int
			targets                       string
			guidanceArtifacts             string
			diagnosticJSON                string
			workingDirectories            string
		)
		err := rows.Scan(
			&sessionID,
			&sourcePath,
			&cwd,
			&sessionMetadata.Model,
			&sessionMetadata.ReasoningEffort,
			&sessionMetadata.AgentKind,
			&sessionMetadata.LineageKey,
			&sessionMetadata.ParentLineageKey,
			&sessionMetadata.SpawnStatus,
			&sessionMetadata.UnrecognizedRecords,
			&sessionMetadata.UnrecognizedContentBlocks,
			&event.Sequence,
			&occurredAtNS,
			&event.Kind,
			&event.ToolName,
			&event.Family,
			&event.Shape,
			&event.NestedToolContext,
			&event.FirstFamily,
			&event.LastFamily,
			&event.ToolRound,
			&callOccurredAtNS,
			&failed,
			&event.Refusal,
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
			&event.ContextTokens,
			&event.ContextWindowTokens,
			&event.CompactionTrigger,
			&event.Cost.CostUSD,
			&event.Cost.APIDurationMs,
			&event.Cost.ToolDurationMs,
			&selectorDigests,
			&ownedOperations,
			&bypassedOperations,
			&ownedFlags,
			&ownedFlagScopes,
			&operationTask,
			&operationAttributionAmbiguous,
			&operationContinues,
			&targets,
			&guidanceArtifacts,
			&event.InlineBytes,
			&concurrentBatch,
			&concurrentBatchSize,
			&diagnosticJSON,
			&workingDirectories,
			&inRepositoryScope,
		)
		if err != nil {
			return report, fmt.Errorf("read indexed session event: %w", err)
		}
		if !pathInsideAnyRoot(sessionDirs, sourcePath) {
			continue
		}
		session := sessions[sessionID]
		if session == nil {
			session = &sessionMetadata
			session.Provider, session.SourcePath, session.CWD = provider, sourcePath, cwd
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
		event.ConcurrentBatch = concurrentBatch != 0
		event.ConcurrentBatchSize = concurrentBatchSize
		event.InRepositoryScope = inRepositoryScope != 0
		event.RepositoryScopeKnown = true
		if diagnosticJSON != "" {
			var diagnostic normalizedDiagnosticObservation
			if json.Unmarshal([]byte(diagnosticJSON), &diagnostic) == nil {
				event.Diagnostic = &diagnostic
			}
		}
		_ = json.Unmarshal([]byte(selectorDigests), &event.SelectorDigests)
		_ = json.Unmarshal([]byte(ownedOperations), &event.OwnedOperations)
		_ = json.Unmarshal([]byte(bypassedOperations), &event.BypassedOperations)
		_ = json.Unmarshal([]byte(ownedFlags), &event.OwnedFlags)
		_ = json.Unmarshal([]byte(ownedFlagScopes), &event.OwnedFlagScopes)
		event.OperationTask = operationTask
		_ = json.Unmarshal([]byte(targets), &event.Targets)
		_ = json.Unmarshal([]byte(guidanceArtifacts), &event.GuidanceArtifacts)
		_ = json.Unmarshal([]byte(workingDirectories), &event.WorkingDirectories)
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
	selection := `SELECT sequence, occurred_at_ns, kind, tool_name, input_tokens, cached_input_tokens,
		uncached_input_tokens, output_tokens, reasoning_tokens, total_tokens,
		context_tokens, context_window_tokens, compaction_trigger,
		provider_cost_usd, provider_api_duration_ms, provider_tool_duration_ms,
		working_directories, in_repository_scope FROM events `
	queries := []string{
		selection + `
		 WHERE session_id = ? AND kind = 'token' AND occurred_at_ns < ?
		 ORDER BY sequence DESC LIMIT 1`,
		selection + `
		 WHERE session_id = ? AND kind = 'provider_cost' AND occurred_at_ns < ?
		 ORDER BY sequence DESC LIMIT 1`,
		selection + `
		 WHERE session_id = ? AND occurred_at_ns < ?
		   AND kind <> 'provider_cost'
		 ORDER BY sequence DESC LIMIT 1`,
		selection + `
		 WHERE session_id = ? AND occurred_at_ns < ?
		   AND (
		     working_directories <> '[]'
		     OR (kind = 'tool_call' AND tool_name IN ('exec', 'exec_command'))
		   )
		 ORDER BY sequence DESC LIMIT 1`,
		selection + `WHERE session_id = ? AND kind = 'token' AND context_tokens > 0 AND occurred_at_ns < ?
		 ORDER BY sequence ASC LIMIT 1`,
		selection + `WHERE session_id = ? AND kind = 'token' AND context_tokens > 0 AND occurred_at_ns < ?
		 ORDER BY sequence DESC LIMIT 1`,
		selection + `WHERE session_id = ? AND kind = 'compaction' AND occurred_at_ns < ?
		 ORDER BY sequence DESC LIMIT 1`,
		// A later re-entry alone cannot establish context continuity. Retain
		// the last outside event so an older sample cannot bridge a scope gap.
		selection + `WHERE session_id = ? AND in_repository_scope = 0 AND occurred_at_ns < ?
		 ORDER BY sequence DESC LIMIT 1`,
	}
	for _, query := range queries {
		event := normalizedSessionEvent{}
		var occurredAtNS int64
		var workingDirectories string
		var inRepositoryScope int
		err := store.db.QueryRowContext(ctx, query, sessionID, since.UnixNano()).Scan(
			&event.Sequence,
			&occurredAtNS,
			&event.Kind,
			&event.ToolName,
			&event.Tokens.InputTokens,
			&event.Tokens.CachedInputTokens,
			&event.Tokens.UncachedInputTokens,
			&event.Tokens.OutputTokens,
			&event.Tokens.ReasoningTokens,
			&event.Tokens.TotalTokens,
			&event.ContextTokens,
			&event.ContextWindowTokens,
			&event.CompactionTrigger,
			&event.Cost.CostUSD,
			&event.Cost.APIDurationMs,
			&event.Cost.ToolDurationMs,
			&workingDirectories,
			&inRepositoryScope,
		)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read indexed analysis boundary: %w", err)
		}
		event.OccurredAt = time.Unix(0, occurredAtNS).UTC()
		_ = json.Unmarshal([]byte(workingDirectories), &event.WorkingDirectories)
		event.InRepositoryScope = inRepositoryScope != 0
		event.RepositoryScopeKnown = true
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
