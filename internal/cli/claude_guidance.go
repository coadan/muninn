package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// claudeGuidanceArtifactSizes resolves the logical skill IDs the Skill tool
// loads to their current SKILL.md size. A bare ID is a project skill
// (<workspace>/.claude/skills/<id>) or a user skill (<config>/skills/<id>); a
// "plugin:skill" ID is a skill of an installed plugin, found under each install
// path that plugins/installed_plugins.json lists for that plugin. Paths are
// transient: only the ID and its estimated token count leave this function.
func claudeGuidanceArtifactSizes(workspaceRoot string, ids []string) map[string]int64 {
	configDir := claudeConfigDir()
	var plugins map[string][]string
	sizes := map[string]int64{}
	for _, id := range ids {
		if !guidanceArtifactIDPattern.MatchString(id) {
			continue
		}
		var candidates []string
		plugin, skill, namespaced := strings.Cut(id, ":")
		if namespaced {
			if !claudeSkillNameValid(plugin) || !claudeSkillNameValid(skill) {
				continue
			}
			if plugins == nil {
				plugins = claudeInstalledPluginPaths(configDir)
			}
			for _, installPath := range plugins[plugin] {
				candidates = append(candidates, filepath.Join(installPath, "skills", skill, "SKILL.md"))
			}
		} else if claudeSkillNameValid(id) {
			if strings.TrimSpace(workspaceRoot) != "" {
				candidates = append(candidates, filepath.Join(workspaceRoot, ".claude", "skills", id, "SKILL.md"))
			}
			if configDir != "" {
				candidates = append(candidates, filepath.Join(configDir, "skills", id, "SKILL.md"))
			}
		}
		for _, candidate := range candidates {
			info, err := os.Stat(candidate)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if tokens := estimatedTokens(info.Size()); tokens > sizes[id] {
				sizes[id] = tokens
			}
			// Project skills shadow user skills. Ambiguous plugin IDs use
			// the largest installed artifact, independent of map order.
			if !namespaced {
				break
			}
		}
	}
	if len(sizes) == 0 {
		return nil
	}
	return sizes
}

// claudeSkillNameValid keeps a name segment from escaping its skills
// directory.
func claudeSkillNameValid(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\:`)
}

func claudeConfigDir() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// claudeInstalledPluginPaths maps a plugin name to its install paths. The
// registry keys plugins as "name@marketplace"; a skill ID carries only the
// name.
func claudeInstalledPluginPaths(configDir string) map[string][]string {
	paths := map[string][]string{}
	if configDir == "" {
		return paths
	}
	raw, err := os.ReadFile(filepath.Join(configDir, "plugins", "installed_plugins.json"))
	if err != nil {
		return paths
	}
	var registry struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(raw, &registry) != nil {
		return paths
	}
	for key, installs := range registry.Plugins {
		name, _, _ := strings.Cut(key, "@")
		for _, install := range installs {
			if strings.TrimSpace(install.InstallPath) != "" {
				paths[name] = append(paths[name], install.InstallPath)
			}
		}
	}
	return paths
}
