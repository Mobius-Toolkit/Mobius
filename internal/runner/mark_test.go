package runner

import "testing"

func TestWithMarkAddsOneMarkAfterABlankLine(t *testing.T) {
	got := WithMark("Text.\n\n", "— Reviewer of #5")

	if want := "Text.\n\n— Reviewer of #5"; got != want {
		t.Errorf("WithMark = %q, want %q", got, want)
	}
}

func TestWithMarkOnASecondCallAddsNoSecondMark(t *testing.T) {
	once := WithMark("Text.", "— Reviewer of #5")

	if got := WithMark(once, "— Reviewer of #5"); got != once {
		t.Errorf("WithMark = %q, want %q", got, once)
	}
}

func TestWithMarkReplacesTheMarkOfAnotherRole(t *testing.T) {
	body := "Text.\n\n— Lead of [#14](https://ui.example/workstreams/o/r/14) The title"

	got := WithMark(body, "— Implementer of #7")

	if want := "Text.\n\n— Implementer of #7"; got != want {
		t.Errorf("WithMark = %q, want %q", got, want)
	}
}

func TestWithMarkKeepsALineThatOnlyLooksLikeAMark(t *testing.T) {
	body := "— Leadership of the team\n— Lead"

	got := WithMark("— Leadership of the team", "— Curator")

	if want := "— Leadership of the team\n\n— Curator"; got != want {
		t.Errorf("WithMark = %q, want %q", got, want)
	}
	if got := WithoutMark(body); got != "— Leadership of the team" {
		t.Errorf("WithoutMark = %q", got)
	}
}

func TestWithoutMarkRemovesTheMarkAndTheWhiteSpaceAtTheEnd(t *testing.T) {
	got := WithoutMark("Text.\n\n— Curator\n\n")

	if got != "Text." {
		t.Errorf("WithoutMark = %q, want %q", got, "Text.")
	}
}
