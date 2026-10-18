package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/voice"
)

// fakeWhisperServer is a whisper-server that answers health with the status health and /inference with the status
// inference and the text " hello ". It counts the requests to /inference.
func fakeWhisperServer(t *testing.T, health, inference int) (*voice.Whisper, *atomic.Int64) {
	t.Helper()
	var inferences atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(health) })
	mux.HandleFunc("POST /inference", func(w http.ResponseWriter, _ *http.Request) {
		inferences.Add(1)
		w.WriteHeader(inference)
		_, _ = w.Write([]byte(`{"text":" hello "}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	w := voice.New(t.TempDir(), "")
	w.Address = strings.TrimPrefix(server.URL, "http://")
	return w, &inferences
}

func transcribe(t *testing.T, handler http.Handler, body []byte) (int, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/voice/transcriptions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "audio/wav")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	text, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return recorder.Code, string(text)
}

func TestTranscribeGivesTheTextOfTheWAVFile(t *testing.T) {
	whisper, _ := fakeWhisperServer(t, http.StatusOK, http.StatusOK)
	handler, _ := testMuxWith(t, whisper)

	status, text := transcribe(t, handler, []byte("RIFF"))

	var response Envelope[Transcription]
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || response.Data.Text != "hello" {
		t.Errorf("status = %d, body = %s", status, text)
	}
}

func TestTranscribeNeedsADeviceLogin(t *testing.T) {
	whisper, inferences := fakeWhisperServer(t, http.StatusOK, http.StatusOK)
	mux, _, _ := newMuxWith(t, whisper)

	status, _ := transcribe(t, mux, []byte("RIFF"))

	if status != http.StatusUnauthorized || inferences.Load() != 0 {
		t.Errorf("status = %d, inferences = %d", status, inferences.Load())
	}
}

func TestTranscribeRefusesABodyAbove20MBeforeItReadsTheBody(t *testing.T) {
	whisper, inferences := fakeWhisperServer(t, http.StatusOK, http.StatusOK)
	handler, _ := testMuxWith(t, whisper)

	status, text := transcribe(t, handler, bytes.Repeat([]byte("a"), 20*1024*1024+1))

	if status != http.StatusBadRequest || !strings.Contains(text, "request body too large") {
		t.Errorf("status = %d, body = %s", status, text)
	}
	if inferences.Load() != 0 {
		t.Errorf("inferences = %d", inferences.Load())
	}
}

func TestTranscribeAcceptsABodyOf20MB(t *testing.T) {
	whisper, _ := fakeWhisperServer(t, http.StatusOK, http.StatusOK)
	handler, _ := testMuxWith(t, whisper)

	status, text := transcribe(t, handler, bytes.Repeat([]byte("a"), 20*1024*1024))

	if status != http.StatusOK {
		t.Errorf("status = %d, body = %s", status, text)
	}
}

func TestTranscribeAnswers503WithTheReasonWhenTheConverterIsNotReady(t *testing.T) {
	notInstalled := voice.New(t.TempDir(), "")
	notInstalled.Address = testkit.FreeAddress(t)
	starting, _ := fakeWhisperServer(t, http.StatusServiceUnavailable, http.StatusOK)
	downloading := voice.New(t.TempDir(), t.TempDir())
	downloading.Address = testkit.FreeAddress(t)
	testkit.InstallFakeWhisper(t, downloading.Path)
	model := testkit.NewFakeModel(t)
	model.Hold()
	downloading.ModelURL = model.URL
	t.Cleanup(func() {
		model.Release()
		testkit.WaitFor(t, func() bool {
			_, err := downloading.Transcribe(context.Background(), nil)
			return !errors.Is(err, voice.Downloading)
		})
		downloading.Stop()
	})
	tests := []struct {
		name    string
		whisper *voice.Whisper
		message string
	}{
		{"no whisper-server", notInstalled, "Install whisper.cpp: brew install whisper-cpp"},
		{"the model downloads", downloading, "The speech model is still downloading"},
		{"the process is not ready", starting, "The speech model is still starting"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, _ := testMuxWith(t, test.whisper)

			status, text := transcribe(t, handler, []byte("RIFF"))

			if status != http.StatusServiceUnavailable || strings.TrimSpace(text) != `{"error":"`+test.message+`"}` {
				t.Errorf("status = %d, body = %s", status, text)
			}
		})
	}
}

func TestTranscribeAnswers502WhenWhisperServerReturnsAnError(t *testing.T) {
	whisper, _ := fakeWhisperServer(t, http.StatusOK, http.StatusInternalServerError)
	handler, _ := testMuxWith(t, whisper)

	status, text := transcribe(t, handler, []byte("RIFF"))

	if status != http.StatusBadGateway || strings.TrimSpace(text) != `{"error":"whisper-server: 500 Internal Server Error"}` {
		t.Errorf("status = %d, body = %s", status, text)
	}
}
