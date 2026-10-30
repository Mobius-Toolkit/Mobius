package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/voice"
)

// maxTranscriptionBody is the size of about 10 minutes of WAV audio (16 kHz, mono, 16-bit PCM).
const maxTranscriptionBody = 20 * 1024 * 1024

// maxChunkBody is more than 30 seconds of 16 kHz mono 16-bit samples.
const maxChunkBody = 1024 * 1024

// Transcription is the text of a voice recording.
type Transcription struct {
	// Text is the text of the recording, with no leading or trailing spaces
	Text string `gork:"text"`
}

// TranscribeRequest is the request of Transcribe.
type TranscribeRequest struct {
	// Body is a WAV file (16 kHz, mono, 16-bit PCM) with the Content-Type audio/wav. It has at most 20 MB
	Body []byte
}

// TranscribeResponse is the response of Transcribe.
type TranscribeResponse struct {
	Body Envelope[Transcription]
}

// Transcribe converts a voice recording to text. It waits up to 5 minutes for the converter. It answers 503 when the
// converter is still not ready after that time, and 502 when the converter returns an error.
func (h *handlers) Transcribe(ctx context.Context, req TranscribeRequest) (*TranscribeResponse, error) {
	text, err := h.recordings.Final.Transcribe(ctx, req.Body, h.recordings.Language())
	if err != nil {
		return nil, voiceError(err)
	}
	return &TranscribeResponse{Body: Envelope[Transcription]{Data: Transcription{Text: text}}}, nil
}

func voiceError(err error) error {
	var unavailable voice.Unavailable
	switch {
	case errors.As(err, &unavailable):
		return api.NewHTTPError(http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, voice.ErrUnknown):
		return api.NewHTTPError(http.StatusNotFound, err.Error())
	case errors.Is(err, voice.ErrSequence):
		return api.NewHTTPError(http.StatusConflict, err.Error())
	}
	return api.NewHTTPError(http.StatusBadGateway, err.Error())
}

// Recording is a voice recording that the server keeps while it runs.
type Recording struct {
	// ID is the id of the recording
	ID string `gork:"id"`
}

// CreateRecordingResponse is the response of CreateRecording.
type CreateRecordingResponse struct {
	Body Envelope[Recording]
}

// CreateRecording starts a voice recording on the server. The server removes the recording after 10 minutes with no
// chunk.
func (h *handlers) CreateRecording(context.Context, struct{}) (*CreateRecordingResponse, error) {
	id, err := h.recordings.Create()
	if err != nil {
		return nil, err
	}
	return &CreateRecordingResponse{Body: Envelope[Recording]{Data: Recording{ID: id}}}, nil
}

// LiveText is the text of a recording so far.
type LiveText struct {
	// Committed is the text of the full recording that does not change any more, with no leading or trailing spaces
	Committed string `gork:"committed"`
	// Draft is the newest guess for the words after Committed, with no leading or trailing spaces
	Draft string `gork:"draft"`
}

// SendChunkRequest is the request of SendChunk.
type SendChunkRequest struct {
	Path struct {
		// ID is the id of the recording
		ID string `gork:"id"`
	}
	Query struct {
		// Sequence is the number of the chunk. The first chunk is 0, and each chunk has the next number
		Sequence int64 `gork:"sequence"`
	}
	// Body is the samples of the chunk (16 kHz, mono, 16-bit PCM) with the Content-Type application/octet-stream. It
	// has at most 1 MB
	Body []byte
}

// SendChunkResponse is the response of SendChunk.
type SendChunkResponse struct {
	Body Envelope[LiveText]
}

// SendChunk adds samples to a recording and gives its live text. It answers 404 for an unknown recording, 409 when
// the sequence is not the next one, 503 while the draft converter is not ready, and 502 when it returns an error.
func (h *handlers) SendChunk(ctx context.Context, req SendChunkRequest) (*SendChunkResponse, error) {
	text, err := h.recordings.Chunk(ctx, req.Path.ID, int(req.Query.Sequence), req.Body)
	if err != nil {
		return nil, voiceError(err)
	}
	return &SendChunkResponse{Body: Envelope[LiveText]{Data: LiveText{Committed: text.Committed, Draft: text.Draft}}}, nil
}

// FinishRecordingRequest is the request of FinishRecording.
type FinishRecordingRequest struct {
	Path struct {
		// ID is the id of the recording
		ID string `gork:"id"`
	}
}

// FinishRecordingResponse is the response of FinishRecording.
type FinishRecordingResponse struct {
	Body Envelope[Transcription]
}

// FinishRecording converts the full audio of a recording to text and removes the recording. It answers 404 for an
// unknown recording. It waits up to 5 minutes for the converter. It answers 503 when the converter is still not ready
// after that time, and 502 when the converter returns an error.
func (h *handlers) FinishRecording(ctx context.Context, req FinishRecordingRequest) (*FinishRecordingResponse, error) {
	text, err := h.recordings.Finish(ctx, req.Path.ID)
	if err != nil {
		return nil, voiceError(err)
	}
	return &FinishRecordingResponse{Body: Envelope[Transcription]{Data: Transcription{Text: text}}}, nil
}

// VoiceLanguageItem is a language that the voice conversion accepts.
type VoiceLanguageItem struct {
	// Code is the code of the language. The code auto detects the language
	Code string `gork:"code"`
	// Name is the English name of the language
	Name string `gork:"name"`
}

// VoiceLanguage is the language of the voice conversion.
type VoiceLanguage struct {
	// Language is the code of the language that new recordings use
	Language string `gork:"language"`
	// Languages are the languages that the voice conversion accepts, with auto first and then sorted by name
	Languages []VoiceLanguageItem `gork:"languages"`
}

// VoiceLanguageResponse is the response of GetVoiceLanguage and SetVoiceLanguage.
type VoiceLanguageResponse struct {
	Body Envelope[VoiceLanguage]
}

// GetVoiceLanguage returns the language of the voice conversion and the languages that it accepts.
func (h *handlers) GetVoiceLanguage(context.Context, struct{}) (*VoiceLanguageResponse, error) {
	return h.voiceLanguage(), nil
}

// SetVoiceLanguageRequest is the request of SetVoiceLanguage.
type SetVoiceLanguageRequest struct {
	Body struct {
		// Language is the code of the language, one of the codes that GetVoiceLanguage gives
		Language string `gork:"language"`
	}
}

// SetVoiceLanguage sets the language of the voice conversion. A recording that starts after it uses the language, and
// a recording that is running keeps its language. It returns 422 for a code that is not in the list.
func (h *handlers) SetVoiceLanguage(ctx context.Context, req SetVoiceLanguageRequest) (*VoiceLanguageResponse, error) {
	if !voice.IsLanguage(req.Body.Language) {
		return nil, api.NewHTTPError(http.StatusUnprocessableEntity, "The language "+req.Body.Language+" does not exist.")
	}
	err := h.queries.UpsertVoiceLanguage(ctx, store.UpsertVoiceLanguageParams{
		Language: req.Body.Language, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, err
	}
	h.recordings.SetLanguage(req.Body.Language)
	return h.voiceLanguage(), nil
}

func (h *handlers) voiceLanguage() *VoiceLanguageResponse {
	languages := make([]VoiceLanguageItem, len(voice.Languages))
	for i, language := range voice.Languages {
		languages[i] = VoiceLanguageItem{Code: language.Code, Name: language.Name}
	}
	return &VoiceLanguageResponse{Body: Envelope[VoiceLanguage]{Data: VoiceLanguage{Language: h.recordings.Language(), Languages: languages}}}
}

// LoadVoiceLanguage sets the language of recordings to the stored language. With no stored language, the language stays
// the default.
func LoadVoiceLanguage(ctx context.Context, queries *store.Queries, recordings *voice.Recordings) error {
	row, err := queries.GetVoiceLanguage(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	recordings.SetLanguage(row.Language)
	return nil
}
