package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
)

// AccessPrompt is the prompt of CheckAccess. The fake agent of the tests answers it with an empty turn.
const AccessPrompt = "Reply with the word OK."

// accessTimeout is the time that the prompt of CheckAccess has.
const accessTimeout = 2 * time.Minute

// CheckAccess starts the Harness in dir with no MCP server, sets the model and the effort, and sends one short prompt.
// The access of the Harness to the model is good when the turn ends with no error. Only a prompt proves it, because a
// model without access passes the start and the set. The error holds the error text of the Harness.
func CheckAccess(ctx context.Context, harness config.Harness, model, effort, dir, dataDir, path string) error {
	session, err := Start(ctx, harness, dir, dataDir, path, "", "", "", "", func(json.RawMessage, bool) {})
	if err != nil {
		return err
	}
	defer session.Close()
	if err := session.Configure(ctx, model, effort); err != nil {
		return err
	}
	limit, cancel := context.WithTimeout(ctx, accessTimeout)
	defer cancel()
	if _, err := session.Prompt(limit, AccessPrompt, nil); err != nil {
		if ctx.Err() == nil && limit.Err() != nil {
			return fmt.Errorf("%s: no answer to the prompt in %s", Program(harness), accessTimeout)
		}
		return fmt.Errorf("%s: %w", Program(harness), describe(err))
	}
	return nil
}
