package testkit

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// FakeNPM is an in-memory npm registry on an httptest server. It answers GET /<package>/latest.
type FakeNPM struct {
	URL string

	mu       sync.Mutex
	versions map[string]string
	silent   bool
	reads    int
}

// NewFakeNPM starts a fake npm registry with no package.
func NewFakeNPM(t testing.TB) *FakeNPM {
	t.Helper()
	n := &FakeNPM{versions: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{scope}/{name}/latest", n.getLatest)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	n.URL = server.URL
	return n
}

// SetVersion makes version the latest version of the package, for example "@anthropic-ai/claude-code".
func (n *FakeNPM) SetVersion(npmPackage, version string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.versions[npmPackage] = version
}

// SetSilent makes the registry close each connection with no answer.
func (n *FakeNPM) SetSilent(silent bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.silent = silent
}

// Reads gives the number of requests that the registry got.
func (n *FakeNPM) Reads() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.reads
}

func (n *FakeNPM) getLatest(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reads++
	if n.silent {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	version, ok := n.versions[r.PathValue("scope")+"/"+r.PathValue("name")]
	if !ok {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"version": version})
}
