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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/voice"
)

// fakeWhisperServer is a whisper-server that answers health with the status health and /inference with the status
// inference and the text " hello ". It counts the requests to /inference.
func fakeWhisperServer(t *testing.T, health, inference int) (*voice.Recordings, *atomic.Int64) {
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
	return withoutDraft(t, w), &inferences
}

func withoutDraft(t *testing.T, whisper *voice.Whisper) *voice.Recordings {
	t.Helper()
	return voice.NewRecordings(whisper, voice.NewDraft(t.TempDir(), ""))
}

func transcribe(t *testing.T, handler http.Handler, body []byte) (int, string) {
	t.Helper()
	return transcribeWithin(context.Background(), t, handler, body)
}

func transcribeWithin(ctx context.Context, t *testing.T, handler http.Handler, body []byte) (int, string) {
	t.Helper()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/voice/transcriptions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "audio/wav")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	text, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return recorder.Code, string(text)
}

func conversionOf(t *testing.T, body string) Conversion {
	t.Helper()
	var response Envelope[Conversion]
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("body = %s, err = %v", body, err)
	}
	return response.Data
}

func getConversion(handler http.Handler, id string) (int, string) {
	request := httptest.NewRequest(http.MethodGet, "/api/voice/conversions/"+id, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// waitForConversion asks GetConversion until the state is not converting.
func waitForConversion(t *testing.T, handler http.Handler, id string) Conversion {
	t.Helper()
	return testkit.WaitForValue(t, func() (Conversion, bool) {
		status, body := getConversion(handler, id)
		if status != http.StatusOK {
			t.Fatalf("status = %d, body = %s", status, body)
		}
		conversion := conversionOf(t, body)
		return conversion, conversion.State != "converting"
	})
}

func TestTranscribeAnswersAtOnceWithAConversionThatGivesTheText(t *testing.T) {
	whisper, _ := fakeWhisperServer(t, http.StatusOK, http.StatusOK)
	handler, _ := testMuxWith(t, whisper)

	status, text := transcribe(t, handler, []byte("RIFF"))
	started := conversionOf(t, text)
	done := waitForConversion(t, handler, started.ID)

	if status != http.StatusOK || len(started.ID) != 32 || started.State != "converting" {
		t.Errorf("status = %d, body = %s", status, text)
	}
	if want := (Conversion{ID: started.ID, State: "done", Text: "hello"}); done != want {
		t.Errorf("done = %+v, want %+v", done, want)
	}
}

// gatedWhisper gives recordings with a final converter that answers " hello " after release is called.
func gatedWhisper(t *testing.T) (recordings *voice.Recordings, release func()) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /inference", func(w http.ResponseWriter, _ *http.Request) {
		<-gate
		_, _ = w.Write([]byte(`{"text":" hello "}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	t.Cleanup(release)
	w := voice.New(t.TempDir(), "")
	w.Address = strings.TrimPrefix(server.URL, "http://")
	return withoutDraft(t, w), release
}

func TestTranscribeAnswersWhileTheConverterWorks(t *testing.T) {
	whisper, _ := gatedWhisper(t)
	handler, _ := testMuxWith(t, whisper)

	_, text := transcribe(t, handler, []byte("RIFF"))
	started := conversionOf(t, text)
	status, body := getConversion(handler, started.ID)

	if want := (Conversion{ID: started.ID, State: "converting"}); started != want || conversionOf(t, body) != want || status != http.StatusOK {
		t.Errorf("started = %+v, status = %d, body = %s", started, status, body)
	}
}

func TestAnUnknownConversionGives404(t *testing.T) {
	handler, _ := testMuxWith(t, liveRecordings(t))

	status, _ := getConversion(handler, "unknown")

	if status != http.StatusNotFound {
		t.Errorf("status = %d", status)
	}
}

func TestGetConversionNeedsADeviceLogin(t *testing.T) {
	mux, _, _ := newMuxWith(t, liveRecordings(t))

	status, _ := getConversion(mux, "unknown")

	if status != http.StatusUnauthorized {
		t.Errorf("status = %d", status)
	}
}

func TestTheEndOfTheRequestContextDoesNotCancelTheConversion(t *testing.T) {
	whisper, release := gatedWhisper(t)
	handler, _ := testMuxWith(t, whisper)
	ctx, cancel := context.WithCancel(context.Background())

	_, text := transcribeWithin(ctx, t, handler, []byte("RIFF"))
	cancel()
	release()
	done := waitForConversion(t, handler, conversionOf(t, text).ID)

	if done.State != "done" || done.Text != "hello" {
		t.Errorf("done = %+v", done)
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

func TestAConversionFailsWithTheReasonWhenTheConverterIsNotReady(t *testing.T) {
	notInstalled := voice.New(t.TempDir(), "")
	notInstalled.Address = testkit.FreeAddress(t)
	starting, _ := fakeWhisperServer(t, http.StatusServiceUnavailable, http.StatusOK)
	downloading := voice.New(t.TempDir(), t.TempDir())
	downloading.Address = testkit.FreeAddress(t)
	testkit.InstallFakeWhisper(t, downloading.Path)
	model := testkit.NewFakeModel(t)
	model.Hold()
	downloading.ModelURL = model.URL
	downloading.VADModelURL = model.URL
	t.Cleanup(func() {
		model.Release()
		testkit.WaitFor(t, func() bool {
			_, err := downloading.Transcribe(context.Background(), nil, "en")
			return !errors.Is(err, voice.Downloading)
		})
		downloading.Stop()
	})
	tests := []struct {
		name    string
		whisper *voice.Recordings
		message string
	}{
		{"no whisper-server", withoutDraft(t, notInstalled), "Install whisper.cpp: brew install whisper-cpp"},
		{"the model downloads", withoutDraft(t, downloading), "The speech model is still downloading"},
		{"the process is not ready", starting, "The speech model is still starting"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.whisper.ConversionTimeout = 100 * time.Millisecond
			handler, _ := testMuxWith(t, test.whisper)

			_, text := transcribe(t, handler, []byte("RIFF"))
			done := waitForConversion(t, handler, conversionOf(t, text).ID)

			if done.State != "failed" || done.Error != test.message || done.Text != "" {
				t.Errorf("done = %+v", done)
			}
		})
	}
}

func TestAConversionFailsWhenWhisperServerReturnsAnError(t *testing.T) {
	whisper, _ := fakeWhisperServer(t, http.StatusOK, http.StatusInternalServerError)
	handler, _ := testMuxWith(t, whisper)

	_, text := transcribe(t, handler, []byte("RIFF"))
	done := waitForConversion(t, handler, conversionOf(t, text).ID)

	if want := (Conversion{ID: done.ID, State: "failed", Error: "whisper-server: 500 Internal Server Error"}); done != want {
		t.Errorf("done = %+v, want %+v", done, want)
	}
}

// liveRecordings gives recordings with a final converter that answers " hello " and a draft converter that answers
// the one segment " draft " up to 1 second.
func liveRecordings(t *testing.T) *voice.Recordings {
	t.Helper()
	serve := func(response string) *voice.Whisper {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /health", func(http.ResponseWriter, *http.Request) {})
		mux.HandleFunc("POST /inference", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(response)) })
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)
		w := voice.New(t.TempDir(), "")
		w.Address = strings.TrimPrefix(server.URL, "http://")
		return w
	}
	return voice.NewRecordings(serve(`{"text":" hello "}`), serve(`{"segments":[{"start":0,"end":1,"text":" draft "}]}`))
}

func post(handler http.Handler, path string, body []byte) (int, string) {
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/octet-stream")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

func createRecording(t *testing.T, handler http.Handler) string {
	t.Helper()
	status, body := post(handler, "/api/voice/recordings", nil)
	var response Envelope[Recording]
	if err := json.Unmarshal([]byte(body), &response); err != nil || status != http.StatusOK || response.Data.ID == "" {
		t.Fatalf("status = %d, body = %s, err = %v", status, body, err)
	}
	return response.Data.ID
}

func TestAChunkGivesTheLiveTextAndFinishGivesTheTextOfTheRecording(t *testing.T) {
	handler, _ := testMuxWith(t, liveRecordings(t))
	id := createRecording(t, handler)

	status, body := post(handler, "/api/voice/recordings/"+id+"/chunks?sequence=0", make([]byte, 64000))
	finishStatus, finishBody := post(handler, "/api/voice/recordings/"+id+"/finish", nil)
	finished := conversionOf(t, finishBody)
	done := waitForConversion(t, handler, finished.ID)

	if status != http.StatusOK || strings.TrimSpace(body) != `{"data":{"committed":"","draft":"draft"}}` {
		t.Errorf("chunk: status = %d, body = %s", status, body)
	}
	if finishStatus != http.StatusOK || finished.State != "converting" || len(finished.ID) != 32 {
		t.Errorf("finish: status = %d, body = %s", finishStatus, finishBody)
	}
	if want := (Conversion{ID: finished.ID, State: "done", Text: "hello"}); done != want {
		t.Errorf("done = %+v, want %+v", done, want)
	}
	if status, _ := post(handler, "/api/voice/recordings/"+id+"/finish", nil); status != http.StatusNotFound {
		t.Errorf("second finish: status = %d", status)
	}
}

func TestAnUnknownRecordingGives404AndAWrongSequenceGives409(t *testing.T) {
	handler, _ := testMuxWith(t, liveRecordings(t))
	id := createRecording(t, handler)

	unknownChunk, _ := post(handler, "/api/voice/recordings/unknown/chunks?sequence=0", []byte("aa"))
	unknownFinish, _ := post(handler, "/api/voice/recordings/unknown/finish", nil)
	wrong, body := post(handler, "/api/voice/recordings/"+id+"/chunks?sequence=1", []byte("aa"))

	if unknownChunk != http.StatusNotFound || unknownFinish != http.StatusNotFound {
		t.Errorf("unknown: chunk %d, finish %d", unknownChunk, unknownFinish)
	}
	if wrong != http.StatusConflict {
		t.Errorf("wrong sequence: status = %d, body = %s", wrong, body)
	}
}

func TestAChunkAnswers503WhenTheDraftConverterIsNotReady(t *testing.T) {
	handler, _ := testMuxWith(t, withoutDraft(t, voice.New(t.TempDir(), "")))
	id := createRecording(t, handler)

	status, body := post(handler, "/api/voice/recordings/"+id+"/chunks?sequence=0", []byte("aa"))

	if status != http.StatusServiceUnavailable || strings.TrimSpace(body) != `{"error":"Install whisper.cpp: brew install whisper-cpp"}` {
		t.Errorf("status = %d, body = %s", status, body)
	}
}

func TestAChunkRefusesABodyAbove1MBAndAcceptsABodyOf1MB(t *testing.T) {
	handler, _ := testMuxWith(t, liveRecordings(t))
	id := createRecording(t, handler)

	tooLarge, text := post(handler, "/api/voice/recordings/"+id+"/chunks?sequence=0", make([]byte, 1024*1024+1))
	accepted, _ := post(handler, "/api/voice/recordings/"+id+"/chunks?sequence=0", make([]byte, 1024*1024))

	if tooLarge != http.StatusBadRequest || !strings.Contains(text, "request body too large") {
		t.Errorf("status = %d, body = %s", tooLarge, text)
	}
	if accepted != http.StatusOK {
		t.Errorf("status = %d", accepted)
	}
}
