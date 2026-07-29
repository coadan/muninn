package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadRepositoryConfigUsesGenericDefaultsAndRepositoryOverride(t *testing.T) {
	root := t.TempDir()
	config, err := loadRepositoryConfig(root, "")
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	if config.SchemaVersion != 1 || !strings.Contains(config.Actions.SourceContext, "bounded repository source-context") {
		t.Fatalf("unexpected default config: %#v", config)
	}
	override := `{
		"schemaVersion": 1,
		"actions": {
			"sourceContext": "Use repo context.",
			"yieldedOperation": "Use the managed test command."
		},
		"suppressSignals": [
			"session-loop/progress-stall/bwb/api-start",
			"session-loop/progress-stall/bwb/api-start"
		],
		"ownedTools": [{
			"id": "bwb",
			"repository": "breyta-workbench",
			"executables": ["bwb"],
			"taskArgumentAfter": "task",
			"operations": [{
				"id": "comments-wait",
				"args": ["comments", "--wait"],
				"expectedWait": true,
				"expectedFailureReasons": ["timeout"]
			}],
			"recommendation": "Improve the local CLI first."
		}]
	}`
	if err := os.WriteFile(filepath.Join(root, ".muninn.json"), []byte(override), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config, err = loadRepositoryConfig(root, "")
	if err != nil {
		t.Fatalf("load repository config: %v", err)
	}
	if config.Actions.SourceContext != "Use repo context." {
		t.Fatalf("repository action override missing: %#v", config)
	}
	if config.Actions.YieldedOperation != "Use the managed test command." {
		t.Fatalf("yielded-operation action override missing: %#v", config)
	}
	if len(config.OwnedTools) != 1 || config.OwnedTools[0].ID != "bwb" {
		t.Fatalf("owned tooling config missing: %#v", config)
	}
	if config.OwnedTools[0].TaskArgumentAfter != "task" {
		t.Fatalf("owned task argument marker missing: %#v", config.OwnedTools[0])
	}
	if got := config.OwnedTools[0].Operations[0].ExpectedFailureReasons; !reflect.DeepEqual(got, []string{"timeout"}) {
		t.Fatalf("expected failure reasons missing: %#v", got)
	}
	if !config.OwnedTools[0].Operations[0].ExpectedWait {
		t.Fatalf("expected wait marker missing: %#v", config.OwnedTools[0].Operations[0])
	}
	if len(config.SuppressSignals) != 1 || config.SuppressSignals[0] != "session-loop/progress-stall/bwb/api-start" {
		t.Fatalf("signal suppressions were not normalized: %#v", config.SuppressSignals)
	}
}

func TestLoadRepositoryConfigRejectsInvalidTaskArgumentMarker(t *testing.T) {
	root := t.TempDir()
	override := `{
		"schemaVersion": 1,
		"ownedTools": [{
			"id": "bwb",
			"executables": ["bwb"],
			"taskArgumentAfter": "two tokens"
		}]
	}`
	if err := os.WriteFile(filepath.Join(root, ".muninn.json"), []byte(override), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := loadRepositoryConfig(root, ""); err == nil || !strings.Contains(err.Error(), "taskArgumentAfter") {
		t.Fatalf("invalid task argument marker error=%v", err)
	}
}

func TestLoadRepositoryConfigRejectsInvalidExpectedFailureReasons(t *testing.T) {
	for _, reasons := range []string{`[""]`, `["timeout", " TIMEOUT "]`} {
		root := t.TempDir()
		override := `{
			"schemaVersion": 1,
			"ownedTools": [{
				"id": "bwb",
				"executables": ["bwb"],
				"operations": [{
					"id": "comments-wait",
					"args": ["comments", "--wait"],
					"expectedFailureReasons": ` + reasons + `
				}]
			}]
		}`
		if err := os.WriteFile(filepath.Join(root, ".muninn.json"), []byte(override), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		if _, err := loadRepositoryConfig(root, ""); err == nil || !strings.Contains(err.Error(), "expectedFailureReasons") {
			t.Fatalf("invalid expected failure reasons %s error=%v", reasons, err)
		}
	}
}

func TestLoadRepositoryConfigRejectsEmptySuppressedSignal(t *testing.T) {
	root := t.TempDir()
	override := `{"schemaVersion":1,"suppressSignals":["  "]}`
	if err := os.WriteFile(filepath.Join(root, ".muninn.json"), []byte(override), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := loadRepositoryConfig(root, ""); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty suppression error=%v want actionable validation", err)
	}
}

func TestNormalizeSuppressedSignalsRejectsNonSignalText(t *testing.T) {
	if _, err := normalizeSuppressedSignals([]string{"Progress stall: /private/path"}); err == nil ||
		!strings.Contains(err.Error(), "exact printed signal ID") {
		t.Fatalf("invalid signal error=%v want exact-ID guidance", err)
	}
}
