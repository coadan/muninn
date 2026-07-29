package cli

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

func discoverCodexSessionSource(explicit string, includeArchived bool) (sessionDiscovery, error) {
	resolved, err := resolveCodexSessionsDir(explicit)
	if err != nil {
		return sessionDiscovery{}, err
	}
	dirs := []string{resolved}
	if includeArchived {
		archivedDir := filepath.Join(filepath.Dir(resolved), "archived_sessions")
		if dirExists(archivedDir) {
			dirs = append(dirs, archivedDir)
		}
	}
	return discoverCodexSessions(dirs)
}

func discoverCodexSessions(sessionDirs []string) (sessionDiscovery, error) {
	discovery := sessionDiscovery{Dirs: append([]string(nil), sessionDirs...)}
	for _, sessionDir := range sessionDirs {
		err := filepath.WalkDir(sessionDir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				discovery.FilesUnreadable++
				return nil
			}
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
				return nil
			}
			discovery.Files = append(discovery.Files, path)
			return nil
		})
		if err != nil {
			return sessionDiscovery{}, fmt.Errorf("scan Codex sessions in %s: %w", sessionDir, err)
		}
	}
	sort.Strings(discovery.Files)
	return discovery, nil
}
