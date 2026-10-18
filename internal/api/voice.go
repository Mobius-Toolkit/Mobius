package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/voice"
)

// maxTranscriptionBody is the size of about 10 minutes of WAV audio (16 kHz, mono, 16-bit PCM).
const maxTranscriptionBody = 20 * 1024 * 1024

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

// Transcribe converts a voice recording to text. It answers 503 while the converter is not ready, and 502 when the
// converter returns an error.
func (h *handlers) Transcribe(ctx context.Context, req TranscribeRequest) (*TranscribeResponse, error) {
	text, err := h.whisper.Transcribe(ctx, req.Body)
	var unavailable voice.Unavailable
	switch {
	case errors.As(err, &unavailable):
		return nil, api.NewHTTPError(http.StatusServiceUnavailable, err.Error())
	case err != nil:
		return nil, api.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	return &TranscribeResponse{Body: Envelope[Transcription]{Data: Transcription{Text: text}}}, nil
}
