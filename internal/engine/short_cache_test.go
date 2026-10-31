package engine_test

import (
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestAReviewerSessionGetsTheFiveMinutePromptCacheAndAnImplementerSessionDoesNot(t *testing.T) {
	// Serial: it changes the environment.
	t.Setenv("FORCE_PROMPT_CACHING_5M", "inherited")
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nwhen = \"Read the work\"\nshell = \"printenv FORCE_PROMPT_CACHING_5M\"\n")

	for _, c := range []struct {
		name string
		role string
		want string
	}{
		{"Reviewer", engine.ReviewerRole, "1\nexit 0"},
		{"Implementer", engine.ImplementerRole, "exit 1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			session := run(t, server, roleSpec(t, c.role), "Read the work")
			if got := reply(t, server, session); got != c.want {
				t.Errorf("reply = %q, want %q", got, c.want)
			}
		})
	}
}
