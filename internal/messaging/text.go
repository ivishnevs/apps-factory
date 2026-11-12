package messaging

import (
	"regexp"
	"strings"
)

var (
	idRe      = regexp.MustCompile(`\b[0-9a-f]{32}\b`)
	exactLine = regexp.MustCompile(`(?m)^(Exact artifact version|Exact call): .*$`)
	prefixRe  = regexp.MustCompile(`^(Artifact approval|Artifact choice approval|Approval) [0-9a-f]{32}: `)
	escRe     = regexp.MustCompile(`^Escalation [a-z_]+ on (task|quota key) [^\n:]*?: `)
	blankRe   = regexp.MustCompile(`\n{3,}`)
	// optionRe is the options list of a choice approval's request (artifact-choice-approvals): people see the options
	// themselves (cards, the inbox list, Telegram lines), not the agents' list.
	optionRe = regexp.MustCompile(`(?m)^(Options:|\d+\. .*\([A-Za-z0-9][A-Za-z0-9_-]*@[^)\s]+\).*)$`)
)

// ShortIDs shortens full ids to their first 8 characters.
func ShortIDs(s string) string {
	return strings.TrimSpace(idRe.ReplaceAllStringFunc(s, func(id string) string { return id[:8] }))
}

// PeopleText is a message body for people (people-topics D7): without the exact JSON lines, approval and escalation
// prefixes and full ids.
func PeopleText(body string) string {
	s := exactLine.ReplaceAllString(body, "")
	if strings.HasPrefix(s, "Artifact choice approval ") {
		s = optionRe.ReplaceAllString(s, "")
	}
	s = prefixRe.ReplaceAllString(s, "")
	s = escRe.ReplaceAllString(s, "")
	s = blankRe.ReplaceAllString(ShortIDs(s), "\n\n")
	return strings.TrimSpace(s)
}

// Headline is what a list shows for a message: its brief's title, else its first cleaned line.
func (m Message) Headline() string {
	if m.Brief != nil && strings.TrimSpace(m.Brief.Title) != "" {
		return m.Brief.Title
	}
	t := PeopleText(m.Body)
	return strings.TrimSpace(strings.SplitN(t, "\n", 2)[0])
}
