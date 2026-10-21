package engine_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type transcriptPage struct {
	Data struct {
		Lines []struct {
			ID     int64  `json:"id"`
			Kind   string `json:"kind"`
			Folded bool   `json:"folded"`
			Raw    string `json:"raw"`
		} `json:"lines"`
		Older bool `json:"older"`
	} `json:"data"`
}

func getTranscriptPage(t *testing.T, server *testserver.Server, session, before int64) transcriptPage {
	t.Helper()
	response, err := server.Client.Get(server.URL + "/api/agents/" + strconv.FormatInt(session, 10) + "/transcript?before=" + strconv.FormatInt(before, 10))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var page transcriptPage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	return page
}

func longSession(t *testing.T, server *testserver.Server) int64 {
	t.Helper()
	session, _ := leadReply(t, server)
	for i := len(transcript(t, server, session)); i < 250; i++ {
		addTranscript(t, server, session, time.Now(), "check", map[string]string{"text": "row " + strconv.Itoa(i)})
	}
	return session
}

func TestTheTranscriptGivesPagesOf100LinesFromTheNewestAndTellsIfOlderRowsExist(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Hello\"]\n")
	session := longSession(t, server)

	newest := getTranscriptPage(t, server, session, 0)
	middle := getTranscriptPage(t, server, session, newest.Data.Lines[0].ID)
	oldest := getTranscriptPage(t, server, session, middle.Data.Lines[0].ID)

	if len(newest.Data.Lines) != 100 || len(middle.Data.Lines) != 100 || len(oldest.Data.Lines) != 50 {
		t.Fatalf("lines = %d, %d, %d", len(newest.Data.Lines), len(middle.Data.Lines), len(oldest.Data.Lines))
	}
	if !newest.Data.Older || !middle.Data.Older || oldest.Data.Older {
		t.Errorf("older = %v, %v, %v", newest.Data.Older, middle.Data.Older, oldest.Data.Older)
	}
	var ids []int64
	for _, page := range []transcriptPage{oldest, middle, newest} {
		for _, line := range page.Data.Lines {
			ids = append(ids, line.ID)
		}
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("ids are not in order at %d: %v", i, ids)
		}
	}
	if len(ids) != 250 {
		t.Errorf("rows = %d", len(ids))
	}
}

func TestTheTranscriptFoldsTheFirstPromptOnlyOnTheOlderPageThatHasIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Hello\"]\n")
	session := longSession(t, server)

	newest := getTranscriptPage(t, server, session, 0)
	middle := getTranscriptPage(t, server, session, newest.Data.Lines[0].ID)
	oldest := getTranscriptPage(t, server, session, middle.Data.Lines[0].ID)

	for _, page := range []transcriptPage{newest, middle} {
		for _, line := range page.Data.Lines {
			if line.Folded {
				t.Errorf("folded line %+v on a page with no first prompt", line)
			}
		}
	}
	var folded []string
	for _, line := range oldest.Data.Lines {
		if line.Folded {
			folded = append(folded, line.Kind)
		}
	}
	if len(folded) != 1 || folded[0] != "prompt" {
		t.Errorf("folded lines = %v", folded)
	}
}

const imageScript = `
[[prompts]]
updates = [
  '{"sessionUpdate":"tool_call_update","toolCallId":"1","content":[{"type":"content","content":{"type":"image","mimeType":"image/png","data":"AAAABBBBCCCC"}}]}',
  '{"sessionUpdate":"tool_call_update","toolCallId":"2","content":[{"type":"content","content":{"type":"text","text":"done"}}]}',
]
reply = ["Hello"]
`

func TestTheTranscriptLinesOfAPageHaveNoImageDataInRaw(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, imageScript)
	session := run(t, server, leadSpec(t), "Read the screenshot")

	var image, text int
	for _, line := range getTranscriptPage(t, server, session, 0).Data.Lines {
		switch {
		case strings.Contains(line.Raw, `"toolCallId":"1"`):
			image++
			if strings.Contains(line.Raw, "AAAABBBBCCCC") || !strings.Contains(line.Raw, `"data":"<12 bytes>"`) || !strings.Contains(line.Raw, `"mimeType":"image/png"`) {
				t.Errorf("raw of the image row = %s", line.Raw)
			}
		case strings.Contains(line.Raw, `"toolCallId":"2"`):
			text++
			var stored string
			if err := server.DB.QueryRow("SELECT json FROM transcript WHERE id = ?", line.ID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if line.Raw != stored {
				t.Errorf("raw of the text row = %s, stored = %s", line.Raw, stored)
			}
		}
	}
	if image != 1 || text != 1 {
		t.Errorf("image rows = %d, text rows = %d", image, text)
	}
}

func TestTheTranscriptStreamLinesHaveNoImageDataInRaw(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, imageScript)
	agent := start(t, server, leadSpec(t))
	listeners := server.Engine.Listeners()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/agents/"+strconv.FormatInt(agent.ID(), 10)+"/transcript/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	events := bufio.NewReader(response.Body)
	testkit.WaitFor(t, func() bool { return server.Engine.Listeners() > listeners })

	if err := agent.Prompt(t.Context(), "Read the screenshot", nil); err != nil {
		t.Fatal(err)
	}

	for {
		name, data := readEvent(t, events)
		if name != "transcript" {
			t.Fatalf("event = %s %s", name, data)
		}
		var line lineEvent
		if err := json.Unmarshal([]byte(data), &line); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(line.Raw, `"toolCallId":"1"`) {
			continue
		}
		if strings.Contains(line.Raw, "AAAABBBBCCCC") || !strings.Contains(line.Raw, `"data":"<12 bytes>"`) {
			t.Errorf("raw of the image row = %s", line.Raw)
		}
		return
	}
}
