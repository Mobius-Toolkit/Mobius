package engine

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

type antigravityEntry struct {
	idx  int64
	data []byte
}

func makeAntigravityData(input, output, cacheRead int64, model string) []byte {
	var b4 []byte
	if input >= 0 {
		b4 = protowire.AppendTag(b4, 2, protowire.VarintType)
		b4 = protowire.AppendVarint(b4, uint64(input))
	}

	if output >= 0 {
		b4 = protowire.AppendTag(b4, 3, protowire.VarintType)
		b4 = protowire.AppendVarint(b4, uint64(output))
	}

	if cacheRead > 0 {
		b4 = protowire.AppendTag(b4, 5, protowire.VarintType)
		b4 = protowire.AppendVarint(b4, uint64(cacheRead))
	}

	var b1 []byte
	b1 = protowire.AppendTag(b1, 4, protowire.BytesType)
	b1 = protowire.AppendBytes(b1, b4)

	if model != "" {
		b1 = protowire.AppendTag(b1, 21, protowire.BytesType)
		b1 = protowire.AppendString(b1, model)
	}

	var top []byte
	top = protowire.AppendTag(top, 1, protowire.BytesType)
	top = protowire.AppendBytes(top, b1)

	return top
}

func writeAntigravityDB(t *testing.T, path string, entries ...antigravityEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS gen_metadata (idx integer, data blob, size integer NOT NULL DEFAULT 0, PRIMARY KEY (idx))`); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, err := db.Exec(`INSERT OR REPLACE INTO gen_metadata (idx, data, size) VALUES (?, ?, ?)`, e.idx, e.data, len(e.data)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadAntigravityUsageSumsNewRowsOfTwoTurns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	session := "session-1"
	dbPath := antigravityDBPath(dir, session)

	writeAntigravityDB(t, dbPath,
		antigravityEntry{idx: 0, data: makeAntigravityData(100, 10, 1000, "gemini-3.8-flash-high")},
		antigravityEntry{idx: 1, data: makeAntigravityData(20, 5, 200, "gemini-3.8-flash-high")},
	)

	first, err := readAntigravityUsage(dir, session, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !first.tokens || first.input != 120 || first.output != 15 || first.cacheRead != 1200 || first.model != "gemini-3.8-flash-high" || first.highestIdx != 1 {
		t.Errorf("first turn = %+v", first)
	}

	writeAntigravityDB(t, dbPath,
		antigravityEntry{idx: 2, data: makeAntigravityData(30, 3, 50, "gemini-3.8-flash-pro")},
	)

	second, err := readAntigravityUsage(dir, session, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !second.tokens || second.input != 30 || second.output != 3 || second.cacheRead != 50 || second.model != "gemini-3.8-flash-pro" || second.highestIdx != 2 {
		t.Errorf("second turn = %+v", second)
	}

	third, err := readAntigravityUsage(dir, session, 2)
	if err != nil {
		t.Fatal(err)
	}
	if third.tokens || third.input != 0 || third.output != 0 || third.cacheRead != 0 || third.model != "" || third.highestIdx != 2 {
		t.Errorf("third turn with no new row = %+v", third)
	}
}

func TestReadAntigravityUsageRowWithNoField5CountsZeroCacheRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	session := "session-no-cache"
	dbPath := antigravityDBPath(dir, session)

	writeAntigravityDB(t, dbPath,
		antigravityEntry{idx: 0, data: makeAntigravityData(50, 5, 0, "gemini-3.8-flash-high")},
	)

	usage, err := readAntigravityUsage(dir, session, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !usage.tokens || usage.input != 50 || usage.output != 5 || usage.cacheRead != 0 || usage.model != "gemini-3.8-flash-high" || usage.highestIdx != 0 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestReadAntigravityUsageRowWithNoField4GivesNoTokensAndNoModel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	session := "session-no-usage"
	dbPath := antigravityDBPath(dir, session)

	var b1 []byte
	b1 = protowire.AppendTag(b1, 21, protowire.BytesType)
	b1 = protowire.AppendString(b1, "gemini-3.8-flash-high")
	var top []byte
	top = protowire.AppendTag(top, 1, protowire.BytesType)
	top = protowire.AppendBytes(top, b1)

	writeAntigravityDB(t, dbPath,
		antigravityEntry{idx: 0, data: top},
	)

	usage, err := readAntigravityUsage(dir, session, -1)
	if err != nil {
		t.Fatal(err)
	}
	if usage.tokens || usage.model != "" || usage.highestIdx != 0 {
		t.Errorf("usage = %+v, want no tokens and no model", usage)
	}
}

func TestReadAntigravityUsageNoFileAndInvalidProtobuf(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, err := readAntigravityUsage(dir, "missing-session", -1); err == nil {
		t.Error("missing session database gave no error")
	}

	session := "session-broken"
	dbPath := antigravityDBPath(dir, session)
	writeAntigravityDB(t, dbPath,
		antigravityEntry{idx: 0, data: []byte("broken protobuf not valid")},
	)

	usage, err := readAntigravityUsage(dir, session, -1)
	if err == nil {
		t.Fatal("broken protobuf gave no error")
	}
	if usage.tokens || usage.model != "" || usage.highestIdx != -1 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestReadAntigravityUsageWALMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	session := "wal-session"
	dbPath := antigravityDBPath(dir, session)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE gen_metadata (idx integer, data blob, size integer NOT NULL DEFAULT 0, PRIMARY KEY (idx))`); err != nil {
		t.Fatal(err)
	}
	data := makeAntigravityData(100, 20, 500, "gemini-3.8-flash-high")
	if _, err := db.Exec(`INSERT INTO gen_metadata (idx, data, size) VALUES (?, ?, ?)`, 0, data, len(data)); err != nil {
		t.Fatal(err)
	}

	usage, err := readAntigravityUsage(dir, session, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !usage.tokens || usage.input != 100 || usage.output != 20 || usage.cacheRead != 500 || usage.model != "gemini-3.8-flash-high" || usage.highestIdx != 0 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestReadAntigravityUsageIgnoresField17(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	session := "session-repeat"
	dbPath := antigravityDBPath(dir, session)

	var b4 []byte
	b4 = protowire.AppendTag(b4, 2, protowire.VarintType)
	b4 = protowire.AppendVarint(b4, 100)
	b4 = protowire.AppendTag(b4, 3, protowire.VarintType)
	b4 = protowire.AppendVarint(b4, 10)

	var b17 []byte
	b17 = protowire.AppendTag(b17, 2, protowire.VarintType)
	b17 = protowire.AppendVarint(b17, 100)

	var b1 []byte
	b1 = protowire.AppendTag(b1, 4, protowire.BytesType)
	b1 = protowire.AppendBytes(b1, b4)
	b1 = protowire.AppendTag(b1, 17, protowire.BytesType)
	b1 = protowire.AppendBytes(b1, b17)
	b1 = protowire.AppendTag(b1, 21, protowire.BytesType)
	b1 = protowire.AppendString(b1, "gemini-3.8-flash-high")

	var top []byte
	top = protowire.AppendTag(top, 1, protowire.BytesType)
	top = protowire.AppendBytes(top, b1)

	writeAntigravityDB(t, dbPath,
		antigravityEntry{idx: 0, data: top},
	)

	usage, err := readAntigravityUsage(dir, session, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !usage.tokens || usage.input != 100 || usage.output != 10 || usage.model != "gemini-3.8-flash-high" {
		t.Errorf("usage = %+v, want input 100 without 1.17", usage)
	}
}
