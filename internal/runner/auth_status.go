package runner

import (
	"encoding/json"
)

const (
	// authStatusMethod is the ACP notification of the account of the agent.
	authStatusMethod = "auth/status_update"
	// extensionAuthStatusMethod is the extension notification of the same account.
	extensionAuthStatusMethod = "_auth/status_update"
)

// AuthStatus is the account that Claude Code uses. Kind is "account" for the Claude subscription. The other kinds are
// "api_key", "gateway", "external" and "none" (logged out). It has no email: the notification has one, and nothing
// reads it.
type AuthStatus struct {
	Kind   string
	Label  string
	Detail string
}

// parseAuthStatus reads the params of an auth/status_update or _auth/status_update notification, and gives false when
// the notification does not tell the account.
func parseAuthStatus(method string, params json.RawMessage) (AuthStatus, bool, error) {
	if method == extensionAuthStatusMethod {
		var message struct {
			AuthStatus struct {
				Kind   string `json:"kind"`
				Label  string `json:"label"`
				Detail string `json:"detail"`
			} `json:"authStatus"`
		}
		if err := json.Unmarshal(params, &message); err != nil {
			return AuthStatus{}, false, err
		}
		status := AuthStatus(message.AuthStatus)
		return status, status.Kind != "", nil
	}
	var message struct {
		State    string `json:"state"`
		Accounts []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
			Meta        struct {
				ClaudeCode struct {
					Kind string `json:"kind"`
				} `json:"claudeCode"`
			} `json:"_meta"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(params, &message); err != nil {
		return AuthStatus{}, false, err
	}
	if message.State == "unauthenticated" {
		return AuthStatus{Kind: "none", Label: "Not logged in"}, true, nil
	}
	if len(message.Accounts) == 0 || message.Accounts[0].Meta.ClaudeCode.Kind == "" {
		return AuthStatus{}, false, nil
	}
	account := message.Accounts[0]
	return AuthStatus{Kind: account.Meta.ClaudeCode.Kind, Label: account.Label, Detail: account.Description}, true, nil
}

// SetAuth makes the session send its account notifications to auth. The agent sends the first notification at its
// start, before Start returns, so SetAuth also sends the last notification that came before it.
func (s *Session) SetAuth(auth func(AuthStatus)) {
	s.mu.Lock()
	s.auth = auth
	pending := s.pending
	s.pending = nil
	s.mu.Unlock()
	if pending != nil {
		auth(*pending)
	}
}

// receiveAuth sends an account notification to the current auth function, or keeps it for SetAuth.
func (s *Session) receiveAuth(status AuthStatus) {
	s.mu.Lock()
	auth := s.auth
	if auth == nil {
		s.pending = &status
	}
	s.mu.Unlock()
	if auth != nil {
		auth(status)
	}
}
