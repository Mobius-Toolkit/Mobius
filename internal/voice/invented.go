package voice

import "strings"

// inventedLines are the lines that whisper gives for silence or at the end of a recording, in lower case.
var inventedLines = []string{
	"thank you.", "thank you!", "thank you", "thanks for watching!", "thanks for watching.",
	"thank you for watching.", "thank you for watching!", "[blank_audio]",
}

// inventedPrefixes are the starts of the invented lines of whisper that have a name or a text after them, in lower case.
var inventedPrefixes = []string{"titulky vytvořil", "subtitles by"}

func invented(line string) bool {
	line = strings.ToLower(strings.TrimSpace(line))
	for _, text := range inventedLines {
		if line == text {
			return true
		}
	}
	for _, prefix := range inventedPrefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// withoutInventedLines removes the last non-empty line of text while it is an invented line.
func withoutInventedLines(text string) string {
	lines := strings.Split(text, "\n")
	for len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if last != "" && !invented(last) {
			break
		}
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// withoutInventedSegments removes the last segment with a text while it is an invented line, and the segments with no
// text after it.
func withoutInventedSegments(segments []Segment) []Segment {
	for len(segments) > 0 {
		last := strings.TrimSpace(segments[len(segments)-1].Text)
		if last != "" && !invented(last) {
			break
		}
		segments = segments[:len(segments)-1]
	}
	return segments
}
