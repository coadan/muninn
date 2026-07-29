package main

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var codexNestedCommandStartPattern = regexp.MustCompile(`(?:^|[,{]\s*)(?:"cmd"|'cmd'|cmd)\s*:\s*`)

func codexShellCommandFamily(toolName, arguments, input string) string {
	family, _ := codexShellCommandAnalysis(toolName, arguments, input)
	return family
}

func codexShellCommandAnalysis(toolName, arguments, input string) (string, string) {
	family, shape, _, _ := codexShellCommandDetails(toolName, arguments, input)
	return family, shape
}

func codexShellCommandDetails(toolName, arguments, input string) (string, string, string, string) {
	commands := codexShellCommands(toolName, arguments, input)
	if len(commands) == 0 {
		return "", "", "", ""
	}
	var sequence []string
	families := make(map[string]struct{})
	for _, command := range commands {
		for _, segment := range codexShellCommandSegments(command) {
			for _, family := range codexShellSegmentFamilySequence(segment) {
				families[family] = struct{}{}
				if len(sequence) == 0 || sequence[len(sequence)-1] != family {
					sequence = append(sequence, family)
				}
			}
		}
	}
	family := codexCombinedShellFamily(families)
	first := ""
	last := ""
	if len(sequence) > 0 {
		first = sequence[0]
		last = sequence[len(sequence)-1]
	}
	if family != "mixed shell" {
		return family, "", first, last
	}
	const maxShapeFamilies = 5
	if len(sequence) > maxShapeFamilies {
		sequence = append(append([]string(nil), sequence[:maxShapeFamilies]...), "additional families")
	}
	return family, strings.Join(sequence, " -> "), first, last
}

func codexShellCommands(toolName, arguments, input string) []string {
	normalizedName := strings.ToLower(strings.TrimSpace(toolName))
	if normalizedName != "exec" && normalizedName != "exec_command" {
		return nil
	}
	var commands []string
	if normalizedName == "exec_command" {
		var decoded struct {
			Command string `json:"cmd"`
		}
		if json.Unmarshal([]byte(arguments), &decoded) == nil && decoded.Command != "" {
			commands = append(commands, decoded.Command)
		} else {
			commands = append(commands, arguments)
		}
	} else {
		commands = codexNestedCommands(input)
		if len(commands) == 0 {
			if strings.Contains(input, "tools.") {
				return nil
			}
			commands = append(commands, input)
		}
	}
	return commands
}

func codexNestedCommands(input string) []string {
	var commands []string
	for _, location := range codexNestedCommandStartPattern.FindAllStringIndex(input, -1) {
		if command, _, ok := codexJavaScriptString(input, location[1]); ok {
			commands = append(commands, command)
		}
	}
	return commands
}

func codexJavaScriptString(input string, start int) (string, int, bool) {
	if start >= len(input) || (input[start] != '"' && input[start] != '\'' && input[start] != '`') {
		return "", start, false
	}
	quote := input[start]
	var decoded strings.Builder
	for index := start + 1; index < len(input); index++ {
		current := input[index]
		if current == quote {
			return decoded.String(), index + 1, true
		}
		if current != '\\' {
			decoded.WriteByte(current)
			continue
		}
		index++
		if index >= len(input) {
			return "", start, false
		}
		escaped := input[index]
		switch escaped {
		case '\n':
			continue
		case 'n':
			decoded.WriteByte('\n')
		case 'r':
			decoded.WriteByte('\r')
		case 't':
			decoded.WriteByte('\t')
		case 'b':
			decoded.WriteByte('\b')
		case 'f':
			decoded.WriteByte('\f')
		case 'v':
			decoded.WriteByte('\v')
		case 'x', 'u':
			digits := 2
			if escaped == 'u' {
				digits = 4
			}
			if index+digits >= len(input) {
				return "", start, false
			}
			value, err := strconv.ParseUint(input[index+1:index+1+digits], 16, 32)
			if err != nil {
				return "", start, false
			}
			decoded.WriteRune(rune(value))
			index += digits
		default:
			decoded.WriteByte(escaped)
		}
	}
	return "", start, false
}

func codexShellSegmentsFamily(segments [][]string) string {
	families := make(map[string]struct{})
	for _, segment := range segments {
		if family := codexShellSegmentFamily(segment); family != "" {
			families[family] = struct{}{}
		}
	}
	return codexCombinedShellFamily(families)
}

func codexShellSegmentFamilySequence(tokens []string) []string {
	for len(tokens) > 0 && codexShellAssignment(tokens[0]) {
		tokens = tokens[1:]
	}
	if len(tokens) == 0 {
		return nil
	}
	executable := strings.ToLower(filepath.Base(tokens[0]))
	if executable == "env" || executable == "command" || executable == "time" || executable == "sudo" {
		return codexShellSegmentFamilySequence(tokens[1:])
	}
	if executable == "bash" || executable == "zsh" || executable == "sh" {
		for index := 1; index < len(tokens); index++ {
			token := tokens[index]
			if token == "--" || token == "-" || !strings.HasPrefix(token, "-") {
				break
			}
			if !strings.HasPrefix(token, "--") && strings.Contains(strings.TrimPrefix(token, "-"), "c") && index+1 < len(tokens) {
				var sequence []string
				for _, segment := range codexShellCommandSegments(tokens[index+1]) {
					sequence = append(sequence, codexShellSegmentFamilySequence(segment)...)
				}
				return sequence
			}
		}
		return []string{"other shell"}
	}
	if family := codexShellSegmentFamily(tokens); family != "" {
		return []string{family}
	}
	return nil
}

func codexCombinedShellFamily(families map[string]struct{}) string {
	if len(families) == 0 {
		return "other shell"
	}
	if len(families) > 1 {
		return "mixed shell"
	}
	for family := range families {
		return family
	}
	return "other shell"
}

func codexShellCommandSegments(command string) [][]string {
	var segments [][]string
	var tokens []string
	var token strings.Builder
	var quote rune
	escaped := false
	flushToken := func() {
		if token.Len() > 0 {
			tokens = append(tokens, token.String())
			token.Reset()
		}
	}
	flushSegment := func() {
		flushToken()
		if len(tokens) > 0 {
			segments = append(segments, tokens)
			tokens = nil
		}
	}
	for _, current := range command {
		if escaped {
			token.WriteRune(current)
			escaped = false
			continue
		}
		if current == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if current == quote {
				quote = 0
			} else {
				token.WriteRune(current)
			}
			continue
		}
		switch current {
		case '\'', '"', '`':
			quote = current
		case ' ', '\t', '\r':
			flushToken()
		case '\n', ';', '|', '&':
			flushSegment()
		default:
			token.WriteRune(current)
		}
	}
	if escaped {
		token.WriteByte('\\')
	}
	flushSegment()
	return segments
}

func codexShellSegmentFamily(tokens []string) string {
	for len(tokens) > 0 && codexShellAssignment(tokens[0]) {
		tokens = tokens[1:]
	}
	if len(tokens) == 0 {
		return ""
	}
	executable := strings.ToLower(filepath.Base(tokens[0]))
	if executable == "env" || executable == "command" || executable == "time" || executable == "sudo" {
		return codexShellSegmentFamily(tokens[1:])
	}
	if executable == "bash" || executable == "zsh" || executable == "sh" {
		for index := 1; index < len(tokens); index++ {
			token := tokens[index]
			if token == "--" || token == "-" || !strings.HasPrefix(token, "-") {
				break
			}
			if !strings.HasPrefix(token, "--") && strings.Contains(strings.TrimPrefix(token, "-"), "c") && index+1 < len(tokens) {
				return codexShellSegmentsFamily(codexShellCommandSegments(tokens[index+1]))
			}
		}
		return "other shell"
	}
	lowerTokens := make([]string, len(tokens))
	for index, token := range tokens {
		lowerTokens[index] = strings.ToLower(token)
	}
	switch executable {
	case "rg", "grep", "egrep", "fgrep":
		if executable == "rg" && len(lowerTokens) > 1 && lowerTokens[1] == "--files" {
			return "file reads"
		}
		return "search"
	case "sed", "head", "tail", "cat", "find", "tree", "wc", "ls":
		return "file reads"
	case "git":
		subcommand := codexGitSubcommand(lowerTokens[1:])
		switch subcommand {
		case "push":
			return "delivery"
		case "revert":
			return "revert"
		case "diff", "status", "log", "show", "branch", "rev-parse", "rev-list", "merge-base", "ls-files", "grep":
			return "git inspect"
		}
	case "go":
		if len(lowerTokens) > 1 {
			if lowerTokens[1] == "test" {
				return "tests"
			}
			if lowerTokens[1] == "vet" || lowerTokens[1] == "build" {
				return "build, lint, or install"
			}
		}
	case "clj", "clojure", "lein", "bb":
		for _, token := range lowerTokens[1:] {
			if strings.Contains(token, "test") {
				return "tests"
			}
		}
	case "pytest":
		return "tests"
	case "node", "deno", "bun":
		for _, token := range lowerTokens[1:] {
			if token == "-e" || token == "--eval" || token == "-p" || token == "--print" {
				return "inline runtime"
			}
		}
	case "python", "python3", "ruby":
		for _, token := range lowerTokens[1:] {
			if token == "-c" || token == "-e" {
				return "inline runtime"
			}
		}
	case "sqlite3", "psql", "duckdb":
		return "database query CLI"
	case "spacetime":
		for _, token := range lowerTokens[1:] {
			if token == "sql" {
				return "database query CLI"
			}
		}
	case "cargo":
		if len(lowerTokens) > 1 && lowerTokens[1] == "test" {
			return "tests"
		}
		if len(lowerTokens) > 1 && (lowerTokens[1] == "build" || lowerTokens[1] == "clippy") {
			return "build, lint, or install"
		}
	case "npm":
		if len(lowerTokens) > 1 && lowerTokens[1] == "test" {
			return "tests"
		}
		if len(lowerTokens) > 2 && lowerTokens[1] == "run" && (lowerTokens[2] == "build" || lowerTokens[2] == "lint") {
			return "build, lint, or install"
		}
	case "make":
		if len(lowerTokens) > 1 && (lowerTokens[1] == "install" || lowerTokens[1] == "build") {
			return "build, lint, or install"
		}
	case "codex":
		if len(lowerTokens) > 1 && lowerTokens[1] == "review" {
			return "review"
		}
	case "clj-kondo", "golangci-lint":
		return "build, lint, or install"
	case "heimdal", "playwright", "playwright-cli":
		if len(lowerTokens) > 1 && executable == "heimdal" && lowerTokens[1] == "run" {
			return "tests"
		}
		return "browser QA"
	case "bwb":
		for index, token := range lowerTokens {
			if token == "inspect" && index > 0 {
				return "bounded task inspect"
			}
			if token == "test" || token == "integration" {
				return "tests"
			}
		}
		return "other bwb task"
	}
	return "other shell"
}

func codexShellAssignment(token string) bool {
	name, _, found := strings.Cut(token, "=")
	if !found || name == "" {
		return false
	}
	for index, current := range name {
		if !(current == '_' || current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' || index > 0 && current >= '0' && current <= '9') {
			return false
		}
	}
	return true
}

func codexGitSubcommand(tokens []string) string {
	for index := 0; index < len(tokens); index++ {
		token := tokens[index]
		if token == "-c" || token == "-C" {
			index++
			continue
		}
		if strings.HasPrefix(token, "-") {
			continue
		}
		return token
	}
	return ""
}
