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
// Prompt returns, or after the next prompt starts. The order of the lines tells which notifications follow a response.
type wire struct {
	src io.Reader

	mu         sync.Mutex
	partial    []byte
	updates    int
	promptID   string
	responded  bool
	responseAt int
	late       []span
}

// span holds the notifications from+1 to to, which the agent wrote between a response and the next prompt.
type span struct {
	from, to int
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

// note counts a session/update line, and marks the position of the response to the last prompt.
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
	case message.Method == "" && message.ID != nil && string(message.ID) == w.promptID && !w.responded:
		w.responded = true
		w.responseAt = w.updates
	}
}

// beginPrompt notes the request id of a prompt that goes to the agent. The update lines that the wire read after the
// response of the previous prompt are late for that prompt, and stay late.
func (w *wire) beginPrompt(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.responded && w.updates > w.responseAt {
		w.late = append(w.late, span{w.responseAt, w.updates})
	}
	w.promptID = id
	w.responded = false
}

// after tells that the agent wrote the notification number n after the response to a prompt, and before the next
// prompt, or after the response to the prompt that runs.
func (w *wire) after(n int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.responded && n > w.responseAt {
		return true
	}
	for _, late := range w.late {
		if n > late.from && n <= late.to {
			return true
		}
	}
	return false
}

// stdin returns a writer to the stdin of the agent that notes each session/prompt request. Each write is one message.
func (w *wire) stdin(dst io.Writer) io.Writer {
	return promptWriter{dst: dst, wire: w}
}

type promptWriter struct {
	dst  io.Writer
	wire *wire
}

func (p promptWriter) Write(b []byte) (int, error) {
	var message struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
	}
	if json.Unmarshal(b, &message) == nil && message.Method == acp.AgentMethodSessionPrompt {
		p.wire.beginPrompt(string(message.ID))
	}
	return p.dst.Write(b)
}
