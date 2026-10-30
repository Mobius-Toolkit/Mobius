package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/voice"
)

type languageServer struct {
	handler http.Handler
	db      *sql.DB
	program string
}

// newLanguageServer gives the API with recordings whose two converters are the fake whisper-server. It returns when both
// processes are ready.
func newLanguageServer(t *testing.T) languageServer {
	t.Helper()
	bin := t.TempDir()
	program := testkit.InstallFakeWhisper(t, bin)
	dataDir := t.TempDir()
	final, draft := voice.New(dataDir, bin), voice.NewDraft(dataDir, bin)
	for _, name := range []string{"ggml-large-v3-turbo.bin", "ggml-small.bin", "ggml-silero-v6.2.0.bin"} {
		if err := os.MkdirAll(final.Dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(final.Dir, name), []byte("model"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	final.Address = testkit.FreeAddress(t)
	draft.Address = testkit.FreeAddress(t)
	recordings := voice.NewRecordings(final, draft)
	recordings.Start()
	t.Cleanup(recordings.Stop)
	for _, address := range []string{final.Address, draft.Address} {
		testkit.WaitFor(t, func() bool {
			response, err := http.Get("http://" + address + "/health")
			if err != nil {
				return false
			}
			_ = response.Body.Close()
			return response.StatusCode == http.StatusOK
		})
	}
	handler, db := testMuxWith(t, recordings)
	return languageServer{handler, db, program}
}

func (s languageServer) request(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

func (s languageServer) language(t *testing.T) VoiceLanguage {
	t.Helper()
	status, body := s.request(t, http.MethodGet, "/api/voice/language", "")
	var response Envelope[VoiceLanguage]
	if err := json.Unmarshal([]byte(body), &response); err != nil || status != http.StatusOK {
		t.Fatalf("status = %d, body = %s, err = %v", status, body, err)
	}
	return response.Data
}

func (s languageServer) setLanguage(t *testing.T, code string) (int, string) {
	t.Helper()
	return s.request(t, http.MethodPut, "/api/voice/language", `{"language":"`+code+`"}`)
}

// record starts a recording, sends one chunk and finishes the recording.
func (s languageServer) record(t *testing.T) {
	t.Helper()
	id := createRecording(t, s.handler)
	s.chunk(t, id)
	s.finish(t, id)
}

func (s languageServer) chunk(t *testing.T, id string) {
	t.Helper()
	if status, body := post(s.handler, "/api/voice/recordings/"+id+"/chunks?sequence=0", append([]byte("RIFF"), make([]byte, 64000)...)); status != http.StatusOK {
		t.Fatalf("chunk: status = %d, body = %s", status, body)
	}
}

func (s languageServer) finish(t *testing.T, id string) {
	t.Helper()
	if status, body := post(s.handler, "/api/voice/recordings/"+id+"/finish", nil); status != http.StatusOK {
		t.Fatalf("finish: status = %d, body = %s", status, body)
	}
}

func (s languageServer) transcribe(t *testing.T) {
	t.Helper()
	if status, body := transcribe(t, s.handler, []byte("RIFF")); status != http.StatusOK {
		t.Fatalf("transcribe: status = %d, body = %s", status, body)
	}
}

func (s languageServer) lines(t *testing.T, extension string) []string {
	t.Helper()
	content, err := os.ReadFile(filepath.Clean(s.program + extension))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}

func TestTheVoiceLanguageIsEnglishAndEachRequestSendsIt(t *testing.T) {
	s := newLanguageServer(t)

	language := s.language(t)
	s.record(t)
	s.transcribe(t)

	if language.Language != "en" || language.Languages[0] != (VoiceLanguageItem{Code: "auto", Name: "Detect automatically"}) {
		t.Errorf("language = %q, first = %+v", language.Language, language.Languages[0])
	}
	if got := strings.Join(s.lines(t, ".languages"), ","); got != "en,en,en" {
		t.Errorf("languages = %s", got)
	}
}

func TestTheLanguagesAreSortedByNameAfterAuto(t *testing.T) {
	s := newLanguageServer(t)

	languages := s.language(t).Languages

	if len(languages) != 101 || languages[1] != (VoiceLanguageItem{Code: "af", Name: "Afrikaans"}) || languages[100] != (VoiceLanguageItem{Code: "yo", Name: "Yoruba"}) {
		t.Errorf("languages = %d, second = %+v, last = %+v", len(languages), languages[1], languages[len(languages)-1])
	}
}

func TestANewRecordingAndATranscriptionSendTheNewLanguageWithNoNewStart(t *testing.T) {
	s := newLanguageServer(t)
	startsBefore := s.lines(t, ".starts")

	status, body := s.setLanguage(t, "de")
	s.record(t)
	s.transcribe(t)

	if status != http.StatusOK || !strings.HasPrefix(body, `{"data":{"language":"de","languages":[{"code":"auto"`) {
		t.Errorf("status = %d, body = %.100s", status, body)
	}
	if got := strings.Join(s.lines(t, ".languages"), ","); got != "de,de,de" {
		t.Errorf("languages = %s", got)
	}
	if got := s.lines(t, ".starts"); len(got) != len(startsBefore) {
		t.Errorf("starts = %q, before = %q", got, startsBefore)
	}
}

func TestARecordingThatStartedBeforeTheChangeKeepsItsLanguage(t *testing.T) {
	s := newLanguageServer(t)
	id := createRecording(t, s.handler)

	s.setLanguage(t, "de")
	s.chunk(t, id)
	s.finish(t, id)

	if got := strings.Join(s.lines(t, ".languages"), ","); got != "en,en" {
		t.Errorf("languages = %s", got)
	}
	if got := s.language(t).Language; got != "de" {
		t.Errorf("language = %q", got)
	}
}

func TestAutoIsSentAndAnUnknownCodeGives422AndChangesNothing(t *testing.T) {
	s := newLanguageServer(t)

	autoStatus, _ := s.setLanguage(t, "auto")
	s.record(t)
	status, body := s.setLanguage(t, "xx")

	if autoStatus != http.StatusOK || status != http.StatusUnprocessableEntity || strings.TrimSpace(body) != `{"error":"The language xx does not exist."}` {
		t.Errorf("auto status = %d, status = %d, body = %s", autoStatus, status, body)
	}
	if got := strings.Join(s.lines(t, ".languages"), ","); got != "auto,auto" {
		t.Errorf("languages = %s", got)
	}
	if got := s.language(t).Language; got != "auto" {
		t.Errorf("language = %q", got)
	}
	var rows int
	if err := s.db.QueryRow("SELECT count(*) FROM voice_language WHERE language = 'xx'").Scan(&rows); err != nil || rows != 0 {
		t.Errorf("rows = %d, err = %v", rows, err)
	}
}

func TestTheVoiceLanguageNeedsADeviceLogin(t *testing.T) {
	mux, _, _ := newMuxWith(t, liveRecordings(t))
	get := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/voice/language", nil)
	put := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/voice/language", bytes.NewReader([]byte(`{"language":"de"}`)))
	getRecorder, putRecorder := httptest.NewRecorder(), httptest.NewRecorder()

	mux.ServeHTTP(getRecorder, get)
	mux.ServeHTTP(putRecorder, put)

	if getRecorder.Code != http.StatusUnauthorized || putRecorder.Code != http.StatusUnauthorized {
		t.Errorf("get = %d, put = %d", getRecorder.Code, putRecorder.Code)
	}
}

func TestANewStartOnTheSameDatabaseKeepsTheLanguage(t *testing.T) {
	handler, db := testMuxWith(t, liveRecordings(t))
	empty := liveRecordings(t)
	if err := LoadVoiceLanguage(t.Context(), store.New(db), empty); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/voice/language", strings.NewReader(`{"language":"de"}`))
	handler.ServeHTTP(httptest.NewRecorder(), request)
	restarted := liveRecordings(t)

	err := LoadVoiceLanguage(t.Context(), store.New(db), restarted)

	if err != nil || empty.Language() != "en" || restarted.Language() != "de" {
		t.Errorf("err = %v, empty = %q, restarted = %q", err, empty.Language(), restarted.Language())
	}
}
