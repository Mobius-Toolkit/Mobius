package engine_test

import (
	"encoding/json"
	"strconv"
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
