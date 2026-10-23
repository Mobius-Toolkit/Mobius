package engine

import (
	"context"
	"log"
	"slices"

	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// moveOldSkillUses moves the rows of old_skill_uses into skill_uses. A use has the source mobius when the skill
// repository of its organization had a skill with that name in a commit. The rows of an organization whose names
// Mobius cannot read stay for the next start.
func (e *Engine) moveOldSkillUses(ctx context.Context) {
	rows, err := e.queries.ListOldSkillUses(ctx)
	if err != nil {
		log.Printf("read the old skill uses: %v", err)
		return
	}
	names := map[string][]string{}
	failed := map[string]bool{}
	for _, row := range rows {
		if failed[row.Organization] {
			continue
		}
		known, found := names[row.Organization]
		if !found {
			known, err = e.skillNamesEver(ctx, row.Organization)
			if err != nil {
				log.Printf("read the skill names of %s for the old skill uses: %v", row.Organization, err)
				failed[row.Organization] = true
				continue
			}
			names[row.Organization] = known
		}
		source := "harness"
		if slices.Contains(known, row.Skill) {
			source = "mobius"
		}
		err := e.inTx(ctx, func(queries *store.Queries) error {
			err := queries.AddSkillUse(ctx, store.AddSkillUseParams{
				Session:      row.Session,
				Organization: row.Organization,
				Repository:   row.Repository,
				Role:         row.Role,
				Harness:      row.Harness,
				Skill:        row.Skill,
				Source:       source,
				UsedAt:       row.UsedAt,
			})
			if err != nil {
				return err
			}
			return queries.DeleteOldSkillUse(ctx, row.ID)
		})
		if err != nil {
			log.Printf("move the old skill use %d: %v", row.ID, err)
		}
	}
}

// skillNamesEver gives the names that the skill repository of organization ever had, and none when the organization has
// no skill repository.
func (e *Engine) skillNamesEver(ctx context.Context, organization string) ([]string, error) {
	repository, ok := e.github.SkillRepository(organization)
	if !ok {
		return nil, nil
	}
	if err := e.fetchRepository(ctx, repository); err != nil {
		log.Printf("fetch of %s for the old skill uses failed, use the last fetch: %v", repository.FullName, err)
	}
	e.gitMu.Lock()
	defer e.gitMu.Unlock()
	return runner.SkillNamesEver(ctx, e.config.DataDir, repository.FullName, repository.DefaultBranch)
}
