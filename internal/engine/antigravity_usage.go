package engine

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"google.golang.org/protobuf/encoding/protowire"
	_ "modernc.org/sqlite" // registers the database/sql driver "sqlite"
)

// antigravityUsage is the usage that the newer model calls of an Antigravity conversation added.
type antigravityUsage struct {
	// highestIdx is the highest gen_metadata idx that the usage rows counted.
	highestIdx int64
	input      int64
	output     int64
	cacheRead  int64
	// model is the reported model of the last counted row that has field 1.21, or "".
	model string
	// tokens tells that at least one counted row has field 1.4.
	tokens bool
}

// antigravityRow is the parsed metadata of one gen_metadata row.
type antigravityRow struct {
	input     int64
	output    int64
	cacheRead int64
	model     string
	hasUsage  bool
	hasModel  bool
}

// readAntigravityUsage reads the rows of gen_metadata of the Antigravity conversation database after the highest
// counted idx, in idx order. It gives the sums of the tokens of these rows and the reported model of the last one.
func readAntigravityUsage(dir, session string, highestIdx int64) (antigravityUsage, error) {
	usage := antigravityUsage{highestIdx: highestIdx}
	dbPath := antigravityDBPath(dir, session)
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro")
	if err != nil {
		return usage, err
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query("SELECT idx, data FROM gen_metadata WHERE idx > ? ORDER BY idx", highestIdx)
	if err != nil {
		return usage, err
	}
	defer func() { _ = rows.Close() }()

	var (
		lastModel string
		parseErr  error
	)
	for rows.Next() {
		var (
			idx  int64
			data []byte
		)
		if err := rows.Scan(&idx, &data); err != nil {
			return antigravityUsage{highestIdx: usage.highestIdx}, err
		}
		usage.highestIdx = idx
		row, err := parseAntigravityMetadata(data)
		if err != nil {
			parseErr = err
			continue
		}
		if row.hasUsage {
			usage.input += row.input
			usage.output += row.output
			usage.cacheRead += row.cacheRead
			usage.tokens = true
		}
		if row.hasModel && row.model != "" {
			lastModel = row.model
		}
	}
	if err := rows.Err(); err != nil {
		return antigravityUsage{highestIdx: usage.highestIdx}, err
	}
	if parseErr != nil {
		return antigravityUsage{highestIdx: usage.highestIdx}, nil
	}
	if usage.tokens {
		usage.model = lastModel
	}
	return usage, nil
}

// parseAntigravityMetadata decodes the protobuf metadata blob of one gen_metadata row.
func parseAntigravityMetadata(b []byte) (antigravityRow, error) {
	var row antigravityRow
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return row, protowire.ParseError(n)
		}
		b = b[n:]
		if num == 1 && typ == protowire.BytesType {
			sub1, nSub1 := protowire.ConsumeBytes(b)
			if nSub1 < 0 {
				return row, protowire.ParseError(nSub1)
			}
			b = b[nSub1:]
			for len(sub1) > 0 {
				num1, typ1, n1 := protowire.ConsumeTag(sub1)
				if n1 < 0 {
					return row, protowire.ParseError(n1)
				}
				sub1 = sub1[n1:]
				switch {
				case num1 == 4 && typ1 == protowire.BytesType:
					sub4, n4 := protowire.ConsumeBytes(sub1)
					if n4 < 0 {
						return row, protowire.ParseError(n4)
					}
					sub1 = sub1[n4:]
					row.hasUsage = true
					for len(sub4) > 0 {
						num4, typ4, n44 := protowire.ConsumeTag(sub4)
						if n44 < 0 {
							return row, protowire.ParseError(n44)
						}
						sub4 = sub4[n44:]
						var target *int64
						switch num4 {
						case 2:
							target = &row.input
						case 3:
							target = &row.output
						case 5:
							target = &row.cacheRead
						}
						if target != nil {
							if typ4 != protowire.VarintType {
								return row, fmt.Errorf("unexpected wire type %v for field 1.4.%d", typ4, num4)
							}
							v, nVal := protowire.ConsumeVarint(sub4)
							if nVal < 0 {
								return row, protowire.ParseError(nVal)
							}
							val, err := varint(v)
							if err != nil {
								return row, err
							}
							sub4 = sub4[nVal:]
							*target = val
							continue
						}
						nVal := protowire.ConsumeFieldValue(num4, typ4, sub4)
						if nVal < 0 {
							return row, protowire.ParseError(nVal)
						}
						sub4 = sub4[nVal:]
					}
				case num1 == 21 && typ1 == protowire.BytesType:
					str, nStr := protowire.ConsumeString(sub1)
					if nStr < 0 {
						return row, protowire.ParseError(nStr)
					}
					sub1 = sub1[nStr:]
					row.model = str
					row.hasModel = true
				default:
					nVal := protowire.ConsumeFieldValue(num1, typ1, sub1)
					if nVal < 0 {
						return row, protowire.ParseError(nVal)
					}
					sub1 = sub1[nVal:]
				}
			}
		} else {
			nVal := protowire.ConsumeFieldValue(num, typ, b)
			if nVal < 0 {
				return row, protowire.ParseError(nVal)
			}
			b = b[nVal:]
		}
	}
	return row, nil
}

// antigravityDBPath gives the path of the Antigravity database of the ACP session.
func antigravityDBPath(dir, session string) string {
	return filepath.Join(dir, "conversations", session+".db")
}

// antigravityDir gives the Antigravity directory: ~/.gemini/antigravity-acp.
func antigravityDir() string {
	userHome, err := os.UserHomeDir()
	if err != nil || userHome == "" {
		return ""
	}
	return filepath.Join(userHome, ".gemini", "antigravity-acp")
}

func varint(v uint64) (int64, error) {
	if v > math.MaxInt64 {
		return 0, fmt.Errorf("token count %d exceeds int64", v)
	}
	return int64(v), nil
}
