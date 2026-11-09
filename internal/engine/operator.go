package engine

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
)

//go:embed prompts/operator.md
var operatorPrompt string

// operatorKey is the key in e.stops of the Worker of the Operator with this session id.
type operatorKey int64

type operatorInput struct {
	Text string `json:"text"`
}

// operatorDir gives the directory of the Operators of the Workstream. The directory keeps the memory of the Operators.
func operatorDir(dataDir, repository string, workstream int64) string {
	return filepath.Join(dataDir, "operators", repository, strconv.FormatInt(workstream, 10))
}

// startOperator starts an Operator with the text of the Lead. The report goes to the Lead chat as an Operator message.
// It gives the id of the Operator.
func (e *Engine) startOperator(ctx context.Context, c caller, repository github.Repository, input operatorInput) (string, error) {
	if empty(input.Text) {
		return "", refuse("text must not be empty.")
	}
	if e.draining() {
		return "", refuse("Mobius prepares an upgrade, so no Operator starts now.")
	}
	a, err := e.newAgent(ctx, Spec{
		Role:         OperatorRole,
		Organization: c.organization,
		Repository:   c.repository,
		Workstream:   c.workstream,
		Dir:          operatorDir(e.config.DataDir, c.repository, c.workstream),
		Parent:       sql.NullInt64{Int64: c.session, Valid: true},
	})
	if err != nil {
		return "", err
	}
	key := operatorKey(a.id)
	started := e.startWorker(key, func(ctx context.Context) {
		defer e.stop(key)
		if err := e.operate(ctx, c, repository, a, input.Text); err != nil {
			log.Printf("Operator %d of %s#%d: %v", a.id, c.repository, c.workstream, err)
		}
	})
	if !started {
		return "", errors.Join(refuse("Mobius stops, so no Operator starts now."), a.End(context.WithoutCancel(ctx), "declined"))
	}
	return fmt.Sprintf("Started the Operator %d. The report arrives later as an Operator message.", a.id), nil
}

// operate runs the session a of the Operator on the text. A failed Operator reports its error. A stop ends the session
// with no report. The report goes to the chat before the session ends, because the end frees the drain count, and
// the chat refuses a message after the seal of an upgrade.
func (e *Engine) operate(ctx context.Context, c caller, repository github.Repository, a *Agent, text string) error {
	if err := a.waitForSlot(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	report, err := e.operateTurn(ctx, repository, a, text)
	a.closeHarness()
	ended := context.WithoutCancel(ctx)
	switch {
	case ctx.Err() != nil:
		return a.End(ended, "stopped")
	case errors.Is(err, errHung):
		delivery := e.deliverOperatorReport(ended, c, a.id, fmt.Sprintf("The Operator %d failed: %s", a.id, err))
		return errors.Join(delivery, a.endHung(ended))
	case err != nil:
		delivery := e.deliverOperatorReport(ended, c, a.id, fmt.Sprintf("The Operator %d failed: %s", a.id, err))
		return errors.Join(delivery, a.Fail(ended, err))
	}
	delivery := e.deliverOperatorReport(ended, c, a.id, report)
	return errors.Join(delivery, a.End(ended, "done"))
}

// operateTurn makes the directory of the Operator a and runs its turn. It gives the report: the reply text after the
// last tool call.
func (e *Engine) operateTurn(ctx context.Context, repository github.Repository, a *Agent, text string) (string, error) {
	if err := os.MkdirAll(a.spec.Dir, 0o750); err != nil {
		return "", err
	}
	brief, err := brief(ctx, repository, a.spec.Workstream)
	if err != nil {
		return "", err
	}
	memory, err := readMemory(a.spec.Dir)
	if err != nil {
		return "", err
	}
	if err := a.open(ctx); err != nil {
		return "", err
	}
	memorySection := ""
	if memory = strings.TrimSpace(memory); memory != "" {
		memorySection = "# MEMORY.md\n\n" + memory + "\n\n"
	}
	prompt := fmt.Sprintf("%s\n# Brief\n\n%s\n\n%s# Text\n\n%s", operatorPrompt, brief, memorySection, text)
	err = a.Prompt(ctx, prompt, nil)
	if err == nil {
		err = a.waitBackground(ctx, backgroundPrompt)
	}
	return a.replyText(), err
}

// deliverOperatorReport gives the report of the Operator id to the Lead chat as an Operator message.
func (e *Engine) deliverOperatorReport(ctx context.Context, c caller, id int64, report string) error {
	text := fmt.Sprintf("Report of the Operator %d:\n\n%s", id, report)
	return e.postChat(ctx, leadChat(c.repository, c.workstream), operatorAuthor, "", text, nil, nil, false)
}
