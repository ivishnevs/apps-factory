package messaging

import (
	"regexp"
	"strings"
)

// ChoicesHint is what an ask that lists choices in its text without options is told (agent-questions-in-chat):
// nothing was sent, the choices go in options so the person gets one button each.
const ChoicesHint = "the question lists choices in its text but has no options: nothing was sent. Ask again with the choices in options (each becomes a button; the person can still write their own answer). If it really offers no choices, pass options: [] to send it as a free-text question."

var (
	// A question signal: a question mark or a choosing word.
	choosing = regexp.MustCompile(`(?i)\?|\b(which|choose|pick|select|prefer)\b`)
	// "Profiles: ASC Key, Cheflet, Stashy" — a label followed by three or more short comma-separated items, or
	// two or more joined by "or".
	labelList = regexp.MustCompile(`:\s*([^,:\n]{1,60},\s*){2,}[^,:\n]{1,60}$|:\s*([^,:\n]{1,60},\s*)*[^,:\n]{1,60}\s+or\s+[^,:\n]{1,60}$`)
	// A bullet, numbered or lettered list item.
	listItem = regexp.MustCompile(`^\s*([-*•]|\d{1,2}[.)]|[a-zA-Z][.)])\s+\S`)
	// "A, B or C" inside a sentence.
	commaOr = regexp.MustCompile(`[^\s,]+,\s+[^,\n]{1,60}?,?\s+or\s+[^\s,?]+`)
)

// ListsChoices reports whether a question's text offers the person choices in prose (a bullet or numbered list, a
// "Label: a, b, c" list, "A, B or C", or a question sentence "A or B?") that should be options instead.
func ListsChoices(text string) bool {
	asked, items := false, 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(strings.TrimSpace(line), ".")
		// Lists count only from the line that asks on (a list of facts above the question is not a choice).
		asked = asked || choosing.MatchString(line)
		if !asked {
			continue
		}
		if listItem.MatchString(line) {
			if items++; items >= 2 {
				return true
			}
			continue
		}
		if labelList.MatchString(line) || commaOr.MatchString(line) {
			return true
		}
		// A question sentence with an alternative: "Should I use Postgres or SQLite?"
		for _, s := range strings.SplitAfter(line, "?") {
			if strings.HasSuffix(s, "?") && strings.Contains(strings.ToLower(s), " or ") {
				return true
			}
		}
	}
	return false
}
