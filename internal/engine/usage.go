package engine

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/coder/acp-go-sdk"
)

// turnUsage is the usage data of one turn. A value that the Harness did not send is not valid.
type turnUsage struct {
	started    string
	model      sql.NullString
	input      sql.NullInt64
	output     sql.NullInt64
	cacheRead  sql.NullInt64
	cacheWrite sql.NullInt64
	// costSeen tells that a cost total came in the turn.
	costSeen bool
}

// readUsage adds a usage_update to the usage data of the turn that runs and to the cost total of the session.
// The caller holds a.mu.
func (a *Agent) readUsage(notification map[string]any, kind string) {
	if kind != "usage_update" {
		return
	}
	update := notification["update"]
	if stringField(update, "cost", "currency") == "USD" {
		if amount, ok := field(update, "cost", "amount").(json.Number); ok {
			if total, err := amount.Float64(); err == nil {
				a.costTotal = total
				if a.turn {
					a.turnUsage.costSeen = true
				}
			}
		}
	}
	if !a.turn {
		return
	}
	switch a.harness {
	case config.ClaudeCode:
		claudeUpdate(&a.turnUsage, update)
	case config.Devin:
		devinUpdate(&a.turnUsage, update)
	}
}

// readResult adds the result of session/prompt to the usage data of the turn.
func readResult(usage *turnUsage, harness config.Harness, result runner.PromptResult) {
	if harness == config.ClaudeCode {
		claudeResult(usage, result)
	}
}

// claudeUpdate reads the model that _meta._claude/model of a usage_update gives. This key is an extension of the
// adapter of Claude Code.
func claudeUpdate(usage *turnUsage, update any) {
	if model := stringField(update, "_meta", "_claude/model"); model != "" {
		usage.model = sql.NullString{String: model, Valid: true}
	}
}

// claudeResult reads the tokens of the turn from the usage field of the result of session/prompt. This field is a
// draft of ACP, and the adapter of Claude Code fills it with the tokens of the turn.
func claudeResult(usage *turnUsage, result runner.PromptResult) {
	var tokens struct {
		Input      *int64 `json:"inputTokens"`
		Output     *int64 `json:"outputTokens"`
		CacheRead  *int64 `json:"cachedReadTokens"`
		CacheWrite *int64 `json:"cachedWriteTokens"`
	}
	if json.Unmarshal(result.Usage, &tokens) != nil {
		return
	}
	usage.input = nullInt(tokens.Input)
	usage.output = nullInt(tokens.Output)
	usage.cacheRead = nullInt(tokens.CacheRead)
	usage.cacheWrite = nullInt(tokens.CacheWrite)
}

// devinUpdate adds the tokens of a usage_update to the usage data. Devin sends the tokens in the cognition.ai/*
// keys of _meta, one usage_update for each model call, and a second copy of each with the key
// cognition.ai/subagent_context. Devin sends no cache write tokens and no model.
func devinUpdate(usage *turnUsage, update any) {
	if field(update, "_meta", "cognition.ai/subagent_context") != nil {
		return
	}
	addTokens(&usage.input, field(update, "_meta", "cognition.ai/inputTokens"))
	addTokens(&usage.output, field(update, "_meta", "cognition.ai/outputTokens"))
	addTokens(&usage.cacheRead, field(update, "_meta", "cognition.ai/cachedReadTokens"))
}

func addTokens(sum *sql.NullInt64, value any) {
	number, ok := value.(json.Number)
	if !ok {
		return
	}
	tokens, err := number.Int64()
	if err != nil {
		return
	}
	sum.Int64 += tokens
	sum.Valid = true
}

func nullInt(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

// addUsage adds the usage row of the turn that ended with result and promptErr. A turn that ends with no error and
// no cancel always has a row, also when all the tokens and the cost are NULL. A turn that ends with an error or a
// cancel has a row only if it has tokens or a cost.
//
// The cost total of the session is the total of the Harness, so the cost of the turn is the difference to the total
// at the end of the last turn with a cost. A total that fell belongs to a new process of the session, so the cost
// of the turn is the new total.
func (a *Agent) addUsage(ctx context.Context, result runner.PromptResult, promptErr error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	usage := a.turnUsage
	readResult(&usage, a.harness, result)
	var cost sql.NullFloat64
	if usage.costSeen {
		cost = a.takeCost()
	}
	hasData := usage.input.Valid || usage.output.Valid || usage.cacheRead.Valid || usage.cacheWrite.Valid || cost.Valid
	ended := promptErr == nil && result.StopReason != acp.StopReasonCancelled
	if !hasData && !ended {
		return nil
	}
	endedAt := now()
	a.turnUsage = turnUsage{started: endedAt, model: usage.model}
	return a.insertUsage(ctx, usage, cost, endedAt)
}

// addAutonomousUsage adds a usage row with the cost that autonomous turns added after the last row. The caller
// calls it when the session ends, so no more updates come.
func (a *Agent) addAutonomousUsage(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.costTotal == a.costBase {
		return nil
	}
	return a.insertUsage(ctx, a.turnUsage, a.takeCost(), now())
}

// takeCost gives the cost since the last row and moves costBase to the cost total. The caller holds a.mu.
func (a *Agent) takeCost() sql.NullFloat64 {
	cost := sql.NullFloat64{Float64: a.costTotal, Valid: true}
	if a.costTotal >= a.costBase {
		cost.Float64 -= a.costBase
	}
	a.costBase = a.costTotal
	return cost
}

// insertUsage adds the usage row. The caller holds a.mu.
func (a *Agent) insertUsage(ctx context.Context, usage turnUsage, cost sql.NullFloat64, endedAt string) error {
	spec := a.spec
	binding, _ := roleBinding(a.engine.config, spec.Role)
	return a.engine.queries.AddTurnUsage(ctx, store.AddTurnUsageParams{
		Session:          a.id,
		Task:             sql.NullInt64{Int64: spec.Task, Valid: spec.Task != 0},
		Issue:            spec.Issue,
		Workstream:       spec.Workstream,
		Organization:     spec.Organization,
		Repository:       spec.Repository,
		Role:             spec.Role,
		Harness:          string(a.harness),
		Model:            binding.Model,
		ReportedModel:    usage.model,
		Effort:           sql.NullString{String: binding.Effort, Valid: binding.Effort != ""},
		StartedAt:        usage.started,
		EndedAt:          endedAt,
		InputTokens:      usage.input,
		OutputTokens:     usage.output,
		CacheReadTokens:  usage.cacheRead,
		CacheWriteTokens: usage.cacheWrite,
		CostUsd:          cost,
	})
}
