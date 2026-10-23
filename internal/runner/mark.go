package runner

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
)

// markLine matches a mark line: the em dash, the name of a Role of Mobius, and the optional reference.
var markLine = regexp.MustCompile(`^— (?:Lead|Coordinator|Triager|Implementer|Researcher|Reviewer|Judge|Curator|Diagnoser)(?: of .*)?$`)

// WithoutMark gives body with no mark line and no white space at the end.
func WithoutMark(body string) string {
	lines := slices.DeleteFunc(strings.Split(body, "\n"), func(line string) bool {
		return markLine.MatchString(strings.TrimSpace(line))
	})
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\r\n")
}

// WithMark gives body with the mark line mark at the end, one time, after a blank line. It removes each old mark line
// first.
func WithMark(body, mark string) string {
	return WithoutMark(body) + "\n\n" + mark
}

// GHMark is the mark of the gh of an agent. Here is the mark line for a text in Repository, and Elsewhere is the mark
// line for a text in any other repository.
type GHMark struct {
	Repository string `json:"repository"`
	Here       string `json:"here"`
	Elsewhere  string `json:"elsewhere"`
}

// Encode gives the value of the environment variable of the gh.
func (m GHMark) Encode() (string, error) {
	text, err := json.Marshal(m)
	return string(text), err
}
