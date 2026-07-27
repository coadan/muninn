package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var shellCommandSubstitutionExecutablePattern = regexp.MustCompile(`\$\(\s*([A-Za-z0-9._/+-]+)`)
var shellCommandSubstitutionPattern = regexp.MustCompile(`\$\(([^)]*)\)`)
var ownedTaskLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)

type ownedOperationConfig struct {
	ID                     string   `json:"id"`
	Args                   []string `json:"args"`
	ExpectedWait           bool     `json:"expectedWait,omitempty"`
	ExpectedFailureReasons []string `json:"expectedFailureReasons,omitempty"`
}

type ownedToolConfig struct {
	ID                string                 `json:"id"`
	Repository        string                 `json:"repository,omitempty"`
	Executables       []string               `json:"executables,omitempty"`
	ToolCalls         []string               `json:"toolCalls,omitempty"`
	Operations        []ownedOperationConfig `json:"operations,omitempty"`
	OperationsOnly    bool                   `json:"operationsOnly,omitempty"`
	TaskArgumentAfter string                 `json:"taskArgumentAfter,omitempty"`
	Recommendation    string                 `json:"recommendation,omitempty"`
}

type ownershipCatalog struct {
	byDigest         map[string][]string
	operations       []ownedOperationRule
	expectedFailures map[string]map[string]struct{}
	expectedWaits    map[string]struct{}
	taskMarkers      map[string][]string
}

type ownedOperationRule struct {
	ToolID      string
	OperationID string
	Executable  string
	Args        []string
}

type ownedCommandInvocation struct {
	Executable string
	Args       []string
}

func validateOwnedToolConfig(configs []ownedToolConfig) error {
	seen := map[string]struct{}{}
	for _, config := range configs {
		id := strings.TrimSpace(config.ID)
		if id == "" {
			return errors.New("ownedTools entries require a non-empty id")
		}
		if _, exists := seen[id]; exists {
			return errors.New("ownedTools ids must be unique")
		}
		seen[id] = struct{}{}
		if filepath.IsAbs(strings.TrimSpace(config.Repository)) {
			return errors.New("ownedTools repository must be a logical name or relative path, not an absolute path")
		}
		if len(config.Executables) == 0 && len(config.ToolCalls) == 0 {
			return errors.New("ownedTools entries require at least one executable or toolCalls selector")
		}
		if config.OperationsOnly && (len(config.Executables) == 0 || len(config.Operations) == 0) {
			return errors.New("ownedTools operationsOnly requires executables and operations")
		}
		taskMarker := strings.TrimSpace(config.TaskArgumentAfter)
		if taskMarker != "" &&
			(len(config.Executables) == 0 || len(strings.Fields(taskMarker)) != 1 || len(taskMarker) > 80) {
			return errors.New("ownedTools taskArgumentAfter requires executables and one bounded argument token")
		}
		operationIDs := map[string]struct{}{}
		for _, operation := range config.Operations {
			operationID := strings.TrimSpace(operation.ID)
			if operationID == "" || len(operation.Args) == 0 {
				return errors.New("ownedTools operations require a non-empty id and args pattern")
			}
			if _, exists := operationIDs[operationID]; exists {
				return errors.New("ownedTools operation ids must be unique within each tool")
			}
			operationIDs[operationID] = struct{}{}
			expectedReasons := map[string]struct{}{}
			for _, reason := range operation.ExpectedFailureReasons {
				normalized := strings.ToLower(strings.TrimSpace(reason))
				if normalized == "" {
					return errors.New("ownedTools operation expectedFailureReasons must not contain empty values")
				}
				if _, exists := expectedReasons[normalized]; exists {
					return errors.New("ownedTools operation expectedFailureReasons must be unique")
				}
				expectedReasons[normalized] = struct{}{}
			}
		}
	}
	return nil
}

func newOwnershipCatalog(configs []ownedToolConfig) ownershipCatalog {
	catalog := ownershipCatalog{
		byDigest:         map[string][]string{},
		expectedFailures: map[string]map[string]struct{}{},
		expectedWaits:    map[string]struct{}{},
		taskMarkers:      map[string][]string{},
	}
	for _, config := range configs {
		id := strings.TrimSpace(config.ID)
		for _, executable := range config.Executables {
			normalizedExecutable := strings.ToLower(filepath.Base(strings.TrimSpace(executable)))
			if marker := strings.ToLower(strings.TrimSpace(config.TaskArgumentAfter)); marker != "" {
				catalog.taskMarkers[normalizedExecutable] = appendUniqueString(catalog.taskMarkers[normalizedExecutable], marker)
			}
			if !config.OperationsOnly {
				digest := ownershipSelectorDigest("exec", normalizedExecutable)
				catalog.byDigest[digest] = appendUniqueString(catalog.byDigest[digest], id)
			}
			for _, operation := range config.Operations {
				operationID := strings.TrimSpace(operation.ID)
				catalog.operations = append(catalog.operations, ownedOperationRule{
					ToolID:      id,
					OperationID: operationID,
					Executable:  normalizedExecutable,
					Args:        normalizeOperationPattern(operation.Args),
				})
				if len(operation.ExpectedFailureReasons) > 0 {
					key := id + "/" + operationID
					if catalog.expectedFailures[key] == nil {
						catalog.expectedFailures[key] = map[string]struct{}{}
					}
					for _, reason := range operation.ExpectedFailureReasons {
						catalog.expectedFailures[key][strings.ToLower(strings.TrimSpace(reason))] = struct{}{}
					}
				}
				if operation.ExpectedWait {
					catalog.expectedWaits[id+"/"+operationID] = struct{}{}
				}
			}
		}
		for _, toolCall := range config.ToolCalls {
			digest := ownershipSelectorDigest("tool", strings.TrimSpace(toolCall))
			catalog.byDigest[digest] = appendUniqueString(catalog.byDigest[digest], id)
		}
	}
	return catalog
}

func (catalog ownershipCatalog) operationWaitExpected(operation string) bool {
	_, ok := catalog.expectedWaits[operation]
	return ok
}

func (catalog ownershipCatalog) operationFailureExpected(operation, reason string) bool {
	if ownedOperationExpectedFailure(operation, reason) {
		return true
	}
	if catalog.operationWaitExpected(operation) &&
		strings.EqualFold(strings.TrimSpace(reason), "interrupted process") {
		return true
	}
	reasons := catalog.expectedFailures[operation]
	_, ok := reasons[strings.ToLower(strings.TrimSpace(reason))]
	return ok
}

func normalizeOperationPattern(pattern []string) []string {
	normalized := make([]string, 0, len(pattern))
	for _, part := range pattern {
		normalized = append(normalized, strings.ToLower(strings.TrimSpace(part)))
	}
	return normalized
}

func (catalog ownershipCatalog) match(digests []string) []string {
	var matches []string
	for _, digest := range digests {
		for _, id := range catalog.byDigest[digest] {
			matches = appendUniqueString(matches, id)
		}
	}
	sort.Strings(matches)
	return matches
}

func (catalog ownershipCatalog) classifyOperations(invocations []ownedCommandInvocation) []string {
	var matches []string
	for _, invocation := range invocations {
		executable := strings.ToLower(filepath.Base(invocation.Executable))
		args := make([]string, len(invocation.Args))
		for index, arg := range invocation.Args {
			args[index] = strings.ToLower(arg)
		}
		var invocationMatches []ownedOperationRule
		bestSpecificity := ownedOperationSpecificity{}
		for _, rule := range catalog.operations {
			if executable != rule.Executable || !operationPatternMatches(rule.Args, args) {
				continue
			}
			specificity := operationRuleSpecificity(rule)
			switch compareOperationSpecificity(specificity, bestSpecificity) {
			case 1:
				invocationMatches = []ownedOperationRule{rule}
				bestSpecificity = specificity
			case 0:
				invocationMatches = append(invocationMatches, rule)
			}
		}
		for _, rule := range invocationMatches {
			matches = appendUniqueString(matches, rule.ToolID+"/"+rule.OperationID)
		}
	}
	sort.Strings(matches)
	return matches
}

func (catalog ownershipCatalog) taskForInvocations(invocations []ownedCommandInvocation) string {
	tasks := map[string]struct{}{}
	for _, invocation := range invocations {
		executable := strings.ToLower(filepath.Base(invocation.Executable))
		for _, marker := range catalog.taskMarkers[executable] {
			for index := 0; index+1 < len(invocation.Args); index++ {
				if strings.ToLower(strings.TrimSpace(invocation.Args[index])) != marker {
					continue
				}
				task := strings.TrimSpace(invocation.Args[index+1])
				if ownedTaskLabelPattern.MatchString(task) {
					tasks[task] = struct{}{}
				}
			}
		}
	}
	if len(tasks) != 1 {
		return ""
	}
	for task := range tasks {
		return task
	}
	return ""
}

type ownedOperationSpecificity struct {
	Literals int
	Segments int
}

func operationRuleSpecificity(rule ownedOperationRule) ownedOperationSpecificity {
	specificity := ownedOperationSpecificity{}
	for _, part := range rule.Args {
		if part != "**" {
			specificity.Segments++
		}
		if part != "*" && part != "**" {
			specificity.Literals++
		}
	}
	return specificity
}

func compareOperationSpecificity(left, right ownedOperationSpecificity) int {
	if left.Literals != right.Literals {
		if left.Literals > right.Literals {
			return 1
		}
		return -1
	}
	if left.Segments != right.Segments {
		if left.Segments > right.Segments {
			return 1
		}
		return -1
	}
	return 0
}

func operationPatternMatches(pattern, args []string) bool {
	patternIndex := 0
	argsIndex := 0
	starPatternIndex := -1
	starArgsIndex := -1
	for argsIndex < len(args) {
		if patternIndex == len(pattern) {
			return true
		}
		expected := pattern[patternIndex]
		switch {
		case expected == "**":
			starPatternIndex = patternIndex
			starArgsIndex = argsIndex
			patternIndex++
		case expected == "*" || expected == strings.ToLower(args[argsIndex]):
			patternIndex++
			argsIndex++
		case starPatternIndex >= 0:
			starArgsIndex++
			argsIndex = starArgsIndex
			patternIndex = starPatternIndex + 1
		default:
			return false
		}
	}
	for patternIndex < len(pattern) && pattern[patternIndex] == "**" {
		patternIndex++
	}
	return patternIndex == len(pattern)
}

func ownershipSelectorDigest(kind, value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	sum := sha256.Sum256([]byte(kind + "\x00" + normalized))
	return hex.EncodeToString(sum[:])
}

func appendUniqueString(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func codexSelectorDigests(toolName, arguments, input string) []string {
	digests := []string{ownershipSelectorDigest("tool", toolName)}
	for _, command := range codexShellCommands(toolName, arguments, input) {
		for _, segment := range codexShellCommandSegments(command) {
			for _, executable := range codexShellExecutables(segment) {
				digests = appendUniqueString(digests, ownershipSelectorDigest("exec", executable))
			}
			for _, token := range segment {
				for _, match := range shellCommandSubstitutionExecutablePattern.FindAllStringSubmatch(token, -1) {
					if len(match) > 1 {
						digests = appendUniqueString(digests, ownershipSelectorDigest("exec", filepath.Base(match[1])))
					}
				}
			}
		}
	}
	return digests
}

func codexCommandInvocations(toolName, arguments, input string) []ownedCommandInvocation {
	var invocations []ownedCommandInvocation
	for _, command := range codexShellCommands(toolName, arguments, input) {
		invocations = append(invocations, commandInvocations(command)...)
		for _, match := range shellCommandSubstitutionPattern.FindAllStringSubmatch(command, -1) {
			if len(match) > 1 {
				invocations = append(invocations, commandInvocations(match[1])...)
			}
		}
	}
	return invocations
}

func commandInvocations(command string) []ownedCommandInvocation {
	var invocations []ownedCommandInvocation
	for _, segment := range codexShellCommandSegments(command) {
		tokens := unwrapShellTokens(segment)
		if len(tokens) == 0 {
			continue
		}
		executable := strings.ToLower(filepath.Base(tokens[0]))
		if executable == "eval" || executable == "cd" || executable == "export" ||
			executable == "source" || executable == "." || executable == "unset" ||
			executable == "set" || executable == "true" {
			continue
		}
		if executable == "bash" || executable == "zsh" || executable == "sh" {
			for index := 1; index < len(tokens); index++ {
				if !strings.HasPrefix(tokens[index], "-") {
					break
				}
				if !strings.HasPrefix(tokens[index], "--") && strings.Contains(strings.TrimPrefix(tokens[index], "-"), "c") && index+1 < len(tokens) {
					invocations = append(invocations, commandInvocations(tokens[index+1])...)
					break
				}
			}
			continue
		}
		invocations = append(invocations, ownedCommandInvocation{
			Executable: executable,
			Args:       append([]string(nil), tokens[1:]...),
		})
	}
	return invocations
}

func codexShellExecutables(tokens []string) []string {
	for len(tokens) > 0 && codexShellAssignment(tokens[0]) {
		tokens = tokens[1:]
	}
	if len(tokens) == 0 {
		return nil
	}
	executable := strings.ToLower(filepath.Base(tokens[0]))
	switch executable {
	case "env", "command", "time", "sudo":
		return codexShellExecutables(tokens[1:])
	case "bash", "zsh", "sh":
		for index := 1; index < len(tokens); index++ {
			token := tokens[index]
			if token == "--" || token == "-" || !strings.HasPrefix(token, "-") {
				break
			}
			if !strings.HasPrefix(token, "--") && strings.Contains(strings.TrimPrefix(token, "-"), "c") && index+1 < len(tokens) {
				var executables []string
				for _, nested := range codexShellCommandSegments(tokens[index+1]) {
					for _, candidate := range codexShellExecutables(nested) {
						executables = appendUniqueString(executables, candidate)
					}
				}
				return executables
			}
		}
	}
	return []string{executable}
}
