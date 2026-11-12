package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

var pullRequestSuffix = regexp.MustCompile(` \(#(\d+)\)$`)

// sendChangelog gives the Coordinator chat of each organization the changelog from the release that ran before to this
// release, one time for each organization and release. A local build and the first start with a kept release send
// nothing.
func (e *Engine) sendChangelog(ctx context.Context) {
	if Release == "" || len(e.github.Organizations()) == 0 {
		return
	}
	last, err := e.queries.GetLastRelease(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		if err := e.queries.SetLastRelease(ctx, Release); err != nil {
			log.Printf("changelog: keep release: %v", err)
		}
		return
	}
	if err != nil {
		log.Printf("changelog: read last release: %v", err)
		return
	}
	if last == Release {
		return
	}
	lines, pullRequests, err := e.changelogLines(ctx, last)
	if err != nil {
		log.Printf("changelog: %v", err)
		return
	}
	if len(lines) > 0 {
		for _, organization := range e.github.Organizations() {
			if !e.sendChangelogTo(ctx, organization, last, pullRequests, lines) {
				return
			}
		}
	}
	if err := e.queries.SetLastRelease(ctx, Release); err != nil {
		log.Printf("changelog: keep release: %v", err)
	}
}

// changelogLines gives the line of each commit after the release last up to Release, the oldest first, and the number
// of pull requests of the lines.
func (e *Engine) changelogLines(ctx context.Context, last string) ([]string, int, error) {
	messages, err := e.github.CommitMessages(ctx, last, Release)
	if err != nil {
		return nil, 0, err
	}
	var numbers []int
	subjects := make([]string, len(messages))
	for i, message := range messages {
		subjects[i], _, _ = strings.Cut(message, "\n")
		if match := pullRequestSuffix.FindStringSubmatch(subjects[i]); match != nil {
			number, _ := strconv.Atoi(match[1])
			numbers = append(numbers, number)
		}
	}
	var closing map[int][]int
	if len(numbers) > 0 {
		if closing, err = e.github.ClosingIssues(ctx, numbers); err != nil {
			return nil, 0, err
		}
	}
	lines := make([]string, len(subjects))
	for i, subject := range subjects {
		match := pullRequestSuffix.FindStringSubmatch(subject)
		if match == nil {
			lines[i] = "- " + subject
			continue
		}
		number, _ := strconv.Atoi(match[1])
		lines[i] = fmt.Sprintf("- #%d %s", number, strings.TrimSuffix(subject, match[0]))
		if issues := closing[number]; len(issues) > 0 {
			refs := make([]string, len(issues))
			for j, issue := range issues {
				refs[j] = fmt.Sprintf("#%d", issue)
			}
			lines[i] += " (closes " + strings.Join(refs, ", ") + ")"
		}
	}
	return lines, len(numbers), nil
}

// sendChangelogTo posts the changelog to the Coordinator chat of organization, unless the chat got it. It tells if
// the changelog is in the chat.
func (e *Engine) sendChangelogTo(ctx context.Context, organization, last string, pullRequests int, lines []string) bool {
	sent, err := e.queries.ReleaseChangelogSent(ctx, store.ReleaseChangelogSentParams{Organization: organization, Tag: Release})
	if err != nil {
		log.Printf("changelog: read sent: %v", err)
		return false
	}
	if sent {
		return true
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Changelog from %s to %s\n\n%d pull requests.\n\n%s\n", last, Release, pullRequests, strings.Join(lines, "\n"))
	if paths := e.memoryPaths(organization); len(paths) > 0 {
		text.WriteString("\nMemory files:\n- " + strings.Join(paths, "\n- ") + "\n")
	}
	if err := e.postChat(ctx, ChatKey{Organization: organization}, mobiusAuthor, "", text.String(), nil, nil, nil, false); err != nil {
		log.Printf("changelog: post to %s: %v", organization, err)
		return false
	}
	err = e.queries.AddReleaseChangelog(ctx, store.AddReleaseChangelogParams{Organization: organization, Tag: Release, SentAt: now()})
	if err != nil {
		log.Printf("changelog: keep sent: %v", err)
		return false
	}
	return true
}

// memoryPaths gives the path of each memory file that exists, for each repository of organization.
func (e *Engine) memoryPaths(organization string) []string {
	var names []string
	for _, repository := range e.github.Repositories() {
		if repository.Owner() == organization {
			names = append(names, repository.FullName)
		}
	}
	slices.Sort(names)
	var paths []string
	for _, name := range names {
		for _, file := range memoryFileNames {
			path := e.memoryPath(name, file)
			if _, err := os.Stat(filepath.Clean(path)); err == nil {
				paths = append(paths, path)
			}
		}
	}
	return paths
}
