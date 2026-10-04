package api

import (
	"context"
	"slices"

	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
)

// GetCheckupRequest is the request of GetCheckup.
type GetCheckupRequest struct {
	Query struct {
		// Organization is the owner of the repositories
		Organization string `gork:"organization" validate:"required"`
	}
}

// LabelCheck is the status of a Mobius label in a repository.
type LabelCheck struct {
	// Name is the name of the Mobius label
	Name string `gork:"name"`
	// Color is the fixed color of the Mobius label, as six hex digits with no "#"
	Color string `gork:"color"`
	// Status is wrong-color for a label with a different color, and wrong-case for a label with its name in a different case. Mobius does not rename labels, so the Owner fixes a wrong case
	Status string `gork:"status" validate:"oneof=present wrong-color wrong-case missing"`
	// Found is the color on GitHub for wrong-color, and the name on GitHub for wrong-case
	Found string `gork:"found"`
}

// RepositoryCheckup is the status of each Mobius label in a repository.
type RepositoryCheckup struct {
	// Repository is the repository as "owner/name"
	Repository string `gork:"repository"`
	// Labels are the Mobius labels
	Labels []LabelCheck `gork:"labels"`
}

// PermissionCheck is the status of a permission that the Mobius App needs.
type PermissionCheck struct {
	// Name is the name of the permission
	Name string `gork:"name"`
	// Level is the required level
	Level string `gork:"level"`
	// Status is not-accepted when the App has the permission and the installation does not
	Status string `gork:"status" validate:"oneof=present not-accepted missing"`
	// URL is the page of GitHub where the Owner accepts or adds the permission. It is empty for present
	URL string `gork:"url"`
}

// Checkup is the status of the App permissions and of the Mobius labels in an organization.
type Checkup struct {
	// Repositories are the repositories of the organization, in name order
	Repositories []RepositoryCheckup `gork:"repositories"`
	// Permissions are the permissions of the App of the first repository. It is empty when the organization has no repository or the check failed
	Permissions []PermissionCheck `gork:"permissions"`
	// PermissionsError is the error of the permission check, or empty
	PermissionsError string `gork:"permissionsError"`
	// LabelFix is create when all Mobius labels are missing, fix when another label is missing or has a different color, and none when the fix has nothing to change
	LabelFix string `gork:"labelFix" validate:"oneof=create fix none"`
}

// GetCheckupResponse is the response of GetCheckup.
type GetCheckupResponse struct {
	Body Envelope[Checkup]
}

// GetCheckup returns the status of the App permissions and of each Mobius label in each repository of the organization.
func (h *handlers) GetCheckup(ctx context.Context, req GetCheckupRequest) (*GetCheckupResponse, error) {
	found, err := h.engine.Checkup(ctx, req.Query.Organization)
	if err != nil {
		return nil, err
	}
	checkup := Checkup{
		Repositories:     make([]RepositoryCheckup, 0, len(found.Repositories)),
		Permissions:      make([]PermissionCheck, 0, len(found.Permissions)),
		PermissionsError: found.PermissionsError,
		LabelFix:         labelFix(found.Repositories),
	}
	for _, repository := range found.Repositories {
		labels := make([]LabelCheck, 0, len(repository.Labels))
		for _, label := range repository.Labels {
			labels = append(labels, LabelCheck{Name: label.Label.Name, Color: label.Label.Color, Status: label.Status, Found: label.Found})
		}
		checkup.Repositories = append(checkup.Repositories, RepositoryCheckup{Repository: repository.Repository, Labels: labels})
	}
	for _, permission := range found.Permissions {
		checkup.Permissions = append(checkup.Permissions, PermissionCheck(permission))
	}
	return &GetCheckupResponse{Body: Envelope[Checkup]{Data: checkup}}, nil
}

func labelFix(repositories []engine.RepositoryCheckup) string {
	fixable := slices.ContainsFunc(repositories, func(r engine.RepositoryCheckup) bool {
		return slices.ContainsFunc(r.Labels, engine.LabelCheck.Fixable)
	})
	allMissing := !slices.ContainsFunc(repositories, func(r engine.RepositoryCheckup) bool {
		return slices.ContainsFunc(r.Labels, func(label engine.LabelCheck) bool { return label.Status != engine.Missing })
	})
	switch {
	case !fixable:
		return "none"
	case allMissing:
		return "create"
	default:
		return "fix"
	}
}

// FixLabelsRequest is the request of FixLabels.
type FixLabelsRequest struct {
	Body struct {
		// Organization is the owner of the repositories
		Organization string `gork:"organization" validate:"required"`
	}
}

// FixLabels creates the missing Mobius labels and sets the fixed color of each Mobius label with a
// different color, in each repository of the organization. The description of an existing label stays.
func (h *handlers) FixLabels(ctx context.Context, req FixLabelsRequest) error {
	return h.engine.FixOrganizationLabels(ctx, req.Body.Organization)
}
