package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const (
	shortLines = 3
	shortChars = 300
	// transcriptPageSize is the number of lines in a page of a Transcript.
	transcriptPageSize = 100
	// mobiusPrefix starts the name of a Mobius tool in a Harness.
	mobiusPrefix = "mcp__mobius__"
)

// Line is a Transcript row as the UI shows it.
type Line struct {
	ID      int64
	Session int64
	Time    time.Time
	// Kind is prompt, update, mcp_call, error, or check for a phase of the local check of an Implementer or a hang of the agent.
	Kind string
	// Text is the one line that the UI always shows.
	Text string
	// HarnessToolName is the name of a Mobius tool in the Harness, or "" for each other row.
	HarnessToolName string
	// Body is the text below Text, or "".
	Body string
	// Folded is true for the first prompt of the session, which starts with the Role prompt.
	Folded bool
	Error  bool
	// Raw is the JSON text of the row.
	Raw string
}

// TranscriptPage is a page of the Transcript of a session.
type TranscriptPage struct {
	// Lines are the lines of the page, the oldest first.
	Lines []Line
	// Older is true when the session has rows before the first line of the page.
	Older bool
}

// Transcript gives the page of the Transcript of the session with the rows before the row id before, or with the newest
// rows when before is 0.
func (e *Engine) Transcript(ctx context.Context, session, before int64) (TranscriptPage, error) {
	if before == 0 {
		before = math.MaxInt64
	}
	// One more row than the page tells if older rows exist.
	rows, err := e.queries.ListTranscriptPage(ctx, store.ListTranscriptPageParams{Session: session, ID: before, Limit: transcriptPageSize + 1})
	if err != nil {
		return TranscriptPage{}, err
	}
	page := TranscriptPage{Older: len(rows) > transcriptPageSize}
	if page.Older {
		rows = rows[:transcriptPageSize]
	}
	slices.Reverse(rows)
	firstPrompt, err := e.queries.GetFirstTranscriptPrompt(ctx, session)
	if err != nil {
		return TranscriptPage{}, err
	}
	page.Lines = make([]Line, 0, len(rows))
	for _, row := range rows {
		line, err := line(row, row.ID == firstPrompt)
		if err != nil {
			return TranscriptPage{}, err
		}
		page.Lines = append(page.Lines, line)
	}
	return page, nil
}

func line(row store.Transcript, folded bool) (Line, error) {
	value, err := decodeObject([]byte(row.Json))
	if err != nil {
		return Line{}, err
	}
	t, err := time.Parse(time.RFC3339Nano, row.Time)
	if err != nil {
		return Line{}, err
	}
	raw := row.Json
	if dropImageData(value) {
		if raw, err = compact(value); err != nil {
			return Line{}, err
		}
	}
	line := Line{ID: row.ID, Session: row.Session, Time: t, Kind: row.Kind, Folded: folded, Raw: raw}
	switch row.Kind {
	case "prompt":
		line.firstLine(stringField(value, "text"))
	case "mcp_call":
		line.Text = "mobius · " + stringField(value, "tool")
		outcome, failed := value["error"]
		if !failed {
			outcome = value["result"]
		}
		arguments, err := compact(value["arguments"])
		if err != nil {
			return Line{}, err
		}
		outcomeText, _ := outcome.(string)
		line.Body = short(arguments+" → "+outcomeText, shortLines)
		line.Error = failed
	case "error":
		line.firstLine(stringField(value, "message"))
		line.Error = true
	case "check":
		line.firstLine(stringField(value, "text"))
	default:
		line.update(field(value, "update"))
	}
	return line, nil
}

// dropImageData replaces the data of each image block in the content of an update with its size. It tells if it
// replaced any.
func dropImageData(value map[string]any) bool {
	blocks, _ := field(value, "update", "content").([]any)
	dropped := false
	for _, block := range blocks {
		image, _ := field(block, "content").(map[string]any)
		data, ok := image["data"].(string)
		if ok && image["type"] == "image" {
			image["data"] = "<" + strconv.Itoa(len(data)) + " bytes>"
			dropped = true
		}
	}
	return dropped
}

func (l *Line) update(update any) {
	kind := stringField(update, "sessionUpdate")
	switch kind {
	case "agent_message_chunk":
		l.chunk("message", update)
	case "agent_thought_chunk":
		l.chunk("thought", update)
	case "tool_call", "tool_call_update":
		label := strings.ReplaceAll(kind, "_", " ")
		title, hasTitle := field(update, "title").(string)
		tool, name, mobius := mobiusTool(update)
		switch {
		case mobius:
			l.Text = "mobius · " + tool
			l.HarnessToolName = name
		case hasTitle:
			l.Text = label + " · " + short(title, 1)
		default:
			l.Text = label
		}
		if output := toolOutput(update); output != "" {
			l.Body = short(output, shortLines)
		}
	default:
		l.Text = kind
	}
}

// mobiusTool gives the name of the Mobius tool of a tool call update and the name of the tool in the Harness.
// Each Harness names a Mobius tool in other fields.
func mobiusTool(update any) (string, string, bool) {
	meta := field(update, "_meta")
	if stringField(meta, "mcp", "server") == "mobius" {
		tool, ok := field(meta, "mcp", "tool").(string)
		if !ok {
			return "", "", false
		}
		name, ok := field(update, "title").(string)
		if !ok {
			name = tool
		}
		return tool, name, true
	}
	for _, candidate := range []any{
		field(update, "title"),
		field(update, "name"),
		field(meta, "claudeCode", "toolName"),
		field(meta, "cognition.ai/toolName"),
		field(meta, "cognition.ai/inferenceToolName"),
	} {
		name, _ := candidate.(string)
		if tool, ok := strings.CutPrefix(name, mobiusPrefix); ok {
			return tool, name, true
		}
	}
	return "", "", false
}

func (l *Line) firstLine(text string) {
	l.Text = short(text, 1)
	if l.Text != text {
		l.Body = text
	}
}

func (l *Line) chunk(text string, update any) {
	l.Text = text
	if content, ok := field(update, "content", "text").(string); ok {
		l.Body = short(content, shortLines)
	}
}

// toolOutput gives the texts of the content blocks of a tool call update, one on each line.
func toolOutput(update any) string {
	blocks, _ := field(update, "content").([]any)
	var texts []string
	for _, block := range blocks {
		if text, ok := field(block, "content", "text").(string); ok {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

// short gives the first lines of text, with no more than shortChars characters. A cut text ends with "…".
func short(text string, lines int) string {
	text = strings.TrimRightFunc(text, unicode.IsSpace)
	split := strings.Split(text, "\n")
	result := strings.Join(split[:min(lines, len(split))], "\n")
	if runes := []rune(result); len(runes) > shortChars {
		result = string(runes[:shortChars])
	}
	if result != text {
		result += "…"
	}
	return result
}

// compact gives the JSON text of value with no spaces and with no escapes of the HTML characters.
func compact(value any) (string, error) {
	var text bytes.Buffer
	encoder := json.NewEncoder(&text)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(text.String(), "\n"), nil
}

// decodeObject decodes a JSON object. Each number keeps its text, so compact writes the same number again.
func decodeObject(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var object map[string]any
	return object, decoder.Decode(&object)
}

// field gives the value at path in a decoded JSON value, or nil.
func field(value any, path ...string) any {
	for _, name := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[name]
	}
	return value
}

// stringField gives the string at path in a decoded JSON value, or "".
func stringField(value any, path ...string) string {
	text, _ := field(value, path...).(string)
	return text
}
