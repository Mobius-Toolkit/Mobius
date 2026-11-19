package engine_test

import (
	"fmt"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// leadMark is the mark of the Lead of the Workstream #12 of the fake GitHub, with no ui_url in the config.
const leadMark = "— Lead of #12 Integrate loyalty plans"

// leadMarkElsewhere is the mark of the Lead of the Workstream #12 for a text in another repository than owner/shop.
const leadMarkElsewhere = "— Lead of owner/shop#12 Integrate loyalty plans"

// marked gives body with mark as its last line.
func marked(body, mark string) string {
	return body + "\n\n" + mark
}

// withoutWriteMarkerText strips a trailing outgoing change write marker and tells if one was present.
func withoutWriteMarkerText(body string) (string, bool) {
	before, rest, found := strings.Cut(body, "\n\n<!-- mobius:write ")
	return before, found && strings.HasSuffix(rest, " -->") && len(rest) > len(" -->")
}

// triagerComment is the comment of the Mobius App with body and the mark of the Triager of the issue.
func triagerComment(issue int64, body string) testkit.Comment {
	return testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: marked(body, fmt.Sprintf("— Triager of #%d", issue))}
}
