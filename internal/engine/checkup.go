package engine

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
)

// Checkup is the status of the App permissions and of the Mobius labels in an organization.
type Checkup struct {
	Repositories []RepositoryCheckup
	Permissions  []PermissionCheck
	// PermissionsError is the error of the permission check. The label status does not depend on it.
	PermissionsError string
}

// RepositoryCheckup is the status of each Mobius label in a repository.
type RepositoryCheckup struct {
	Repository string
	Labels     []LabelCheck
}

// PermissionCheck is the status of a permission that the Mobius App needs.
type PermissionCheck struct {
	Name string
	// Level is the required level.
	Level string
	// Status is Present, NotAccepted or Missing.
	Status string
	// URL is the page of GitHub where the Owner accepts a NotAccepted permission or adds a Missing permission.
	URL string
}

// managed gives the repositories of the organization, in name order.
func (e *Engine) managed(organization string) []github.Repository {
	repositories := slices.DeleteFunc(e.github.Repositories(), func(r github.Repository) bool {
		owner, _, _ := strings.Cut(r.FullName, "/")
		return owner != organization
	})
	slices.SortFunc(repositories, func(a, b github.Repository) int { return cmp.Compare(a.FullName, b.FullName) })
	return repositories
}

// Checkup gives the status of the App permissions and of each Mobius label in each repository of the organization.
func (e *Engine) Checkup(ctx context.Context, organization string) (Checkup, error) {
	repositories := e.managed(organization)
	var checkup Checkup
	if len(repositories) > 0 {
		permissions, err := e.permissions(ctx, repositories[0].AppID, organization)
		if err != nil {
			checkup.PermissionsError = err.Error()
		}
		checkup.Permissions = permissions
	}
	for _, repository := range repositories {
		labels, err := CheckLabels(ctx, repository)
		if err != nil {
			return Checkup{}, err
		}
		checkup.Repositories = append(checkup.Repositories, RepositoryCheckup{Repository: repository.FullName, Labels: labels})
	}
	return checkup, nil
}

func (e *Engine) permissions(ctx context.Context, appID int64, organization string) ([]PermissionCheck, error) {
	access, err := e.github.AppAccess(ctx, appID, organization)
	if err != nil {
		return nil, err
	}
	checks := make([]PermissionCheck, 0, len(github.RequiredPermissions))
	for _, permission := range github.RequiredPermissions {
		check := PermissionCheck{Name: permission.Name, Level: permission.Level, Status: Present}
		switch {
		case github.Grants(access.InstallationPermissions, permission.Name, permission.Level):
		case github.Grants(access.AppPermissions, permission.Name, permission.Level):
			check.Status, check.URL = NotAccepted, access.InstallationURL
		default:
			check.Status, check.URL = Missing, access.AppPermissionsURL
		}
		checks = append(checks, check)
	}
	return checks, nil
}

// FixOrganizationLabels runs FixLabels on each repository of the organization.
func (e *Engine) FixOrganizationLabels(ctx context.Context, organization string) error {
	for _, repository := range e.managed(organization) {
		if err := FixLabels(ctx, repository); err != nil {
			return err
		}
	}
	return nil
}
