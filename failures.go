package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ownedOperationFailureEvent struct {
	OccurredAt           time.Time `json:"occurredAt"`
	Operation            string    `json:"operation"`
	Task                 string    `json:"task"`
	Reason               string    `json:"reason"`
	Family               string    `json:"family"`
	OutputBytes          int64     `json:"outputBytes"`
	AttributionAmbiguous bool      `json:"attributionAmbiguous"`
}

type ownedOperationFailureReport struct {
	Provider   string                       `json:"provider"`
	Repository string                       `json:"repository"`
	Since      time.Time                    `json:"since"`
	Generated  time.Time                    `json:"generatedAt"`
	Operation  string                       `json:"operation"`
	Reason     string                       `json:"reason,omitempty"`
	Task       string                       `json:"task,omitempty"`
	Events     []ownedOperationFailureEvent `json:"events"`
}

func cmdFailures(root string, args []string) error {
	fs := flag.NewFlagSet("muninn failures", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	sinceRaw := fs.String("since", "7d", "lookback duration (for example 24h, 7d, or 2w)")
	providerName := fs.String("provider", defaultSessionProvider, sessionProviderFlagHelp())
	sessionsDir := fs.String("sessions-dir", "", "override the selected provider's default session directory")
	repoRoot := root
	fs.StringVar(&repoRoot, "repo", root, "only include session activity attributed to this repository")
	configPath := fs.String("config", "", "repository config path (default: <repo>/.muninn.json when present)")
	includeArchived := fs.Bool("include-archived", false, "also scan provider archives when supported")
	defaultStorePath, err := defaultSessionStorePath()
	if err != nil {
		return err
	}
	storePath := fs.String("db", defaultStorePath, "local privacy-safe SQLite index path")
	forceRefresh := fs.Bool("refresh", false, "re-index all discovered session files")
	operation := fs.String("operation", "", "configured owned operation ID, for example repository-cli/test")
	reason := fs.String("reason", "", "only include this fixed failure-reason label")
	task := fs.String("task", "", "only include failures attributed to this exact worktree/task ID")
	limit := fs.Int("limit", 20, "maximum failure events to return (1-100)")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON")
	setFlagSetUsage(
		fs,
		"muninn failures --operation <tool/operation> [--repo <path>] [--since <duration>] [--task <task-id>] [--reason <label>] [--limit <n>] [--json]",
		"Inspect bounded, privacy-safe failure events for one configured owned operation.",
		[]string{
			"muninn failures --repo . --operation repository-cli/test --since 14d",
			"muninn failures --repo . --operation repository-cli/test --task task-id",
			"muninn failures --repo . --operation repository-cli/test --reason \"test harness protocol\" --json",
		},
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: muninn failures --operation <tool/operation> [flags]")
	}
	if *limit < 1 || *limit > 100 {
		return errors.New("--limit must be between 1 and 100")
	}
	selectedOperation := strings.TrimSpace(*operation)
	if selectedOperation == "" {
		return errors.New("--operation is required")
	}
	lookback, err := parseCodexLookback(*sinceRaw)
	if err != nil {
		return fmt.Errorf("invalid --since value %q: %w", *sinceRaw, err)
	}
	source, err := resolveSessionSource(*providerName)
	if err != nil {
		return err
	}
	discovery, err := source.Discover(*sessionsDir, *includeArchived)
	if err != nil {
		return err
	}
	resolvedRepoRoot, err := filepath.Abs(strings.TrimSpace(repoRoot))
	if err != nil {
		return fmt.Errorf("resolve --repo: %w", err)
	}
	config, err := loadRepositoryConfig(resolvedRepoRoot, *configPath)
	if err != nil {
		return err
	}
	operations := configuredOwnedOperationIDs(config.OwnedTools)
	if !containsString(operations, selectedOperation) {
		if len(operations) == 0 {
			return errors.New("repository config has no owned operations; add ownedTools.operations to .muninn.json")
		}
		return fmt.Errorf("unknown --operation %q (available: %s)", selectedOperation, strings.Join(operations, ", "))
	}
	ownership := newOwnershipCatalog(config.OwnedTools)
	store, err := openSessionStore(*storePath)
	if err != nil {
		return err
	}
	defer store.Close()
	if _, err := store.refresh(
		context.Background(),
		source.Name(),
		discovery,
		resolvedRepoRoot,
		source,
		ownership,
		*forceRefresh,
	); err != nil {
		return err
	}
	now := time.Now().UTC()
	since := now.Add(-lookback)
	events, err := store.ownedOperationFailures(
		context.Background(),
		source.Name(),
		resolvedRepoRoot,
		ownership,
		since,
		selectedOperation,
		strings.TrimSpace(*reason),
		strings.TrimSpace(*task),
		*limit,
	)
	if err != nil {
		return err
	}
	report := ownedOperationFailureReport{
		Provider:   source.Name(),
		Repository: filepath.Base(resolvedRepoRoot),
		Since:      since,
		Generated:  now,
		Operation:  selectedOperation,
		Reason:     strings.TrimSpace(*reason),
		Task:       strings.TrimSpace(*task),
		Events:     events,
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	printOwnedOperationFailureReport(report)
	return nil
}

func configuredOwnedOperationIDs(configs []ownedToolConfig) []string {
	var operations []string
	for _, tool := range configs {
		for _, operation := range tool.Operations {
			operations = append(operations, strings.TrimSpace(tool.ID)+"/"+strings.TrimSpace(operation.ID))
		}
	}
	sort.Strings(operations)
	return operations
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func printOwnedOperationFailureReport(report ownedOperationFailureReport) {
	fmt.Printf("Owned-operation failures for %s\n", report.Operation)
	fmt.Printf("Repository: %s\n", report.Repository)
	fmt.Printf("Since: %s\n", report.Since.Format(time.RFC3339))
	if report.Reason != "" {
		fmt.Printf("Reason: %s\n", report.Reason)
	}
	if report.Task != "" {
		fmt.Printf("Task: %s\n", report.Task)
	}
	fmt.Printf("Events: %s\n", formatCodexCount(int64(len(report.Events))))
	for _, event := range report.Events {
		attribution := "exact"
		if event.AttributionAmbiguous {
			attribution = "ambiguous"
		}
		family := event.Family
		if family == "" {
			family = "(unknown)"
		}
		fmt.Printf(
			"- %s · task %s · %s · %s · %s bytes · %s\n",
			event.OccurredAt.Format(time.RFC3339),
			event.Task,
			event.Reason,
			family,
			formatCodexCount(event.OutputBytes),
			attribution,
		)
	}
}
