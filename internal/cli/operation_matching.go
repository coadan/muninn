package cli

import (
	"path/filepath"
	"sort"
	"strings"
)

func normalizeOperationPattern(pattern []string) []string {
	normalized := make([]string, 0, len(pattern))
	for _, part := range pattern {
		normalized = append(normalized, strings.ToLower(strings.TrimSpace(part)))
	}
	return normalized
}

func (catalog ownershipCatalog) classifyOperations(invocations []ownedCommandInvocation) []string {
	return classifyOperationRules(catalog.operations, invocations)
}

func (catalog ownershipCatalog) classifyBypassedOperations(invocations []ownedCommandInvocation) []string {
	return classifyOperationRules(catalog.bypassOperations, invocations)
}

func classifyOperationRules(rules []ownedOperationRule, invocations []ownedCommandInvocation) []string {
	var matches []string
	for _, invocation := range invocations {
		executable := strings.ToLower(filepath.Base(invocation.Executable))
		args := make([]string, len(invocation.Args))
		for index, arg := range invocation.Args {
			args[index] = strings.ToLower(arg)
		}
		var invocationMatches []ownedOperationRule
		bestSpecificity := ownedOperationSpecificity{}
		for _, rule := range rules {
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
