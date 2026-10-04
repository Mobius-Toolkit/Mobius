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
	r.Post("/api/login", h.Login, api.WithTags("devices"), api.WithErrorResponses(http.StatusUnauthorized))
	r.Get("/api/devices", h.ListDevices, loggedIn("devices")...)
	r.Delete("/api/devices/{id}", h.Logout, loggedIn("devices")...)
	r.Get("/api/health", GetHealth, loggedIn("health")...)
	r.Get("/api/workstreams", h.ListWorkstreams, loggedIn("workstreams")...)
	r.Get("/api/events", h.StreamEvents, loggedIn("events")...)
	mux.Handle("/api/", h.guard(routes))
	return r
}

// loggedIn gives the options of a route that needs the cookie of a device login.
func loggedIn(tag string) []api.Option {
	return []api.Option{api.WithTags(tag), api.WithCookieAuth(sessionCookie), api.WithErrorResponses(http.StatusUnauthorized)}
}

type handlers struct {
	queries *store.Queries
	auth    *auth.Auth
}
