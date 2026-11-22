package engine

import (
	"context"

	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// recordGHWrites stores the comments that the gh of the Lead is about to write.
func (a *Agent) recordGHWrites(ctx context.Context, writes []mcp.GHWrite) error {
	for _, write := range writes {
		err := a.engine.queries.AddLeadWrite(ctx, store.AddLeadWriteParams{
			Session:    a.id,
			Repository: write.Repository,
			Issue:      write.Issue,
			Kind:       write.Kind,
			Labels:     "[]",
			Text:       write.Text,
			CreatedAt:  now(),
		})
		if err != nil {
			return err
		}
	}
	return nil
}
