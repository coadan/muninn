package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailuresUsesPositionalOperation(t *testing.T) {
	repository := t.TempDir()
	sessions := t.TempDir()
	config := `{
	  "schemaVersion": 1,
	  "ownedTools": [{
	    "id": "runner",
	    "executables": ["runner"],
	    "operations": [{"id": "check", "args": ["check"]}]
	  }]
	}`
	if err := os.WriteFile(filepath.Join(repository, ".muninn.json"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	output, err := captureStdout(t, func() error {
		return cmdFailures(repository, []string{
			"runner/check",
			"--sessions-dir", sessions,
			"--db", filepath.Join(t.TempDir(), "muninn.db"),
			"--since", "1d",
		})
	})
	if err != nil {
		t.Fatalf("positional operation failed: %v", err)
	}
	var report ownedOperationFailureReport
	if !json.Valid([]byte(output)) {
		t.Fatalf("failures output is not JSON: %q", output)
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != codexSessionInsightsSchemaVersion ||
		report.Operation != "runner/check" ||
		report.Events == nil {
		t.Fatalf("failure report mismatch: %#v", report)
	}
}

func TestFailuresRejectsRemovedOperationFlag(t *testing.T) {
	err := cmdFailures(t.TempDir(), []string{"--operation", "runner/check"})
	if err == nil || !strings.Contains(err.Error(), "muninn failures <tool/operation>") {
		t.Fatalf("removed --operation syntax was not rejected clearly: %v", err)
	}
}

func TestFailuresRejectsRemovedJSONFlag(t *testing.T) {
	err := cmdFailures(t.TempDir(), []string{"runner/check", "--json"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -json") {
		t.Fatalf("removed --json flag error=%v", err)
	}
}
