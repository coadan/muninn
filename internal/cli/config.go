package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var suppressedSignalPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,199}$`)

type repositoryConfig struct {
	SchemaVersion   int      `json:"schemaVersion"`
	SuppressSignals []string `json:"suppressSignals,omitempty"`
	Actions         struct {
		SourceContext       string `json:"sourceContext"`
		RecurringFailure    string `json:"recurringFailure"`
		AgentInterface      string `json:"agentInterface"`
		CodeStructure       string `json:"codeStructure"`
		SessionLoop         string `json:"sessionLoop"`
		InlineOrchestration string `json:"inlineOrchestration"`
		YieldedOperation    string `json:"yieldedOperation"`
	} `json:"actions"`
	OwnedTools []ownedToolConfig `json:"ownedTools"`
}

func defaultRepositoryConfig() repositoryConfig {
	config := repositoryConfig{SchemaVersion: 1}
	config.Actions.SourceContext = "Use or add one bounded repository source-context command that combines ranked search pointers with small excerpts."
	config.Actions.RecurringFailure = "Reproduce the shared failure once, then fix the owned tool/default or add concise repository guidance instead of repeating per-session workarounds."
	config.Actions.AgentInterface = "Consider one compact agent-facing command that owns bootstrap, state, bounded output, and recovery for this repeated workflow."
	config.Actions.CodeStructure = "Inspect whether this owner mixes responsibilities; split or add a stable routed entry point when the repeated reads reflect real ownership boundaries."
	config.Actions.SessionLoop = "Save progress, start a focused continuation, and remove repeated rediscovery or validation loops from the repository workflow."
	config.Actions.InlineOrchestration = "Extract the repeated orchestration into a tested repository helper or agent-facing CLI command."
	config.Actions.YieldedOperation = "Use the repository's bounded command for this workflow, resume every yielded process to a terminal result, and explicitly terminate work that should not continue."
	return config
}

func loadRepositoryConfig(repoRoot, explicit string) (repositoryConfig, error) {
	config := defaultRepositoryConfig()
	path := strings.TrimSpace(explicit)
	required := path != ""
	if path == "" {
		path = filepath.Join(repoRoot, ".muninn.json")
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !required {
			return config, nil
		}
		return repositoryConfig{}, fmt.Errorf("read Muninn config %s: %w", path, err)
	}
	var decoded repositoryConfig
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return repositoryConfig{}, fmt.Errorf("parse Muninn config %s: %w", path, err)
	}
	if decoded.SchemaVersion != 1 {
		return repositoryConfig{}, fmt.Errorf("unsupported Muninn config schemaVersion %d in %s", decoded.SchemaVersion, path)
	}
	if strings.TrimSpace(decoded.Actions.SourceContext) != "" {
		config.Actions.SourceContext = strings.TrimSpace(decoded.Actions.SourceContext)
	}
	if strings.TrimSpace(decoded.Actions.RecurringFailure) != "" {
		config.Actions.RecurringFailure = strings.TrimSpace(decoded.Actions.RecurringFailure)
	}
	if strings.TrimSpace(decoded.Actions.AgentInterface) != "" {
		config.Actions.AgentInterface = strings.TrimSpace(decoded.Actions.AgentInterface)
	}
	if strings.TrimSpace(decoded.Actions.CodeStructure) != "" {
		config.Actions.CodeStructure = strings.TrimSpace(decoded.Actions.CodeStructure)
	}
	if strings.TrimSpace(decoded.Actions.SessionLoop) != "" {
		config.Actions.SessionLoop = strings.TrimSpace(decoded.Actions.SessionLoop)
	}
	if strings.TrimSpace(decoded.Actions.InlineOrchestration) != "" {
		config.Actions.InlineOrchestration = strings.TrimSpace(decoded.Actions.InlineOrchestration)
	}
	if strings.TrimSpace(decoded.Actions.YieldedOperation) != "" {
		config.Actions.YieldedOperation = strings.TrimSpace(decoded.Actions.YieldedOperation)
	}
	if err := validateOwnedToolConfig(decoded.OwnedTools); err != nil {
		return repositoryConfig{}, fmt.Errorf("parse Muninn config %s: %w", path, err)
	}
	suppressSignals, err := normalizeSuppressedSignals(decoded.SuppressSignals)
	if err != nil {
		return repositoryConfig{}, fmt.Errorf("parse Muninn config %s: %w", path, err)
	}
	config.SuppressSignals = suppressSignals
	config.OwnedTools = decoded.OwnedTools
	return config, nil
}

func normalizeSuppressedSignals(signals []string) ([]string, error) {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(signals))
	for index, signal := range signals {
		signal = strings.TrimSpace(signal)
		if signal == "" {
			return nil, fmt.Errorf("suppressSignals[%d] must not be empty", index)
		}
		if !suppressedSignalPattern.MatchString(signal) || strings.Contains(signal, "..") || strings.Contains(signal, "//") {
			return nil, fmt.Errorf("suppressSignals[%d] must be an exact printed signal ID", index)
		}
		if _, exists := seen[signal]; exists {
			continue
		}
		seen[signal] = struct{}{}
		result = append(result, signal)
	}
	sort.Strings(result)
	return result, nil
}
