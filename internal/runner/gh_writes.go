package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
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

var itemURL = regexp.MustCompile(`^https?://[^/]+/([^/]+/[^/]+)/(?:issues|pull)/(\d+)`)

type ghArgs struct {
	positional []string
	flags      map[string][]string
}

func parseGHArgs(args []string) ghArgs {
	parsed := ghArgs{flags: map[string][]string{}}
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") || args[i] == "-" {
			parsed.positional = append(parsed.positional, args[i])
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

// labels gives the labels of the repeatable, comma separated flags name.
func (a ghArgs) labels(name string) []string {
	var labels []string
	for _, value := range a.flags[name] {
		for label := range strings.SplitSeq(value, ",") {
			if label = strings.TrimSpace(label); label != "" {
				labels = append(labels, label)
			}
		}
	}
	return labels
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

// targets gives the repository and the number of each issue or pull request of the positional arguments. An argument
// with no repository is not a target.
func (a ghArgs) targets() []mcp.GHWrite {
	repository := a.repository()
	var targets []mcp.GHWrite
	for _, arg := range a.positional {
		target := mcp.GHWrite{Repository: repository}
		if match := itemURL.FindStringSubmatch(arg); match != nil {
			if target.Repository == "" {
				target.Repository = match[1]
			}
			arg = match[2]
		}
		number, err := strconv.ParseInt(strings.TrimPrefix(arg, "#"), 10, 64)
		if err != nil || target.Repository == "" {
			continue
		}
		target.Issue = number
		targets = append(targets, target)
	}
	return targets
}

// ghWrites gives the writes of the gh call with args, and the stdin for the real gh. It reads stdin one time, only
// when a body comes from it.
func ghWrites(args []string, stdin io.Reader) ([]mcp.GHWrite, io.Reader, error) {
	if len(args) < 2 || args[0] != "issue" && args[0] != "pr" {
		return nil, stdin, nil
	}
	parsed := parseGHArgs(args[2:])
	var writes []mcp.GHWrite
	switch {
	case args[1] == "edit":
		for _, target := range parsed.targets() {
			for _, label := range parsed.labels("add-label") {
				writes = append(writes, withKind(target, "labeled", []string{label}, ""))
			}
			for _, label := range parsed.labels("remove-label") {
				writes = append(writes, withKind(target, "unlabeled", []string{label}, ""))
			}
		}
	case args[0] == "issue" && args[1] == "create":
		title, titled := parsed.flag("title")
		if repository := parsed.repository(); repository != "" && titled {
			writes = append(writes, withKind(mcp.GHWrite{Repository: repository}, "created", parsed.labels("label"), title))
		}
	case args[1] == "comment" || args[0] == "issue" && args[1] == "close":
		var text string
		var found bool
		var err error
		if args[1] == "close" {
			text, found = parsed.flag("comment")
		} else if text, found = parsed.flag("body"); !found {
			if file, ok := parsed.flag("body-file"); ok {
				text, stdin, err = readBody(file, stdin)
				found = err == nil
			}
		}
		if err != nil {
			return nil, stdin, err
		}
		if found {
			for _, target := range parsed.targets() {
				writes = append(writes, withKind(target, "commented", nil, text))
			}
		}
	}
	return writes, stdin, nil
}

func withKind(target mcp.GHWrite, kind string, labels []string, text string) mcp.GHWrite {
	target.Kind, target.Labels, target.Text = kind, labels, text
	return target
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

// sendGHWrites sends writes to url. The body of a response with an error status is the error.
func sendGHWrites(url string, writes []mcp.GHWrite) error {
	body, err := json.Marshal(map[string][]mcp.GHWrite{"writes": writes})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		text, _ := io.ReadAll(response.Body)
		return errors.New(strings.TrimSpace(string(text)))
	}
	return nil
}
