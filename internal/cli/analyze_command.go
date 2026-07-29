package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func cmdAnalyze(root string, args []string) error {
	fs := flag.NewFlagSet("muninn analyze", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	sinceRaw := fs.String("since", "7d", "lookback duration (for example 24h, 7d, or 2w)")
	sinceCommit := fs.String("since-commit", "", "analyze activity after a Git commit timestamp")
	providerName := fs.String("provider", defaultSessionProvider, sessionProviderFlagHelp())
	sessionsDir := fs.String("sessions-dir", "", "override the selected provider's default session directory")
	repoRoot := root
	fs.StringVar(&repoRoot, "repo", root, "only include session activity attributed to this repository")
	taskFilter := fs.String("task", "", "only include sessions attributed to this exact worktree/task ID")
	configPath := fs.String("config", "", "repository config path (default: <repo>/.muninn.json when present)")
	includeArchived := fs.Bool("include-archived", false, "also scan provider archives when supported")
	defaultStorePath, err := defaultSessionStorePath()
	if err != nil {
		return err
	}
	storePath := fs.String("db", defaultStorePath, "local privacy-safe SQLite index path")
	noCache := fs.Bool("no-cache", false, "scan provider files directly without the SQLite index")
	forceRefresh := fs.Bool("refresh", false, "re-index all discovered session files")
	compare := fs.String("compare", "", "comparison cohort: previous")
	detailsOutput := fs.Bool("details", false, "show full human rankings or the full JSON report; with --operation, show all operation rows")
	focus := fs.String("focus", "", "filter findings: friction (broad), tooling, instructions, interface, structure, discovery, failures, loops, output, or quality")
	operation := fs.String("operation", "", "show configured operations for one locally owned tool or exact tool/operation ID")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON")
	limit := fs.Int("limit", 5, "maximum intervention or detail rows in human output (0 shows all)")
	setFlagSetUsage(
		fs,
		"muninn analyze [--repo <path>] [--since <duration>] [--task <task-id>] [--focus <area>] [--details] [--operation <tool-or-operation>] [--compare previous] [--json]",
		"Summarize token usage and tool-output attribution without exposing session content or command text.",
		[]string{
			"muninn analyze --repo .",
			"muninn analyze --repo . --since 24h",
			"muninn analyze --repo . --since 1d --compare previous",
			"muninn analyze --repo . --since 7d --compare previous",
			"muninn analyze --repo . --since 24h --operation repository-cli",
			"muninn analyze --repo . --since 24h --operation repository-cli/test",
			"muninn analyze --repo . --since 24h --operation repository-cli --details",
			"muninn analyze --repo . --focus structure --details",
			"muninn analyze --repo . --task my-worktree",
			"muninn analyze --repo . --since 14d --include-archived --limit 20",
			"muninn analyze --repo . --json",
		},
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: muninn analyze [flags]")
	}
	if *limit < 0 {
		return errors.New("--limit must be 0 or greater")
	}
	sinceWasSet := false
	limitWasSet := false
	fs.Visit(func(visited *flag.Flag) {
		switch visited.Name {
		case "since":
			sinceWasSet = true
		case "limit":
			limitWasSet = true
		}
	})
	if *detailsOutput && strings.TrimSpace(*focus) == "" &&
		strings.TrimSpace(*operation) == "" && !limitWasSet {
		*limit = 0
	}
	if strings.TrimSpace(*sinceCommit) != "" && sinceWasSet {
		return errors.New("--since and --since-commit are mutually exclusive")
	}
	comparison := strings.ToLower(strings.TrimSpace(*compare))
	if comparison != "" && comparison != "previous" {
		return errors.New("--compare must be \"previous\" when set")
	}
	if comparison == "previous" && strings.TrimSpace(*sinceCommit) != "" {
		return errors.New("--compare previous requires a rolling --since lookback")
	}
	if comparison == "previous" && strings.TrimSpace(*operation) != "" {
		return errors.New("--compare previous cannot be combined with --operation")
	}
	if *noCache && *forceRefresh {
		return errors.New("--no-cache cannot be combined with --refresh")
	}
	if *jsonOutput && comparison == "previous" {
		return errors.New("--compare previous currently requires human output")
	}
	outputSelection, err := resolveAnalyzeOutputSelection(
		*detailsOutput,
		*focus,
		*operation,
		*limit,
		limitWasSet,
	)
	if err != nil {
		return err
	}
	source, err := resolveSessionSource(*providerName)
	if err != nil {
		return err
	}
	discovery, err := source.Discover(*sessionsDir, *includeArchived)
	if err != nil {
		return err
	}
	sessionMetadata := source.Metadata(discovery)
	resolvedRepoRoot, err := filepath.Abs(strings.TrimSpace(repoRoot))
	if err != nil {
		return fmt.Errorf("resolve --repo: %w", err)
	}
	now := time.Now().UTC()
	since := time.Time{}
	windowKind := "lookback"
	lookbackSeconds := int64(0)
	if reference := strings.TrimSpace(*sinceCommit); reference != "" {
		windowKind = "since-commit"
		since, err = resolveSinceCommit(resolvedRepoRoot, reference)
		if err != nil {
			return err
		}
	} else {
		lookback, err := parseCodexLookback(*sinceRaw)
		if err != nil {
			return fmt.Errorf("invalid --since value %q: %w", *sinceRaw, err)
		}
		since = now.Add(-lookback)
		lookbackSeconds = int64(lookback / time.Second)
	}
	config, err := loadRepositoryConfig(resolvedRepoRoot, *configPath)
	if err != nil {
		return err
	}
	ownership := newOwnershipCatalog(config.OwnedTools)
	var store *sessionStore
	var analyzeWindow func(time.Time, time.Time) (codexSessionInsightsReport, error)
	if *noCache {
		analyzeWindow = func(windowSince, windowUntil time.Time) (codexSessionInsightsReport, error) {
			return analyzeProviderSessions(
				source,
				discovery,
				resolvedRepoRoot,
				windowSince,
				windowUntil,
				strings.TrimSpace(*taskFilter),
				ownership,
				sessionMetadata,
			)
		}
	} else {
		store, err = openSessionStore(*storePath)
		if err != nil {
			return fmt.Errorf("%w (use --no-cache to bypass the index)", err)
		}
		defer store.Close()
		if *forceRefresh && !*jsonOutput {
			fmt.Fprintf(os.Stderr, "Refreshing Muninn index for %s...\n", filepath.Base(resolvedRepoRoot))
		}
		stats, err := store.refresh(context.Background(), source.Name(), discovery, resolvedRepoRoot, source, ownership, *forceRefresh)
		if err != nil {
			return err
		}
		if *forceRefresh && !*jsonOutput {
			fmt.Fprintln(os.Stderr, formatRefreshCompletion(stats))
		}
		analyzeWindow = func(windowSince, windowUntil time.Time) (codexSessionInsightsReport, error) {
			return store.analyze(
				context.Background(),
				source.Name(),
				discovery.Dirs,
				resolvedRepoRoot,
				windowSince,
				windowUntil,
				strings.TrimSpace(*taskFilter),
				ownership,
				stats,
				sessionMetadata,
			)
		}
	}
	report, err := analyzeWindow(since, now)
	if err != nil {
		return err
	}
	report.Provider = source.Name()
	report.AnalysisScope = sessionAnalysisScope{
		WindowKind:      windowKind,
		LookbackSeconds: lookbackSeconds,
		Task:            strings.TrimSpace(*taskFilter),
		IncludeArchived: *includeArchived,
		Focus:           normalizeTrendFocus(*focus),
	}
	report.Instructions = inspectRepositoryInstructions(resolvedRepoRoot, source.Name())
	report.Findings = buildSessionFindings(report, config)
	report.Findings, err = filterSessionFindings(report.Findings, *focus)
	if err != nil {
		return err
	}
	report.Interventions = buildSessionInterventions(report.Findings)
	var baseline *codexSessionInsightsReport
	if comparison == "previous" {
		previousSince, previousUntil, err := previousLookbackWindow(since, now, lookbackSeconds)
		if err != nil {
			return err
		}
		previous, err := analyzeWindow(previousSince, previousUntil)
		if err != nil {
			return err
		}
		previous.Provider = source.Name()
		previous.AnalysisScope = report.AnalysisScope
		previous.Instructions = report.Instructions
		previous.Findings = buildSessionFindings(previous, config)
		previous.Findings, err = filterSessionFindings(previous.Findings, *focus)
		if err != nil {
			return err
		}
		previous.Interventions = buildSessionInterventions(previous.Findings)
		baseline = &previous
	}
	if toolID := strings.TrimSpace(*operation); toolID != "" {
		drilldown, err := buildOwnedOperationsDrilldown(report, config, toolID, outputSelection.OperationLimit)
		if err != nil {
			return err
		}
		if *jsonOutput {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(drilldown)
		}
		printOwnedOperationsDrilldown(drilldown)
		return nil
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(analysisJSONPayload(report, *detailsOutput))
	}
	printCodexSessionInsights(report, config, *limit, outputSelection.View)
	if baseline != nil {
		printSessionTrend(
			*baseline,
			report,
			"previous non-overlapping "+formatTrendLookback(lookbackSeconds),
			outputSelection.View == "details",
		)
	}
	return nil
}

type analyzeOutputSelection struct {
	View           string
	OperationLimit int
}

func resolveAnalyzeOutputSelection(
	details bool,
	focus,
	operations string,
	limit int,
	limitWasSet bool,
) (analyzeOutputSelection, error) {
	focus = strings.TrimSpace(focus)
	operations = strings.TrimSpace(operations)
	if operations != "" && focus != "" {
		return analyzeOutputSelection{}, errors.New("--operation cannot be combined with --focus")
	}

	selection := analyzeOutputSelection{
		View:           "findings",
		OperationLimit: limit,
	}
	switch {
	case operations != "":
		if details && !limitWasSet {
			selection.OperationLimit = 0
		}
	case focus != "":
		// Focused views add bounded evidence where it helps act on the finding.
		// Accept --details without requiring a help/retry roundtrip.
		selection.View = "focused"
	case details:
		selection.View = "details"
	}
	return selection, nil
}

func formatRefreshCompletion(stats sessionRefreshStats) string {
	return fmt.Sprintf(
		"Refresh complete: %s scanned, %s indexed, %s reused, %s pruned, %s unreadable.",
		formatCodexCount(int64(stats.FilesScanned)),
		formatCodexCount(int64(stats.FilesIndexed)),
		formatCodexCount(int64(stats.FilesReused)),
		formatCodexCount(int64(stats.FilesPruned)),
		formatCodexCount(int64(stats.FilesUnreadable)),
	)
}

func resolveCodexSessionsDir(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		path, err := filepath.Abs(strings.TrimSpace(explicit))
		if err != nil {
			return "", err
		}
		if !dirExists(path) {
			return "", fmt.Errorf("Codex sessions directory does not exist: %s", path)
		}
		return path, nil
	}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		codexHome = filepath.Join(home, ".codex")
	}
	path := filepath.Join(codexHome, "sessions")
	if !dirExists(path) {
		return "", fmt.Errorf("Codex sessions directory does not exist: %s (use --sessions-dir)", path)
	}
	return path, nil
}

func parseCodexLookback(raw string) (time.Duration, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return 0, errors.New("duration cannot be empty")
	}
	multiplier := time.Duration(1)
	switch {
	case strings.HasSuffix(value, "d"):
		multiplier = 24 * time.Hour
		value = strings.TrimSuffix(value, "d")
	case strings.HasSuffix(value, "w"):
		multiplier = 7 * 24 * time.Hour
		value = strings.TrimSuffix(value, "w")
	default:
		duration, err := time.ParseDuration(value)
		if err != nil {
			return 0, err
		}
		if duration <= 0 {
			return 0, errors.New("duration must be positive")
		}
		return duration, nil
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number <= 0 {
		return 0, errors.New("duration must be positive")
	}
	return time.Duration(number * float64(multiplier)), nil
}
