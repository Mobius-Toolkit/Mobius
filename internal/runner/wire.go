package runner

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"

	"github.com/coder/acp-go-sdk"
)

// wire reads the stdout of the agent and notes the order of the lines. The ACP connection handles the notifications
// in a queue, so a handler can run for a notification that the agent wrote after the response of a prompt, before
// Prompt returns. The order of the lines tells which notifications follow the response.
type wire struct {
	src io.Reader

	mu        sync.Mutex
	partial   []byte
	updates   int
	responded bool
	boundary  int
}

func (w *wire) Read(p []byte) (int, error) {
	n, err := w.src.Read(p)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial = append(w.partial, p[:n]...)
	for {
		end := bytes.IndexByte(w.partial, '\n')
		if end < 0 {
			return n, err
		}
		w.note(w.partial[:end])
		w.partial = w.partial[end+1:]
	}
}

// note counts a session/update line, and marks the position of the first response after beginPrompt.
func (w *wire) note(line []byte) {
	var message struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
	}
	if json.Unmarshal(line, &message) != nil {
		return
	}
	switch {
	case message.Method == acp.ClientMethodSessionUpdate:
		w.updates++
	case message.Method == "" && message.ID != nil && !w.responded:
		w.responded = true
		w.boundary = w.updates
	}
}

// beginPrompt forgets the response of the last prompt.
func (w *wire) beginPrompt() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.responded = false
}

// after tells that the agent wrote the notification number n after the response to the prompt that runs.
func (w *wire) after(n int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.responded && n > w.boundary
}
