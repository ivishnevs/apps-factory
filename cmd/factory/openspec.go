package main

import (
	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
	"appfactory/internal/worker"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
)

func openSpecCommand(ctx context.Context, api worker.API, args []string) error {
	usage := fmt.Errorf("usage: openspec init PROJECT | openspec list PROJECT [DIR] [QUERY] | openspec read PROJECT PATH [QUERY] | openspec check PROJECT CHANGE | openspec manifest PROJECT | openspec import PROJECT DIR; QUERY e.g. offset=8192&sha256=DIGEST or after=NAME&limit=20")
	if len(args) < 3 {
		return usage
	}
	method, path := "GET", "/projects/"+url.PathEscape(args[2])+"/openspec/"+args[1]
	var body any
	q := url.Values{}
	switch args[1] {
	case "import":
		if len(args) != 4 {
			return usage
		}
		v, err := openSpecImport(ctx, api, args[2], args[3])
		if err != nil {
			return err
		}
		return printJSON(v)
	case "init":
		if len(args) != 3 {
			return usage
		}
		method = "POST"
		body = map[string]string{"request_id": protocol.ID()}
	case "manifest":
		if len(args) != 3 {
			return usage
		}
	case "check":
		if len(args) != 4 {
			return usage
		}
		path += "/" + url.PathEscape(args[3])
	case "list":
		if len(args) > 5 {
			return usage
		}
		if len(args) == 5 {
			var e error
			q, e = url.ParseQuery(args[4])
			if e != nil {
				return e
			}
		}
		if len(args) >= 4 {
			q.Set("dir", args[3])
		}
	case "read":
		if len(args) < 4 || len(args) > 5 {
			return usage
		}
		if len(args) == 5 {
			var e error
			q, e = url.ParseQuery(args[4])
			if e != nil {
				return e
			}
		}
		q.Set("path", args[3])
	default:
		return usage
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var v any
	if e := api.Do(ctx, method, path, body, &v); e != nil {
		return e
	}
	return printJSON(v)
}

// imported is the summary of factory openspec import.
type imported struct {
	Written   []string `json:"written"`
	Unchanged int      `json:"unchanged"`
	Skipped   []string `json:"skipped"`
}

// openSpecImport uploads an OpenSpec directory (the one holding specs/ and changes/) of this machine into the
// project's tree in Core (core-owned-project-data D7): each OpenSpec document whose digest differs from Core's
// current one is written with compare-and-set on that digest; other files are skipped and reported. Nothing
// is deleted in Core.
func openSpecImport(ctx context.Context, api worker.API, project, dir string) (imported, error) {
	out := imported{Written: []string{}, Skipped: []string{}}
	base := "/projects/" + url.PathEscape(project) + "/openspec/"
	var docs []openspec.Doc
	if err := api.Do(ctx, "GET", base+"manifest", nil, &docs); err != nil {
		return out, err
	}
	current := map[string]string{}
	for _, d := range docs {
		current[d.Path] = d.SHA256
	}
	info, err := os.Stat(dir)
	if err != nil {
		return out, err
	}
	if !info.IsDir() {
		return out, fmt.Errorf("%s: OpenSpec directory required", dir)
	}
	type file struct {
		path    string
		content []byte
	}
	var files []file
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if !d.Type().IsRegular() || !openspec.ValidPath(rel) {
			out.Skipped = append(out.Skipped, rel)
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err = openspec.ValidContent(b); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		files = append(files, file{rel, b})
		return nil
	})
	if err != nil {
		return out, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	for _, f := range files {
		if current[f.path] == openspec.Digest(f.content) {
			out.Unchanged++
			continue
		}
		body := map[string]string{"path": f.path, "base_sha256": current[f.path], "content": string(f.content)}
		if err = api.Do(ctx, "POST", base+"write", body, nil); err != nil {
			var h *worker.HTTPError
			if errors.As(err, &h) && h.Status == 409 {
				return out, fmt.Errorf("%s changed in Factory during the import (%s); run the import again", f.path, h.Message)
			}
			return out, fmt.Errorf("%s: %w", f.path, err)
		}
		out.Written = append(out.Written, f.path)
	}
	return out, nil
}
