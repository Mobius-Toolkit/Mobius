package voice

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"sync"
	"time"
)

const (
	sampleRate     = 16000
	bytesPerSecond = sampleRate * 2
	// maxWindow is the size of the longest part of a recording that one draft run converts.
	maxWindow     = 15 * bytesPerSecond
	idleTimeout   = 10 * time.Minute
	draftTimeout  = 10 * time.Second
	finishTimeout = 5 * time.Minute
)

// The errors of Recordings.
var (
	ErrUnknown  = errors.New("unknown recording")
	ErrSequence = errors.New("the chunk is not the next one")
)

// LiveText is the text of a recording so far. The words of Committed do not change. Draft is the newest guess for the
// words after them.
type LiveText struct {
	Committed string
	Draft     string
}

// Recordings keeps the audio of the voice recordings that run, and gives their live text.
type Recordings struct {
	// Final converts the full audio of a recording when it ends.
	Final *Whisper
	// Draft converts the newest audio of a recording while it runs.
	Draft *Whisper
	// IdleTimeout is the time after which a recording with no chunk ends.
	IdleTimeout time.Duration

	mu         sync.Mutex
	recordings map[string]*recording
}

// NewRecordings gives Recordings that convert with final and draft.
func NewRecordings(final, draft *Whisper) *Recordings {
	return &Recordings{Final: final, Draft: draft, IdleTimeout: idleTimeout, recordings: map[string]*recording{}}
}

// Start starts the two converters in the background.
func (r *Recordings) Start() {
	r.Final.Start()
	r.Draft.Start()
}

// Stop stops the two converters.
func (r *Recordings) Stop() {
	r.Final.Stop()
	r.Draft.Stop()
}

type recording struct {
	// timer and last belong to Recordings.mu.
	timer *time.Timer
	last  time.Time

	mu       sync.Mutex
	sequence int
	// pcm is the audio of the full recording. window is the audio that the committed text does not cover.
	pcm       []byte
	window    []byte
	committed string
	// previous has the text of each segment that the newest draft run gave and that is not committed.
	previous []string
	running  bool
}

func (r *recording) text() LiveText {
	return LiveText{Committed: r.committed, Draft: strings.Join(r.previous, " ")}
}

// apply commits the segments that the newest run and the run before it give in the same way (LocalAgreement-2). When
// the window is still longer than maxWindow, it also commits the first segment that is left.
func (r *recording) apply(segments []Segment) {
	var texts []string
	var ends []float64
	for _, segment := range segments {
		if text := strings.TrimSpace(segment.Text); text != "" {
			texts = append(texts, text)
			ends = append(ends, segment.End)
		}
	}
	commit := 0
	for commit < len(texts) && commit < len(r.previous) && texts[commit] == r.previous[commit] {
		commit++
	}
	cut := cutAt(commit, ends, len(r.window))
	if len(r.window)-cut > maxWindow && commit < len(texts) {
		commit++
		cut = cutAt(commit, ends, len(r.window))
	}
	r.committed = strings.TrimSpace(r.committed + " " + strings.Join(texts[:commit], " "))
	r.window = r.window[cut:]
	r.previous = texts[commit:]
}

// cutAt gives the number of bytes of the window up to the end of the segment before commit.
func cutAt(commit int, ends []float64, window int) int {
	if commit == 0 {
		return 0
	}
	return min(int(ends[commit-1]*sampleRate)*2, window)
}

// Create starts a recording and gives its id.
func (r *Recordings) Create() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	rec := &recording{last: time.Now()}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec.timer = time.AfterFunc(r.IdleTimeout, func() { r.expire(id, rec) })
	r.recordings[id] = rec
	return id, nil
}

func (r *Recordings) expire(id string, rec *recording) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recordings[id] == rec && time.Since(rec.last) >= r.IdleTimeout {
		delete(r.recordings, id)
	}
}

func (r *Recordings) find(id string) (*recording, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.recordings[id]
	if !ok {
		return nil, ErrUnknown
	}
	rec.last = time.Now()
	rec.timer.Reset(r.IdleTimeout)
	return rec, nil
}

// Chunk adds the 16 kHz mono 16-bit samples pcm to the recording id. The sequence of the first chunk is 0, and each
// chunk has the next number. Chunk starts a draft run unless the run of an earlier chunk still goes on. It gives an
// Unavailable error while the draft converter is not ready. The audio stays in the recording also when the run fails.
func (r *Recordings) Chunk(ctx context.Context, id string, sequence int, pcm []byte) (LiveText, error) {
	rec, err := r.find(id)
	if err != nil {
		return LiveText{}, err
	}
	rec.mu.Lock()
	if sequence != rec.sequence {
		rec.mu.Unlock()
		return LiveText{}, ErrSequence
	}
	rec.sequence++
	rec.pcm = append(rec.pcm, pcm...)
	rec.window = append(rec.window, pcm...)
	if rec.running {
		defer rec.mu.Unlock()
		return rec.text(), nil
	}
	rec.running = true
	wav := wavFile(rec.window)
	rec.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, draftTimeout)
	defer cancel()
	segments, err := r.Draft.Segments(ctx, wav)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.running = false
	if err != nil {
		return LiveText{}, err
	}
	rec.apply(segments)
	return rec.text(), nil
}

// Finish removes the recording id and gives the text of its full audio.
func (r *Recordings) Finish(ctx context.Context, id string) (string, error) {
	r.mu.Lock()
	rec, ok := r.recordings[id]
	if !ok {
		r.mu.Unlock()
		return "", ErrUnknown
	}
	delete(r.recordings, id)
	rec.timer.Stop()
	r.mu.Unlock()

	rec.mu.Lock()
	wav := wavFile(rec.pcm)
	rec.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, finishTimeout)
	defer cancel()
	return r.Final.Transcribe(ctx, wav)
}

func wavFile(pcm []byte) []byte {
	size := uint32(min(len(pcm), math.MaxUint32-36))
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], 36+size)
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], sampleRate)
	binary.LittleEndian.PutUint32(header[28:], bytesPerSecond)
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], size)
	return append(header, pcm...)
}
