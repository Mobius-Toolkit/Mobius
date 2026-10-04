package engine_test

import (
	"testing"

	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
)

func TestTrustedAuthor(t *testing.T) {
	e := engine.New(nil, nil, []string{"owner"}, []string{"coderabbitai[bot]"}, engine.Agents{})
	cases := []struct {
		login string
		want  bool
	}{
		{"owner", true},
		{"Owner", true},
		{"coderabbitai[bot]", true},
		{"CodeRabbitAI[bot]", true},
		{"mobius-app[bot]", true},
		{"Mobius-App[bot]", true},
		{"mallory", false},
		{"mobius-app", false},
		{"coderabbitai", false},
		{"owner[bot]", false},
	}
	for _, c := range cases {
		if got := e.TrustedAuthor("mobius-app", c.login); got != c.want {
			t.Errorf("TrustedAuthor(%q) = %v, want %v", c.login, got, c.want)
		}
	}
}
