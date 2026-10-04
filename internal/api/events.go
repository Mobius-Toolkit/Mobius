package api

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

const (
	backlog      = 20
	pollInterval = time.Second
)

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
	// Workstreams is a change of the Workstream list. It has no data, so the client reads the list again
	Workstreams *struct{} `gork:"workstreams"`
}

// StreamEvents sends the latest activities, and then each new activity, each change of a session,
// each new or changed Transcript row, each change of the drain, each start and failure of an upgrade,
// and each change of the Workstream list.
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
			if err := sendChange(stream, change); err != nil {
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

func sendChange(stream *api.Stream[LiveEvents], change engine.Change) error {
	switch {
	case change.Line != nil:
		return stream.Send(LiveEvents{Transcript: new(transcriptLineOf(*change.Line))})
	case change.Drain != nil:
		return stream.Send(LiveEvents{Drain: new(drainOf(*change.Drain))})
	case change.Upgrade != nil:
		return stream.Send(LiveEvents{Upgrade: &Upgrade{Failure: *change.Upgrade}})
	case change.Workstreams:
		return stream.Send(LiveEvents{Workstreams: &struct{}{}})
	}
	agent, err := agentOf(*change.Node)
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
