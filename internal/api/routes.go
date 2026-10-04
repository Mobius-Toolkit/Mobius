// Package api holds the HTTP API of Mobius.
package api

import (
	"net/http"

	"github.com/gork-labs/gork/pkg/adapters/stdlib"
	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

// Routes registers the API routes on mux.
func Routes(mux *http.ServeMux, queries *store.Queries) *stdlib.Router {
	h := &handlers{queries: queries}
	r := stdlib.NewRouter(mux)
	r.Get("/api/health", GetHealth, api.WithTags("health"))
	r.Get("/api/workstreams", h.ListWorkstreams, api.WithTags("workstreams"))
	r.Get("/api/events", h.StreamEvents, api.WithTags("events"))
	return r
}

type handlers struct {
	queries *store.Queries
}
