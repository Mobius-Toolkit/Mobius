package api

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

type actionRequest struct{}

type actionResponse struct{}

var errAction = errors.New("the action failed")

func recordEngine(t *testing.T) *engine.Engine {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return engine.New(db, nil, &config.Config{}, engine.Agents{})
}

func recordTexts(t *testing.T, e *engine.Engine, key engine.ChatKey) []string {
	t.Helper()
	view, err := e.ChatView(t.Context(), key, 0)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, message := range view.Messages {
		if message.Author == "Record" {
			texts = append(texts, message.Text)
		}
	}
	return texts
}

func TestOwnerActionAddsTheRecordOfTheHandlerOnceAfterSuccessAndNothingAfterAnError(t *testing.T) {
	e := recordEngine(t)
	key := engine.ChatKey{Organization: "owner"}
	var run func(context.Context, actionRequest) (*actionResponse, error)
	route := func(_ string, handler interface{}, _ ...api.Option) {
		run = handler.(func(context.Context, actionRequest) (*actionResponse, error))
	}
	var failure error
	handler := func(context.Context, actionRequest) (*actionResponse, engine.Record, error) {
		return &actionResponse{}, engine.Record{Chats: []engine.ChatKey{key}, Text: "You acted."}, failure
	}
	ownerAction(e, route, "/path", handler)

	failure = errAction
	if _, err := run(t.Context(), actionRequest{}); !errors.Is(err, errAction) {
		t.Fatalf("error = %v", err)
	}
	if got := recordTexts(t, e, key); len(got) != 0 {
		t.Errorf("records after an error = %q", got)
	}

	failure = nil
	if _, err := run(t.Context(), actionRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := recordTexts(t, e, key); len(got) != 1 || got[0] != "You acted." {
		t.Errorf("records after success = %q", got)
	}
}

func TestOwnerActionNoBodyAddsTheRecordOfTheHandlerOnceAfterSuccessAndNothingAfterAnError(t *testing.T) {
	e := recordEngine(t)
	key := engine.ChatKey{Organization: "owner"}
	var run func(context.Context, actionRequest) error
	route := func(_ string, handler interface{}, _ ...api.Option) {
		run = handler.(func(context.Context, actionRequest) error)
	}
	var failure error
	handler := func(context.Context, actionRequest) (engine.Record, error) {
		return engine.Record{Chats: []engine.ChatKey{key}, Text: "You acted."}, failure
	}
	ownerActionNoBody(e, route, "/path", handler)

	failure = errAction
	if err := run(t.Context(), actionRequest{}); !errors.Is(err, errAction) {
		t.Fatalf("error = %v", err)
	}
	if got := recordTexts(t, e, key); len(got) != 0 {
		t.Errorf("records after an error = %q", got)
	}

	failure = nil
	if err := run(t.Context(), actionRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := recordTexts(t, e, key); len(got) != 1 || got[0] != "You acted." {
		t.Errorf("records after success = %q", got)
	}
}
