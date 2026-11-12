package storage

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"appfactory/internal/messaging"
)

// Artifact links (artifact-links): a message to the owner, a question forwarded up the chain and a decision
// proposal that name an artifact of their project in the text carry that artifact version as a material, so
// people open it (and from there its Library card) where they answer instead of searching for it. Only versions
// registered in the message's project are linked; the sender's own materials come first and are not duplicated.

var (
	// artifactURI is an uploaded version's URI, factory://artifacts/<id>/<version>.
	artifactURI = regexp.MustCompile(`factory://artifacts/([A-Za-z0-9][A-Za-z0-9_-]{0,95})/([^\s<>"'\x60()\[\]{},;|]{1,128})`)
	// artifactAt is <id>@<version>.
	artifactAt = regexp.MustCompile(`([A-Za-z0-9][A-Za-z0-9_-]{0,95})@([A-Za-z0-9][A-Za-z0-9._-]{0,127})`)
	// artifactWord is a word that may be an artifact id.
	artifactWord = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9_-]{0,95}`)
)

// artifactMention is one artifact version a text references; Named: the text names the version (a URI,
// id@version or "id version"), else it names the artifact and the mention is its latest version.
type artifactMention struct {
	ID, Version string
	Named       bool
}

// artifactMentions finds references to registered artifacts in texts, in the order they appear: a
// factory://artifacts/<id>/<version> URI, <id>@<version>, and a bare id, optionally followed by one of its versions
// ("stashy-pouch v2"). A bare id must contain '-', '_' or a digit, so a plain word ("logo", "design") that happens to
// be an artifact id links nothing; such an artifact is linked by its URI or id@version. known maps each registered
// artifact id to its versions, oldest first; unregistered ids and versions are ignored. A bare mention of an
// artifact whose version the texts name elsewhere adds nothing.
func artifactMentions(known map[string][]string, texts ...string) []artifactMention {
	if len(known) == 0 {
		return nil
	}
	trim := func(v string) string { return strings.TrimRight(v, ".:!?") }
	type found struct {
		text, at int
		m        artifactMention
	}
	var all []found
	for i, text := range texts {
		taken := make([]bool, len(text)+1)
		take := func(from, to int) {
			for k := from; k < to; k++ {
				taken[k] = true
			}
		}
		for _, m := range artifactURI.FindAllStringSubmatchIndex(text, -1) {
			id, v := text[m[2]:m[3]], trim(text[m[4]:m[5]])
			if slices.Contains(known[id], v) {
				all = append(all, found{i, m[0], artifactMention{id, v, true}})
			}
			take(m[0], m[1])
		}
		for _, m := range artifactAt.FindAllStringSubmatchIndex(text, -1) {
			if taken[m[0]] || m[0] > 0 && isWordByte(text[m[0]-1]) {
				continue
			}
			id, v := text[m[2]:m[3]], trim(text[m[4]:m[5]])
			if slices.Contains(known[id], v) {
				all = append(all, found{i, m[0], artifactMention{id, v, true}})
				take(m[0], m[1])
			}
		}
		for _, m := range artifactWord.FindAllStringIndex(text, -1) {
			if taken[m[0]] || m[0] > 0 && (isWordByte(text[m[0]-1]) || text[m[0]-1] == '@') || m[1] < len(text) && text[m[1]] == '@' {
				continue
			}
			id := text[m[0]:m[1]]
			versions, ok := known[id]
			if !ok || !strings.ContainsAny(id, "-_0123456789") {
				continue
			}
			mention := artifactMention{ID: id, Version: versions[len(versions)-1]}
			rest := text[m[1]:]
			if next := strings.Fields(rest); len(next) > 0 && strings.TrimLeft(rest, " \t") != rest && slices.Contains(versions, trim(next[0])) {
				mention.Version, mention.Named = trim(next[0]), true
			}
			all = append(all, found{i, m[0], mention})
		}
	}
	slices.SortStableFunc(all, func(a, b found) int {
		if a.text != b.text {
			return a.text - b.text
		}
		return a.at - b.at
	})
	named := map[string]bool{}
	for _, f := range all {
		if f.m.Named {
			named[f.m.ID] = true
		}
	}
	var out []artifactMention
	seen := map[string]bool{}
	for _, f := range all {
		if f.m.Named == named[f.m.ID] && !seen[f.m.ID+"\x00"+f.m.Version] {
			seen[f.m.ID+"\x00"+f.m.Version] = true
			out = append(out, f.m)
		}
	}
	return out
}

// questionsText is the text of a message's form: its questions and options.
func questionsText(qs []messaging.Question) string {
	var b strings.Builder
	for _, q := range qs {
		b.WriteString(q.Question + "\n" + strings.Join(q.Options, "\n") + "\n")
	}
	return b.String()
}

func isWordByte(b byte) bool {
	return b == '-' || b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// mentionedMaterials are the artifact versions of project that texts reference (artifactMentions) and have does
// not carry yet, as many as fit with have into one message (messaging.MaxVisuals). A bare mention of an artifact
// have carries in any version adds nothing; a version the text names is added when have lacks that version.
func (s *Store) mentionedMaterials(ctx context.Context, company, project string, have []messaging.MaterialRef, texts ...string) []messaging.MaterialRef {
	room := messaging.MaxVisuals - len(have)
	if project == "" || room <= 0 || strings.TrimSpace(strings.Join(texts, "")) == "" {
		return nil
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT id,version FROM artifact_versions WHERE company_id=? AND project_id=? ORDER BY seq", company, project)
	if err != nil {
		return nil // links are a convenience: the message goes without them
	}
	defer rows.Close()
	known := map[string][]string{}
	for rows.Next() {
		var id, v string
		if rows.Scan(&id, &v) == nil {
			known[id] = append(known[id], v)
		}
	}
	if rows.Err() != nil {
		return nil
	}
	carried := map[string]bool{}
	for _, m := range have {
		if m.ArtifactID != "" {
			carried[m.ArtifactID] = true
			carried[m.ArtifactID+"\x00"+m.ArtifactVersion] = true
		}
	}
	var out []messaging.MaterialRef
	for _, m := range artifactMentions(known, texts...) {
		if len(out) == room || carried[m.ID+"\x00"+m.Version] || carried[m.ID] && !m.Named {
			continue
		}
		out = append(out, messaging.MaterialRef{ArtifactID: m.ID, ArtifactVersion: m.Version})
	}
	return out
}
