// Package api holds the HTTP API of Mobius.
package api

import (
	"net/http"

	"github.com/gork-labs/gork/pkg/adapters/stdlib"
	"github.com/gork-labs/gork/pkg/api"
)

// Routes registers the API routes on mux.
func Routes(mux *http.ServeMux) *stdlib.Router {
	r := stdlib.NewRouter(mux)
	r.Get("/api/health", GetHealth, api.WithTags("health"))
	return r
}
