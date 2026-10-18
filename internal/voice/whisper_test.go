package voice_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/voice"
)

func TestMain(m *testing.M) {
	testkit.Main(m)
}

type converter struct {
	*voice.Whisper
	model *testkit.FakeModel
	bin   string
}

// newConverter gives a converter with a fake model server and a free address. It finds whisper-server only in bin.
func newConverter(t *testing.T) converter {
	t.Helper()
	bin := t.TempDir()
	model := testkit.NewFakeModel(t)
	w := voice.New(t.TempDir(), bin)
	w.ModelURL = model.URL
	w.Address = testkit.FreeAddress(t)
	t.Cleanup(w.Stop)
	return converter{w, model, bin}
}

func (c converter) modelFile() string {
	return filepath.Join(c.Dir, "ggml-large-v3-turbo.bin")
}

func (c converter) putModel(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(c.Dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.modelFile(), []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (c converter) transcribe(wav string) (string, error) {
	return c.Transcribe(context.Background(), []byte(wav))
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

	_, first := c.transcribe("RIFF")
	_, second := c.transcribe("RIFF")

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
	starts, err := os.ReadFile(program + ".starts")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(c.Address)
	want := fmt.Sprintf("-m %s --host 127.0.0.1 --port %s -l auto -t 4\n", c.modelFile(), port)
	if string(starts) != want {
		t.Errorf("starts = %q, want %q", starts, want)
	}
}

func TestWhileTheProcessLoadsTheModelTheConverterTellsItIsStarting(t *testing.T) {
	c := newConverter(t)
	program := testkit.InstallFakeWhisper(t, c.bin)
	c.putModel(t)
	if err := os.WriteFile(program+".loading", nil, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := c.transcribe("RIFF")
	testkit.WaitFor(t, func() bool {
		_, err = c.transcribe("RIFF")
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
	server := &http.Server{Handler: mux}
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

	_, err := c.transcribe("RIFF")
	testkit.WaitFor(t, func() bool {
		_, err = c.transcribe("RIFF")
		return c.model.Downloads() >= 2
	})

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
