package runner

import (
	"io"
	"slices"
	"strings"
	"testing"
)

func TestTheWireTellsWhichUpdatesFollowTheResponseOfThePrompt(t *testing.T) {
	const update = `{"jsonrpc":"2.0","method":"session/update","params":{}}`
	const response = `{"jsonrpc":"2.0","id":3,"result":{}}`
	w := &wire{src: strings.NewReader(update + "\n" + update + "\n" + response + "\n" + update + "\n" + update + "\n")}

	if _, err := io.ReadAll(w); err != nil {
		t.Fatal(err)
	}
	var got []bool
	for n := 1; n <= 4; n++ {
		got = append(got, w.after(n))
	}

	if want := []bool{false, false, true, true}; !slices.Equal(got, want) {
		t.Errorf("after = %v, want %v", got, want)
	}
	w.beginPrompt()
	if w.after(4) {
		t.Error("beginPrompt keeps the response")
	}
}

func TestTheWireReadsALineThatArrivesInTwoParts(t *testing.T) {
	w := &wire{src: io.MultiReader(strings.NewReader(`{"method":"session/up`), strings.NewReader("date\"}\n{\"id\":1}\n{\"method\":\"session/update\"}\n"))}

	if _, err := io.ReadAll(w); err != nil {
		t.Fatal(err)
	}

	if w.after(1) || !w.after(2) {
		t.Errorf("after(1) = %v, after(2) = %v", w.after(1), w.after(2))
	}
}
