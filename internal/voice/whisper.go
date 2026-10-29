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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

const (
	program      = "whisper-server"
	modelName    = "ggml-large-v3-turbo.bin"
	address      = "127.0.0.1:6365"
	draftModel   = "ggml-small.bin"
	draftAddress = "127.0.0.1:6366"
	modelsURL    = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/"
	vadName      = "ggml-silero-v6.2.0.bin"
	vadURL       = "https://huggingface.co/ggml-org/whisper-vad/resolve/main/" + vadName
)

// healthTimeout is the time that whisper-server has to answer GET /health.
const healthTimeout = 2 * time.Second

// stallTimeout is the time without a byte after which the download of a model file ends.
const stallTimeout = time.Minute

// portWaits and portInterval limit the wait of Start until the stopped process of an earlier program frees the address.
const (
	portWaits    = 100
	portInterval = 100 * time.Millisecond
)

// finishTimeout is the time that Transcribe waits for the converter and converts.
const finishTimeout = 5 * time.Minute

// readyInterval is the time between two checks of the converter while Transcribe waits for it.
const readyInterval = time.Second

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
	// Dir holds the model file and the VAD model file.
	Dir string
	// Path is the PATH where Whisper looks for whisper-server.
	Path string
	// ModelURL is the address of the model file.
	ModelURL string
	// VADModelURL is the address of the VAD model file.
	VADModelURL string
	// Address is the "host:port" where whisper-server listens.
	Address string
	// StallTimeout is the time without a byte after which the download of a model file ends.
	StallTimeout time.Duration

	modelName string
	pidName   string

	mu          sync.Mutex
	downloading bool
	// cmd is the process that Whisper started. It is nil after the process ends.
	cmd *exec.Cmd
}

// New gives a Whisper that keeps its model in the directory whisper of dataDir and looks for whisper-server in path.
func New(dataDir, path string) *Whisper {
	return &Whisper{
		Dir: filepath.Join(dataDir, "whisper"), Path: path, ModelURL: modelsURL + modelName, VADModelURL: vadURL,
		Address: address, StallTimeout: stallTimeout, modelName: modelName, pidName: program + ".pid",
	}
}

// NewDraft gives a Whisper with the small model for the live draft. It has its own address and its own PID file.
func NewDraft(dataDir, path string) *Whisper {
	return &Whisper{
		Dir: filepath.Join(dataDir, "whisper"), Path: path, ModelURL: modelsURL + draftModel, VADModelURL: vadURL,
		Address: draftAddress, StallTimeout: stallTimeout, modelName: draftModel, pidName: program + "-draft.pid",
	}
}

func (w *Whisper) model() string {
	return filepath.Join(w.Dir, w.modelName)
}

func (w *Whisper) vadModel() string {
	return filepath.Join(w.Dir, vadName)
}

// Start stops a whisper-server of an earlier program, so the arguments of this program apply. Then it removes the
// temporary files of stopped downloads, downloads the model files if they are missing and starts whisper-server, in
// the background. The final Whisper starts before the draft Whisper, so only it removes the temporary files of the
// shared VAD model before a download of this program can write one.
func (w *Whisper) Start() {
	w.Stop()
	w.removeParts(w.modelName)
	if w.modelName == modelName {
		w.removeParts(vadName)
	}
	go func() {
		for i := 0; i < portWaits; i++ {
			if _, answered := w.health(context.Background()); !answered {
				break
			}
			time.Sleep(portInterval)
		}
		_ = w.ensure(context.Background())
	}()
}

// removeParts removes the temporary files that a stopped download of the file leaves in Dir.
func (w *Whisper) removeParts(name string) {
	parts, err := filepath.Glob(filepath.Join(w.Dir, name+"*.part"))
	if err != nil {
		return
	}
	for _, part := range parts {
		_ = os.Remove(part)
	}
}

func (w *Whisper) pidFile() string {
	return filepath.Join(w.Dir, w.pidName)
}

// Stop stops the process that Whisper started. It also stops a process that an earlier program of the same data
// directory started before an exec, when that process answers on the address.
func (w *Whisper) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	defer func() { _ = os.Remove(w.pidFile()) }()
	if w.cmd != nil {
		_ = w.cmd.Process.Kill()
		return
	}
	content, err := os.ReadFile(w.pidFile())
	if err != nil {
		return
	}
	if _, answered := w.health(context.Background()); !answered {
		return
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(content))); err == nil {
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
		}
	}
}

// Segment is a part of the text of a WAV file with its times in seconds from the start of the file.
type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type answer struct {
	Text     string    `json:"text"`
	Segments []Segment `json:"segments"`
	Error    string    `json:"error"`
}

// Transcribe gives the text of the WAV file wav. While the converter downloads or starts the model, it waits. When the
// wait and the conversion together take more than finishTimeout, it gives the last Unavailable error. It gives
// NotInstalled and each other error at once.
func (w *Whisper) Transcribe(ctx context.Context, wav []byte) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, finishTimeout)
	defer cancel()
	for {
		err := w.ensure(ctx)
		if err == nil {
			break
		}
		if !errors.Is(err, Starting) && !errors.Is(err, Downloading) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", err
		case <-time.After(readyInterval):
		}
	}
	result, err := w.infer(ctx, wav, "json")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Text), nil
}

// Segments gives the segments of the WAV file wav. It gives an Unavailable error while the converter is not ready.
func (w *Whisper) Segments(ctx context.Context, wav []byte) ([]Segment, error) {
	result, err := w.infer(ctx, wav, "verbose_json")
	if err != nil {
		return nil, err
	}
	return result.Segments, nil
}

func (w *Whisper) infer(ctx context.Context, wav []byte, format string) (answer, error) {
	if err := w.ensure(ctx); err != nil {
		return answer{}, err
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", "voice.wav")
	if err != nil {
		return answer{}, err
	}
	if _, err := file.Write(wav); err != nil {
		return answer{}, err
	}
	if err := form.WriteField("response_format", format); err != nil {
		return answer{}, err
	}
	if err := form.Close(); err != nil {
		return answer{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+w.Address+"/inference", &body)
	if err != nil {
		return answer{}, err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return answer{}, fmt.Errorf("%s: %w", program, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return answer{}, fmt.Errorf("%s: %s", program, res.Status)
	}
	var result answer
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return answer{}, fmt.Errorf("%s: %w", program, err)
	}
	if result.Error != "" {
		return answer{}, fmt.Errorf("%s: %s", program, result.Error)
	}
	return result, nil
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
	if w.cmd == nil {
		_ = os.Remove(w.pidFile())
	}
	file := runner.Find(program, w.Path)
	if file == "" {
		return NotInstalled
	}
	if w.downloading {
		return Downloading
	}
	for _, file := range w.files() {
		if _, err := os.Stat(file.path); errors.Is(err, fs.ErrNotExist) {
			w.downloading = true
			w.startDownload()
			return Downloading
		} else if err != nil {
			return err
		}
	}
	if w.cmd != nil {
		return Starting
	}
	host, port, err := net.SplitHostPort(w.Address)
	if err != nil {
		return err
	}
	cmd := server(file, w.model(), w.vadModel(), host, port)
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := os.WriteFile(w.pidFile(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	w.cmd = cmd
	go func() {
		_ = cmd.Wait()
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.cmd == cmd {
			w.cmd = nil
			_ = os.Remove(w.pidFile())
		}
	}()
	return Starting
}

func server(file, model, vadModel, host, port string) *exec.Cmd {
	cmd := exec.Command(file)
	cmd.Args = append(cmd.Args, "-m", model, "--host", host, "--port", port, "-l", "en", "-t", "4",
		"--vad", "-vm", vadModel, "-sns", "-mc", "0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
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

// startDownload runs download in the background. The download outlives the request that starts it.
func (w *Whisper) startDownload() {
	go w.download()
}

// modelFile is a file that whisper-server needs, with the address where Whisper gets it.
type modelFile struct {
	url  string
	path string
}

func (w *Whisper) files() []modelFile {
	return []modelFile{{w.ModelURL, w.model()}, {w.VADModelURL, w.vadModel()}}
}

// download gets the missing model files, and then starts the process. A failed download ends, and the next ensure
// starts it again.
func (w *Whisper) download() {
	err := w.fetchMissing()
	w.mu.Lock()
	w.downloading = false
	w.mu.Unlock()
	if err != nil {
		log.Printf("download the speech model: %v", err)
		return
	}
	_ = w.ensure(context.Background())
}

func (w *Whisper) fetchMissing() error {
	for _, file := range w.files() {
		if _, err := os.Stat(file.path); err == nil {
			continue
		}
		if err := w.fetch(file); err != nil {
			return err
		}
	}
	return nil
}

// fetch writes the file to a temporary file and renames it after the full download, so the file is always complete.
// The final and the draft Whisper share Dir and can fetch the VAD model at the same time, so each fetch has its own
// temporary file.
func (w *Whisper) fetch(file modelFile) error {
	if err := os.MkdirAll(w.Dir, 0o750); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stall := time.AfterFunc(w.StallTimeout, cancel)
	defer stall.Stop()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, file.url, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", file.url, res.Status)
	}
	part, err := os.CreateTemp(w.Dir, filepath.Base(file.path)+".*.part")
	if err != nil {
		return err
	}
	_, err = io.Copy(part, &stallReader{body: res.Body, timer: stall, timeout: w.StallTimeout})
	if closeErr := part.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(part.Name())
		return err
	}
	return os.Rename(part.Name(), file.path)
}

// stallReader restarts the timer at each read, so the timer fires only when no bytes come for the timeout.
type stallReader struct {
	body    io.Reader
	timer   *time.Timer
	timeout time.Duration
}

func (r *stallReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	r.timer.Reset(r.timeout)
	return n, err
}
