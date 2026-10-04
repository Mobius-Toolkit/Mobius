package web

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandler(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":    {Data: []byte("index")},
		"assets/app.js": {Data: []byte("app")},
	}
	h := Handler(fsys)

	tests := []struct {
		path string
		want string
	}{
		{"/", "index"},
		{"/assets/app.js", "app"},
		{"/assets", "index"},
		{"/workstreams/42", "index"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if got := rec.Body.String(); got != tt.want {
				t.Errorf("body = %q, want %q", got, tt.want)
			}
		})
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status = %d, want %d", path, rec.Code, http.StatusOK)
	}
	return rec
}

func TestTheShellAndThePWAFilesComeWithCacheControlNoCache(t *testing.T) {
	h := Handler(fstest.MapFS{
		"index.html":           {Data: []byte("index")},
		"sw.js":                {Data: []byte("sw")},
		"manifest.webmanifest": {Data: []byte("{}")},
		"icon.svg":             {Data: []byte("<svg/>")},
	})

	for _, path := range []string{"/", "/sw.js", "/manifest.webmanifest", "/workstreams"} {
		if got := get(t, h, path).Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control = %q, want no-cache", path, got)
		}
	}
	if got := get(t, h, "/icon.svg").Header().Get("Cache-Control"); got == "no-cache" {
		t.Errorf("/icon.svg: Cache-Control = %q", got)
	}
}

func TestTheUIVersionReportsTheBuildOfTheServer(t *testing.T) {
	rec := get(t, Handler(fstest.MapFS{"index.html": {Data: []byte("index")}, "ui-version": {Data: []byte("build")}}), "/ui-version")

	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	if got := rec.Body.String(); got != "build" {
		t.Errorf("body = %q, want build", got)
	}
}

// The web directory holds index.html and the public directory that Vite copies into dist.
func TestTheShellLinksTheManifestAndRegistersTheServiceWorker(t *testing.T) {
	html := get(t, Handler(os.DirFS(".")), "/").Body.String()

	for _, want := range []string{
		`rel="manifest"`,
		`href="/manifest.webmanifest"`,
		`name="theme-color" content="#2d5f8b"`,
		`name="apple-mobile-web-app-capable"`,
		`name="apple-mobile-web-app-title" content="Mobius"`,
		"navigator.serviceWorker?.register('/sw.js')",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html does not contain %s", want)
		}
	}
}

type icon struct {
	Src     string `json:"src"`
	Purpose string `json:"purpose"`
}

func TestTheManifestDescribesTheInstallableApp(t *testing.T) {
	rec := get(t, Handler(os.DirFS("public")), "/manifest.webmanifest")

	if got := rec.Header().Get("Content-Type"); got != "application/manifest+json" {
		t.Errorf("Content-Type = %q", got)
	}
	var manifest struct {
		Name      string `json:"name"`
		ShortName string `json:"short_name"`
		StartURL  string `json:"start_url"`
		Scope     string `json:"scope"`
		Display   string `json:"display"`
		Icons     []icon `json:"icons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "Mobius" || manifest.ShortName != "Mobius" || manifest.StartURL != "/" || manifest.Scope != "/" || manifest.Display != "standalone" {
		t.Errorf("manifest = %+v", manifest)
	}
	for _, want := range []icon{
		{"/icon-192.png", "any"},
		{"/icon-512.png", "any"},
		{"/icon-maskable-512.png", "maskable"},
		{"/icon.svg", ""},
	} {
		if !slices.Contains(manifest.Icons, want) {
			t.Errorf("no icon %+v", want)
		}
		if _, err := fs.Stat(os.DirFS("public"), strings.TrimPrefix(want.Src, "/")); err != nil {
			t.Error(err)
		}
	}
}

func TestTheServiceWorkerPassesEachRequestToTheNetwork(t *testing.T) {
	script := get(t, Handler(os.DirFS("public")), "/sw.js").Body.String()

	for _, want := range []string{"skipWaiting()", "clients.claim()", "respondWith(fetch(event.request))"} {
		if !strings.Contains(script, want) {
			t.Errorf("sw.js does not contain %s", want)
		}
	}
	// The service worker keeps no data in a cache.
	if strings.Contains(script, "caches") {
		t.Error("sw.js uses caches")
	}
}
