package runner

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// valueFlags are the flags of gh issue and gh pr that take a value. The other flags take none.
var valueFlags = map[string]string{
	"add-label":       "",
	"remove-label":    "",
	"title":           "t",
	"label":           "l",
	"body":            "b",
	"body-file":       "F",
	"repo":            "R",
	"comment":         "c",
	"reason":          "r",
	"assignee":        "a",
	"milestone":       "m",
	"project":         "p",
	"template":        "T",
	"add-assignee":    "",
	"remove-assignee": "",
	"add-project":     "",
	"remove-project":  "",
	"add-reviewer":    "",
	"remove-reviewer": "",
	"base":            "B",
	"reviewer":        "",
}

type ghArgs struct {
	flags map[string][]string
}

func parseGHArgs(args []string) ghArgs {
	parsed := ghArgs{flags: map[string][]string{}}
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") || args[i] == "-" {
			continue
		}
		name, value, inline := valueFlag(args[i])
		if name == "" {
			continue
		}
		if !inline {
			if i++; i == len(args) {
				break
			}
			value = args[i]
		}
		parsed.flags[name] = append(parsed.flags[name], value)
	}
	return parsed
}

// valueFlag gives the name of the flag that arg sets, if the flag takes a value, and the value when arg has it. The
// forms are --name=value, -n=value and -nvalue.
func valueFlag(arg string) (name, value string, inline bool) {
	if long, ok := strings.CutPrefix(arg, "--"); ok {
		name, value, inline = strings.Cut(long, "=")
		if _, known := valueFlags[name]; !known {
			return "", "", false
		}
		return name, value, inline
	}
	for long, short := range valueFlags {
		if short != "" && arg[1:2] == short {
			return long, strings.TrimPrefix(arg[2:], "="), len(arg) > 2
		}
	}
	return "", "", false
}

func (a ghArgs) flag(name string) (string, bool) {
	values := a.flags[name]
	if len(values) == 0 {
		return "", false
	}
	return values[len(values)-1], true
}

// repository gives the owner/name of the --repo flag, or "" with no flag. The flag can also have a host.
func (a ghArgs) repository() string {
	repository, _ := a.flag("repo")
	fields := strings.Split(repository, "/")
	return strings.Join(fields[max(len(fields)-2, 0):], "/")
}

// markBodies gives args with the mark line at the end of each body that the gh call writes, and the stdin for the
// real gh. A body file becomes "-", and the real gh reads the marked text from the stdin.
func markBodies(args []string, stdin io.Reader, ghMark GHMark) ([]string, io.Reader, error) {
	if len(args) < 2 {
		return args, stdin, nil
	}
	var bodyFlag, fileFlag string
	switch args[0] + " " + args[1] {
	case "issue create", "issue comment", "pr comment", "issue edit":
		bodyFlag, fileFlag = "body", "body-file"
	case "issue close":
		bodyFlag = "comment"
	default:
		return args, stdin, nil
	}
	mark := ghMark.Here
	parsed := parseGHArgs(args[2:])
	if repository := parsed.repository(); repository != "" && !strings.EqualFold(repository, ghMark.Repository) {
		mark = ghMark.Elsewhere
	}
	marked := slices.Clone(args)
	for i := 2; i < len(marked); i++ {
		if !strings.HasPrefix(marked[i], "-") || marked[i] == "-" {
			continue
		}
		name, value, inline := valueFlag(marked[i])
		if name == "" {
			continue
		}
		prefix := ""
		if inline {
			prefix = marked[i][:len(marked[i])-len(value)]
		} else if i++; i == len(marked) {
			break
		} else {
			value = marked[i]
		}
		switch name {
		case bodyFlag:
			marked[i] = prefix + WithMark(value, mark)
		case fileFlag:
			file, _ := parsed.flag(fileFlag)
			text, _, err := readBody(file, stdin)
			if err != nil {
				return nil, nil, err
			}
			marked[i], stdin = prefix+"-", strings.NewReader(WithMark(text, mark))
		}
	}
	return marked, stdin, nil
}

// readBody gives the text of the body file, and the stdin for the real gh. For "-", it reads stdin and gives a reader
// with the same text.
func readBody(file string, stdin io.Reader) (string, io.Reader, error) {
	if file != "-" {
		text, err := os.ReadFile(filepath.Clean(file))
		return string(text), stdin, err
	}
	text, err := io.ReadAll(stdin)
	return string(text), bytes.NewReader(text), err
}
