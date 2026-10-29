package engine

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// blockerIssue is an issue in the graph of blockers.
type blockerIssue struct {
	repository string
	number     int64
}

func (i blockerIssue) String() string {
	return fmt.Sprintf("%s#%d", i.repository, i.number)
}

func compareBlockerIssues(a, b blockerIssue) int {
	return strings.Compare(a.String(), b.String())
}

// blockerWorkstream is the Workstream of a copy that holds an issue of a cycle.
type blockerWorkstream struct {
	repository string
	workstream int64
}

// reportBlockerCycles tells the Lead of each Workstream that has an issue in a cycle of blockers, one time for each
// cycle. The edges of one cycle can be in the copies of many repositories, so it reads them all. A failure ends the
// step, and the next poll runs it again.
func (e *Engine) reportBlockerCycles(ctx context.Context) {
	if err := e.sendBlockerCycles(ctx); err != nil {
		log.Printf("report the cycles of blockers: %v", err)
	}
}

func (e *Engine) sendBlockerCycles(ctx context.Context) error {
	rows, err := e.queries.ListBlockerEdges(ctx)
	if err != nil {
		return err
	}
	edges := map[blockerIssue][]blockerIssue{}
	workstreams := map[blockerIssue][]blockerWorkstream{}
	for _, row := range rows {
		blocked := blockerIssue{repositoryOfURL(row.RepositoryUrl), row.Number}
		blocker := blockerIssue{row.BlockerRepository, row.BlockerNumber}
		if !slices.Contains(edges[blocked], blocker) {
			edges[blocked] = append(edges[blocked], blocker)
		}
		node := blockerWorkstream{row.Repository, row.Workstream}
		if !slices.Contains(workstreams[blocked], node) {
			workstreams[blocked] = append(workstreams[blocked], node)
		}
	}
	for _, blockers := range edges {
		slices.SortFunc(blockers, compareBlockerIssues)
	}
	stored, err := e.queries.ListBlockerCycles(ctx)
	if err != nil {
		return err
	}
	reported := map[blockerCycle]bool{}
	for _, row := range stored {
		reported[blockerCycle{row.Cycle, row.Repository, row.Workstream}] = true
	}
	found := map[string]bool{}
	for _, component := range cycleComponents(edges) {
		references := make([]string, 0, len(component))
		for _, issue := range component {
			references = append(references, issue.String())
		}
		key := strings.Join(references, " ")
		found[key] = true
		var targets []blockerWorkstream
		for _, issue := range component {
			for _, node := range workstreams[issue] {
				if !slices.Contains(targets, node) {
					targets = append(targets, node)
				}
			}
		}
		path := cyclePath(edges, component)
		for _, target := range targets {
			if reported[blockerCycle{key, target.repository, target.workstream}] {
				continue
			}
			text := blockerCycleText(time.Now(), target.repository, component, path)
			if err := e.addLeadEvent(ctx, target.repository, target.workstream, "", sql.NullInt64{}, "blocker_cycle", text); err != nil {
				return err
			}
			err := e.queries.AddBlockerCycle(ctx, store.AddBlockerCycleParams{Cycle: key, Repository: target.repository, Workstream: target.workstream})
			if err != nil {
				return err
			}
		}
	}
	for _, row := range stored {
		if !found[row.Cycle] {
			if err := e.queries.DeleteBlockerCycle(ctx, row.Cycle); err != nil {
				return err
			}
		}
	}
	return nil
}

// blockerCycle is the row of a reported cycle.
type blockerCycle struct {
	cycle      string
	repository string
	workstream int64
}

// blockerCycleText gives the text of the event for the Workstream in repository. The path is the issues of one cycle
// in blocker order, and component holds all issues of the cycle.
func blockerCycleText(t time.Time, repository string, component, path []blockerIssue) string {
	ref := func(issue blockerIssue) string { return eventRef(repository, issue.repository, issue.number) }
	steps := make([]string, 0, len(path))
	for i, issue := range path {
		step := fmt.Sprintf("%s is blocked by %s", ref(issue), ref(path[(i+1)%len(path)]))
		if i == len(path)-1 {
			step = "and " + step
		}
		steps = append(steps, step)
	}
	text := fmt.Sprintf("%s cycle of blockers: %s. Thus no issue of the cycle can start. Remove one blocker of the cycle with `update_blockers`.",
		t.UTC().Format(timeFormat), strings.Join(steps, ", "))
	var others []string
	for _, issue := range component {
		if !slices.Contains(path, issue) {
			others = append(others, ref(issue))
		}
	}
	switch len(others) {
	case 0:
	case 1:
		text += fmt.Sprintf(" The cycle also holds %s.", others[0])
	default:
		text += fmt.Sprintf(" The cycle also holds %s and %s.", strings.Join(others[:len(others)-1], ", "), others[len(others)-1])
	}
	return text
}

// cycleComponents gives the strongly connected components of the graph with two or more issues, each sorted, in
// sorted order. An edge goes from a blocked issue to its blocker.
func cycleComponents(edges map[blockerIssue][]blockerIssue) [][]blockerIssue {
	nodes := make([]blockerIssue, 0, len(edges))
	for node := range edges {
		nodes = append(nodes, node)
	}
	slices.SortFunc(nodes, compareBlockerIssues)
	index := map[blockerIssue]int{}
	low := map[blockerIssue]int{}
	onStack := map[blockerIssue]bool{}
	var stack []blockerIssue
	var components [][]blockerIssue
	var visit func(node blockerIssue)
	visit = func(node blockerIssue) {
		index[node] = len(index) + 1
		low[node] = index[node]
		stack = append(stack, node)
		onStack[node] = true
		for _, next := range edges[node] {
			if index[next] == 0 {
				visit(next)
				low[node] = min(low[node], low[next])
			} else if onStack[next] {
				low[node] = min(low[node], index[next])
			}
		}
		if low[node] != index[node] {
			return
		}
		var component []blockerIssue
		for {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[last] = false
			component = append(component, last)
			if last == node {
				break
			}
		}
		if len(component) > 1 {
			slices.SortFunc(component, compareBlockerIssues)
			components = append(components, component)
		}
	}
	for _, node := range nodes {
		if index[node] == 0 {
			visit(node)
		}
	}
	slices.SortFunc(components, func(a, b []blockerIssue) int { return compareBlockerIssues(a[0], b[0]) })
	return components
}

// cyclePath gives the issues of one cycle through the component, from its smallest issue, so that each issue is
// blocked by the next one and the last by the first.
func cyclePath(edges map[blockerIssue][]blockerIssue, component []blockerIssue) []blockerIssue {
	start := component[0]
	visited := map[blockerIssue]bool{start: true}
	var walk func(path []blockerIssue) []blockerIssue
	walk = func(path []blockerIssue) []blockerIssue {
		node := path[len(path)-1]
		for _, next := range edges[node] {
			if next == start && len(path) > 1 {
				return path
			}
			if visited[next] || !slices.Contains(component, next) {
				continue
			}
			visited[next] = true
			if found := walk(append(path, next)); found != nil {
				return found
			}
		}
		return nil
	}
	return walk([]blockerIssue{start})
}
