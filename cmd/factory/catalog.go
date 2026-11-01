package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"appfactory/internal/core"
	"appfactory/internal/worker"
)

// catalogVersionFile records, in a pulled directory, the version its files came from (the base of a push).
const catalogVersionFile = ".catalog-version"

// catalogCommand is `factory catalog ...` (company-self-service 3.6): the company's stored catalog.
func catalogCommand(ctx context.Context, api worker.API, args []string) error {
	usage := errors.New("usage: catalog [VERSION] | catalog history | catalog get PATH [VERSION] | catalog put PATH FILE [NOTE] | catalog rm PATH [NOTE] | catalog validate PATH FILE | catalog rollback VERSION [NOTE] | catalog pull DIR | catalog push DIR [NOTE]")
	current := func() (core.CatalogView, error) {
		var v core.CatalogView
		return v, api.Do(ctx, "GET", "/catalog", nil, &v)
	}
	note := func(i int) string {
		if len(args) > i {
			return args[i]
		}
		return ""
	}
	if len(args) == 1 || len(args) == 2 && isNumber(args[1]) {
		path := "/catalog"
		if len(args) == 2 {
			path += "?version=" + args[1]
		}
		var v any
		if err := api.Do(ctx, "GET", path, nil, &v); err != nil {
			return err
		}
		return printJSON(v)
	}
	switch args[1] {
	case "history":
		var v any
		if err := api.Do(ctx, "GET", "/catalog/history", nil, &v); err != nil {
			return err
		}
		return printJSON(v)
	case "get":
		if len(args) < 3 || len(args) > 4 {
			return usage
		}
		q := url.Values{"path": {args[2]}}
		if len(args) == 4 {
			q.Set("version", args[3])
		}
		var d struct{ Content string }
		if err := api.Do(ctx, "GET", "/catalog/document?"+q.Encode(), nil, &d); err != nil {
			return err
		}
		_, err := os.Stdout.WriteString(d.Content)
		return err
	case "put", "validate":
		if len(args) < 4 || len(args) > 5 || args[1] == "validate" && len(args) != 4 {
			return usage
		}
		b, err := os.ReadFile(args[3])
		if err != nil {
			return err
		}
		cur, err := current()
		if err != nil {
			return err
		}
		return catalogPost(ctx, api, core.CatalogChange{Base: cur.Version, Put: map[string]string{args[2]: string(b)}, Note: note(4), DryRun: args[1] == "validate"})
	case "rm":
		if len(args) < 3 || len(args) > 4 {
			return usage
		}
		cur, err := current()
		if err != nil {
			return err
		}
		return catalogPost(ctx, api, core.CatalogChange{Base: cur.Version, Delete: []string{args[2]}, Note: note(3)})
	case "rollback":
		if len(args) < 3 || len(args) > 4 || !isNumber(args[2]) {
			return usage
		}
		cur, err := current()
		if err != nil {
			return err
		}
		to, _ := strconv.ParseInt(args[2], 10, 64)
		var v any
		if err = api.Do(ctx, "POST", "/catalog/rollback", map[string]any{"base": cur.Version, "to": to, "note": note(3)}, &v); err != nil {
			return err
		}
		return printJSON(v)
	case "pull":
		if len(args) != 3 {
			return usage
		}
		return catalogPull(ctx, api, args[2])
	case "push":
		if len(args) < 3 || len(args) > 4 {
			return usage
		}
		return catalogPush(ctx, api, args[2], note(3))
	}
	return usage
}

func isNumber(s string) bool {
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

func catalogPost(ctx context.Context, api worker.API, ch core.CatalogChange) error {
	var v any
	if err := api.Do(ctx, "POST", "/catalog", ch, &v); err != nil {
		return err
	}
	return printJSON(v)
}

// catalogPull writes the current version's documents below dir (which must not hold another catalog's files).
func catalogPull(ctx context.Context, api worker.API, dir string) error {
	var v core.CatalogView
	if err := api.Do(ctx, "GET", "/catalog", nil, &v); err != nil {
		return err
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		if _, err := os.Stat(filepath.Join(dir, catalogVersionFile)); err != nil {
			return fmt.Errorf("%s is not empty and holds no pulled catalog", dir)
		}
	}
	for _, d := range v.Documents {
		var doc struct{ Content string }
		q := url.Values{"path": {d.Path}, "version": {strconv.FormatInt(v.Version, 10)}}
		if err := api.Do(ctx, "GET", "/catalog/document?"+q.Encode(), nil, &doc); err != nil {
			return err
		}
		p := filepath.Join(dir, filepath.FromSlash(d.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(doc.Content), 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, catalogVersionFile), []byte(strconv.FormatInt(v.Version, 10)+"\n"), 0o644); err != nil {
		return err
	}
	return printJSON(map[string]any{"company": v.Company, "version": v.Version, "documents": len(v.Documents), "dir": dir})
}

// catalogPush saves dir (a pulled catalog, edited) as the next version on top of the version it was pulled
// from: changed and new files are put, files gone from dir are deleted.
func catalogPush(ctx context.Context, api worker.API, dir, note string) error {
	b, err := os.ReadFile(filepath.Join(dir, catalogVersionFile))
	if err != nil {
		return fmt.Errorf("%s: no %s: pull the catalog first", dir, catalogVersionFile)
	}
	base, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return err
	}
	var v core.CatalogView
	if err = api.Do(ctx, "GET", "/catalog?version="+strconv.FormatInt(base, 10), nil, &v); err != nil {
		return err
	}
	local := map[string]string{}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == catalogVersionFile || strings.HasPrefix(filepath.Base(rel), ".") {
			return nil
		}
		c, err := os.ReadFile(p)
		local[filepath.ToSlash(rel)] = string(c)
		return err
	})
	if err != nil {
		return err
	}
	ch := core.CatalogChange{Base: base, Put: map[string]string{}, Note: note}
	for _, d := range v.Documents {
		if _, ok := local[d.Path]; !ok {
			ch.Delete = append(ch.Delete, d.Path)
		}
	}
	for p, c := range local {
		ch.Put[p] = c // unchanged documents are put as they are: the server compares nothing, the result is the same
	}
	var saved struct {
		Version int64 `json:"version"`
	}
	if err = api.Do(ctx, "POST", "/catalog", ch, &saved); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, catalogVersionFile), []byte(strconv.FormatInt(saved.Version, 10)+"\n"), 0o644); err != nil {
		return err
	}
	return printJSON(map[string]any{"version": saved.Version, "put": len(ch.Put), "deleted": len(ch.Delete)})
}
