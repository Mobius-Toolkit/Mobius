package api

import (
	"context"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
)

// register is the method of the Gork router that registers a route, for example r.Put.
type register func(path string, handler interface{}, opts ...api.Option)

// ownerAction registers a route for an action of the Owner that returns a body. After the handler succeeds, the route
// adds the Record of the handler to the chats of the Record. The operation keeps the operationId and the doc comment of
// the handler.
func ownerAction[Req, Resp any](e *engine.Engine, route register, path string, handler func(context.Context, Req) (*Resp, engine.Record, error), opts ...api.Option) {
	route(path, func(ctx context.Context, req Req) (*Resp, error) {
		resp, record, err := handler(ctx, req)
		if err != nil {
			return nil, err
		}
		e.AddRecord(ctx, record)
		return resp, nil
	}, append(opts, api.WithNameOf(handler))...)
}

// ownerActionNoBody is ownerAction for a handler that returns no body.
func ownerActionNoBody[Req any](e *engine.Engine, route register, path string, handler func(context.Context, Req) (engine.Record, error), opts ...api.Option) {
	route(path, func(ctx context.Context, req Req) error {
		record, err := handler(ctx, req)
		if err != nil {
			return err
		}
		e.AddRecord(ctx, record)
		return nil
	}, append(opts, api.WithNameOf(handler))...)
}
