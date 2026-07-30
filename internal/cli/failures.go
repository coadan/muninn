package cli

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

const (
	defaultFailureEventLimit   = 5
	maximumFailureEventLimit   = 100
	ambiguousFailureEventLimit = 5
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

type ownedOperationFailureSummary struct {
	TotalDefinite     int `json:"totalDefinite"`
	TotalAmbiguous    int `json:"totalAmbiguous"`
	ReturnedDefinite  int `json:"returnedDefinite"`
	ReturnedAmbiguous int `json:"returnedAmbiguous"`
}

type ownedOperationFailureReport struct {
	SchemaVersion   int                          `json:"schemaVersion"`
	Provider        string                       `json:"provider"`
	Repository      string                       `json:"repository"`
	Since           time.Time                    `json:"since"`
	Generated       time.Time                    `json:"generatedAt"`
	Operation       string                       `json:"operation"`
	Reason          string                       `json:"reason,omitempty"`
	Task            string                       `json:"task,omitempty"`
	Summary         ownedOperationFailureSummary `json:"summary"`
	DefiniteEvents  []ownedOperationFailureEvent `json:"definiteEvents"`
	AmbiguousEvents []ownedOperationFailureEvent `json:"ambiguousEvents"`
}

func cmdFailures(root string, args []string) error {
	fs := flag.NewFlagSet("muninn failures", flag.ContinueOnError)
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
	reason := fs.String("reason", "", "only include this fixed failure-reason label")
	task := fs.String("task", "", "only include failures attributed to this exact worktree/task ID")
	limit := fs.Int(
		"limit",
		defaultFailureEventLimit,
		"maximum definite failure events to return (1-100); ambiguous evidence is capped at 5",
	)
	showUsage := setFlagSetUsage(
		fs,
		"muninn failures <tool/operation> [--repo <path>] [--since <duration>] [--task <task-id>] [--reason <label>] [--limit <n>]",
		"Inspect bounded, privacy-safe failure events for one configured owned operation.",
		[]string{
			"muninn failures repository-cli/test --repo . --since 14d",
			"muninn failures repository-cli/test --repo . --task task-id",
			"muninn failures repository-cli/test --repo . --reason \"test harness protocol\"",
		},
	)
	if len(args) == 0 {
		return errors.New("usage: muninn failures <tool/operation> [flags]")
	}
	if isHelpToken(args[0]) || flagHelpRequested(args[1:]) {
		showUsage()
		return flag.ErrHelp
	}
	selectedOperation := strings.TrimSpace(args[0])
	if selectedOperation == "" || strings.HasPrefix(selectedOperation, "-") {
		return errors.New("usage: muninn failures <tool/operation> [flags]")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: muninn failures <tool/operation> [flags]")
	}
	if *limit < 1 || *limit > maximumFailureEventLimit {
		return errors.New("--limit must be between 1 and 100")
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
		return fmt.Errorf("unknown operation %q (available: %s)", selectedOperation, strings.Join(operations, ", "))
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
	timeline, err := store.ownedOperationFailures(
		context.Background(),
		source.Name(),
		resolvedRepoRoot,
		ownership,
		since,
		selectedOperation,
		strings.TrimSpace(*reason),
		strings.TrimSpace(*task),
		*limit,
		min(*limit, ambiguousFailureEventLimit),
	)
	if err != nil {
		return err
	}
	report := ownedOperationFailureReport{
		SchemaVersion: codexSessionInsightsSchemaVersion,
		Provider:      source.Name(),
		Repository:    filepath.Base(resolvedRepoRoot),
		Since:         since,
		Generated:     now,
		Operation:     selectedOperation,
		Reason:        strings.TrimSpace(*reason),
		Task:          strings.TrimSpace(*task),
		Summary: ownedOperationFailureSummary{
			TotalDefinite:     timeline.TotalDefinite,
			TotalAmbiguous:    timeline.TotalAmbiguous,
			ReturnedDefinite:  len(timeline.Definite),
			ReturnedAmbiguous: len(timeline.Ambiguous),
		},
		DefiniteEvents:  timeline.Definite,
		AmbiguousEvents: timeline.Ambiguous,
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
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
