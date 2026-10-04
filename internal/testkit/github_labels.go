package testkit

import (
	"cmp"
	"net/http"
	"slices"
	"strings"
)

// Label is a label of a repository.
type Label struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

type labelKey struct {
	repository string
	name       string
}

// AddRepositoryLabel adds the label to repository, or replaces the label with the same name.
func (g *FakeGitHub) AddRepositoryLabel(repository, name, color, description string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.repositoryLabels[labelKey{repository, name}] = Label{Name: name, Color: color, Description: description}
}

// DeleteRepositoryLabel deletes the label name of repository.
func (g *FakeGitHub) DeleteRepositoryLabel(repository, name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.repositoryLabels, labelKey{repository, name})
}

// RepositoryLabels gives the labels of repository, in name order.
func (g *FakeGitHub) RepositoryLabels(repository string) []Label {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.labelsOf(repository)
}

// LabelPatches gives the name of each label of repository that got a PATCH request, in request order.
func (g *FakeGitHub) LabelPatches(repository string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	names := []string{}
	for _, patch := range g.labelPatches {
		if patch.repository == repository {
			names = append(names, patch.name)
		}
	}
	return names
}

func (g *FakeGitHub) labelsOf(repository string) []Label {
	labels := []Label{}
	for key, label := range g.repositoryLabels {
		if key.repository == repository {
			labels = append(labels, label)
		}
	}
	slices.SortFunc(labels, func(a, b Label) int { return cmp.Compare(a.Name, b.Name) })
	return labels
}

// findLabel finds a label of repository by its name. GitHub compares label names with no regard to case.
func (g *FakeGitHub) findLabel(repository, name string) (labelKey, bool) {
	for key := range g.repositoryLabels {
		if key.repository == repository && strings.EqualFold(key.name, name) {
			return key, true
		}
	}
	return labelKey{}, false
}

func (g *FakeGitHub) listRepositoryLabels(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	writeJSON(w, http.StatusOK, page(w, r, g.labelsOf(repository(r))))
}

func (g *FakeGitHub) createRepositoryLabel(w http.ResponseWriter, r *http.Request) {
	var label Label
	if !decode(w, r, &label) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.findLabel(repository(r), label.Name); exists {
		message(w, http.StatusUnprocessableEntity, "Validation Failed")
		return
	}
	g.repositoryLabels[labelKey{repository(r), label.Name}] = label
	writeJSON(w, http.StatusCreated, label)
}

func (g *FakeGitHub) updateRepositoryLabel(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Color string `json:"color"`
	}
	if !decode(w, r, &request) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	key, ok := g.findLabel(repository(r), r.PathValue("name"))
	if !ok {
		notFound(w)
		return
	}
	label := g.repositoryLabels[key]
	label.Color = request.Color
	g.repositoryLabels[key] = label
	g.labelPatches = append(g.labelPatches, labelKey{repository(r), r.PathValue("name")})
	writeJSON(w, http.StatusOK, label)
}
