package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sort"
	"strings"
)

type ownedToolConfig struct {
	ID             string   `json:"id"`
	Repository     string   `json:"repository,omitempty"`
	Executables    []string `json:"executables,omitempty"`
	ToolCalls      []string `json:"toolCalls,omitempty"`
	Recommendation string   `json:"recommendation,omitempty"`
}

type ownershipCatalog struct {
	byDigest map[string][]string
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
	}
	return nil
}

func newOwnershipCatalog(configs []ownedToolConfig) ownershipCatalog {
	catalog := ownershipCatalog{byDigest: map[string][]string{}}
	for _, config := range configs {
		id := strings.TrimSpace(config.ID)
		for _, executable := range config.Executables {
			digest := ownershipSelectorDigest("exec", filepath.Base(strings.TrimSpace(executable)))
			catalog.byDigest[digest] = appendUniqueString(catalog.byDigest[digest], id)
		}
		for _, toolCall := range config.ToolCalls {
			digest := ownershipSelectorDigest("tool", strings.TrimSpace(toolCall))
			catalog.byDigest[digest] = appendUniqueString(catalog.byDigest[digest], id)
		}
	}
	return catalog
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
		}
	}
	return digests
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
