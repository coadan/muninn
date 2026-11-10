package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeClaudeSkill(t *testing.T, dir string, bytes int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(strings.Repeat("x", bytes)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeGuidanceArtifactSizesResolvesUserProjectAndPluginSkills(t *testing.T) {
	config := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)

	writeClaudeSkill(t, filepath.Join(config, "skills", "user-skill"), 4_000)
	writeClaudeSkill(t, filepath.Join(config, "skills", "shadowed"), 8_000)
	writeClaudeSkill(t, filepath.Join(workspace, ".claude", "skills", "shadowed"), 2_000)
	installed := filepath.Join(config, "plugins", "cache", "market", "tools", "2.0.0")
	other := filepath.Join(config, "plugins", "cache", "other", "tools", "1.0.0")
	stale := filepath.Join(config, "plugins", "cache", "market", "tools", "1.0.0")
	writeClaudeSkill(t, filepath.Join(installed, "skills", "deploy"), 12_000)
	writeClaudeSkill(t, filepath.Join(other, "skills", "deploy"), 16_000)
	writeClaudeSkill(t, filepath.Join(stale, "skills", "deploy"), 40_000)
	writeClaudeSkill(t, filepath.Join(stale, "skills", "retired"), 40_000)
	registry, err := json.Marshal(map[string]any{
		"version": 2,
		"plugins": map[string]any{
			"tools@market": []any{
				map[string]any{"scope": "user", "installPath": installed},
				map[string]any{"scope": "project", "installPath": other},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "plugins", "installed_plugins.json"), registry, 0o644); err != nil {
		t.Fatal(err)
	}

	got := claudeGuidanceArtifactSizes(workspace, []string{
		"user-skill", "shadowed", "tools:deploy", "tools:retired",
		"unknown", "other-plugin:deploy", "tools:../tools", "../escape",
	})
	want := map[string]int64{
		"user-skill":   1_000,
		"shadowed":     500,
		"tools:deploy": 4_000,
	}
	if len(got) != len(want) {
		t.Fatalf("sizes=%v, want %v", got, want)
	}
	for id, tokens := range want {
		if got[id] != tokens {
			t.Fatalf("size of %s=%d, want %d (all %v)", id, got[id], tokens, got)
		}
	}
}

func TestClaudeProviderRefreshesSkillToolLoadSizes(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	writeClaudeSkill(t, filepath.Join(config, "skills", "planning"), 6_000)
	sizes := claudeSessionProvider.GuidanceArtifactSizes(t.TempDir(), []string{"planning"})
	if sizes["planning"] != 1_500 {
		t.Fatalf("Claude provider guidance sizes=%v", sizes)
	}
}
