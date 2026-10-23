package engine_test

import (
	"fmt"

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

// triagerComment is the comment of the Mobius App with body and the mark of the Triager of the issue.
func triagerComment(issue int64, body string) testkit.Comment {
	return testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: marked(body, fmt.Sprintf("— Triager of #%d", issue))}
}
