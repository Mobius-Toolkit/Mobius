package runner

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/coder/acp-go-sdk"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
)

// ModelChoice is a model of a Harness with the effort levels that the Harness gives for it.
type ModelChoice struct {
	Model   string
	Efforts []string
}

// ReadChoices starts the Harness in dir with no MCP server, and reads each model with its effort levels. A Harness that
// has no effort levels gives no efforts. The effort list follows the model, so ReadChoices sets each model and reads the
// list that the Harness gives after the set.
func ReadChoices(ctx context.Context, harness config.Harness, dir, dataDir, path string) ([]ModelChoice, error) {
	session, err := Start(ctx, harness, 0, dir, dataDir, path, "", "", "", "", func(json.RawMessage, bool) {})
	if err != nil {
		return nil, err
	}
	defer session.Close()
	models := session.Models().Values
	if len(models) == 0 {
		return nil, errors.New(Program(harness) + " gave no model option")
	}
	choices := make([]ModelChoice, 0, len(models))
	for _, model := range models {
		choice := ModelChoice{Model: model}
		if harness.HasEffortLevels() {
			err := session.limited(ctx, func(ctx context.Context) error {
				return session.set(ctx, acp.SessionConfigOptionCategoryModel, "model", model)
			})
			if err != nil {
				return nil, err
			}
			choice.Efforts = session.Efforts().Values
		}
		choices = append(choices, choice)
	}
	return choices, nil
}
