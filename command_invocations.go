package main

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
)

var shellCommandSubstitutionExecutablePattern = regexp.MustCompile(`\$\(\s*([A-Za-z0-9._/+-]+)`)
var shellCommandSubstitutionPattern = regexp.MustCompile(`\$\(([^)]*)\)`)

type ownedCommandInvocation struct {
	Executable string
	Args       []string
}

func ownershipSelectorDigest(kind, value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	sum := sha256.Sum256([]byte(kind + "\x00" + normalized))
	return hex.EncodeToString(sum[:])
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
