package runner

import (
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
