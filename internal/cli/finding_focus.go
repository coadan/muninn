package cli

import (
	"fmt"
	"strings"
)

var sessionFocusCategories = map[string]map[string]bool{
	"tooling": {
		"owned-tool":          true,
		"owned-operation":     true,
		"operation-chain":     true,
		"diagnostic-contract": true,
		"recovery-loop":       true,
		"default-candidate":   true,
		"recurring-failure":   true,
		"diagnostic-failure":  true,
		"output-cost":         true,
	},
	"instructions": {
		"instruction-discovery": true,
		"instruction-footprint": true,
	},
	"interface": {
		"agent-interface":   true,
		"default-candidate": true,
		"operation-chain":   true,
	},
	"structure": {
		"code-structure": true,
	},
	"discovery": {
		"discovery":             true,
		"instruction-discovery": true,
	},
	"failures": {
		"recurring-failure":   true,
		"diagnostic-failure":  true,
		"diagnostic-contract": true,
		"recovery-loop":       true,
	},
	"loops": {
		"agent-interface":   true,
		"recovery-loop":     true,
		"session-loop":      true,
		"verification-loop": true,
		"delegation-cost":   true,
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

var preferredSessionFindingFocus = map[string]string{
	"owned-tool":            "tooling",
	"owned-operation":       "tooling",
	"default-candidate":     "interface",
	"recurring-failure":     "failures",
	"diagnostic-failure":    "failures",
	"output-cost":           "output",
	"instruction-discovery": "instructions",
	"instruction-footprint": "instructions",
	"agent-interface":       "interface",
	"operation-chain":       "interface",
	"diagnostic-contract":   "failures",
	"recovery-loop":         "failures",
	"code-structure":        "structure",
	"discovery":             "discovery",
	"session-loop":          "loops",
	"verification-loop":     "loops",
	"delegation-cost":       "quality",
	"delivery-quality":      "quality",
	"task-cost":             "quality",
}

func filterSessionFindings(findings []sessionFinding, focus string) ([]sessionFinding, error) {
	focus = strings.ToLower(strings.TrimSpace(focus))
	if focus == "" || focus == "friction" {
		return findings, nil
	}
	categories, ok := sessionFocusCategories[focus]
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

func sessionFindingFocus(finding sessionFinding) string {
	return preferredSessionFindingFocus[finding.Category]
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
