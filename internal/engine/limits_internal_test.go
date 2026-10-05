package engine

import (
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
)

var limitNow = time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

func TestClaudeCodeTakesTheResetTimeOfTheUsageUpdate(t *testing.T) {
	limit := &acp.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"errorKind": "rate_limit"}}
	hint := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)

	if until, ok := detect(config.ClaudeCode, limit, hint, limitNow); !ok || !until.Equal(hint) {
		t.Errorf("until = %v, %v", until, ok)
	}
}

func TestClaudeCodeReadsTheResetTimeInTheText(t *testing.T) {
	limit := &acp.RequestError{Code: -32603, Message: "Claude usage limit reached. Your limit resets 3pm.", Data: map[string]any{"errorKind": "rate_limit"}}
	early := &acp.RequestError{Code: -32603, Message: "Your limit resets 9:30am", Data: map[string]any{"errorKind": "rate_limit"}}

	if until, ok := detect(config.ClaudeCode, limit, time.Time{}, limitNow); !ok || !until.Equal(time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC)) {
		t.Errorf("until = %v, %v", until, ok)
	}
	if until, ok := detect(config.ClaudeCode, early, time.Time{}, limitNow); !ok || !until.Equal(time.Date(2026, 9, 29, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("until = %v, %v", until, ok)
	}
}

func TestClaudeCodeWithNoTimeWaits30MinutesAndAnotherErrorIsNoLimit(t *testing.T) {
	limit := &acp.RequestError{Code: -32603, Message: "Rate limit", Data: map[string]any{"errorKind": "rate_limit"}}
	other := &acp.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"errorKind": "overloaded"}}

	if until, ok := detect(config.ClaudeCode, limit, time.Time{}, limitNow); !ok || !until.Equal(limitNow.Add(30*time.Minute)) {
		t.Errorf("until = %v, %v", until, ok)
	}
	if until, ok := detect(config.ClaudeCode, other, time.Time{}, limitNow); ok {
		t.Errorf("until = %v", until)
	}
}

func TestAntigravityReadsTheDaysAndHours(t *testing.T) {
	limit := &acp.RequestError{Code: -32603, Message: "Usage Limit Reached. Your quota will reset in 2 days, 3 hours."}
	other := &acp.RequestError{Code: -32603, Message: "Model is busy"}

	if until, ok := detect(config.Antigravity, limit, time.Time{}, limitNow); !ok || !until.Equal(time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC)) {
		t.Errorf("until = %v, %v", until, ok)
	}
	if until, ok := detect(config.Antigravity, other, time.Time{}, limitNow); ok {
		t.Errorf("until = %v", until)
	}
}

func TestDevinTakesTheRetryTimeAndWaits30MinutesWithNoTime(t *testing.T) {
	limit := &acp.RequestError{Code: -32011, Message: "Rate limited", Data: map[string]any{"retryAfterSeconds": 600}}
	noTime := &acp.RequestError{Code: -32011, Message: "Rate limited"}
	other := &acp.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"retryAfterSeconds": 600}}

	if until, ok := detect(config.Devin, limit, time.Time{}, limitNow); !ok || !until.Equal(limitNow.Add(10*time.Minute)) {
		t.Errorf("until = %v, %v", until, ok)
	}
	if until, ok := detect(config.Devin, noTime, time.Time{}, limitNow); !ok || !until.Equal(limitNow.Add(30*time.Minute)) {
		t.Errorf("until = %v, %v", until, ok)
	}
	if until, ok := detect(config.Devin, other, time.Time{}, limitNow); ok {
		t.Errorf("until = %v", until)
	}
}
