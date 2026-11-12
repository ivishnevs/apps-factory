package tasks

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Duplicate warnings (task-duplicate-warnings): a task names the work it covers as stable keys, and Core warns,
// never refuses, when two open tasks cover the same work.
//
// Covered work keys (Task.Covers):
//   - <change>#<n.m> an OpenSpec tasks.md item, <change>#<n> a group of items (it covers <change>#<n>.*);
//   - pr#<number> a pull request;
//   - msg:<id8> a review finding or request (the first 8 characters of the message id).

// Duplicate warning reasons. The first three are strong (a possible duplicate, kept on both tasks until answered);
// same_change_open is context only.
const (
	ReasonSameWorkItem   = "same_work_item"
	ReasonSameTitle      = "same_title"
	ReasonOriginalOpen   = "original_open"
	ReasonSameChangeOpen = "same_change_open"
)

// MaxSameChangeOpen bounds the same_change_open context list.
const MaxSameChangeOpen = 10

// MaxCovers bounds a task's covered keys.
const MaxCovers = 64

// DuplicateWarning names another open task that may cover the same work, and why.
type DuplicateWarning struct {
	TaskID   string   `json:"task_id"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Assignee string   `json:"assignee,omitempty"`
	Reasons  []string `json:"reasons"`
	// Keys are the covered keys both tasks share (same_work_item).
	Keys []string `json:"keys,omitempty"`
}

// Strong reports whether the warning claims a possible duplicate (not only same_change_open context).
func (w DuplicateWarning) Strong() bool {
	return slices.ContainsFunc(w.Reasons, func(r string) bool { return r != ReasonSameChangeOpen })
}

// DuplicateHint is the instruction that comes with duplicate warnings in a create or update answer.
const DuplicateHint = "Possible duplicate tasks (warnings; nothing was refused). For each strong warning decide now: cancel the new task " +
	"(factory_task_cancel) and update the existing one instead, or cancel the original if this task replaces it (replaces), or keep both and " +
	"say so with factory_task_update not_duplicate_of: [ids]. An unanswered warning stays on both tasks and in your feed. same_change_open " +
	"is only context: other open tasks of the same assignee and change."

var (
	changeKey  = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*[a-z0-9]|[a-z0-9])\s*#\s*(\d{1,3}(?:\.\d{1,3})?)$`)
	changeItem = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*[a-z0-9]|[a-z0-9])\s+(\d{1,3}(?:\.\d{1,3})?)$`)
	prKey      = regexp.MustCompile(`^(?:pr|pull request|pull)\s*#?\s*(\d{1,7})$`)
	bareNumber = regexp.MustCompile(`^#(\d{1,7})$`)
	msgKey     = regexp.MustCompile(`^(?:msg|message)\s*[:#]?\s*([0-9a-z][0-9a-z-]{7,})$`)
)

// NormalizeCover returns the canonical form of a covered work key: <change>#<n.m>, <change>#<n>, pr#<n> or
// msg:<id8>. It accepts the usual spellings ("PR #148", "#148", "player-profile-and-xp 3.2", a full message id).
func NormalizeCover(key string) (string, error) {
	k := strings.Join(strings.Fields(strings.ToLower(key)), " ")
	if m := prKey.FindStringSubmatch(k); m != nil {
		return "pr#" + trimZeros(m[1]), nil
	}
	if m := bareNumber.FindStringSubmatch(k); m != nil {
		return "pr#" + trimZeros(m[1]), nil
	}
	if m := msgKey.FindStringSubmatch(k); m != nil {
		if id := strings.TrimPrefix(m[1], "m-"); len(id) >= 8 {
			return "msg:" + id[:8], nil
		}
	}
	if m := changeKey.FindStringSubmatch(k); m != nil {
		return m[1] + "#" + m[2], nil
	}
	if m := changeItem.FindStringSubmatch(k); m != nil && strings.Contains(m[1], "-") { // "item 3" is not a change
		return m[1] + "#" + m[2], nil
	}
	return "", fmt.Errorf("covers: %q is not a work key: use <change>#<n.m> (an OpenSpec tasks.md item), <change>#<n> (a group), pr#<number> or msg:<id8>", key)
}

func trimZeros(n string) string {
	v, err := strconv.Atoi(n)
	if err != nil {
		return n
	}
	return strconv.Itoa(v)
}

// NormalizeCovers normalizes, deduplicates and sorts keys.
func NormalizeCovers(keys []string) ([]string, error) {
	if len(keys) > MaxCovers {
		return nil, fmt.Errorf("covers: at most %d keys", MaxCovers)
	}
	out := []string{}
	for _, k := range keys {
		n, err := NormalizeCover(k)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out, nil
}

// CoverChange is the change of an OpenSpec key (<change>#<n>), "" for other keys.
func CoverChange(key string) string {
	if strings.HasPrefix(key, "pr#") || strings.HasPrefix(key, "msg:") {
		return ""
	}
	change, _, ok := strings.Cut(key, "#")
	if !ok {
		return ""
	}
	return change
}

// CoversOverlap reports whether two keys name the same work: equal keys, or a group and one of its items
// (<change>#3 and <change>#3.2).
func CoversOverlap(a, b string) bool {
	if a == b {
		return true
	}
	if CoverChange(a) == "" || CoverChange(a) != CoverChange(b) {
		return false
	}
	return strings.HasPrefix(b, a+".") || strings.HasPrefix(a, b+".")
}

// SharedCovers lists the keys of a that overlap a key of b.
func SharedCovers(a, b []string) []string {
	var out []string
	for _, x := range a {
		for _, y := range b {
			if CoversOverlap(x, y) {
				k := x
				if len(y) > len(x) {
					k = y // the narrower key names the shared item
				}
				if !slices.Contains(out, k) {
					out = append(out, k)
				}
			}
		}
	}
	return out
}

// CoversMatch reports whether covers holds the key (or overlaps it), for the covers filter of a task list.
func CoversMatch(covers []string, key string) bool {
	return slices.ContainsFunc(covers, func(k string) bool { return CoversOverlap(k, key) })
}

// Inference (when a task names no covers): deterministic, from the title and description.
//   - "<known change> <n.m>" (also "<change> task 3.2", "<change> tasks.md 3.2", "<change>#3.2", lists and ranges
//     such as "world-models 7.1–7.3" or "4.1, 4.2 and 4.3");
//   - "tasks.md <n.m>" when the task links openspec_change;
//   - "#<n>", "PR <n>", "pull request #<n>" for pull requests.
//
// Known changes are the project's changes Core knows (its tasks' openspec_change and the changes reported by
// done exits); a change name shorter than 4 characters is not looked for in text.

// InferCovers returns the keys named by title and description and the known changes named there (sorted).
func InferCovers(title, description, change string, known []string) (keys, changes []string) {
	text := strings.ToLower(title + "\n" + description)
	add := func(k string) {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	names := slices.Clone(known)
	// Longer names first, so "world-models-v2" is not read as "world-models".
	slices.SortFunc(names, func(a, b string) int { return len(b) - len(a) })
	taken := make([]bool, len(text)) // bytes already matched by a longer change name
	for _, name := range names {
		if len(name) < 4 {
			continue
		}
		for from := 0; ; {
			i := strings.Index(text[from:], name)
			if i < 0 {
				break
			}
			start, end := from+i, from+i+len(name)
			from = end
			if taken[start] || start > 0 && nameByte(text[start-1]) || end < len(text) && nameByte(text[end]) {
				continue
			}
			for j := start; j < end; j++ {
				taken[j] = true
			}
			if !slices.Contains(changes, name) {
				changes = append(changes, name)
			}
			for _, item := range itemsAfter(text[end:], true) {
				add(name + "#" + item)
			}
		}
	}
	if change != "" {
		for from := 0; ; {
			i := strings.Index(text[from:], "tasks.md")
			if i < 0 {
				break
			}
			end := from + i + len("tasks.md")
			from = end
			for _, item := range itemsAfter(text[end:], false) {
				add(change + "#" + item)
			}
		}
	}
	for _, n := range prNumbers(text) {
		add("pr#" + n)
	}
	slices.Sort(keys)
	slices.Sort(changes)
	return keys, changes
}

func nameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

var itemWords = []string{"tasks.md", "tasks", "task", "items", "item", "sections", "section", "groups", "group", "steps", "step", "§"}

// itemsAfter parses the item numbers that follow a change name (or "tasks.md"): an optional '#' or a separator
// and an item word, then numbers joined by ranges and lists.
func itemsAfter(s string, afterName bool) []string {
	p := 0
	skip := func(chars string) {
		for p < len(s) && strings.IndexByte(chars, s[p]) >= 0 {
			p++
		}
	}
	if afterName && strings.HasPrefix(s, "#") {
		p = 1
	} else {
		skip(" \t:,(")
		if p == 0 && !(p < len(s) && s[p] == '#') {
			return nil
		}
		rest := s[p:]
		for _, w := range itemWords {
			if strings.HasPrefix(rest, w) && (len(rest) == len(w) || !nameByte(rest[len(w)])) {
				p += len(w)
				break
			}
		}
		skip(" \t:#§")
	}
	var out []string
	for {
		first, ok := number(s, &p)
		if !ok {
			break
		}
		q := p
		skipSpace(s, &q)
		last := first
		for _, sep := range []string{"-", "–", "—", "..", "to "} {
			if strings.HasPrefix(s[q:], sep) {
				r := q + len(sep)
				skipSpace(s, &r)
				if n, ok := number(s, &r); ok {
					last, p = n, r
				}
				break
			}
		}
		out = append(out, expand(first, last)...)
		q = p
		skipSpace(s, &q)
		joined := false
		for _, sep := range []string{",", "/", "&", "and "} {
			if strings.HasPrefix(s[q:], sep) {
				r := q + len(sep)
				skipSpace(s, &r)
				if r < len(s) && s[r] >= '0' && s[r] <= '9' {
					p, joined = r, true
				}
				break
			}
		}
		if !joined {
			break
		}
	}
	return out
}

func skipSpace(s string, p *int) {
	for *p < len(s) && (s[*p] == ' ' || s[*p] == '\t') {
		*p++
	}
}

// number reads an item number (n or n.m: up to 2 digits, then up to 3) at *p; a longer number, a date or a
// version (3.2.1) is not an item.
func number(s string, p *int) (string, bool) {
	i := *p
	digits := func() int {
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		return j - i
	}
	n := digits()
	if n == 0 || n > 2 {
		return "", false
	}
	i += n
	if i+1 < len(s) && s[i] == '.' && s[i+1] >= '0' && s[i+1] <= '9' {
		i++
		m := digits()
		if m > 3 {
			return "", false
		}
		i += m
		if i+1 < len(s) && s[i] == '.' && s[i+1] >= '0' && s[i+1] <= '9' {
			return "", false
		}
	}
	if i < len(s) && (s[i] >= 'a' && s[i] <= 'z' || s[i] >= '0' && s[i] <= '9' || s[i] == '_') {
		return "", false
	}
	v := s[*p:i]
	*p = i
	return v, true
}

// expand lists a range of items: 7.1–7.3 is 7.1, 7.2, 7.3; 3–5 is 3, 4, 5; anything else its two ends.
func expand(first, last string) []string {
	if first == last {
		return []string{first}
	}
	fm, fn, fdot := strings.Cut(first, ".")
	lm, ln, ldot := strings.Cut(last, ".")
	a, b := fn, ln
	if !fdot && !ldot {
		a, b = fm, lm
	} else if !fdot || !ldot || fm != lm {
		return []string{first, last}
	}
	x, err1 := strconv.Atoi(a)
	y, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || y <= x || y-x > 20 {
		return []string{first, last}
	}
	var out []string
	for i := x; i <= y; i++ {
		if fdot {
			out = append(out, fm+"."+strconv.Itoa(i))
		} else {
			out = append(out, strconv.Itoa(i))
		}
	}
	return out
}

var prWord = regexp.MustCompile(`\b(?:pr|pull request)\s*#?\s*(\d{1,6})\b`)

// prNumbers lists the pull request numbers named in text ("#148", "PR 148", "pull request #148"). A '#' right
// after a word, a path or another '#' (a change key, a markdown heading) is not a pull request, nor is "#3.2".
func prNumbers(text string) []string {
	var out []string
	add := func(n string) {
		if n = trimZeros(n); !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	for i := 0; i < len(text); i++ {
		if text[i] != '#' || i > 0 && (nameByte(text[i-1]) || strings.IndexByte("&/#.", text[i-1]) >= 0) {
			continue
		}
		j := i + 1
		for j < len(text) && j-i <= 7 && text[j] >= '0' && text[j] <= '9' {
			j++
		}
		if j == i+1 || j-i > 7 || j < len(text) && (nameByte(text[j]) && text[j] != '-' || text[j] == '.' && j+1 < len(text) && text[j+1] >= '0' && text[j+1] <= '9') {
			continue
		}
		add(text[i+1 : j])
	}
	for _, m := range prWord.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	slices.SortFunc(out, func(a, b string) int { x, _ := strconv.Atoi(a); y, _ := strconv.Atoi(b); return x - y })
	return out
}

var (
	titlePrefix = regexp.MustCompile(`^\s*([a-z0-9][a-z0-9./_-]*)\s*:\s*`)
	hexID       = regexp.MustCompile(`\b[0-9a-f]{8,}\b`)
	titleNoise  = regexp.MustCompile(`[^a-z0-9.#-]+`)
)

// DuplicateTitle normalizes a title for same_title: lowercase, milestone prefixes such as "3D-WQ2:" and task ids
// removed, punctuation and spaces collapsed.
func DuplicateTitle(s string) string {
	s = strings.ToLower(s)
	for {
		m := titlePrefix.FindStringSubmatch(s)
		if m == nil || !strings.ContainsAny(m[1], "0123456789-") {
			break
		}
		s = s[len(m[0]):]
	}
	s = hexID.ReplaceAllStringFunc(s, func(id string) string {
		if strings.ContainsAny(id, "0123456789") {
			return " "
		}
		return id
	})
	s = titleNoise.ReplaceAllString(s, " ")
	return strings.Trim(strings.Join(strings.Fields(s), " "), " .-")
}
