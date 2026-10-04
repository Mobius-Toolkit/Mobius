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
	Status string `gork:"status" validate:"required"`
	// Time is the current time of the server
	Time time.Time `gork:"time" validate:"required"`
}

// HealthResponse is the response of GetHealth.
type HealthResponse struct {
	Body Health
}

// GetHealth returns the state of the server.
func GetHealth(_ context.Context, _ HealthRequest) (*HealthResponse, error) {
	return &HealthResponse{Body: Health{Status: "ok", Time: time.Now()}}, nil
}
