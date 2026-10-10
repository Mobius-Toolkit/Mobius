package runner

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"
)

const (
	updateLine   = `{"jsonrpc":"2.0","method":"session/update","params":{}}` + "\n"
	promptLine   = `{"jsonrpc":"2.0","id":%d,"method":"session/prompt","params":{}}`
	responseLine = `{"jsonrpc":"2.0","id":%d,"result":{}}` + "\n"
)

func read(t *testing.T, w *wire, lines string) {
	t.Helper()
	w.src = strings.NewReader(lines)
	if _, err := io.ReadAll(w); err != nil {
		t.Fatal(err)
	}
}

func send(t *testing.T, w *wire, line string) {
	t.Helper()
	if _, err := w.stdin(io.Discard).Write([]byte(line)); err != nil {
		t.Fatal(err)
	}
}

func lates(w *wire, count int) []bool {
	var got []bool
	for n := 1; n <= count; n++ {
		got = append(got, w.after(n))
	}
	return got
}

func TestTheWireTellsWhichUpdatesFollowTheResponseOfThePrompt(t *testing.T) {
	w := &wire{}
	send(t, w, strings.Replace(promptLine, "%d", "3", 1))

	read(t, w, updateLine+updateLine+strings.Replace(responseLine, "%d", "3", 1)+updateLine+updateLine)

	if want := []bool{false, false, true, true}; !slices.Equal(lates(w, 4), want) {
		t.Errorf("after = %v, want %v", lates(w, 4), want)
	}
}

func TestTheWireReadsALineThatArrivesInTwoParts(t *testing.T) {
	w := &wire{promptID: "1"}
	w.src = io.MultiReader(strings.NewReader(`{"method":"session/up`), strings.NewReader("date\"}\n{\"id\":1}\n{\"method\":\"session/update\"}\n"))
	if _, err := io.ReadAll(w); err != nil {
		t.Fatal(err)
	}

	if w.after(1) || !w.after(2) {
		t.Errorf("after(1) = %v, after(2) = %v", w.after(1), w.after(2))
	}
}

func TestTheWireIgnoresTheResponseOfAnEarlierPrompt(t *testing.T) {
	w := &wire{}
	send(t, w, strings.Replace(promptLine, "%d", "3", 1))
	send(t, w, strings.Replace(promptLine, "%d", "4", 1))

	read(t, w, strings.Replace(responseLine, "%d", "3", 1)+updateLine+updateLine)

	if want := []bool{false, false}; !slices.Equal(lates(w, 2), want) {
		t.Errorf("after = %v, want %v", lates(w, 2), want)
	}
}

func TestTheWireKeepsTheLateUpdatesOfAPromptAfterTheNextPromptStarts(t *testing.T) {
	w := &wire{}
	send(t, w, strings.Replace(promptLine, "%d", "3", 1))
	read(t, w, updateLine+strings.Replace(responseLine, "%d", "3", 1)+updateLine)
	send(t, w, strings.Replace(promptLine, "%d", "4", 1))
	w.src = bytes.NewReader([]byte(updateLine))
	if _, err := io.ReadAll(w); err != nil {
		t.Fatal(err)
	}

	if want := []bool{false, true, false}; !slices.Equal(lates(w, 3), want) {
		t.Errorf("after = %v, want %v", lates(w, 3), want)
	}
}
