package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var providerLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

type normalizedSessionMetadata struct {
	Model            string
	ReasoningEffort  string
	AgentKind        string
	LineageKey       string
	ParentLineageKey string
	SpawnStatus      string
}

func loadCodexSessionMetadata(sessionDirs []string) map[string]normalizedSessionMetadata {
	path := newestCodexStateDB(sessionDirs)
	if path == "" {
		return nil
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.Query(`SELECT
		threads.id,
		threads.rollout_path,
		COALESCE(threads.model, ''),
		COALESCE(threads.reasoning_effort, ''),
		COALESCE(threads.thread_source, threads.source, ''),
		COALESCE(thread_spawn_edges.parent_thread_id, ''),
		COALESCE(thread_spawn_edges.status, '')
		FROM threads
		LEFT JOIN thread_spawn_edges
		  ON thread_spawn_edges.child_thread_id = threads.id
		WHERE threads.rollout_path <> ''`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	metadata := map[string]normalizedSessionMetadata{}
	for rows.Next() {
		var threadID, rolloutPath, model, effort, source, parentID, status string
		if rows.Scan(&threadID, &rolloutPath, &model, &effort, &source, &parentID, &status) != nil {
			continue
		}
		entry := normalizedSessionMetadata{
			Model:           normalizedProviderLabel(model),
			ReasoningEffort: normalizedProviderLabel(effort),
			AgentKind:       "root",
			LineageKey:      ownershipSelectorDigest("provider-thread", threadID),
			SpawnStatus:     normalizedSpawnStatus(status),
		}
		if parentID != "" {
			entry.AgentKind = "subagent"
			entry.ParentLineageKey = ownershipSelectorDigest("provider-thread", parentID)
		} else if strings.Contains(strings.ToLower(source), "subagent") {
			entry.AgentKind = "subagent"
		}
		if entry.AgentKind == "subagent" && entry.ParentLineageKey == "" {
			lineageKey, parentLineageKey := readCodexRolloutLineage(rolloutPath)
			if lineageKey != "" {
				entry.LineageKey = lineageKey
			}
			if parentLineageKey != "" {
				entry.ParentLineageKey = parentLineageKey
			}
		}
		metadata[filepath.Clean(rolloutPath)] = entry
	}
	return metadata
}

func newestCodexStateDB(sessionDirs []string) string {
	var candidates []string
	seen := map[string]struct{}{}
	for _, sessionDir := range sessionDirs {
		parent := filepath.Dir(filepath.Clean(sessionDir))
		matches, _ := filepath.Glob(filepath.Join(parent, "state_*.sqlite"))
		for _, match := range matches {
			if _, exists := seen[match]; exists {
				continue
			}
			seen[match] = struct{}{}
			candidates = append(candidates, match)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		leftInfo, leftErr := os.Stat(candidates[i])
		rightInfo, rightErr := os.Stat(candidates[j])
		if leftErr == nil && rightErr == nil &&
			!leftInfo.ModTime().Equal(rightInfo.ModTime()) {
			return leftInfo.ModTime().After(rightInfo.ModTime())
		}
		return candidates[i] > candidates[j]
	})
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func normalizedProviderLabel(value string) string {
	value = strings.TrimSpace(value)
	if providerLabelPattern.MatchString(value) {
		return value
	}
	return "(unknown)"
}

func normalizedSpawnStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pending", "running", "completed", "failed", "cancelled", "interrupted", "closed":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "(unknown)"
	}
}

func enrichNormalizedSession(session *normalizedSession, metadata map[string]normalizedSessionMetadata) {
	if session == nil || len(metadata) == 0 {
		return
	}
	entry, ok := metadata[filepath.Clean(session.SourcePath)]
	if !ok {
		return
	}
	if entry.Model != "" {
		session.Model = entry.Model
	}
	if entry.ReasoningEffort != "" {
		session.ReasoningEffort = entry.ReasoningEffort
	}
	if entry.AgentKind != "" {
		session.AgentKind = entry.AgentKind
	}
	if entry.LineageKey != "" {
		session.LineageKey = entry.LineageKey
	}
	if entry.ParentLineageKey != "" {
		session.ParentLineageKey = entry.ParentLineageKey
	}
	if entry.SpawnStatus != "" {
		session.SpawnStatus = entry.SpawnStatus
	}
}
