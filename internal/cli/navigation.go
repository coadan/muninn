package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var codexPatchTargetPattern = regexp.MustCompile(`(?m)^\*\*\* (?:Add|Update|Delete) File: (.+)$|^\*\*\* Move to: (.+)$`)
var codexNestedToolCallPattern = regexp.MustCompile(`\btools\.([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
var guidanceArtifactIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

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

func codexGuidanceArtifacts(candidates []string, workingDirectories ...string) []normalizedGuidanceArtifact {
	var artifacts []normalizedGuidanceArtifact
	seen := map[string]bool{}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if !strings.EqualFold(filepath.Base(candidate), "SKILL.md") {
			continue
		}
		id := filepath.Base(filepath.Dir(candidate))
		if !guidanceArtifactIDPattern.MatchString(id) || seen[id] {
			continue
		}
		seen[id] = true
		artifacts = append(artifacts, normalizedGuidanceArtifact{
			Kind:            "skill",
			ID:              id,
			EstimatedTokens: guidanceArtifactEstimatedTokens(candidate, workingDirectories),
		})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].ID < artifacts[j].ID })
	return artifacts
}

// ponytail: only literal cat operands can be confirmed from shell success.
// Other readers/options need per-read evidence, not a larger shell interpreter.
func guidanceReadStatusUnconfirmed(toolName, arguments, input string) bool {
	name := strings.ToLower(strings.TrimSpace(toolName))
	if name != "exec" && name != "exec_command" {
		return false
	}
	if name == "exec" && len(codexNestedCommands(input)) > 0 {
		return true
	}
	commands := codexShellCommands(name, arguments, input)
	if len(commands) != 1 {
		return true
	}
	// Reject these even when quoted: reduced coverage is cheaper and safer
	// than interpreting escapes, expansions and shell comments here.
	if strings.ContainsAny(commands[0], "\\\n\r#$`{}*?[]~") {
		return true
	}
	tokens := standaloneForegroundCommandTokens(commands[0])
	if len(tokens) < 2 || filepath.Base(tokens[0]) != "cat" {
		return true
	}
	for _, operand := range tokens[1:] {
		if strings.HasPrefix(operand, "-") {
			return true
		}
	}
	return false
}

func guidanceArtifactEstimatedTokens(candidate string, workingDirectories []string) int64 {
	paths := []string{candidate}
	if !filepath.IsAbs(candidate) {
		paths = paths[:0]
		for _, directory := range workingDirectories {
			if strings.TrimSpace(directory) != "" {
				paths = append(paths, filepath.Join(directory, candidate))
			}
		}
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			return estimatedTokens(info.Size())
		}
	}
	return 0
}

func codexGuidanceArtifactSizes(workspaceRoot string, ids []string) map[string]int64 {
	wanted := map[string]bool{}
	for _, id := range ids {
		if guidanceArtifactIDPattern.MatchString(id) {
			wanted[id] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	sizes := map[string]int64{}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		if home, err := os.UserHomeDir(); err == nil {
			codexHome = filepath.Join(home, ".codex")
		}
	}
	roots := []string{filepath.Join(workspaceRoot, ".codex", "skills")}
	if codexHome != "" {
		roots = append(
			roots,
			filepath.Join(codexHome, "skills"),
			filepath.Join(codexHome, "plugins", "cache"),
		)
	}
	for _, root := range roots {
		collectGuidanceArtifactSizes(root, wanted, sizes)
	}
	return sizes
}

func collectGuidanceArtifactSizes(root string, wanted map[string]bool, sizes map[string]int64) {
	if strings.TrimSpace(root) == "" {
		return
	}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(entry.Name(), "SKILL.md") {
			return nil
		}
		id := filepath.Base(filepath.Dir(path))
		if !wanted[id] {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		tokens := estimatedTokens(info.Size())
		if current, ok := sizes[id]; !ok || tokens > current {
			sizes[id] = tokens
		}
		return nil
	})
}

func codexEditTargetCandidates(toolName, input string) []string {
	if strings.ToLower(strings.TrimSpace(toolName)) != "apply_patch" {
		return nil
	}
	var targets []string
	for _, match := range codexPatchTargetPattern.FindAllStringSubmatch(input, -1) {
		for _, target := range match[1:] {
			target = strings.TrimSpace(target)
			if target != "" {
				targets = appendUniqueString(targets, target)
			}
		}
	}
	return targets
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
	return normalizeRepositoryTargetCandidates(candidates, cwd, repositoryRoot, true)
}

func normalizeRepositoryEditTargets(candidates []string, cwd, repositoryRoot string) []string {
	return normalizeRepositoryTargetCandidates(candidates, cwd, repositoryRoot, false)
}

func repositoryTaskForTargetCandidates(candidates []string, cwd, repositoryRoot string) string {
	task := ""
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
		relative, err := filepath.Rel(repositoryRoot, absolute)
		if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
			continue
		}
		parts := strings.Split(filepath.ToSlash(filepath.Clean(relative)), "/")
		candidateTask := ""
		switch {
		case len(parts) >= 2 && parts[0] == ".worktrees":
			candidateTask = parts[1]
		case len(parts) >= 3 && parts[0] == ".claude" && parts[1] == "worktrees":
			candidateTask = parts[2]
		case len(parts) >= 3 && parts[0] == ".workbench" && parts[1] == "worktrees":
			candidateTask = parts[2]
		}
		if !ownedTaskLabelPattern.MatchString(candidateTask) {
			continue
		}
		if task != "" && task != candidateTask {
			return ""
		}
		task = candidateTask
	}
	return task
}

func repositoryTaskForWorkingDirectory(cwd, repositoryRoot string) string {
	task := codexTaskName(repositoryRoot, cwd)
	if !ownedTaskLabelPattern.MatchString(task) {
		return ""
	}
	return task
}

func normalizeRepositoryTargetCandidates(candidates []string, cwd, repositoryRoot string, requireCurrentFile bool) []string {
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
		relative, err := filepath.Rel(repositoryRoot, absolute)
		if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
			continue
		}
		target := canonicalRepositoryTarget(repositoryRoot, relative)
		// A read in a since-removed task worktree still counts when the
		// canonical repository file exists.
		if requireCurrentFile && !regularFileExists(absolute) &&
			!regularFileExists(filepath.Join(repositoryRoot, filepath.FromSlash(target))) {
			continue
		}
		targets = appendUniqueString(targets, target)
	}
	return targets
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// canonicalRepositoryTarget maps a repository-relative path to the one target
// every task checkout of that file shares. A Claude Code task worktree
// (.claude/worktrees/<task>) is a checkout of the repository itself, so its
// prefix always strips to the in-repository path, also inside a nested
// checkout such as a cached repository's own task worktree.
func canonicalRepositoryTarget(repositoryRoot, relative string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(relative)), "/")
	for index := 0; index+3 < len(parts); index++ {
		if parts[index] == ".claude" && parts[index+1] == "worktrees" {
			parts = append(parts[:index:index], parts[index+3:]...)
			break
		}
	}
	if len(parts) > 0 && parts[0] != ".worktrees" && parts[0] != ".workbench" {
		return strings.Join(parts, "/")
	}
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
	threshold := 4096
	switch name {
	case "exec":
		if codexRoutineCodeModeWrapper(input) {
			return 0
		}
		size = len(input)
	case "exec_command":
		threshold = 2048
		for _, command := range codexShellCommands(toolName, arguments, input) {
			size += len(command)
		}
	default:
		return 0
	}
	if size < threshold {
		return 0
	}
	return int64(size)
}

var (
	inlineScriptFileWritePattern  = regexp.MustCompile(`\.write_(?:text|bytes)\(|\bopen\((?:[^()\n]|\([^()\n]*\))*(?:,\s*|\bmode\s*=\s*)['"][wax]b?\+?['"]|\.open\(\s*['"][wax]b?\+?['"]|\bwriteFileSync\(|\bfs\.(?:promises\.)?writeFile\(|\bsed\s+-i\b|\bperl\s+-[a-z]*i`)
	shellRedirectTargetPattern    = regexp.MustCompile(`(?:^|[^>&0-9])(?:1|&)?>{1,2}\s*([^\s&>;|)]+)`)
	teeOperandsPattern            = regexp.MustCompile(`\btee\b((?:\s+[^\s|;&<>]+)*)`)
	inlineQuotedTextPattern       = regexp.MustCompile(`'(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*"`)
	inlineCommentPattern          = regexp.MustCompile(`(?m)(?:^|\s)(?:#|//).*$`)
	inlineFileModePattern         = regexp.MustCompile(`^['"][wax]b?\+?['"]$`)
	inlineInterpreterPattern      = regexp.MustCompile(`\b(?:python[0-9.]*|node|ruby|perl|bun|deno|php)\b`)
	inlineInterpreterNamePattern  = regexp.MustCompile(`^(?:python[0-9.]*|node|ruby|perl|bun|deno|php)$`)
	inlineTripleQuotedTextPattern = regexp.MustCompile(`(?s)""".*?"""|'''.*?'''`)
	heredocMarkerPattern          = regexp.MustCompile(`<<-?\s*(['"]?)([A-Za-z_][A-Za-z0-9_]*)['"]?`)
)

// inlineExecutableText blanks quoted text, except bare file-mode literals that
// open() needs, and comments, so write syntax that is only mentioned in a
// string or comment is not mistaken for a write.
func inlineExecutableText(command string) string {
	command = inlineTripleQuotedTextPattern.ReplaceAllStringFunc(command, func(quoted string) string {
		return quoted[:3] + quoted[len(quoted)-3:]
	})
	command = inlineQuotedTextPattern.ReplaceAllStringFunc(command, func(quoted string) string {
		if inlineFileModePattern.MatchString(quoted) {
			return quoted
		}
		return quoted[:1] + quoted[len(quoted)-1:]
	})
	return inlineCommentPattern.ReplaceAllString(command, "")
}

// inlineScriptArguments returns each unquoted -c or -e program argument given
// to an interpreter on an executable shell line, so a script passed as one
// shell-quoted argument is examined as code rather than blanked as quoted text.
func inlineScriptArguments(shellLines []string) []string {
	var texts []string
	for _, line := range shellLines {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, segment := range codexShellCommandSegments(line) {
			interpreter := false
			for index := 0; index+1 < len(segment); index++ {
				if inlineInterpreterNamePattern.MatchString(filepath.Base(segment[index])) {
					interpreter = true
				} else if interpreter && (segment[index] == "-c" || segment[index] == "-e") {
					texts = append(texts, segment[index+1])
				}
			}
		}
	}
	return texts
}

// codexInlineFileEdit reports whether an inline-sized shell call writes files:
// a heredoc redirected or teed into a file, or an embedded script or in-place
// editor that writes files. Writing files through the shell is routine harness
// behaviour, not orchestration: such calls are counted apart from inline
// orchestration and never raise a finding of their own.
func codexInlineFileEdit(toolName, arguments, input string) bool {
	for _, command := range codexShellCommands(toolName, arguments, input) {
		command = strings.ReplaceAll(command, "\\\n", " ")
		// Code is the shell's own lines, the bodies of heredocs fed to an
		// interpreter, and -c/-e program arguments. Other heredoc bodies are
		// data, and quoted text and comments are mentions, not operations.
		var code, shellLines []string
		terminator, interpreted := "", false
		for _, line := range strings.Split(command, "\n") {
			if terminator != "" {
				if strings.TrimSpace(line) == terminator {
					terminator = ""
				} else if interpreted {
					code = append(code, line)
				}
				continue
			}
			code = append(code, line)
			shellLines = append(shellLines, line)
			executable := inlineExecutableText(line)
			if !strings.Contains(executable, "<<") {
				continue
			}
			if marker := heredocMarkerPattern.FindStringSubmatch(line); marker != nil {
				terminator = marker[2]
				interpreted = inlineInterpreterPattern.MatchString(executable)
			}
			for _, match := range teeOperandsPattern.FindAllStringSubmatch(executable, -1) {
				for _, operand := range strings.Fields(match[1]) {
					if !strings.HasPrefix(operand, "-") && !strings.HasPrefix(operand, "/dev/") {
						return true
					}
				}
			}
			if !strings.Contains(executable, "cat") {
				continue
			}
			for _, match := range shellRedirectTargetPattern.FindAllStringSubmatch(executable, -1) {
				if !strings.HasPrefix(match[1], "/dev/") {
					return true
				}
			}
		}
		for _, script := range append(inlineScriptArguments(shellLines), strings.Join(code, "\n")) {
			if inlineScriptFileWritePattern.MatchString(inlineExecutableText(script)) {
				return true
			}
		}
	}
	return false
}

func codexRoutineCodeModeWrapper(input string) bool {
	matches := codexNestedToolCallPattern.FindAllStringSubmatch(input, -1)
	if len(matches) == 1 && matches[0][1] == "apply_patch" {
		return true
	}
	if len(matches) < 2 ||
		(!strings.Contains(input, "Promise.allSettled(") && !strings.Contains(input, "Promise.all(")) {
		return false
	}
	for _, match := range matches {
		if match[1] == "apply_patch" {
			return false
		}
	}
	const maximumRoutineBatchBytesPerCall = 8 * 1024
	return len(input)/len(matches) <= maximumRoutineBatchBytesPerCall
}

func codexConcurrentToolBatch(toolName, input string) bool {
	return codexConcurrentToolBatchSize(toolName, input) > 0
}

func codexConcurrentToolBatchSize(toolName, input string) int {
	if strings.ToLower(strings.TrimSpace(toolName)) != "exec" {
		return 0
	}
	matches := codexNestedToolCallPattern.FindAllStringSubmatch(input, -1)
	if len(matches) < 2 ||
		(!strings.Contains(input, "Promise.allSettled(") && !strings.Contains(input, "Promise.all(")) {
		return 0
	}
	return len(matches)
}

func codexNestedToolContext(toolName, input string) string {
	if strings.ToLower(strings.TrimSpace(toolName)) != "exec" {
		return ""
	}
	var names []string
	for _, match := range codexNestedToolCallPattern.FindAllStringSubmatch(input, -1) {
		name := normalizedToolName(strings.ToLower(match[1]))
		names = appendUniqueString(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	if len(names) == 1 {
		return "nested tool " + names[0]
	}
	const shown = 3
	if len(names) <= shown {
		return "nested tools " + strings.Join(names, " + ")
	}
	return fmt.Sprintf(
		"nested tools %s + %d more",
		strings.Join(names[:shown], " + "),
		len(names)-shown,
	)
}
