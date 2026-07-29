package cli

import (
	"fmt"
	"strings"
)

func filterSessionFindings(findings []sessionFinding, focus string) ([]sessionFinding, error) {
	focus = strings.ToLower(strings.TrimSpace(focus))
	if focus == "" || focus == "friction" {
		return findings, nil
	}
	allowed := map[string]map[string]bool{
		"tooling": {
			"owned-tool":         true,
			"owned-operation":    true,
			"recurring-failure":  true,
			"diagnostic-failure": true,
			"output-cost":        true,
		},
		"instructions": {
			"instruction-discovery": true,
			"instruction-footprint": true,
		},
		"interface": {
			"agent-interface": true,
		},
		"structure": {
			"code-structure": true,
		},
		"discovery": {
			"discovery":             true,
			"instruction-discovery": true,
		},
		"failures": {
			"recurring-failure":  true,
			"diagnostic-failure": true,
		},
		"loops": {
			"agent-interface": true,
			"session-loop":    true,
			"delegation-cost": true,
		},
		"output": {
			"output-cost": true,
		},
		"quality": {
			"delivery-quality": true,
			"task-cost":        true,
			"delegation-cost":  true,
		},
	}
	categories, ok := allowed[focus]
	if !ok {
		return nil, fmt.Errorf("unsupported --focus %q (available: friction, tooling, instructions, interface, structure, discovery, failures, loops, output, quality)", focus)
	}
	filtered := make([]sessionFinding, 0, len(findings))
	for _, finding := range findings {
		if categories[finding.Category] || supportingSignalsMatchCategories(finding.Supporting, categories) {
			filtered = append(filtered, finding)
		}
	}
	return filtered, nil
}

func supportingSignalsMatchCategories(signals []string, categories map[string]bool) bool {
	for _, signal := range signals {
		category, _, _ := strings.Cut(signal, "/")
		if categories[category] {
			return true
		}
	}
	return false
}
