package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var feedbackTargetPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,79}$`)
var feedbackSignalPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,79}$`)

var feedbackCategories = map[string]bool{
	"failure":      true,
	"roundtrip":    true,
	"output":       true,
	"discovery":    true,
	"instructions": true,
	"interface":    true,
	"structure":    true,
	"loop":         true,
	"inline-code":  true,
	"other":        true,
}

var feedbackControls = map[string]bool{
	"local":       true,
	"repository":  true,
	"third-party": true,
	"unknown":     true,
}

var feedbackSources = map[string]bool{
	"agent":    true,
	"codex":    true,
	"claude":   true,
	"opencode": true,
	"human":    true,
}

type agentFeedback struct {
	RepositoryKey string
	Source        string
	Control       string
	Category      string
	Target        string
	Signal        string
	Occurrences   int
	ObservedAt    time.Time
}

type agentFeedbackAggregate struct {
	Control     string   `json:"control"`
	Category    string   `json:"category"`
	Target      string   `json:"target"`
	Signal      string   `json:"signal"`
	Occurrences int      `json:"occurrences"`
	Sources     []string `json:"sources"`
	Status      string   `json:"status"`
	LastSeen    string   `json:"lastSeen"`
}

func normalizeFeedbackCategory(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if !feedbackCategories[value] {
		return "", fmt.Errorf("invalid feedback category %q (available: failure, roundtrip, output, discovery, instructions, interface, structure, loop, inline-code, other)", raw)
	}
	return value, nil
}

func normalizeFeedbackControl(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if !feedbackControls[value] {
		return "", fmt.Errorf("invalid feedback control %q (available: local, repository, third-party, unknown)", raw)
	}
	return value, nil
}

func normalizeFeedbackSource(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if !feedbackSources[value] {
		return "", fmt.Errorf("invalid feedback source %q (available: agent, codex, claude, opencode, human)", raw)
	}
	return value, nil
}

func normalizeFeedbackTarget(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if !feedbackTargetPattern.MatchString(value) || strings.Contains(value, "..") || strings.Contains(value, "//") {
		return "", fmt.Errorf("--target must be a privacy-safe logical ID such as bwb/pr or heimdal/session")
	}
	return value, nil
}

func normalizeFeedbackSignal(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if !feedbackSignalPattern.MatchString(value) || strings.Contains(value, "..") {
		return "", fmt.Errorf("--signal must be a privacy-safe slug such as existing-pr-create-failed")
	}
	return value, nil
}

func (store *sessionStore) addFeedback(ctx context.Context, feedback agentFeedback) error {
	observedAt := feedback.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	if feedback.Occurrences < 1 {
		return errors.New("feedback occurrences must be positive")
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO feedback(
		repository_key, source, control, category, target, signal, occurrences,
		status, first_seen_ns, last_seen_ns, resolved_at_ns
	) VALUES(?, ?, ?, ?, ?, ?, ?, 'open', ?, ?, 0)
	ON CONFLICT(repository_key, source, control, category, target, signal) DO UPDATE SET
	  occurrences = feedback.occurrences + excluded.occurrences,
	  status = 'open',
	  last_seen_ns = excluded.last_seen_ns,
	  resolved_at_ns = 0`,
		feedback.RepositoryKey,
		feedback.Source,
		feedback.Control,
		feedback.Category,
		feedback.Target,
		feedback.Signal,
		feedback.Occurrences,
		observedAt.UnixNano(),
		observedAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("record Muninn feedback: %w", err)
	}
	return nil
}

func (store *sessionStore) resolveFeedback(ctx context.Context, repositoryKey, category, target, signal string, resolvedAt time.Time) (int64, error) {
	if resolvedAt.IsZero() {
		resolvedAt = time.Now().UTC()
	}
	result, err := store.db.ExecContext(ctx, `UPDATE feedback
	SET status = 'resolved', resolved_at_ns = ?
	WHERE repository_key = ? AND category = ? AND target = ? AND signal = ? AND status = 'open'`,
		resolvedAt.UnixNano(),
		repositoryKey,
		category,
		target,
		signal,
	)
	if err != nil {
		return 0, fmt.Errorf("resolve Muninn feedback: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count resolved Muninn feedback: %w", err)
	}
	return count, nil
}

func (store *sessionStore) listFeedback(ctx context.Context, repositoryKey string, since time.Time, includeResolved bool) ([]agentFeedbackAggregate, error) {
	query := `SELECT source, control, category, target, signal, occurrences, status, last_seen_ns
		FROM feedback
		WHERE repository_key = ? AND last_seen_ns >= ?`
	args := []any{repositoryKey, since.UnixNano()}
	if !includeResolved {
		query += ` AND status = 'open'`
	}
	query += ` ORDER BY last_seen_ns DESC, id DESC`
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list Muninn feedback: %w", err)
	}
	defer rows.Close()

	type aggregateKey struct {
		Control  string
		Category string
		Target   string
		Signal   string
		Status   string
	}
	byKey := map[aggregateKey]*agentFeedbackAggregate{}
	for rows.Next() {
		var source, control, category, target, signal, status string
		var occurrences int
		var lastSeenNS int64
		if err := rows.Scan(&source, &control, &category, &target, &signal, &occurrences, &status, &lastSeenNS); err != nil {
			return nil, fmt.Errorf("read Muninn feedback: %w", err)
		}
		key := aggregateKey{Control: control, Category: category, Target: target, Signal: signal, Status: status}
		aggregate := byKey[key]
		if aggregate == nil {
			aggregate = &agentFeedbackAggregate{
				Control:  control,
				Category: category,
				Target:   target,
				Signal:   signal,
				Status:   status,
			}
			byKey[key] = aggregate
		}
		aggregate.Occurrences += occurrences
		aggregate.Sources = appendUniqueFeedbackSource(aggregate.Sources, source)
		observedAt := time.Unix(0, lastSeenNS).UTC()
		if aggregate.LastSeen == "" || observedAt.Format(time.RFC3339) > aggregate.LastSeen {
			aggregate.LastSeen = observedAt.Format(time.RFC3339)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Muninn feedback: %w", err)
	}
	result := make([]agentFeedbackAggregate, 0, len(byKey))
	for _, aggregate := range byKey {
		sort.Strings(aggregate.Sources)
		result = append(result, *aggregate)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Status != result[j].Status {
			return result[i].Status < result[j].Status
		}
		if result[i].Occurrences != result[j].Occurrences {
			return result[i].Occurrences > result[j].Occurrences
		}
		if result[i].Target != result[j].Target {
			return result[i].Target < result[j].Target
		}
		return result[i].Signal < result[j].Signal
	})
	return result, nil
}

func appendUniqueFeedbackSource(values []string, candidate string) []string {
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func cmdFeedback(root string, args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printFeedbackHelp()
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "add":
		return cmdFeedbackAdd(root, args[1:])
	case "resolve":
		return cmdFeedbackResolve(root, args[1:])
	case "list":
		return cmdFeedbackList(root, args[1:])
	default:
		if strings.HasPrefix(args[0], "-") {
			return cmdFeedbackAdd(root, args)
		}
		return fmt.Errorf("unknown Muninn feedback command: %s", args[0])
	}
}

func feedbackRepositoryKey(repo string) (string, error) {
	resolved, err := resolveRepositoryRoot(repo)
	if err != nil {
		return "", err
	}
	return ownershipSelectorDigest("repo", resolved), nil
}

func resolveRepositoryRoot(repo string) (string, error) {
	resolved, err := filepath.Abs(strings.TrimSpace(repo))
	if err != nil {
		return "", fmt.Errorf("resolve --repo: %w", err)
	}
	if !dirExists(resolved) {
		return "", fmt.Errorf("repository does not exist: %s", resolved)
	}
	return resolved, nil
}

func feedbackStorePath() (string, error) {
	return defaultSessionStorePath()
}

func cmdFeedbackAdd(root string, args []string) error {
	fs := flag.NewFlagSet("muninn feedback add", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	repo := fs.String("repo", root, "repository this feedback applies to")
	dbPath, err := feedbackStorePath()
	if err != nil {
		return err
	}
	db := fs.String("db", dbPath, "local privacy-safe SQLite index path")
	sourceRaw := fs.String("source", "agent", "feedback source: agent, codex, claude, opencode, or human")
	controlRaw := fs.String("control", "local", "change control: local, repository, third-party, or unknown")
	categoryRaw := fs.String("category", "", "friction category")
	targetRaw := fs.String("target", "", "privacy-safe logical tool or workflow ID")
	signalRaw := fs.String("signal", "", "privacy-safe kebab-case friction signal")
	count := fs.Int("count", 1, "number of observed occurrences")
	setFlagSetUsage(fs,
		"muninn feedback add --category <category> --target <logical-id> --signal <slug> [flags]",
		"Record normalized friction feedback without storing prose, commands, output, paths, or session IDs.",
		[]string{
			"muninn feedback add --category roundtrip --target bwb/pr --signal existing-pr-create-failed",
			"muninn feedback --category interface --target heimdal/session --signal reconnect-needs-custom-script --source codex",
		},
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: muninn feedback add [flags]")
	}
	if *count < 1 || *count > 1000 {
		return errors.New("--count must be between 1 and 1000")
	}
	source, err := normalizeFeedbackSource(*sourceRaw)
	if err != nil {
		return err
	}
	control, err := normalizeFeedbackControl(*controlRaw)
	if err != nil {
		return err
	}
	category, err := normalizeFeedbackCategory(*categoryRaw)
	if err != nil {
		return err
	}
	target, err := normalizeFeedbackTarget(*targetRaw)
	if err != nil {
		return err
	}
	signal, err := normalizeFeedbackSignal(*signalRaw)
	if err != nil {
		return err
	}
	repositoryKey, err := feedbackRepositoryKey(*repo)
	if err != nil {
		return err
	}
	store, err := openSessionStore(*db)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.addFeedback(context.Background(), agentFeedback{
		RepositoryKey: repositoryKey,
		Source:        source,
		Control:       control,
		Category:      category,
		Target:        target,
		Signal:        signal,
		Occurrences:   *count,
	}); err != nil {
		return err
	}
	fmt.Printf("Recorded feedback: [%s/%s] %s · %s (occurrences +%d, source=%s)\n",
		category, control, signal, target, *count, source)
	return nil
}

func cmdFeedbackResolve(root string, args []string) error {
	fs := flag.NewFlagSet("muninn feedback resolve", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	repo := fs.String("repo", root, "repository this feedback applies to")
	dbPath, err := feedbackStorePath()
	if err != nil {
		return err
	}
	db := fs.String("db", dbPath, "local privacy-safe SQLite index path")
	categoryRaw := fs.String("category", "", "friction category")
	targetRaw := fs.String("target", "", "privacy-safe logical tool or workflow ID")
	signalRaw := fs.String("signal", "", "privacy-safe kebab-case friction signal")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: muninn feedback resolve [flags]")
	}
	category, err := normalizeFeedbackCategory(*categoryRaw)
	if err != nil {
		return err
	}
	target, err := normalizeFeedbackTarget(*targetRaw)
	if err != nil {
		return err
	}
	signal, err := normalizeFeedbackSignal(*signalRaw)
	if err != nil {
		return err
	}
	repositoryKey, err := feedbackRepositoryKey(*repo)
	if err != nil {
		return err
	}
	store, err := openSessionStore(*db)
	if err != nil {
		return err
	}
	defer store.Close()
	resolved, err := store.resolveFeedback(context.Background(), repositoryKey, category, target, signal, time.Time{})
	if err != nil {
		return err
	}
	if resolved == 0 {
		return fmt.Errorf("no open feedback matched [%s] %s · %s", category, signal, target)
	}
	fmt.Printf("Resolved feedback: [%s] %s · %s (%d source records)\n", category, signal, target, resolved)
	return nil
}

func cmdFeedbackList(root string, args []string) error {
	fs := flag.NewFlagSet("muninn feedback list", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	repo := fs.String("repo", root, "repository to report")
	dbPath, err := feedbackStorePath()
	if err != nil {
		return err
	}
	db := fs.String("db", dbPath, "local privacy-safe SQLite index path")
	sinceRaw := fs.String("since", "30d", "lookback duration")
	includeResolved := fs.Bool("all", false, "include resolved feedback")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: muninn feedback list [flags]")
	}
	lookback, err := parseCodexLookback(*sinceRaw)
	if err != nil {
		return fmt.Errorf("invalid --since value %q: %w", *sinceRaw, err)
	}
	repositoryKey, err := feedbackRepositoryKey(*repo)
	if err != nil {
		return err
	}
	store, err := openSessionStore(*db)
	if err != nil {
		return err
	}
	defer store.Close()
	rows, err := store.listFeedback(context.Background(), repositoryKey, time.Now().UTC().Add(-lookback), *includeResolved)
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(rows)
	}
	if len(rows) == 0 {
		fmt.Println("No matching feedback.")
		return nil
	}
	fmt.Println("Muninn feedback:")
	for _, row := range rows {
		fmt.Printf("- [%s/%s/%s] %s · %s: %d occurrences; sources=%s; last=%s\n",
			row.Category,
			row.Control,
			row.Status,
			row.Signal,
			row.Target,
			row.Occurrences,
			strings.Join(row.Sources, ","),
			row.LastSeen,
		)
	}
	return nil
}

func printFeedbackHelp() {
	fmt.Print(`Record normalized agent friction without storing raw session content.

Usage:
  muninn feedback add --category <category> --target <logical-id> --signal <slug>
  muninn feedback resolve --category <category> --target <logical-id> --signal <slug>
  muninn feedback list [--since 30d] [--all] [--json]

Categories: failure, roundtrip, output, discovery, instructions, interface,
structure, loop, inline-code, other.

Target and signal accept only bounded logical labels. Do not paste prose,
commands, paths, URLs, prompts, output, or secrets.
`)
}
