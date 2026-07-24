package main

import (
	"os"
	"path/filepath"
	"strings"
)

func codexReadTargetCandidates(toolName, arguments, input string) []string {
	var candidates []string
	for _, command := range codexShellCommands(toolName, arguments, input) {
		for _, segment := range codexShellCommandSegments(command) {
			tokens := unwrapShellTokens(segment)
			if len(tokens) == 0 {
				continue
			}
			switch strings.ToLower(filepath.Base(tokens[0])) {
			case "cat", "head", "tail", "sed":
				for _, token := range tokens[1:] {
					if token == "" || strings.HasPrefix(token, "-") {
						continue
					}
					candidates = appendUniqueString(candidates, token)
				}
			}
		}
	}
	return candidates
}

func unwrapShellTokens(tokens []string) []string {
	for len(tokens) > 0 && codexShellAssignment(tokens[0]) {
		tokens = tokens[1:]
	}
	for len(tokens) > 0 {
		switch strings.ToLower(filepath.Base(tokens[0])) {
		case "env", "command", "time", "sudo":
			tokens = tokens[1:]
			for len(tokens) > 0 && (codexShellAssignment(tokens[0]) || strings.HasPrefix(tokens[0], "-")) {
				tokens = tokens[1:]
			}
		default:
			return tokens
		}
	}
	return nil
}

func normalizeRepositoryTargets(candidates []string, cwd, repositoryRoot string) []string {
	var targets []string
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || strings.ContainsAny(candidate, "*?[]{}") {
			continue
		}
		path := candidate
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		inside, err := pathInsideRoot(repositoryRoot, absolute)
		if err != nil || !inside {
			continue
		}
		info, err := os.Stat(absolute)
		if err != nil || info.IsDir() {
			continue
		}
		relative, err := filepath.Rel(repositoryRoot, absolute)
		if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
			continue
		}
		targets = appendUniqueString(targets, canonicalRepositoryTarget(repositoryRoot, relative))
	}
	return targets
}

func canonicalRepositoryTarget(repositoryRoot, relative string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(relative)), "/")
	repo := ""
	remainder := []string(nil)
	switch {
	case len(parts) >= 4 && parts[0] == ".worktrees":
		repo = parts[2]
		remainder = parts[3:]
	case len(parts) >= 5 && parts[0] == ".workbench" && parts[1] == "worktrees":
		repo = parts[3]
		remainder = parts[4:]
	}
	if repo != "" && dirExists(filepath.Join(repositoryRoot, ".workbench", "repos", repo)) {
		return strings.Join(append([]string{".workbench", "repos", repo}, remainder...), "/")
	}
	return strings.Join(parts, "/")
}

func codexInlineOrchestrationBytes(toolName, arguments, input string) int64 {
	name := strings.ToLower(strings.TrimSpace(toolName))
	var size int
	switch name {
	case "exec":
		size = len(input)
	case "exec_command":
		for _, command := range codexShellCommands(toolName, arguments, input) {
			size += len(command)
		}
	default:
		return 0
	}
	const inlineOrchestrationThreshold = 4096
	if size < inlineOrchestrationThreshold {
		return 0
	}
	return int64(size)
}
