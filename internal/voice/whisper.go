// Package voice converts voice recordings to text with a whisper-server process of whisper.cpp that Mobius manages.
package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

const (
	program   = "whisper-server"
	modelName = "ggml-large-v3-turbo.bin"
	modelURL  = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/" + modelName
	address   = "127.0.0.1:6365"
)

// healthTimeout is the time that whisper-server has to answer GET /health.
const healthTimeout = 2 * time.Second

// Unavailable is the reason why the converter cannot convert now. Its text is for the user.
type Unavailable string

func (u Unavailable) Error() string { return string(u) }

// The reasons of Unavailable.
const (
	NotInstalled Unavailable = "Install whisper.cpp: brew install whisper-cpp"
	Downloading  Unavailable = "The speech model is still downloading"
	Starting     Unavailable = "The speech model is still starting"
)

// Whisper converts WAV files to text with a whisper-server process.
type Whisper struct {
	// Dir holds the model file.
	Dir string
	// Path is the PATH where Whisper looks for whisper-server.
	Path string
	// ModelURL is the address of the model file.
	ModelURL string
	// Address is the "host:port" where whisper-server listens.
	Address string

	mu          sync.Mutex
	downloading bool
	// cmd is the process that Whisper started. It is nil after the process ends.
	cmd *exec.Cmd
}

// New gives a Whisper that keeps its model in the directory whisper of dataDir and looks for whisper-server in path.
func New(dataDir, path string) *Whisper {
	return &Whisper{Dir: filepath.Join(dataDir, "whisper"), Path: path, ModelURL: modelURL, Address: address}
}

func (w *Whisper) model() string {
	return filepath.Join(w.Dir, modelName)
}

// Start downloads the model if it is missing and starts whisper-server, in the background.
func (w *Whisper) Start() {
	go func() { _ = w.ensure(context.Background()) }()
}

// Stop stops the process that Whisper started.
func (w *Whisper) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd != nil {
		_ = w.cmd.Process.Kill()
	}
}

// Transcribe gives the text of the WAV file wav. It gives an Unavailable error while the converter is not ready.
func (w *Whisper) Transcribe(ctx context.Context, wav []byte) (string, error) {
	if err := w.ensure(ctx); err != nil {
		return "", err
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", "voice.wav")
	if err != nil {
		return "", err
	}
	if _, err := file.Write(wav); err != nil {
		return "", err
	}
	if err := form.WriteField("response_format", "json"); err != nil {
		return "", err
	}
	if err := form.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+w.Address+"/inference", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: %w", program, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", program, res.Status)
	}
	var answer struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&answer); err != nil {
		return "", fmt.Errorf("%s: %w", program, err)
	}
	if answer.Error != "" {
		return "", fmt.Errorf("%s: %s", program, answer.Error)
	}
	return strings.TrimSpace(answer.Text), nil
}

// ensure gives nil when whisper-server answers GET /health with 200. Else it gives the reason, and it starts the
// download of the model or the process when they are missing.
func (w *Whisper) ensure(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if status, answered := w.health(ctx); answered {
		if status == http.StatusOK {
			return nil
		}
		return Starting
	}
	file := runner.Find(program, w.Path)
	if file == "" {
		return NotInstalled
	}
	if w.downloading {
		return Downloading
	}
	if _, err := os.Stat(w.model()); errors.Is(err, fs.ErrNotExist) {
		w.downloading = true
		go w.download()
		return Downloading
	} else if err != nil {
		return err
	}
	if w.cmd != nil {
		return Starting
	}
	host, port, err := net.SplitHostPort(w.Address)
	if err != nil {
		return err
	}
	cmd := exec.Command(file, "-m", w.model(), "--host", host, "--port", port, "-l", "auto", "-t", "4")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	w.cmd = cmd
	go func() {
		_ = cmd.Wait()
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.cmd == cmd {
			w.cmd = nil
		}
	}()
	return Starting
}

// health gives the status of GET /health, and false when nothing answers.
func (w *Whisper) health(ctx context.Context) (int, bool) {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+w.Address+"/health", nil)
	if err != nil {
		return 0, false
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, false
	}
	_ = res.Body.Close()
	return res.StatusCode, true
}

// download gets the model, and then starts the process. A failed download ends, and the next ensure starts it again.
func (w *Whisper) download() {
	err := w.fetch()
	w.mu.Lock()
	w.downloading = false
	w.mu.Unlock()
	if err != nil {
		log.Printf("download the speech model: %v", err)
		return
	}
	_ = w.ensure(context.Background())
}

// fetch writes the model to a temporary file and renames it after the full download, so the model file is always
// complete.
func (w *Whisper) fetch() error {
	if err := os.MkdirAll(w.Dir, 0o750); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, w.ModelURL, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", w.ModelURL, res.Status)
	}
	part := w.model() + ".part"
	file, err := os.Create(filepath.Clean(part))
	if err != nil {
		return err
	}
	_, err = io.Copy(file, res.Body)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(part)
		return err
	}
	return os.Rename(part, w.model())
}
