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
	bin   string
}

// newConverter gives a converter with a fake model server and a free address. It finds whisper-server only in bin. A
// download can still write in the data directory when the test ends, so the cleanup removes the directory until it
// succeeds.
func newConverter(t *testing.T) converter {
	t.Helper()
	bin := t.TempDir()
	model := testkit.NewFakeModel(t)
	data, err := os.MkdirTemp("", "whisper")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testkit.WaitFor(t, func() bool { return os.RemoveAll(data) == nil })
	})
	w := voice.New(data, bin)
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

// transcribeBriefly gives the Unavailable error that Transcribe has after a wait of 100 ms.
func (c converter) transcribeBriefly() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	return c.Transcribe(ctx, []byte("RIFF"))
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

	_, first := c.transcribeBriefly()
	_, second := c.transcribeBriefly()

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

	_, err := c.transcribeBriefly()
	testkit.WaitFor(t, func() bool {
		_, err = c.transcribeBriefly()
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

	_, err := c.transcribeBriefly()
	testkit.WaitFor(t, func() bool {
		_, err = c.transcribeBriefly()
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

	_, err := c.transcribeBriefly()
	testkit.WaitFor(t, func() bool {
		_, err = c.transcribeBriefly()
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
