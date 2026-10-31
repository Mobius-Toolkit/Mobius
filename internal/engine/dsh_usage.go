package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/klauspost/compress/zstd"
)

// dshSessionVersion is the version of the dsh session log that Mobius reads. dsh refuses another version.
const dshSessionVersion = 4

// dshUsage is the usage that the newer model calls of a dsh session log added.
type dshUsage struct {
	// records is the number of assistant/message records of the log, also the records before the turn.
	records    int
	input      int64
	output     int64
	cacheRead  int64
	cacheWrite int64
	// model is the route/model of the last assistant/message record, or "".
	model string
	// tokens tells that at least one counted record has a data.usage field.
	tokens bool
}

// readDSHUsage reads the assistant/message records of the dsh session log of the ACP session after the first counted
// records. It gives the sums of the tokens of these records and the route/model of the last one. A log that Mobius
// cannot read gives an error and no usage.
func readDSHUsage(home, cwd, session string, counted int) (dshUsage, error) {
	var usage dshUsage
	root, err := os.OpenRoot(home)
	if err != nil {
		return usage, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(filepath.Join("sessions", dshProjectKey(cwd), dshEncodeSegment(session), "session.v4.jsonl.zstd"))
	if err != nil {
		return usage, err
	}
	defer func() { _ = file.Close() }()
	decoder, err := zstd.NewReader(file)
	if err != nil {
		return usage, err
	}
	defer decoder.Close()
	scanner := bufio.NewScanner(decoder)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	header := true
	for scanner.Scan() {
		line := scanner.Bytes()
		if header {
			header = false
			var value struct {
				Version int `json:"version"`
			}
			if err := json.Unmarshal(line, &value); err != nil {
				return usage, err
			}
			if value.Version != dshSessionVersion {
				return usage, fmt.Errorf("dsh session log version %d, want %d", value.Version, dshSessionVersion)
			}
			continue
		}
		var record struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			return usage, err
		}
		if record.Type != "assistant/message" {
			continue
		}
		if usage.records >= counted {
			var data struct {
				Message struct {
					Source struct {
						Provider string `json:"provider"`
						Model    string `json:"model"`
					} `json:"source"`
				} `json:"message"`
				Usage *struct {
					Input      int64 `json:"inputTokens"`
					Output     int64 `json:"outputTokens"`
					CacheRead  int64 `json:"cacheReadTokens"`
					CacheWrite int64 `json:"cacheWriteTokens"`
				} `json:"usage"`
			}
			if len(record.Data) > 0 {
				if err := json.Unmarshal(record.Data, &data); err != nil {
					return usage, err
				}
			}
			if data.Message.Source.Provider != "" && data.Message.Source.Model != "" {
				usage.model = data.Message.Source.Provider + "/" + data.Message.Source.Model
			}
			if data.Usage != nil {
				usage.input += data.Usage.Input
				usage.output += data.Usage.Output
				usage.cacheRead += data.Usage.CacheRead
				usage.cacheWrite += data.Usage.CacheWrite
				usage.tokens = true
			}
		}
		usage.records++
	}
	if err := scanner.Err(); err != nil {
		return usage, err
	}
	if header {
		return usage, fmt.Errorf("the dsh session log of %q has no header", cwd)
	}
	return usage, nil
}

// dshLogPath gives the path of the dsh session log of the ACP session in cwd.
func dshLogPath(home, cwd, session string) string {
	return filepath.Join(home, "sessions", dshProjectKey(cwd), dshEncodeSegment(session), "session.v4.jsonl.zstd")
}

// dshHome gives the home of dsh: DSH_HOME when it is not empty, else ~/.dsh. It reads DSH_HOME as dsh reads it.
func dshHome() string {
	home := strings.TrimSpace(os.Getenv("DSH_HOME"))
	userHome, err := os.UserHomeDir()
	if err != nil {
		userHome = ""
	}
	switch {
	case home == "":
		if userHome == "" {
			return ""
		}
		return filepath.Join(userHome, ".dsh")
	case home == "~":
		return userHome
	case strings.HasPrefix(home, "~/") || strings.HasPrefix(home, `~\`):
		if userHome == "" {
			return home
		}
		return filepath.Join(userHome, home[2:])
	}
	return home
}

// dshProjectKey gives the name of the dsh session directory of the project at cwd. It is projectKey of the dsh
// package dsh-session-persistence-jsonl.
func dshProjectKey(cwd string) string {
	var key strings.Builder
	separator := false
	for _, unit := range utf16.Encode([]rune(cwd)) {
		ch := rune(unit)
		switch {
		case ch == '/' || ch == '\\' || ch == ':':
			if !separator {
				key.WriteByte('-')
			}
			separator = true
		case ch != '~' && dshPathChar(ch):
			key.WriteRune(ch)
			separator = false
		default:
			fmt.Fprintf(&key, "~%04X", unit)
			separator = false
		}
	}
	name := strings.TrimLeft(key.String(), "-")
	if name == "" {
		name = "root"
	}
	if len(name) > 251 {
		name = name[:251]
	}
	return "--" + name + "--"
}

// dshEncodeSegment gives the dsh path segment of an ACP session id. It is encodeSegment of the dsh package
// dsh-session-persistence-jsonl.
func dshEncodeSegment(session string) string {
	if session == "." {
		return "~002E"
	}
	if session == ".." {
		return "~002E~002E"
	}
	var encoded strings.Builder
	for _, unit := range utf16.Encode([]rune(session)) {
		ch := rune(unit)
		if ch != '~' && dshPathChar(ch) {
			encoded.WriteRune(ch)
		} else {
			fmt.Fprintf(&encoded, "~%04X", unit)
		}
	}
	return encoded.String()
}

// dshPathChar tells if dsh keeps ch in a path segment.
func dshPathChar(ch rune) bool {
	return ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == '-'
}
