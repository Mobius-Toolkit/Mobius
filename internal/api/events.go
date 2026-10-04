package api

import (
	"context"
	"slices"
	"time"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

const (
	backlog      = 20
	pollInterval = time.Second
)

// StreamEventsRequest is the request of StreamEvents.
type StreamEventsRequest struct{}

// Activity is an entry of the activity feed of a Workstream.
type Activity struct {
	// ID increases with each new activity
	ID int64 `gork:"id" validate:"required"`
	// Time is the time of the activity
	Time time.Time `gork:"time" validate:"required"`
	// Repository is the repository of the Workstream issue, as "owner/name"
	Repository string `gork:"repository" validate:"required"`
	// Workstream is the number of the Workstream issue
	Workstream int64 `gork:"workstream" validate:"required"`
	// Issue is the number of the issue of the activity
	Issue int64 `gork:"issue" validate:"required"`
	// Actor is the GitHub login that did the activity
	Actor string `gork:"actor" validate:"required"`
	// Text tells what happened
	Text string `gork:"text" validate:"required"`
	// Link is the GitHub URL of the activity
	Link string `gork:"link" validate:"required"`
}

// LiveEvents are the events of the live event stream.
type LiveEvents struct {
	Activity *Activity `gork:"activity"`
}

// StreamEvents sends the latest activities, and then each new activity.
func (h *handlers) StreamEvents(ctx context.Context, _ StreamEventsRequest, stream *api.Stream[LiveEvents]) error {
	events, err := h.queries.ListLatestEvents(ctx, backlog)
	if err != nil {
		return err
	}
	slices.Reverse(events)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var last int64
	for {
		for _, e := range events {
			if err := sendActivity(stream, e); err != nil {
				return err
			}
			last = e.ID
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		events, err = h.queries.ListEventsAfter(ctx, last)
		if err != nil {
			return err
		}
	}
}

func sendActivity(stream *api.Stream[LiveEvents], e store.Event) error {
	t, err := time.Parse(time.RFC3339Nano, e.Time)
	if err != nil {
		return err
	}
	return stream.Send(LiveEvents{Activity: &Activity{
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
