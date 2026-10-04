package api

import (
	"context"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
)

// Agent is the session of an agent.
type Agent struct {
	// ID is the id of the session
	ID int64 `gork:"id"`
	// Role is the Role as the sessions table names it, for example lead_chat
	Role string `gork:"role"`
	// Name is the name of the Role, for example Lead
	Name string `gork:"name"`
	// Title tells what the session works on, for example "chat session". It can be empty
	Title string `gork:"title"`
	// Harness is the Harness of the session, for example claude-code
	Harness string `gork:"harness"`
	// Model is the model of the session
	Model string `gork:"model"`
	// Organization is the owner of the repository
	Organization string `gork:"organization"`
	// Repository is the repository as "owner/name". It is empty for the Triager chat
	Repository string `gork:"repository"`
	// Workstream is the number of the Workstream issue. It is 0 for the Triager
	Workstream int64 `gork:"workstream"`
	// Issue is the one issue that the session works on, or null
	Issue *int64 `gork:"issue"`
	// Parent is the id of the session of the agent that started this session, or null
	Parent *int64 `gork:"parent"`
	// StartedAt is the start of the session
	StartedAt time.Time `gork:"startedAt"`
	// EndedAt is the end of the session, or null while the session is live
	EndedAt *time.Time `gork:"endedAt"`
	// EndReason tells why the session ended, for example done or failed. It is empty while the session is live
	EndReason string `gork:"endReason"`
}

// TranscriptLine is a row of the Transcript of a session.
type TranscriptLine struct {
	// ID increases with each new row of all sessions
	ID int64 `gork:"id"`
	// Session is the id of the session
	Session int64 `gork:"session"`
	// Time is the time of the row
	Time time.Time `gork:"time"`
	// Kind is prompt, update, mcp_call or error
	Kind string `gork:"kind" validate:"oneof=prompt update mcp_call error"`
	// Text is the one line that the UI always shows
	Text string `gork:"text"`
	// HarnessToolName is the name of a Mobius tool in the Harness, for example mcp__mobius__list_tasks. It is empty for each other row
	HarnessToolName string `gork:"harnessToolName"`
	// Body is the text below Text. It can be empty
	Body string `gork:"body"`
	// Folded is true for the first prompt of the session, which starts with the Role prompt
	Folded bool `gork:"folded"`
	// Failed is true for an error row and for a Mobius tool call that failed
	Failed bool `gork:"failed"`
	// Raw is the JSON text of the row, for example the full ACP update
	Raw string `gork:"raw"`
}

// ListAgentsRequest is the request of ListAgents.
type ListAgentsRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
		// Number is the number of the Workstream issue
		Number int64 `gork:"number"`
	}
}

// ListAgentsResponse is the response of ListAgents.
type ListAgentsResponse struct {
	Body Envelope[[]Agent]
}

// ListAgents returns the sessions of a Workstream, the oldest first. Each session has the session that started it as its parent.
func (h *handlers) ListAgents(ctx context.Context, req ListAgentsRequest) (*ListAgentsResponse, error) {
	nodes, err := h.engine.Tree(ctx, req.Path.Owner+"/"+req.Path.Name, req.Path.Number)
	if err != nil {
		return nil, err
	}
	agents := make([]Agent, 0, len(nodes))
	for _, node := range nodes {
		agent, err := agentOf(node)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return &ListAgentsResponse{Body: Envelope[[]Agent]{Data: agents}}, nil
}

// GetTranscriptRequest is the request of GetTranscript.
type GetTranscriptRequest struct {
	Path struct {
		// ID is the id of the session
		ID int64 `gork:"id"`
	}
}

// GetTranscriptResponse is the response of GetTranscript.
type GetTranscriptResponse struct {
	Body Envelope[[]TranscriptLine]
}

// GetTranscript returns the Transcript of a session, the oldest row first.
func (h *handlers) GetTranscript(ctx context.Context, req GetTranscriptRequest) (*GetTranscriptResponse, error) {
	lines, err := h.engine.Transcript(ctx, req.Path.ID)
	if err != nil {
		return nil, err
	}
	transcript := make([]TranscriptLine, 0, len(lines))
	for _, line := range lines {
		transcript = append(transcript, transcriptLineOf(line))
	}
	return &GetTranscriptResponse{Body: Envelope[[]TranscriptLine]{Data: transcript}}, nil
}

func agentOf(node engine.Node) (Agent, error) {
	session := node.Session
	agent := Agent{
		ID:           session.ID,
		Role:         session.Role,
		Name:         node.Name,
		Title:        node.Title,
		Harness:      session.Harness,
		Model:        session.Model,
		Organization: session.Organization,
		Repository:   session.Repository,
		Workstream:   session.Workstream,
		EndReason:    session.EndReason.String,
	}
	if session.Issue.Valid {
		agent.Issue = &session.Issue.Int64
	}
	if session.Parent.Valid {
		agent.Parent = &session.Parent.Int64
	}
	var err error
	if agent.StartedAt, err = time.Parse(time.RFC3339Nano, session.StartedAt); err != nil {
		return Agent{}, err
	}
	if session.EndedAt.Valid {
		endedAt, err := time.Parse(time.RFC3339Nano, session.EndedAt.String)
		if err != nil {
			return Agent{}, err
		}
		agent.EndedAt = &endedAt
	}
	return agent, nil
}

func transcriptLineOf(line engine.Line) TranscriptLine {
	return TranscriptLine{
		ID:              line.ID,
		Session:         line.Session,
		Time:            line.Time,
		Kind:            line.Kind,
		Text:            line.Text,
		HarnessToolName: line.HarnessToolName,
		Body:            line.Body,
		Folded:          line.Folded,
		Failed:          line.Error,
		Raw:             line.Raw,
	}
}
