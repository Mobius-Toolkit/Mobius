package voice_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/voice"
)

func TestMain(m *testing.M) {
	testkit.Main(m)
}

type converter struct {
	*voice.Whisper
	model *testkit.FakeModel
	vad   *testkit.FakeModel
	bin   string
}

// newConverter gives a converter with a fake model server, a fake VAD model server and a free address. It finds whisper-server only in bin. A
// download can still write in the data directory when the test ends, so the cleanup removes the directory until it
// succeeds.
func newConverter(t *testing.T) converter {
	t.Helper()
	bin := t.TempDir()
	model := testkit.NewFakeModel(t)
	vad := testkit.NewFakeModel(t)
	data, err := os.MkdirTemp("", "whisper")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testkit.WaitFor(t, func() bool { return os.RemoveAll(data) == nil })
	})
	w := voice.New(data, bin)
	w.ModelURL = model.URL
	w.VADModelURL = vad.URL
	w.Address = testkit.FreeAddress(t)
	t.Cleanup(w.Stop)
	return converter{w, model, vad, bin}
}

func (c converter) modelFile() string {
	return filepath.Join(c.Dir, "ggml-large-v3-turbo.bin")
}

func (c converter) vadFile() string {
	return filepath.Join(c.Dir, "ggml-silero-v6.2.0.bin")
}

func (c converter) putModel(t *testing.T) {
	t.Helper()
	c.putFile(t, c.modelFile())
	c.putFile(t, c.vadFile())
}

func (c converter) putFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(c.Dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (c converter) transcribe(wav string) (string, error) {
	return c.Transcribe(context.Background(), []byte(wav))
}

// transcribeBriefly gives the Unavailable error that Transcribe has after a wait of 100 ms.
func (c converter) transcribeBriefly() error {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.Transcribe(ctx, []byte("RIFF"))
	return err
}

func TestWithoutWhisperServerTheConverterTellsToInstallIt(t *testing.T) {
	c := newConverter(t)

	_, err := c.transcribe("RIFF")

	if !errors.Is(err, voice.NotInstalled) || err.Error() != "Install whisper.cpp: brew install whisper-cpp" {
		t.Errorf("err = %v", err)
	}
	if c.model.Downloads() != 0 {
		t.Errorf("downloads = %d", c.model.Downloads())
	}
}

func TestWhileTheModelDownloadsTheConverterTellsItAndThenItStartsTheProcess(t *testing.T) {
	c := newConverter(t)
	program := testkit.InstallFakeWhisper(t, c.bin)
	c.model.Hold()

	first := c.transcribeBriefly()
	second := c.transcribeBriefly()

	if !errors.Is(first, voice.Downloading) || !errors.Is(second, voice.Downloading) {
		t.Errorf("errors = %v, %v", first, second)
	}
	if first.Error() != "The speech model is still downloading" {
		t.Errorf("message = %q", first)
	}
	c.model.Release()
	text := testkit.WaitForValue(t, func() (string, bool) {
		text, err := c.transcribe("RIFF 12345")
		return text, err == nil
	})
	if text != "10 bytes" {
		t.Errorf("text = %q", text)
	}
	if c.model.Downloads() != 1 {
		t.Errorf("downloads = %d", c.model.Downloads())
	}
	starts, err := os.ReadFile(filepath.Clean(program + ".starts"))
	if err != nil {
		t.Fatal(err)
	}
	if want := c.startLine(c.modelFile()); string(starts) != want {
		t.Errorf("starts = %q, want %q", starts, want)
	}
	if c.vad.Downloads() != 1 {
		t.Errorf("VAD downloads = %d", c.vad.Downloads())
	}
	if content, err := os.ReadFile(filepath.Clean(c.vadFile())); err != nil || string(content) != "model" {
		t.Errorf("VAD model = %q, err = %v", content, err)
	}
}

func (c converter) startLine(model string) string {
	_, port, _ := net.SplitHostPort(c.Address)
	return fmt.Sprintf("-m %s --host 127.0.0.1 --port %s -l en -t 4 --vad -vm %s -sns -mc 0\n", model, port, c.vadFile())
}

func TestWhileOnlyTheVADModelIsMissingTheConverterDownloadsItAndThenStartsTheProcess(t *testing.T) {
	c := newConverter(t)
	program := testkit.InstallFakeWhisper(t, c.bin)
	c.putFile(t, c.modelFile())
	c.vad.Hold()

	err := c.transcribeBriefly()
	c.vad.Release()
	testkit.WaitFor(t, func() bool {
		_, err := c.transcribe("RIFF")
		return err == nil
	})

	if !errors.Is(err, voice.Downloading) {
		t.Errorf("err = %v", err)
	}
	if c.model.Downloads() != 0 || c.vad.Downloads() != 1 {
		t.Errorf("downloads = %d, VAD downloads = %d", c.model.Downloads(), c.vad.Downloads())
	}
	if content, err := os.ReadFile(filepath.Clean(c.vadFile())); err != nil || string(content) != "model" {
		t.Errorf("VAD model = %q, err = %v", content, err)
	}
	if !fileHasLines(program+".starts", 1) {
		t.Error("the converter did not start one process")
	}
}

func TestTheFinalAndTheDraftConverterShareTheDirectoryAndBothStart(t *testing.T) {
	c := newConverter(t)
	program := testkit.InstallFakeWhisper(t, c.bin)
	draft := voice.NewDraft(filepath.Dir(c.Dir), c.bin)
	draft.ModelURL = c.model.URL
	draft.VADModelURL = c.vad.URL
	draft.Address = testkit.FreeAddress(t)
	t.Cleanup(draft.Stop)
	c.vad.Hold()
	_ = c.transcribeBriefly()
	_, _ = draft.Segments(context.Background(), []byte("RIFF"))
	testkit.WaitFor(t, func() bool { return c.vad.Downloads() == 2 })

	c.vad.Release()

	testkit.WaitFor(t, func() bool {
		_, err := c.transcribe("RIFF")
		_, draftErr := draft.Segments(context.Background(), []byte("RIFF"))
		return err == nil && draftErr == nil
	})
	if content, err := os.ReadFile(filepath.Clean(c.vadFile())); err != nil || string(content) != "model" {
		t.Errorf("VAD model = %q, err = %v", content, err)
	}
	parts, err := filepath.Glob(filepath.Join(c.Dir, "*.part"))
	if err != nil || len(parts) != 0 {
		t.Errorf("parts = %v, err = %v", parts, err)
	}
	starts, err := os.ReadFile(filepath.Clean(program + ".starts"))
	if err != nil {
		t.Fatal(err)
	}
	_, draftPort, _ := net.SplitHostPort(draft.Address)
	wantDraft := fmt.Sprintf("-m %s --host 127.0.0.1 --port %s -l en -t 4 --vad -vm %s -sns -mc 0\n",
		filepath.Join(c.Dir, "ggml-small.bin"), draftPort, c.vadFile())
	if got := string(starts); !strings.Contains(got, c.startLine(c.modelFile())) || !strings.Contains(got, wantDraft) {
		t.Errorf("starts = %q", got)
	}
}

func TestStartStopsAProcessOfAnEarlierProgramAndStartsOneWithTheNewArguments(t *testing.T) {
	c := newConverter(t)
	program := testkit.InstallFakeWhisper(t, c.bin)
	c.putModel(t)
	testkit.WaitFor(t, func() bool {
		_, err := c.transcribe("RIFF")
		return err == nil
	})
	next := voice.New(filepath.Dir(c.Dir), c.bin)
	next.Address = c.Address

	next.Start()

	testkit.WaitFor(t, func() bool { return fileHasLines(program+".starts", 2) })
	testkit.WaitFor(t, func() bool {
		_, err := next.Transcribe(context.Background(), []byte("RIFF"))
		return err == nil
	})
	t.Cleanup(next.Stop)
}

func TestStartRemovesTheTemporaryFilesOfStoppedDownloadsAndKeepsThoseOfTheDraftModel(t *testing.T) {
	c := newConverter(t)
	testkit.InstallFakeWhisper(t, c.bin)
	stale := []string{
		"ggml-large-v3-turbo.bin.part", "ggml-large-v3-turbo.bin.123.part",
		"ggml-silero-v6.2.0.bin.part", "ggml-silero-v6.2.0.bin.456.part",
	}
	draftPart := filepath.Join(c.Dir, "ggml-small.bin.789.part")
	for _, name := range stale {
		c.putFile(t, filepath.Join(c.Dir, name))
	}
	c.putFile(t, draftPart)

	c.Start()

	testkit.WaitFor(t, func() bool {
		_, err := c.transcribe("RIFF")
		return err == nil
	})
	for _, name := range stale {
		if _, err := os.Stat(filepath.Join(c.Dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := os.Stat(draftPart); err != nil {
		t.Errorf("part of the draft model: %v", err)
	}
}

func TestWhileTheProcessLoadsTheModelTheConverterTellsItIsStarting(t *testing.T) {
	c := newConverter(t)
	program := testkit.InstallFakeWhisper(t, c.bin)
	c.putModel(t)
	if err := os.WriteFile(program+".loading", nil, 0o600); err != nil {
		t.Fatal(err)
	}

	err := c.transcribeBriefly()
	testkit.WaitFor(t, func() bool {
		err = c.transcribeBriefly()
		return errors.Is(err, voice.Starting) && fileHasLines(program+".starts", 1)
	})

	if err.Error() != "The speech model is still starting" {
		t.Errorf("message = %q", err)
	}
	if err := os.Remove(program + ".loading"); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool {
		_, err := c.transcribe("RIFF")
		return err == nil
	})
	if !fileHasLines(program+".starts", 1) {
		t.Error("the converter started a second process")
	}
}

func TestTheConverterUsesAProcessThatAnswersOnHealthAndStartsNoSecondProcess(t *testing.T) {
	c := newConverter(t)
	program := testkit.InstallFakeWhisper(t, c.bin)
	listener, err := net.Listen("tcp", c.Address)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /inference", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"text":" from the old process"}`))
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	text, err := c.transcribe("RIFF")

	if err != nil || text != "from the old process" {
		t.Errorf("text = %q, err = %v", text, err)
	}
	if _, err := os.Stat(program + ".starts"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the converter started a process: %v", err)
	}
	if c.model.Downloads() != 0 {
		t.Errorf("downloads = %d", c.model.Downloads())
	}
}

func TestAStoppedDownloadLeavesNoModelFile(t *testing.T) {
	c := newConverter(t)
	testkit.InstallFakeWhisper(t, c.bin)
	c.model.SetBroken(true)

	err := c.transcribeBriefly()
	testkit.WaitFor(t, func() bool {
		err = c.transcribeBriefly()
		return c.model.Downloads() >= 2
	})
	testkit.WaitFor(t, func() bool { return !c.IsDownloading() })

	if !errors.Is(err, voice.Downloading) {
		t.Errorf("err = %v", err)
	}
	if _, err := os.Stat(c.modelFile()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("model file: %v", err)
	}
}

func TestADownloadThatGetsNoBytesEndsAndTheNextRequestStartsItAgain(t *testing.T) {
	c := newConverter(t)
	testkit.InstallFakeWhisper(t, c.bin)
	c.StallTimeout = 100 * time.Millisecond
	c.model.SetStalled(true)

	err := c.transcribeBriefly()
	testkit.WaitFor(t, func() bool {
		err = c.transcribeBriefly()
		return c.model.Downloads() >= 2
	})
	testkit.WaitFor(t, func() bool { return !c.IsDownloading() })

	if !errors.Is(err, voice.Downloading) {
		t.Errorf("err = %v", err)
	}
	if _, err := os.Stat(c.modelFile()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("model file: %v", err)
	}
}

func TestStopStopsTheProcessThatTheConverterStarted(t *testing.T) {
	c := newConverter(t)
	testkit.InstallFakeWhisper(t, c.bin)
	c.putModel(t)
	testkit.WaitFor(t, func() bool {
		_, err := c.transcribe("RIFF")
		return err == nil
	})

	c.Stop()

	testkit.WaitFor(t, func() bool {
		response, err := http.Get("http://" + c.Address + "/health")
		if err == nil {
			_ = response.Body.Close()
		}
		return err != nil
	})
}

func TestStopStopsAProcessThatAnEarlierConverterStarted(t *testing.T) {
	c := newConverter(t)
	testkit.InstallFakeWhisper(t, c.bin)
	c.putModel(t)
	testkit.WaitFor(t, func() bool {
		_, err := c.transcribe("RIFF")
		return err == nil
	})
	next := voice.New(filepath.Dir(c.Dir), c.bin)
	next.Address = c.Address
	if _, err := next.Transcribe(context.Background(), []byte("RIFF")); err != nil {
		t.Fatal(err)
	}

	next.Stop()

	testkit.WaitFor(t, func() bool {
		response, err := http.Get("http://" + c.Address + "/health")
		if err == nil {
			_ = response.Body.Close()
		}
		return err != nil
	})
}

func TestStopKillsNoProcessThatOnlyHasThePIDOfAnEndedConverter(t *testing.T) {
	c := newConverter(t)
	other := exec.Command("sleep", "60")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = other.Process.Kill()
		_ = other.Wait()
	})
	if err := os.MkdirAll(c.Dir, 0o750); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(c.Dir, "whisper-server.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(other.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}

	c.Stop()

	if err := other.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("the process ended: %v", err)
	}
	if _, err := os.Stat(pidFile); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the PID file stays: %v", err)
	}
}

func TestAnErrorOfWhisperServerIsNoUnavailableError(t *testing.T) {
	c := newConverter(t)
	testkit.InstallFakeWhisper(t, c.bin)
	c.putModel(t)
	testkit.WaitFor(t, func() bool {
		_, err := c.transcribe("RIFF")
		return err == nil
	})

	_, err := c.transcribe("not a WAV file")

	var unavailable voice.Unavailable
	if err == nil || errors.As(err, &unavailable) || !strings.Contains(err.Error(), "whisper-server") {
		t.Errorf("err = %v", err)
	}
}

func fileHasLines(path string, lines int) bool {
	content, err := os.ReadFile(filepath.Clean(path))
	return err == nil && strings.Count(string(content), "\n") == lines
}
