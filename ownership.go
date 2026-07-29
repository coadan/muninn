package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var ownedTaskLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)

type ownershipCatalog struct {
	byDigest         map[string][]string
	operations       []ownedOperationRule
	expectedFailures map[string]map[string]struct{}
	expectedWaits    map[string]struct{}
	taskMarkers      map[string][]string
	cacheKey         string
}

type ownedOperationRule struct {
	ToolID      string
	OperationID string
	Executable  string
	Args        []string
}

type ownershipIndexConfig struct {
	ID                string                          `json:"id"`
	Executables       []string                        `json:"executables,omitempty"`
	Operations        []ownershipIndexOperationConfig `json:"operations,omitempty"`
	TaskArgumentAfter string                          `json:"taskArgumentAfter,omitempty"`
}

type ownershipIndexOperationConfig struct {
	ID   string   `json:"id"`
	Args []string `json:"args"`
}

func newOwnershipCatalog(configs []ownedToolConfig) ownershipCatalog {
	indexConfigs := make([]ownershipIndexConfig, 0, len(configs))
	for _, config := range configs {
		indexConfig := ownershipIndexConfig{
			ID:                config.ID,
			Executables:       config.Executables,
			TaskArgumentAfter: config.TaskArgumentAfter,
		}
		for _, operation := range config.Operations {
			indexConfig.Operations = append(indexConfig.Operations, ownershipIndexOperationConfig{
				ID:   operation.ID,
				Args: operation.Args,
			})
		}
		indexConfigs = append(indexConfigs, indexConfig)
	}
	encoded, _ := json.Marshal(indexConfigs)
	configDigest := sha256.Sum256(encoded)
	catalog := ownershipCatalog{
		byDigest:         map[string][]string{},
		expectedFailures: map[string]map[string]struct{}{},
		expectedWaits:    map[string]struct{}{},
		taskMarkers:      map[string][]string{},
		cacheKey:         hex.EncodeToString(configDigest[:8]),
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
