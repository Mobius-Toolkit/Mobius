package api

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const (
	backlog      = 20
	pollInterval = time.Second
)

// pingInterval is the time between two ping events. The client connects again after two intervals with no ping.
var pingInterval = 15 * time.Second

// StreamEventsRequest is the request of StreamEvents.
type StreamEventsRequest struct {
	Headers struct {
		// LastEventID is the id of the last activity that the client got
		LastEventID *int64 `gork:"Last-Event-ID"`
	}
}

// Activity is an entry of the activity feed of a Workstream.
type Activity struct {
	// ID increases with each new activity
	ID int64 `gork:"id"`
	// Time is the time of the activity
	Time time.Time `gork:"time"`
	// Repository is the repository of the Workstream issue, as "owner/name"
	Repository string `gork:"repository"`
	// Workstream is the number of the Workstream issue
	Workstream int64 `gork:"workstream"`
	// Issue is the number of the issue of the activity
	Issue int64 `gork:"issue"`
	// Actor is the GitHub login that did the activity
	Actor string `gork:"actor"`
	// Text tells what happened
	Text string `gork:"text"`
	// Link is the GitHub URL of the activity
	Link string `gork:"link"`
}

// LiveEvents are the events of the live event stream. Only an activity has an event id.
type LiveEvents struct {
	Activity *Activity `gork:"activity"`
	// Agent is a session at its start and at its end
	Agent *Agent `gork:"agent"`
	// Transcript is a new row of a Transcript, or a row that got more text
	Transcript *TranscriptLine `gork:"transcript"`
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
	// Message is a new chat message, or a message of the Lead or of the Triager that got more text
	Message *ChatMessage `gork:"message"`
	// Unread is the new number of unread messages of a chat
	Unread *Unread `gork:"unread"`
	// Chat is the state of a chat at the start and at the end of the work of its agent
	Chat *ChatState `gork:"chat"`
	// Inbox is a new or dismissed Inbox item
	Inbox *InboxItem `gork:"inbox"`
	// WorkstreamCreated is a Workstream that the Triager chat created, so the client can open it
	WorkstreamCreated *WorkstreamRef `gork:"workstreamCreated"`
	// Ping has no data. The server sends it at a fixed interval, so the client can find a connection that is dead
	Ping *struct{} `gork:"ping"`
}

// ChatState is the state of a chat.
type ChatState struct {
	// Organization is the owner of the repository, or the organization of the Triager chat
	Organization string `gork:"organization"`
	// Repository is the repository of the Workstream as "owner/name". It is empty for the Triager chat
	Repository string `gork:"repository"`
	// Workstream is the number of the Workstream issue. It is 0 for the Triager chat
	Workstream int64 `gork:"workstream"`
	// Writing is true while the agent has a turn that runs or an item that waits
	Writing bool `gork:"writing"`
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

// StreamEvents sends the latest activities, and then each new activity, each change of a session,
// each new or changed Transcript row, each change of the drain, each start and failure of an upgrade,
// each change of the Workstream list, each new or longer chat message, each new unread count, each change of the
// state of a chat, each new or dismissed Inbox item, and each Workstream that the Triager chat created.
// It also sends a ping at a fixed interval.
// When the request has Last-Event-ID, it sends the activities after that id in place of the latest activities.
// When the client does not read the session changes fast enough, the stream ends.
func (h *handlers) StreamEvents(ctx context.Context, req StreamEventsRequest, stream *api.Stream[LiveEvents]) error {
	changes, stop := h.engine.Listen()
	defer stop()
	var events []store.Event
	var err error
	var last int64
	if req.Headers.LastEventID != nil {
		last = *req.Headers.LastEventID
		events, err = h.queries.ListEventsAfter(ctx, last)
	} else {
		events, err = h.queries.ListLatestEvents(ctx, backlog)
		slices.Reverse(events)
	}
	if err != nil {
		return err
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	pings := time.NewTicker(pingInterval)
	defer pings.Stop()
	for {
		for _, e := range events {
			if err := sendActivity(stream, e); err != nil {
				return err
			}
			last = e.ID
		}
		events = nil
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
		case <-ticker.C:
			events, err = h.queries.ListEventsAfter(ctx, last)
			if err != nil {
				return err
			}
		}
	}
}

func (h *handlers) sendChange(ctx context.Context, stream *api.Stream[LiveEvents], change engine.Change) error {
	switch {
	case change.Line != nil:
		return stream.Send(LiveEvents{Transcript: new(transcriptLineOf(*change.Line))})
	case change.Drain != nil:
		return stream.Send(LiveEvents{Drain: new(drainOf(*change.Drain))})
	case change.Upgrade != nil:
		return stream.Send(LiveEvents{Upgrade: &Upgrade{Failure: *change.Upgrade}})
	case change.Workstreams:
		return stream.Send(LiveEvents{Workstreams: &struct{}{}})
	case change.Repositories:
		return stream.Send(LiveEvents{Repositories: &struct{}{}})
	case change.Message != nil:
		message, err := h.chatMessageOf(*change.Message)
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
			PausedUntil:  pausedUntil,
			Error:        change.Chat.Error,
		}})
	case change.Inbox != nil:
		item, err := h.inboxItemOf(ctx, *change.Inbox)
		if err != nil {
			return err
		}
		return stream.Send(LiveEvents{Inbox: &item})
	case change.Created != nil:
		return stream.Send(LiveEvents{WorkstreamCreated: &WorkstreamRef{Repository: change.Created.Repository, Number: change.Created.Number}})
	}
	agent, err := h.agentOf(ctx, *change.Node)
	if err != nil {
		return err
	}
	return stream.Send(LiveEvents{Agent: &agent})
}

func sendActivity(stream *api.Stream[LiveEvents], e store.Event) error {
	t, err := time.Parse(time.RFC3339Nano, e.Time)
	if err != nil {
		return err
	}
	return stream.SendWithID(strconv.FormatInt(e.ID, 10), LiveEvents{Activity: &Activity{
		ID:         e.ID,
		Time:       t,
		Repository: e.Repository,
		Workstream: e.Workstream,
		Issue:      e.Issue,
		Actor:      e.Actor,
		Text:       e.Text,
		Link:       e.Link,
	}})
}
