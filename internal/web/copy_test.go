package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoConnectorInWebCopy (nodes-naming 1.3): the web UI says "node", never "connector", outside comments and the
// InstallConnector component's name (Dex's sign-in connectors are only mentioned in comments).
func TestNoConnectorInWebCopy(t *testing.T) {
	root := "../../web/src"
	if _, err := os.Stat(root); err != nil {
		t.Skip("no web sources")
	}
	word := regexp.MustCompile(`(?i)connector`)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(p, ".tsx") || strings.HasSuffix(p, ".ts")) {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			s := strings.TrimSpace(line)
			if strings.HasPrefix(s, "//") || strings.HasPrefix(s, "*") || strings.HasPrefix(s, "/*") {
				continue
			}
			s = strings.ReplaceAll(s, "InstallConnector", "")
			if c := strings.Index(s, "// "); c >= 0 {
				s = s[:c]
			}
			if word.MatchString(s) {
				t.Errorf("%s:%d says connector: %s", p, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
