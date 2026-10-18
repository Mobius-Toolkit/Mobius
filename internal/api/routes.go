// Package api holds the HTTP API of Mobius.
package api

import (
	"net/http"

	"github.com/gork-labs/gork/pkg/adapters/stdlib"
	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/auth"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/voice"
)

// maxSendChatBody is the size of 4 images of 5 MB, and 1 MB for the text and the multipart headers.
const maxSendChatBody = 4*5*1024*1024 + 1024*1024

// Routes registers the API routes below /api/ on mux.
// Each route but POST /api/login needs the cookie of a device login.
func Routes(mux *http.ServeMux, queries *store.Queries, a *auth.Auth, gh *github.GitHub, e *engine.Engine, w *voice.Whisper) *stdlib.Router {
	h := &handlers{queries: queries, auth: a, github: gh, engine: e, whisper: w}
	routes := http.NewServeMux()
	r := stdlib.NewRouter(routes)
	r.Post("/api/login", h.Login, api.WithTags("devices"), api.WithErrorResponses(http.StatusUnauthorized))
	r.Get("/api/devices", h.ListDevices, loggedIn("devices")...)
	r.Delete("/api/devices/{id}", h.Logout, loggedIn("devices")...)
	r.Get("/api/health", GetHealth, loggedIn("health")...)
	r.Get("/api/workstreams", h.ListWorkstreams, loggedIn("workstreams")...)
	r.Put("/api/workstreams/{owner}/{name}/{number}/autopilot", h.SetAutopilot, append(loggedIn("workstreams"), api.WithErrorResponses(http.StatusConflict))...)
	r.Post("/api/workstreams/{owner}/{name}/{number}/complete", h.CompleteWorkstream, append(loggedIn("workstreams"), api.WithErrorResponses(http.StatusConflict))...)
	r.Get("/api/workstreams/{owner}/{name}/{number}/details", h.GetWorkstreamDetails, append(loggedIn("workstreams"), api.WithErrorResponses(http.StatusConflict))...)
	r.Get("/api/workstreams/{owner}/{name}/{number}/closure", h.WorkstreamClosure, append(loggedIn("workstreams"), api.WithErrorResponses(http.StatusConflict))...)
	r.Post("/api/workstreams/{owner}/{name}/{number}/close", h.CloseWorkstream, append(loggedIn("workstreams"), api.WithErrorResponses(http.StatusConflict))...)
	r.Get("/api/repositories/{owner}/{name}/memory", h.ListMemoryVersions, append(loggedIn("memory"), api.WithErrorResponses(http.StatusNotFound))...)
	r.Put("/api/repositories/{owner}/{name}/memory", h.SaveMemory, append(loggedIn("memory"), api.WithErrorResponses(http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity))...)
	r.Post("/api/repositories/{owner}/{name}/memory/{id}/revert", h.RevertMemory, append(loggedIn("memory"), api.WithErrorResponses(http.StatusNotFound, http.StatusConflict))...)
	r.Get("/api/events", h.StreamEvents, loggedIn("events")...)
	r.Get("/api/workstreams/{owner}/{name}/{number}/agents", h.ListAgents, loggedIn("agents")...)
	r.Get("/api/agents", h.ListActiveAgents, loggedIn("agents")...)
	r.Get("/api/agents/{id}/transcript", h.GetTranscript, loggedIn("agents")...)
	r.Get("/api/agents/{id}/transcript/events", h.StreamTranscript, loggedIn("agents")...)
	r.Post("/api/agents/{id}/start-check", h.StartCheck, append(loggedIn("agents"), api.WithErrorResponses(http.StatusConflict))...)
	r.Get("/api/workstreams/{owner}/{name}/{number}/tasks", h.ListTasks, append(loggedIn("tasks"), api.WithErrorResponses(http.StatusNotFound))...)
	r.Get("/api/needs-human", h.ListNeedsHuman, loggedIn("tasks")...)
	r.Post("/api/repositories/{owner}/{name}/issues/{number}/resume", h.ResumeIssue, append(loggedIn("tasks"), api.WithErrorResponses(http.StatusConflict))...)
	r.Post("/api/repositories/{owner}/{name}/issues/{number}/start", h.StartIssue, append(loggedIn("tasks"), api.WithErrorResponses(http.StatusConflict))...)
	r.Get("/api/chat", h.GetChat, loggedIn("chat")...)
	r.Post("/api/chat/messages", h.SendChat, append(loggedIn("chat"), api.WithErrorResponses(http.StatusConflict))...)
	r.Get("/api/chat/messages/{id}/images/{position}", h.GetChatImage, append(loggedIn("chat"), api.WithResponseContentTypes(imageTypes...), api.WithErrorResponses(http.StatusNotFound))...)
	r.Post("/api/chat/stop", h.StopChat, loggedIn("chat")...)
	r.Post("/api/chat/seen", h.SeeChat, loggedIn("chat")...)
	r.Get("/api/unread", h.ListUnread, loggedIn("chat")...)
	r.Get("/api/inbox", h.ListInbox, loggedIn("inbox")...)
	r.Post("/api/inbox/{id}/dismiss", h.Dismiss, loggedIn("inbox")...)
	r.Post("/api/inbox/{id}/send", h.SendAgain, loggedIn("inbox")...)
	r.Post("/api/inbox/{id}/answer", h.AnswerInboxItem, append(loggedIn("inbox"), api.WithErrorResponses(http.StatusBadRequest, http.StatusNotFound, http.StatusConflict))...)
	r.Get("/api/pauses", h.ListPauses, loggedIn("pauses")...)
	r.Post("/api/pauses/{harness}/resume", h.Resume, loggedIn("pauses")...)
	r.Get("/api/ci-stops", h.ListCIStops, loggedIn("ci-stops")...)
	r.Get("/api/drain", h.GetDrain, loggedIn("upgrade")...)
	r.Post("/api/drain", h.StartDrain, loggedIn("upgrade")...)
	r.Delete("/api/drain", h.CancelDrain, loggedIn("upgrade")...)
	r.Get("/api/upgrade", h.GetUpgrade, loggedIn("upgrade")...)
	r.Post("/api/upgrade", h.StartUpgrade, append(loggedIn("upgrade"), api.WithErrorResponses(http.StatusConflict))...)
	r.Get("/api/release", h.GetRelease, loggedIn("upgrade")...)
	r.Get("/api/release/changes", h.ListReleaseChanges, append(loggedIn("upgrade"), api.WithErrorResponses(http.StatusConflict))...)
	r.Get("/api/github/apps", h.ListGitHubApps, loggedIn("github")...)
	r.Post("/api/github/manifest", h.CreateManifestForm, loggedIn("github")...)
	r.Get("/api/organizations", h.ListOrganizations, loggedIn("github")...)
	r.Get("/api/checkup", h.GetCheckup, loggedIn("checkup")...)
	r.Get("/api/checkup/tools", h.GetCheckupTools, loggedIn("checkup")...)
	r.Post("/api/checkup/fix", h.FixLabels, loggedIn("checkup")...)
	r.Get("/api/usage", h.GetUsage, append(loggedIn("usage"), api.WithErrorResponses(http.StatusBadRequest))...)
	r.Get("/api/usage/times", h.GetUsageTimes, append(loggedIn("usage"), api.WithErrorResponses(http.StatusBadRequest))...)
	r.Post("/api/voice/transcriptions", h.Transcribe, append(loggedIn("voice"), api.WithRequestContentTypes("audio/wav"), api.WithErrorResponses(http.StatusBadGateway, http.StatusServiceUnavailable))...)
	r.Get("/api/github/manifest-callback", h.ManifestCallback, redirect("github")...)
	r.Get("/api/github/user-callback", h.UserCallback, redirect("github")...)
	mux.Handle("/api/", h.guard(limitBodies(routes)))
	return r
}

// limitBodies makes a request to SendChat or to Transcribe fail when its body is larger than maxSendChatBody or
// maxTranscriptionBody, before Gork reads the body into memory.
func limitBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			switch r.URL.Path {
			case "/api/chat/messages":
				r.Body = http.MaxBytesReader(w, r.Body, maxSendChatBody)
			case "/api/voice/transcriptions":
				r.Body = http.MaxBytesReader(w, r.Body, maxTranscriptionBody)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// redirect gives the options of a page where GitHub sends the browser. The page needs the cookie
// of a device login and sends the browser on with 303 See Other.
func redirect(tag string) []api.Option {
	return append(loggedIn(tag), api.WithStatus(http.StatusSeeOther), api.WithErrorResponses(http.StatusForbidden))
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
	whisper *voice.Whisper
}
