package cli

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
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
	comparisonResolved    bool
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
			if path != root && (ignoredInstructionDirectory(entry.Name()) ||
				claudeWorktreesDirectory(filepath.Base(filepath.Dir(path)), entry.Name())) {
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
	footprint.comparisonResolved = true
	sort.Slice(footprint.Files, func(i, j int) bool {
		return footprint.Files[i].Path < footprint.Files[j].Path
	})
	return footprint
}

func inspectRepositoryInstructionsAt(
	root,
	provider string,
	at time.Time,
) (repositoryInstructionFootprint, bool) {
	commitOutput, err := exec.Command(
		"git", "-C", root, "rev-list", "-1", "--before="+at.UTC().Format(time.RFC3339), "HEAD",
	).Output()
	if err != nil || strings.TrimSpace(string(commitOutput)) == "" {
		return repositoryInstructionFootprint{}, false
	}
	commit := strings.TrimSpace(string(commitOutput))
	treeOutput, err := exec.Command("git", "-C", root, "ls-tree", "-r", "-l", "-z", commit).Output()
	if err != nil {
		return repositoryInstructionFootprint{}, false
	}
	var footprint repositoryInstructionFootprint
	for _, entry := range strings.Split(string(treeOutput), "\x00") {
		metadata, path, ok := strings.Cut(entry, "\t")
		if !ok || ignoredInstructionPath(path) || !repositoryInstructionFileName(filepath.Base(path), provider) {
			continue
		}
		fields := strings.Fields(metadata)
		if len(fields) < 4 || fields[1] != "blob" {
			continue
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil || size < 0 {
			continue
		}
		path = filepath.ToSlash(path)
		scope := "scoped"
		if !strings.Contains(path, "/") {
			scope = "root"
			footprint.RootFiles++
			footprint.RootBytes += size
		} else {
			footprint.ScopedFiles++
			footprint.ScopedBytes += size
		}
		footprint.Files = append(footprint.Files, repositoryInstructionFile{
			Path: path, Scope: scope, Bytes: size, EstimatedTokens: estimatedTokens(size),
		})
	}
	footprint.RootEstimatedTokens = estimatedTokens(footprint.RootBytes)
	footprint.ScopedEstimatedTokens = estimatedTokens(footprint.ScopedBytes)
	footprint.comparisonResolved = true
	sort.Slice(footprint.Files, func(i, j int) bool { return footprint.Files[i].Path < footprint.Files[j].Path })
	return footprint, true
}

func ignoredInstructionPath(path string) bool {
	parent := ""
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if ignoredInstructionDirectory(part) || claudeWorktreesDirectory(parent, part) {
			return true
		}
		parent = part
	}
	return false
}

// claudeWorktreesDirectory matches Claude Code's task checkouts. Only that
// subdirectory is ignored: .claude/CLAUDE.md is a legitimate instruction file.
func claudeWorktreesDirectory(parent, name string) bool {
	return parent == ".claude" && name == "worktrees"
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
	case ".cache", ".git", ".midgard", ".next", ".workbench", ".worktrees", "build", "coverage", "dist", "node_modules", "target", "vendor":
		return true
	default:
		return false
	}
}
