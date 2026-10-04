package api

import (
	"context"
	"time"
)

// HealthRequest is the request of GetHealth.
type HealthRequest struct{}

// Health is the state of the server.
type Health struct {
	// Status is "ok" when the server can serve requests
	Status string `gork:"status"`
	// Time is the current time of the server
	Time time.Time `gork:"time"`
}

// HealthResponse is the response of GetHealth.
type HealthResponse struct {
	Body Envelope[Health]
}

// GetHealth returns the state of the server.
func GetHealth(_ context.Context, _ HealthRequest) (*HealthResponse, error) {
	return &HealthResponse{Body: Envelope[Health]{Data: Health{Status: "ok", Time: time.Now().UTC()}}}, nil
}
