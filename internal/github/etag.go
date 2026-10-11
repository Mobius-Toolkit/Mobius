package github

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// An entry that no request used for this time is removed.
const etagLife = time.Hour

var etagClock = time.Now

// etagEntry is a kept response with its ETag.
type etagEntry struct {
	etag   string
	header http.Header
	body   []byte
	usedAt time.Time
}

// etagTransport sends each GET request as a conditional request when it has the ETag of the URL.
// GitHub does not count a 304 answer against the rate limit, and the caller gets the kept response.
// The key of an entry is the URL with the Accept header, because one URL can give different bodies.
// A request that has an If-None-Match header, and a request of another method, pass unchanged.
type etagTransport struct {
	base http.RoundTripper

	mu      sync.Mutex
	entries map[string]*etagEntry
}

func newETagTransport(base http.RoundTripper) *etagTransport {
	return &etagTransport{base: base, entries: map[string]*etagEntry{}}
}

func (t *etagTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || req.Header.Get("If-None-Match") != "" {
		return t.base.RoundTrip(req)
	}
	key := req.URL.String() + "\n" + req.Header.Get("Accept")
	now := etagClock()
	entry := t.use(key, now)
	if entry != nil {
		req = req.Clone(req.Context())
		req.Header.Set("If-None-Match", entry.etag)
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusNotModified && entry != nil:
		_ = resp.Body.Close()
		return t.kept(resp, entry), nil
	case resp.StatusCode == http.StatusOK && resp.Header.Get("ETag") != "":
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		t.mu.Lock()
		t.entries[key] = &etagEntry{etag: resp.Header.Get("ETag"), header: resp.Header.Clone(), body: body, usedAt: now}
		t.mu.Unlock()
		resp.Body = io.NopCloser(bytes.NewReader(body))
	}
	return resp, nil
}

// use removes the entries that expired, marks the entry of key as used and gives it.
func (t *etagTransport) use(key string, now time.Time) *etagEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, entry := range t.entries {
		if now.Sub(entry.usedAt) >= etagLife {
			delete(t.entries, k)
		}
	}
	entry := t.entries[key]
	if entry != nil {
		entry.usedAt = now
	}
	return entry
}

// kept gives a 200 response with the body and the headers of entry. The rate limit headers come from the 304 answer.
func (t *etagTransport) kept(answer *http.Response, entry *etagEntry) *http.Response {
	t.mu.Lock()
	header := entry.header.Clone()
	t.mu.Unlock()
	for name, values := range answer.Header {
		if strings.HasPrefix(name, "X-Ratelimit-") {
			header[name] = values
		}
	}
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         answer.Proto,
		ProtoMajor:    answer.ProtoMajor,
		ProtoMinor:    answer.ProtoMinor,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(entry.body)),
		ContentLength: int64(len(entry.body)),
		Request:       answer.Request,
	}
}
