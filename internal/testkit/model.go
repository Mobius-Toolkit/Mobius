package testkit

import (
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// FakeModel is an httptest server that serves a speech model file at each path.
type FakeModel struct {
	URL string

	mu        sync.Mutex
	hold      chan struct{}
	broken    bool
	downloads int
}

// NewFakeModel starts a fake model server.
func NewFakeModel(t testing.TB) *FakeModel {
	t.Helper()
	m := &FakeModel{}
	server := httptest.NewServer(http.HandlerFunc(m.get))
	t.Cleanup(server.Close)
	t.Cleanup(m.Release)
	m.URL = server.URL
	return m
}

// Hold makes the server wait for Release before it answers.
func (m *FakeModel) Hold() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hold = make(chan struct{})
}

// Release lets the requests that Hold holds go on.
func (m *FakeModel) Release() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hold != nil {
		close(m.hold)
		m.hold = nil
	}
}

// SetBroken makes the server close the connection before it sends the full file.
func (m *FakeModel) SetBroken(broken bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broken = broken
}

// Downloads gives the number of requests that the server got.
func (m *FakeModel) Downloads() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.downloads
}

func (m *FakeModel) get(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.downloads++
	hold, broken := m.hold, m.broken
	m.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-r.Context().Done():
			return
		}
	}
	if broken {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}
	_, _ = w.Write([]byte("model"))
}

// FreeAddress gives a local address "127.0.0.1:<port>" that no program listens on now.
func FreeAddress(t testing.TB) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().String()
}

// InstallFakeWhisper makes the program whisper-server in dir run the fake whisper-server of the fakewhisper package.
// The program is a link to the test binary, so the test package must call Main from its TestMain. It gives the path of
// the program.
func InstallFakeWhisper(t testing.TB, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(dir, "whisper-server")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, program); err != nil && !errors.Is(err, fs.ErrExist) {
		t.Fatal(err)
	}
	return program
}
