package cli

import (
	"errors"
	"path/filepath"
	"strings"
)

type ownedOperationConfig struct {
	ID                     string                     `json:"id"`
	Args                   []string                   `json:"args"`
	Kind                   string                     `json:"kind,omitempty"`
	BypassPatterns         []ownedBypassPatternConfig `json:"bypassPatterns,omitempty"`
	ExpectedWait           bool                       `json:"expectedWait,omitempty"`
	ExpectedFailureReasons []string                   `json:"expectedFailureReasons,omitempty"`
}

type ownedBypassPatternConfig struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
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
			switch operation.Kind {
			case "", "help", "search", "verification-focused", "verification-broad":
			default:
				return errors.New("ownedTools operation kind must be help, search, verification-focused, or verification-broad")
			}
			for _, pattern := range operation.BypassPatterns {
				executable := strings.TrimSpace(pattern.Executable)
				if executable == "" || filepath.Base(executable) != executable ||
					len(strings.Fields(executable)) != 1 || len(pattern.Args) == 0 {
					return errors.New("ownedTools operation bypassPatterns require one executable name and a non-empty args pattern")
				}
			}
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
