package engine

import (
	"context"
	"slices"
	"strings"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
)

// Label is a Mobius label.
type Label struct {
	Name string
	// Color is six hex digits with no "#".
	Color       string
	Description string
}

// The names of the Mobius labels that the Mobius tools use.
const (
	workstreamLabel   = "mobius:workstream"
	autopilotLabel    = "mobius:autopilot"
	readyLabel        = "mobius:ready"
	noWorkstreamLabel = "mobius:no-workstream"
	wontDoLabel       = "mobius:wont-do"
)

// Labels are the Mobius labels.
var Labels = []Label{
	{"mobius:workstream", "5319E7", "Mobius Workstream: a parent issue for a group of tasks"},
	{"mobius:autopilot", "1D76DB", "Mobius dispatches the ready tasks of this Workstream"},
	{"mobius:ready", "0E8A16", "Mobius can dispatch this task"},
	{"mobius:working", "FBCA04", "A Mobius agent works on this task"},
	{"mobius:needs-human", "D93F0B", "The task stopped and needs a human"},
	{"mobius:question", "C5A3F5", "Mobius waits for an answer from a human"},
	{"mobius:review", "006B75", "The pull request waits for a human review"},
	{"mobius:no-workstream", "BFD4F2", "The Triager found no Workstream for this issue"},
	{"mobius:wont-do", "CFD3D7", "The Owner closed the Workstream of this issue as \"won't do\""},
}

// The status values of a label check and of a permission check.
const (
	Present = "present"
	Missing = "missing"
	// WrongColor is a label with a different color.
	WrongColor = "wrong-color"
	// WrongCase is a label with its name in a different case. Mobius compares label names
	// exactly, so it does not see this label on issues. Mobius does not rename labels.
	WrongCase = "wrong-case"
	// NotAccepted is a permission that the App has and the installation does not.
	NotAccepted = "not-accepted"
)

// LabelCheck is the status of a Mobius label in a repository.
type LabelCheck struct {
	Label  Label
	Status string
	// Found is the color on GitHub for WrongColor, and the name on GitHub for WrongCase.
	Found string
}

// Fixable tells if FixLabels fixes the label.
func (c LabelCheck) Fixable() bool {
	return c.Status == Missing || c.Status == WrongColor
}

// CheckLabels gives the status of each Mobius label in repository, in the order of Labels.
func CheckLabels(ctx context.Context, repository github.Repository) ([]LabelCheck, error) {
	owner, name, _ := strings.Cut(repository.FullName, "/")
	var found []*gh.Label
	for label, err := range repository.Client.Issues.ListLabelsIter(ctx, owner, name, &gh.ListOptions{PerPage: 100}) {
		if err != nil {
			return nil, err
		}
		found = append(found, label)
	}
	checks := make([]LabelCheck, 0, len(Labels))
	for _, label := range Labels {
		// GitHub compares label names with no regard to case, and gives colors in lowercase.
		i := slices.IndexFunc(found, func(other *gh.Label) bool { return strings.EqualFold(other.GetName(), label.Name) })
		check := LabelCheck{Label: label}
		switch {
		case i < 0:
			check.Status = Missing
		case found[i].GetName() != label.Name:
			check.Status, check.Found = WrongCase, found[i].GetName()
		case strings.EqualFold(found[i].GetColor(), label.Color):
			check.Status = Present
		default:
			check.Status, check.Found = WrongColor, found[i].GetColor()
		}
		checks = append(checks, check)
	}
	return checks, nil
}

// FixLabels creates the missing Mobius labels in repository and sets the fixed color of each
// Mobius label with a different color. The description of an existing label stays.
func FixLabels(ctx context.Context, repository github.Repository) error {
	checks, err := CheckLabels(ctx, repository)
	if err != nil {
		return err
	}
	owner, name, _ := strings.Cut(repository.FullName, "/")
	for _, check := range checks {
		label := check.Label
		switch check.Status {
		case Missing:
			_, _, err = repository.Client.Issues.CreateLabel(ctx, owner, name, gh.CreateIssueLabelRequest{
				Name:        label.Name,
				Color:       &label.Color,
				Description: &label.Description,
			})
		case WrongColor:
			_, _, err = repository.Client.Issues.UpdateLabel(ctx, owner, name, label.Name, gh.UpdateIssueLabelRequest{Color: &label.Color})
		}
		if err != nil {
			return err
		}
	}
	return nil
}
