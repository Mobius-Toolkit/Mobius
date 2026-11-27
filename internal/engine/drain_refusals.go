package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// drainEndedKind is the kind of the Lead event that lists the calls that the drain refused.
const drainEndedKind = "drain_ended"

// drainEndedHeading is the first sentence of the text of the Lead event and of the Coordinator message. The prompts
// name it.
const drainEndedHeading = "The drain of an upgrade ended."

// drainEndedText gives the text of the Lead event and of the Coordinator message. The JSON of drainEnded follows
// its sentences.
func drainEndedText(restarted bool, release string, encoded []byte) string {
	ending := "The upgrade did not happen."
	switch {
	case restarted && release != "":
		ending = "Mobius restarted with the release " + release + "."
	case restarted:
		ending = "Mobius restarted."
	}
	return drainEndedHeading + " " + ending + " Mobius refused the calls in the JSON below during the drain. Call each tool again when the work still needs it.\n\n" + string(encoded)
}

type drainCall struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

type drainEnded struct {
	Restarted bool        `json:"restarted"`
	Release   string      `json:"release"`
	Calls     []drainCall `json:"calls"`
}

// refuseInDrain gives the refusal of a start of a session of kind noun (for example "Operator") while the drain is on,
// and gives nil otherwise. For the Lead and the Coordinator, it also stores the call, so giveDrainRefusals tells the
// agent after the drain. It stores the call while it holds the lock of the drain, so releaseDrain reads each call that
// a refusal stored.
func (e *Engine) refuseInDrain(ctx context.Context, c caller, tool, noun string, input any) error {
	d := &e.drain
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.on {
		return nil
	}
	if role := c.agent.spec.Role; role == LeadRole || role == CoordinatorRole {
		arguments, err := json.Marshal(input)
		if err != nil {
			return err
		}
		err = e.queries.AddDrainRefusal(ctx, store.AddDrainRefusalParams{
			Organization: c.organization,
			Repository:   c.repository,
			Workstream:   c.workstream,
			Tool:         tool,
			Arguments:    string(arguments),
			CreatedAt:    now(),
		})
		if err != nil {
			return err
		}
	}
	return refuse("Mobius prepares an upgrade, so no %s starts now.", noun)
}

// giveDrainRefusals tells each Lead and the Coordinator the calls that the drain refused, and deletes them. restarted
// tells that Mobius restarted after the drain. A Lead gets a Lead event, and the Coordinator gets a Mobius message.
func (e *Engine) giveDrainRefusals(ctx context.Context, restarted bool) error {
	refusals, err := e.queries.ListDrainRefusals(ctx)
	if err != nil {
		return err
	}
	type agentKey struct {
		organization, repository string
		workstream               int64
	}
	var keys []agentKey
	calls := map[agentKey][]drainCall{}
	last := map[agentKey]int64{}
	for _, refusal := range refusals {
		key := agentKey{refusal.Organization, refusal.Repository, refusal.Workstream}
		if _, ok := calls[key]; !ok {
			keys = append(keys, key)
		}
		calls[key] = append(calls[key], drainCall{Tool: refusal.Tool, Arguments: json.RawMessage(refusal.Arguments)})
		last[key] = refusal.ID
	}
	for _, key := range keys {
		encoded, err := json.Marshal(drainEnded{Restarted: restarted, Release: Release, Calls: calls[key]})
		if err != nil {
			return err
		}
		text := drainEndedText(restarted, Release, encoded)
		remove := func(q *store.Queries) error {
			return q.DeleteDrainRefusals(ctx, store.DeleteDrainRefusalsParams{Organization: key.organization, Repository: key.repository, Workstream: key.workstream, ID: last[key]})
		}
		if key.workstream != 0 {
			err = e.addLeadEventWith(ctx, key.repository, key.workstream, "", sql.NullInt64{}, drainEndedKind, text, remove)
		} else {
			err = e.giveCoordinatorDrainRefusals(ctx, ChatKey{Organization: key.organization}, text, remove)
		}
		if err != nil {
			log.Printf("tell %s #%d the calls that the drain refused: %v", key.repository, key.workstream, err)
		}
	}
	return nil
}

// giveCoordinatorDrainRefusals adds the Mobius message text to the Coordinator chat of key, and runs remove in the same
// transaction.
func (e *Engine) giveCoordinatorDrainRefusals(ctx context.Context, key ChatKey, text string, remove func(*store.Queries) error) error {
	e.chatOrder.Lock()
	defer e.chatOrder.Unlock()
	var message store.ChatMessage
	err := e.inTx(ctx, func(q *store.Queries) error {
		var err error
		if message, err = e.storeChatMessage(ctx, q, key, mobiusAuthor, text, "", "", nil, nil, false, 0); err != nil {
			return err
		}
		return remove(q)
	})
	if err != nil {
		return err
	}
	if err := e.announceChatMessage(ctx, key, mobiusAuthor, message); err != nil {
		return err
	}
	e.give(key, false, item{message: &message})
	return nil
}
