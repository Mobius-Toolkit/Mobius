package api

import (
	"context"
	"net/http"
	"slices"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
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
	// IssueRepository is the repository of the Workstream issues of the organization as "owner/name", or empty when IssueRepositoryError is not empty
	IssueRepository string `gork:"issueRepository"`
	// IssueRepositoryError tells why Mobius cannot find the issue repository, or is empty
	IssueRepositoryError string `gork:"issueRepositoryError"`
	// MaintenanceWorkstream is the number of the open Workstream with mobius:maintenance and the lowest number in the issue repository, or 0 when it has none or IssueRepositoryError is not empty
	MaintenanceWorkstream int64 `gork:"maintenanceWorkstream"`
	// SkillRepository is the skill repository of the organization as "owner/name", or empty when the organization has none
	SkillRepository string `gork:"skillRepository"`
	// SkillsWorkstreamRepository is the repository of the open Workstream with mobius:skills and the lowest number as "owner/name", or empty when the organization has none
	SkillsWorkstreamRepository string `gork:"skillsWorkstreamRepository"`
	// SkillsWorkstream is the number of the Workstream in SkillsWorkstreamRepository, or 0 when the organization has none
	SkillsWorkstream int64 `gork:"skillsWorkstream"`
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
		Repositories:               make([]RepositoryCheckup, 0, len(found.Repositories)),
		Permissions:                make([]PermissionCheck, 0, len(found.Permissions)),
		PermissionsError:           found.PermissionsError,
		LabelFix:                   labelFix(found.Repositories),
		IssueRepository:            found.IssueRepository,
		IssueRepositoryError:       found.IssueRepositoryError,
		MaintenanceWorkstream:      found.MaintenanceWorkstream,
		SkillRepository:            found.SkillRepository,
		SkillsWorkstreamRepository: found.SkillsWorkstreamRepository,
		SkillsWorkstream:           found.SkillsWorkstream,
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

// ToolCheck is the path and the version of a program that Mobius runs.
type ToolCheck struct {
	// Name is the name of the program
	Name string `gork:"name"`
	// Path is the file that Mobius runs. It is empty when Mobius cannot find the program, and for the Claude Code CLI that is built in the adapter
	Path string `gork:"path"`
	// Version is the first line of the version output. It is empty when Status is not empty
	Version string `gork:"version"`
	// Status is not-found for a program that Mobius cannot find, and no-version for a program that gives no version. It is empty for a program with a version
	Status string `gork:"status"`
	// Newest is the newest version of the npm package of a critical tool. It is empty when the registry gave none, and for a tool that is not critical
	Newest string `gork:"newest"`
	// Outdated tells that the version of a critical tool is older than Newest
	Outdated bool `gork:"outdated"`
	// Required tells that a role uses the Harness of the row, or that the program is git
	Required bool `gork:"required"`
	// Package is the npm package of the row. It is empty for a program that Mobius does not upgrade
	Package string `gork:"package"`
	// AutoUpgrade tells that Mobius installs the newest version of Package by itself
	AutoUpgrade bool `gork:"autoUpgrade"`
}

// GetCheckupToolsRequest is the request of GetCheckupTools.
type GetCheckupToolsRequest struct{}

// GetCheckupToolsResponse is the response of GetCheckupTools.
type GetCheckupToolsResponse struct {
	Body Envelope[[]ToolCheck]
}

// GetCheckupTools returns the path and the version of each program that Mobius runs for its agents and its work.
func (h *handlers) GetCheckupTools(ctx context.Context, _ GetCheckupToolsRequest) (*GetCheckupToolsResponse, error) {
	found := h.engine.Tools(ctx)
	tools := make([]ToolCheck, 0, len(found))
	for _, tool := range found {
		tools = append(tools, ToolCheck(tool))
	}
	return &GetCheckupToolsResponse{Body: Envelope[[]ToolCheck]{Data: tools}}, nil
}

// UpgradeCheckupToolRequest is the request of UpgradeCheckupTool.
type UpgradeCheckupToolRequest struct {
	Body struct {
		// Package is the npm package of the tool to upgrade
		Package string `gork:"package" validate:"required"`
	}
}

// UpgradeCheckupToolResponse is the response of UpgradeCheckupTool.
type UpgradeCheckupToolResponse struct {
	Body Envelope[[]ToolCheck]
}

// UpgradeCheckupTool installs the newest version of the npm package of a tool, and returns the new tool list. It
// returns 409 with the reason when Mobius does not upgrade the package or the npm run fails.
func (h *handlers) UpgradeCheckupTool(ctx context.Context, req UpgradeCheckupToolRequest) (*UpgradeCheckupToolResponse, error) {
	found, err := h.engine.UpgradeTool(ctx, req.Body.Package)
	if err != nil {
		return nil, api.NewHTTPError(http.StatusConflict, err.Error())
	}
	tools := make([]ToolCheck, 0, len(found))
	for _, tool := range found {
		tools = append(tools, ToolCheck(tool))
	}
	return &UpgradeCheckupToolResponse{Body: Envelope[[]ToolCheck]{Data: tools}}, nil
}

// SetToolAutoUpgradeRequest is the request of SetToolAutoUpgrade.
type SetToolAutoUpgradeRequest struct {
	Body struct {
		// Package is the npm package of the tool
		Package string `gork:"package" validate:"required"`
		// On turns the auto-upgrade of the package on when true, and off when false
		On *bool `gork:"on" validate:"required"`
	}
}

// SetToolAutoUpgradeResponse is the response of SetToolAutoUpgrade.
type SetToolAutoUpgradeResponse struct {
	Body Envelope[[]ToolCheck]
}

// SetToolAutoUpgrade turns the auto-upgrade of the npm package of a tool on or off, and returns the new tool list. It
// returns 409 with the reason when Mobius does not upgrade the package.
func (h *handlers) SetToolAutoUpgrade(ctx context.Context, req SetToolAutoUpgradeRequest) (*SetToolAutoUpgradeResponse, error) {
	found, err := h.engine.SetToolAutoUpgrade(ctx, req.Body.Package, *req.Body.On)
	if err != nil {
		return nil, api.NewHTTPError(http.StatusConflict, err.Error())
	}
	tools := make([]ToolCheck, 0, len(found))
	for _, tool := range found {
		tools = append(tools, ToolCheck(tool))
	}
	return &SetToolAutoUpgradeResponse{Body: Envelope[[]ToolCheck]{Data: tools}}, nil
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
