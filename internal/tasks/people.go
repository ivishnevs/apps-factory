package tasks

import (
	"regexp"
	"strconv"
	"strings"
)

// People as assignees (people-assignees): a task's assignee is an agent of the catalog or a person of the company,
// "member:<user>". A person's task uses the same statuses and transitions as any task; Core dispatches no run for
// it, and the person (or the owner) moves it: start, submit for review with a pull request, done after the approval
// and the merge.

// PersonPrefix marks a person in the assignee (and review_by) field.
const PersonPrefix = "member:"

// PersonUser is the user of a person assignee ("member:alice" → "alice", true); false for an agent or "".
func PersonUser(assignee string) (string, bool) {
	user, ok := strings.CutPrefix(assignee, PersonPrefix)
	return user, ok && user != ""
}

// IsPerson tells whether the assignee is a person.
func IsPerson(assignee string) bool {
	_, ok := PersonUser(assignee)
	return ok
}

// PullRequest is the pull request a person names when they submit their task for review.
type PullRequest struct {
	Number int    `json:"number,omitempty"`
	URL    string `json:"url,omitempty"`
}

var prURL = regexp.MustCompile(`/pull/(\d+)`)

// ParsePullRequest reads "#200", "200" or a pull request URL (…/pull/200); ok is false for anything else.
func ParsePullRequest(s string) (PullRequest, bool) {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(strings.TrimPrefix(s, "#")); err == nil && n > 0 {
		return PullRequest{Number: n}, true
	}
	if m := prURL.FindStringSubmatch(s); m != nil && (strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")) {
		n, _ := strconv.Atoi(m[1])
		return PullRequest{Number: n, URL: s}, n > 0
	}
	return PullRequest{}, false
}

// String is "#200", with the URL when known.
func (p PullRequest) String() string {
	s := "#" + strconv.Itoa(p.Number)
	if p.URL != "" {
		s += " (" + p.URL + ")"
	}
	return s
}
