// Package api holds the HTTP API of Mobius.
package api

import (
	"net/http"

	"github.com/gork-labs/gork/pkg/adapters/stdlib"
	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/mobius-go/internal/auth"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

// Routes registers the API routes below /api/ on mux.
// Each route but POST /api/login needs the cookie of a device login.
func Routes(mux *http.ServeMux, queries *store.Queries, a *auth.Auth) *stdlib.Router {
	h := &handlers{queries: queries, auth: a}
	routes := http.NewServeMux()
	r := stdlib.NewRouter(routes)
	r.Post("/api/login", h.Login, api.WithTags("devices"))
	r.Get("/api/devices", h.ListDevices, api.WithTags("devices"))
	r.Delete("/api/devices/{id}", h.Logout, api.WithTags("devices"))
	r.Get("/api/health", GetHealth, api.WithTags("health"))
	r.Get("/api/workstreams", h.ListWorkstreams, api.WithTags("workstreams"))
	r.Get("/api/events", h.StreamEvents, api.WithTags("events"))
	mux.Handle("/api/", h.guard(routes))
	return r
}

type handlers struct {
	queries *store.Queries
	auth    *auth.Auth
}
