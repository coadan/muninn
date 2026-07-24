package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func resolveSinceCommit(repositoryRoot, reference string) (time.Time, error) {
	command := exec.Command("git", "-C", repositoryRoot, "show", "-s", "--format=%cI", strings.TrimSpace(reference))
	output, err := command.Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("resolve --since-commit %q: %w", reference, err)
	}
	timestamp, err := time.Parse(time.RFC3339, strings.TrimSpace(string(output)))
	if err != nil {
		return time.Time{}, fmt.Errorf("parse commit timestamp for %q: %w", reference, err)
	}
	return timestamp.UTC(), nil
}
