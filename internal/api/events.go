package api

import (
	"context"
	"time"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
)

// pingInterval is the time between two ping events. The client connects again after two intervals with no ping.
var pingInterval = 15 * time.Second

// StreamEventsRequest is the request of StreamEvents.
type StreamEventsRequest struct{}

// LiveEvents are the events of the live event stream.
type LiveEvents struct {
	// Agent is a session at its start and at its end
	Agent *Agent `gork:"agent"`
	// Drain is the state of the drain at each change
	Drain *Drain `gork:"drain"`
	// Upgrade is the state of the last upgrade at its start and at its failure
	Upgrade *Upgrade `gork:"upgrade"`
	// Workstreams is a change of the Workstream list or of the tasks of a Workstream. It has no data, so the client
	// reads the lists again
	Workstreams *struct{} `gork:"workstreams"`
	// Repositories is a change of the repositories of the Apps. It has no data, so the client reads the Apps and the
	// organizations again
	Repositories *struct{} `gork:"repositories"`
	// Message is a new chat message, or a message of the Lead or of the Coordinator that got more text
	Message *ChatMessage `gork:"message"`
	// Unread is the new number of unread messages of a chat
	Unread *Unread `gork:"unread"`
	// Chat is the state of a chat at the start and at the end of the work of its agent
	Chat *ChatState `gork:"chat"`
	// Inbox is a new or dismissed Inbox item
	Inbox *InboxItem `gork:"inbox"`
	// Action is a new pending action, and each change of an action
	Action *PendingAction `gork:"action"`
	// Question is a question item with a chat message, when the question ends, with its final answer and withdrawn
	Question *InboxItem `gork:"question"`
	// Pauses is the list of the pauses of all Harnesses at each start and each end of a pause, and at each change of a Role or of a session that waits for a pause
	Pauses *[]Pause `gork:"pauses"`
	// CIStops is the list of the CI stops of all repositories at each change of a stop
	CIStops *[]CIStop `gork:"ciStops"`
	// Switches is the state of the global switches with the held work at each change
	Switches *Switches `gork:"switches"`
	// WorkstreamCreated is a Workstream that the Coordinator chat created, so the client can open it
	WorkstreamCreated *WorkstreamRef `gork:"workstreamCreated"`
	// Ping has no data. The server sends it at a fixed interval, so the client can find a connection that is dead
	Ping *struct{} `gork:"ping"`
}

// ChatState is the state of a chat.
type ChatState struct {
	// Organization is the owner of the repository, or the organization of the Coordinator chat
	Organization string `gork:"organization"`
	// Repository is the repository of the Workstream as "owner/name". It is empty for the Coordinator chat
	Repository string `gork:"repository"`
	// Workstream is the number of the Workstream issue. It is 0 for the Coordinator chat
	Workstream int64 `gork:"workstream"`
	// Writing is true while the agent has a turn that runs or an item that waits
	Writing bool `gork:"writing"`
	// Stoppable is true while a stop ends the turn that answers a message, or drops a message of the Owner that waits
	Stoppable bool `gork:"stoppable"`
	// PausedUntil is the end of the usage-limit pause that the agent waits for, or null while the agent does not wait for a pause
	PausedUntil *time.Time `gork:"pausedUntil"`
	// Error is the error that ended the last session of the agent, or empty
	Error string `gork:"error"`
}

// WorkstreamRef names a Workstream.
type WorkstreamRef struct {
	// Repository is the repository of the Workstream issue, as "owner/name"
	Repository string `gork:"repository"`
	// Number is the number of the Workstream issue
	Number int64 `gork:"number"`
}

// StreamEvents sends a ping at once, and then each change of a session,
// each change of the drain, each start and failure of an upgrade,
// each change of the Workstream list, each new or longer chat message, each new unread count, each change of the
// state of a chat, each new or dismissed Inbox item, each new or changed pending action, each change of the pauses, each change of the CI stops, each change of the global switches, and each Workstream that the Coordinator chat created.
// It also sends a ping at a fixed interval.
// When the client does not read the session changes fast enough, the stream ends.
func (h *handlers) StreamEvents(ctx context.Context, _ StreamEventsRequest, stream *api.Stream[LiveEvents]) error {
	changes, stop := h.engine.Listen()
	defer stop()
	if err := stream.Send(LiveEvents{Ping: &struct{}{}}); err != nil {
		return err
	}
	pings := time.NewTicker(pingInterval)
	defer pings.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case change, ok := <-changes:
			if !ok {
				return nil
			}
			if err := h.sendChange(ctx, stream, change); err != nil {
				return err
			}
		case <-pings.C:
			if err := stream.Send(LiveEvents{Ping: &struct{}{}}); err != nil {
				return err
			}
		}
	}
}

func (h *handlers) sendChange(ctx context.Context, stream *api.Stream[LiveEvents], change engine.Change) error {
	switch {
	case change.Line != nil:
		// StreamTranscript sends the Transcript rows.
		return nil
	case change.Drain != nil:
		return stream.Send(LiveEvents{Drain: new(drainOf(*change.Drain))})
	case change.Upgrade != nil:
		return stream.Send(LiveEvents{Upgrade: &Upgrade{Failure: *change.Upgrade}})
	case change.Workstreams:
		return stream.Send(LiveEvents{Workstreams: &struct{}{}})
	case change.Repositories:
		return stream.Send(LiveEvents{Repositories: &struct{}{}})
	case change.Message != nil:
		message, err := h.chatMessageOf(ctx, *change.Message)
		if err != nil {
			return err
		}
		return stream.Send(LiveEvents{Message: &message})
	case change.Unread != nil:
		return stream.Send(LiveEvents{Unread: new(unreadOf(*change.Unread))})
	case change.Chat != nil:
		key := change.Chat.Key
		pausedUntil, err := h.engine.ChatPausedUntil(ctx, key)
		if err != nil {
			return err
		}
		return stream.Send(LiveEvents{Chat: &ChatState{
			Organization: key.Organization,
			Repository:   key.Repository,
			Workstream:   key.Workstream,
			Writing:      change.Chat.Writing,
			Stoppable:    change.Chat.Stoppable,
			PausedUntil:  pausedUntil,
			Error:        change.Chat.Error,
		}})
	case change.Inbox != nil:
		item, err := h.inboxItemOf(ctx, *change.Inbox)
		if err != nil {
			return err
		}
		if err := stream.Send(LiveEvents{Inbox: &item}); err != nil {
			return err
		}
		if change.Inbox.Kind == "question" && change.Inbox.ChatMessage.Valid && change.Inbox.DismissedAt.Valid {
			return stream.Send(LiveEvents{Question: &item})
		}
		return nil
	case change.Action != nil:
		action, err := h.actionOf(ctx, *change.Action)
		if err != nil {
			return err
		}
		return stream.Send(LiveEvents{Action: &action})
	case change.Pauses:
		pauses, err := h.pauses(ctx)
		if err != nil {
			return err
		}
		return stream.Send(LiveEvents{Pauses: &pauses})
	case change.Switches:
		switches, err := h.switches(ctx)
		if err != nil {
			return err
		}
		return stream.Send(LiveEvents{Switches: &switches})
	case change.CIStops:
		stops, err := h.ciStops(ctx)
		if err != nil {
			return err
		}
		return stream.Send(LiveEvents{CIStops: &stops})
	case change.Created != nil:
		return stream.Send(LiveEvents{WorkstreamCreated: &WorkstreamRef{Repository: change.Created.Repository, Number: change.Created.Number}})
	}
	agent, err := h.agentOf(ctx, *change.Node)
	if err != nil {
		return err
	}
	return stream.Send(LiveEvents{Agent: &agent})
}
