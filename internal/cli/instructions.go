package cli

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

type repositoryInstructionFile struct {
	Path            string `json:"path"`
	Scope           string `json:"scope"`
	Bytes           int64  `json:"bytes"`
	EstimatedTokens int64  `json:"estimatedTokens"`
}

type repositoryInstructionFootprint struct {
	Files                 []repositoryInstructionFile `json:"files,omitempty"`
	RootFiles             int                         `json:"rootFiles"`
	ScopedFiles           int                         `json:"scopedFiles"`
	RootBytes             int64                       `json:"rootBytes"`
	ScopedBytes           int64                       `json:"scopedBytes"`
	RootEstimatedTokens   int64                       `json:"rootEstimatedTokens"`
	ScopedEstimatedTokens int64                       `json:"scopedEstimatedTokens"`
}

func inspectRepositoryInstructions(root, provider string) repositoryInstructionFootprint {
	var footprint repositoryInstructionFootprint
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if path != root && ignoredInstructionDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !repositoryInstructionFileName(entry.Name(), provider) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
			return nil
		}
		relative = filepath.ToSlash(relative)
		scope := "scoped"
		if !strings.Contains(relative, "/") {
			scope = "root"
			footprint.RootFiles++
			footprint.RootBytes += info.Size()
		} else {
			footprint.ScopedFiles++
			footprint.ScopedBytes += info.Size()
		}
		footprint.Files = append(footprint.Files, repositoryInstructionFile{
			Path:            relative,
			Scope:           scope,
			Bytes:           info.Size(),
			EstimatedTokens: estimatedTokens(info.Size()),
		})
		return nil
	})
	footprint.RootEstimatedTokens = estimatedTokens(footprint.RootBytes)
	footprint.ScopedEstimatedTokens = estimatedTokens(footprint.ScopedBytes)
	sort.Slice(footprint.Files, func(i, j int) bool {
		return footprint.Files[i].Path < footprint.Files[j].Path
	})
	return footprint
}

func repositoryInstructionFileName(name, provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex", "opencode":
		return strings.EqualFold(strings.TrimSpace(name), "AGENTS.md")
	case "claude", "claude-code":
		return strings.EqualFold(strings.TrimSpace(name), "CLAUDE.md")
	}
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "AGENTS.MD", "CLAUDE.MD":
		return true
	default:
		return false
	}
}

func ignoredInstructionDirectory(name string) bool {
	switch name {
	case ".cache", ".git", ".next", ".workbench", ".worktrees", "build", "coverage", "dist", "node_modules", "target", "vendor":
		return true
	default:
		return false
	}
}
