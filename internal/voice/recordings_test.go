package voice_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/voice"
)

const second = 32000

// fakeServer is a whisper-server that answers health with 200 and /inference with handler.
func fakeServer(t *testing.T, handler http.HandlerFunc) *voice.Whisper {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /inference", handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	w := voice.New(t.TempDir(), "")
	w.Address = strings.TrimPrefix(server.URL, "http://")
	return w
}

func wavSize(r *http.Request) int {
	file, _, err := r.FormFile("file")
	if err != nil {
		return -1
	}
	defer func() { _ = file.Close() }()
	wav, _ := io.ReadAll(file)
	return len(wav)
}

// draft is a draft converter that gives the segments of the next run of its script for each request, and the last
// segments after the script ends. It keeps the size of the WAV file of each request.
type draft struct {
	*voice.Whisper
	mu      sync.Mutex
	sizes   []int
	started chan struct{}
	release chan struct{}
}

func newDraft(t *testing.T, script ...[]voice.Segment) *draft {
	t.Helper()
	d := &draft{started: make(chan struct{}, 10)}
	d.Whisper = fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		size := wavSize(r)
		d.mu.Lock()
		d.sizes = append(d.sizes, size)
		run := min(len(d.sizes), len(script)) - 1
		release := d.release
		d.mu.Unlock()
		d.started <- struct{}{}
		if release != nil {
			<-release
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"segments": script[run]})
	})
	return d
}

func (d *draft) runs() []int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]int(nil), d.sizes...)
}

func recordingsOf(t *testing.T, d *draft) *voice.Recordings {
	t.Helper()
	final := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"text": fmt.Sprintf(" %d bytes ", wavSize(r))})
	})
	return voice.NewRecordings(final, d.Whisper)
}

func audio(seconds int) []byte {
	return make([]byte, seconds*second)
}

func segment(end float64, text string) voice.Segment {
	return voice.Segment{End: end, Text: " " + text + " "}
}

func chunk(t *testing.T, r *voice.Recordings, id string, sequence, seconds int) voice.LiveText {
	t.Helper()
	text, err := r.Chunk(context.Background(), id, sequence, audio(seconds))
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func create(t *testing.T, r *voice.Recordings) string {
	t.Helper()
	id, err := r.Create()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTextThatTwoRunsGiveInTheSameWayStaysCommittedAndTheNewestTextIsTheDraft(t *testing.T) {
	d := newDraft(t,
		[]voice.Segment{segment(1, "Plant"), segment(2, "red")},
		[]voice.Segment{segment(1, "Plant"), segment(2, "red"), segment(3, "roses")},
		[]voice.Segment{segment(1, "roses"), segment(2, "now")},
	)
	r := recordingsOf(t, d)
	id := create(t, r)

	first := chunk(t, r, id, 0, 3)
	secondText := chunk(t, r, id, 1, 3)
	third := chunk(t, r, id, 2, 3)

	if want := (voice.LiveText{Draft: "Plant red"}); first != want {
		t.Errorf("first = %+v, want %+v", first, want)
	}
	if want := (voice.LiveText{Committed: "Plant red", Draft: "roses"}); secondText != want {
		t.Errorf("second = %+v, want %+v", secondText, want)
	}
	if want := (voice.LiveText{Committed: "Plant red roses", Draft: "now"}); third != want {
		t.Errorf("third = %+v, want %+v", third, want)
	}
	// The third run gets the audio after the end of "red" (2 s): 3 s + 3 s + 3 s of audio, without the first 2 s.
	if got, want := d.runs(), []int{44 + 3*second, 44 + 6*second, 44 + 7*second}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("sizes = %v, want %v", got, want)
	}
}

func TestAWindowLongerThan15SecondsCommitsTheFirstSegmentOfTheNewestRun(t *testing.T) {
	d := newDraft(t,
		[]voice.Segment{segment(4, "one"), segment(8, "two")},
		[]voice.Segment{segment(5, "alpha"), segment(10, "beta"), segment(16, "gamma")},
		[]voice.Segment{segment(6, "beta"), segment(12, "gamma")},
	)
	r := recordingsOf(t, d)
	id := create(t, r)

	chunk(t, r, id, 0, 8)
	secondText := chunk(t, r, id, 1, 8)
	chunk(t, r, id, 2, 1)

	if want := (voice.LiveText{Committed: "alpha", Draft: "beta gamma"}); secondText != want {
		t.Errorf("second = %+v, want %+v", secondText, want)
	}
	if got, want := d.runs(), []int{44 + 8*second, 44 + 16*second, 44 + 12*second}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("sizes = %v, want %v", got, want)
	}
}

func TestAWindowWithNoTextNeverGrowsAbove15Seconds(t *testing.T) {
	d := newDraft(t, []voice.Segment{})
	r := recordingsOf(t, d)
	id := create(t, r)

	for sequence := range 9 {
		chunk(t, r, id, sequence, 2)
	}

	want := []int{44 + 2*second, 44 + 4*second, 44 + 6*second, 44 + 8*second, 44 + 10*second, 44 + 12*second,
		44 + 14*second, 44 + 15*second, 44 + 15*second}
	if got := d.runs(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("sizes = %v, want %v", got, want)
	}
}

func TestAChunkDuringARunningDraftRunStartsNoSecondRun(t *testing.T) {
	d := newDraft(t, []voice.Segment{segment(1, "red")}, []voice.Segment{segment(1, "red"), segment(2, "roses")})
	d.release = make(chan struct{})
	r := recordingsOf(t, d)
	id := create(t, r)
	done := make(chan voice.LiveText)
	go func() { done <- chunk(t, r, id, 0, 2) }()
	<-d.started

	during := chunk(t, r, id, 1, 2)

	if during != (voice.LiveText{}) || len(d.runs()) != 1 {
		t.Errorf("during = %+v, runs = %v", during, d.runs())
	}
	close(d.release)
	if first := <-done; first.Draft != "red" {
		t.Errorf("first = %+v", first)
	}
	chunk(t, r, id, 2, 2)
	if got, want := d.runs(), []int{44 + 2*second, 44 + 6*second}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("sizes = %v, want %v", got, want)
	}
}

func TestFinishGivesTheTextOfTheFullAudioAndRemovesTheRecording(t *testing.T) {
	d := newDraft(t, []voice.Segment{segment(1, "red")})
	r := recordingsOf(t, d)
	id := create(t, r)
	chunk(t, r, id, 0, 2)
	chunk(t, r, id, 1, 3)

	text, err := r.Finish(context.Background(), id)

	if err != nil || text != fmt.Sprintf("%d bytes", 44+5*second) {
		t.Errorf("text = %q, err = %v", text, err)
	}
	if _, err := r.Finish(context.Background(), id); !errors.Is(err, voice.ErrUnknown) {
		t.Errorf("second finish: %v", err)
	}
	if _, err := r.Chunk(context.Background(), id, 2, audio(1)); !errors.Is(err, voice.ErrUnknown) {
		t.Errorf("chunk: %v", err)
	}
}

func TestFinishWaitsWhileTheModelStartsAndThenGivesTheText(t *testing.T) {
	c := newConverter(t)
	program := testkit.InstallFakeWhisper(t, c.bin)
	c.putModel(t)
	if err := os.WriteFile(program+".loading", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := voice.NewRecordings(c.Whisper, newDraft(t, []voice.Segment{segment(1, "red")}).Whisper)
	id := create(t, r)
	chunk(t, r, id, 0, 2)
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, err := r.Finish(context.Background(), id)
		done <- result{text, err}
	}()
	testkit.WaitFor(t, func() bool { return fileHasLines(program+".starts", 1) })

	select {
	case got := <-done:
		t.Fatalf("Finish ended while the model starts: %q, %v", got.text, got.err)
	default:
	}
	if err := os.Remove(program + ".loading"); err != nil {
		t.Fatal(err)
	}

	got := <-done
	if got.err != nil || got.text != fmt.Sprintf("%d bytes", 44+2*second) {
		t.Errorf("text = %q, err = %v", got.text, got.err)
	}
}

func TestAChunkWithAWrongSequenceIsRefusedAndAddsNoAudio(t *testing.T) {
	d := newDraft(t, []voice.Segment{segment(1, "red")})
	r := recordingsOf(t, d)
	id := create(t, r)
	chunk(t, r, id, 0, 1)

	for _, sequence := range []int{0, 2} {
		if _, err := r.Chunk(context.Background(), id, sequence, audio(1)); !errors.Is(err, voice.ErrSequence) {
			t.Errorf("sequence %d: %v", sequence, err)
		}
	}

	text, err := r.Finish(context.Background(), id)
	if err != nil || text != fmt.Sprintf("%d bytes", 44+second) {
		t.Errorf("text = %q, err = %v", text, err)
	}
}

func TestADraftErrorKeepsTheAudioOfTheChunk(t *testing.T) {
	bad := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	final := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"text": fmt.Sprint(wavSize(r))})
	})
	r := voice.NewRecordings(final, bad)
	id := create(t, r)

	_, err := r.Chunk(context.Background(), id, 0, audio(1))
	text, finishErr := r.Finish(context.Background(), id)

	if err == nil || !strings.Contains(err.Error(), "whisper-server") {
		t.Errorf("err = %v", err)
	}
	if finishErr != nil || text != fmt.Sprint(44+second) {
		t.Errorf("text = %q, err = %v", text, finishErr)
	}
}

func TestTheDraftConverterHasItsOwnPIDFileAndStopStopsBothProcesses(t *testing.T) {
	bin := t.TempDir()
	program := testkit.InstallFakeWhisper(t, bin)
	model := testkit.NewFakeModel(t)
	dataDir := t.TempDir()
	final, draft := voice.New(dataDir, bin), voice.NewDraft(dataDir, bin)
	for _, w := range []*voice.Whisper{final, draft} {
		w.ModelURL = model.URL
		w.VADModelURL = model.URL
		w.Address = testkit.FreeAddress(t)
	}
	r := voice.NewRecordings(final, draft)
	t.Cleanup(r.Stop)
	testkit.WaitFor(t, func() bool {
		_, err := final.Transcribe(context.Background(), []byte("RIFF"))
		return err == nil
	})
	segments := testkit.WaitForValue(t, func() ([]voice.Segment, bool) {
		segments, err := draft.Segments(context.Background(), append([]byte("RIFF"), make([]byte, 40+second)...))
		return segments, err == nil
	})

	if len(segments) != 1 || segments[0].End != 1 {
		t.Errorf("segments = %+v", segments)
	}
	finalPID, finalErr := os.ReadFile(filepath.Join(final.Dir, "whisper-server.pid"))
	draftPID, draftErr := os.ReadFile(filepath.Join(draft.Dir, "whisper-server-draft.pid"))
	if finalErr != nil || draftErr != nil || string(finalPID) == string(draftPID) {
		t.Errorf("pids = %q (%v), %q (%v)", finalPID, finalErr, draftPID, draftErr)
	}
	starts, err := os.ReadFile(filepath.Clean(program + ".starts"))
	if err != nil || !strings.Contains(string(starts), "ggml-small.bin") || !strings.Contains(string(starts), "ggml-large-v3-turbo.bin") {
		t.Errorf("starts = %q, err = %v", starts, err)
	}

	r.Stop()

	for _, w := range []*voice.Whisper{final, draft} {
		testkit.WaitFor(t, func() bool {
			response, err := http.Get("http://" + w.Address + "/health")
			if err == nil {
				_ = response.Body.Close()
			}
			return err != nil
		})
	}
}
