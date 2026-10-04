// Package api holds the HTTP API of Mobius.
package api

import (
	"net/http"

	"github.com/gork-labs/gork/pkg/adapters/stdlib"
	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/mobius-go/internal/auth"
	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
	"github.com/Mobius-Toolkit/mobius-go/internal/github"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

// Routes registers the API routes below /api/ on mux.
// Each route but POST /api/login needs the cookie of a device login.
func Routes(mux *http.ServeMux, queries *store.Queries, a *auth.Auth, gh *github.GitHub, e *engine.Engine) *stdlib.Router {
	h := &handlers{queries: queries, auth: a, github: gh, engine: e}
	routes := http.NewServeMux()
	r := stdlib.NewRouter(routes)
	r.Post("/api/login", h.Login, api.WithTags("devices"), api.WithErrorResponses(http.StatusUnauthorized))
	r.Get("/api/devices", h.ListDevices, loggedIn("devices")...)
	r.Delete("/api/devices/{id}", h.Logout, loggedIn("devices")...)
	r.Get("/api/health", GetHealth, loggedIn("health")...)
	r.Get("/api/workstreams", h.ListWorkstreams, loggedIn("workstreams")...)
	r.Get("/api/events", h.StreamEvents, loggedIn("events")...)
	r.Get("/api/workstreams/{owner}/{name}/{number}/agents", h.ListAgents, loggedIn("agents")...)
	r.Get("/api/agents", h.ListActiveAgents, loggedIn("agents")...)
	r.Get("/api/agents/{id}/transcript", h.GetTranscript, loggedIn("agents")...)
	r.Post("/api/inbox/{id}/resume", h.Resume, loggedIn("inbox")...)
	r.Get("/api/drain", h.GetDrain, loggedIn("upgrade")...)
	r.Post("/api/drain", h.StartDrain, loggedIn("upgrade")...)
	r.Delete("/api/drain", h.CancelDrain, loggedIn("upgrade")...)
	r.Get("/api/upgrade", h.GetUpgrade, loggedIn("upgrade")...)
	r.Post("/api/upgrade", h.StartUpgrade, append(loggedIn("upgrade"), api.WithErrorResponses(http.StatusConflict))...)
	r.Get("/api/github/apps", h.ListGitHubApps, loggedIn("github")...)
	r.Post("/api/github/manifest", h.CreateManifestForm, loggedIn("github")...)
	r.Get("/api/organizations", h.ListOrganizations, loggedIn("github")...)
	r.Get("/api/checkup", h.GetCheckup, loggedIn("checkup")...)
	r.Post("/api/checkup/fix", h.FixLabels, loggedIn("checkup")...)
	// GitHub sends the browser to these pages. Gork cannot give a redirect, so they are plain handlers outside the spec.
	routes.HandleFunc("GET /api/github/manifest-callback", h.manifestCallback)
	routes.HandleFunc("GET /api/github/user-callback", h.userCallback)
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
	github  *github.GitHub
	engine  *engine.Engine
}
