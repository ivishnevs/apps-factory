package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"appfactory/internal/core"
	"appfactory/internal/worker"
)

// skillsCommand is `factory skills ...` (skill-registry 4.6): sources, sync, library, installed skills.
func skillsCommand(ctx context.Context, api worker.API, args []string) error {
	usage := errors.New("usage: skills sources | skills sources add URL [-id ID] [-ref REF] [-subpath DIR] [-access public|node] [-update pinned|track] | skills sources rm ID | skills upload NAME ARCHIVE | skills sync ID | skills library [QUERY] | skills installed [-team TEAM] | skills install SOURCE NAME [-scope team:T|agent:A] | skills uninstall NAME")
	if len(args) < 2 {
		return usage
	}
	method, path := "GET", ""
	var body any
	switch args[1] {
	case "sources":
		switch {
		case len(args) == 2:
			path = "/skills/sources"
		case len(args) >= 4 && args[2] == "add":
			fs := flag.NewFlagSet("add", flag.ContinueOnError)
			p := core.SourceCreate{URL: args[3]}
			fs.StringVar(&p.ID, "id", "", "source id")
			fs.StringVar(&p.Ref, "ref", "", "branch or tag")
			fs.StringVar(&p.Subpath, "subpath", "", "directory of the skills")
			fs.StringVar(&p.Access, "access", "public", "public (Core fetches) or node (a node fetches)")
			fs.StringVar(&p.Update, "update", "pinned", "pinned or track")
			if err := fs.Parse(args[4:]); err != nil {
				return err
			}
			method, path, body = "POST", "/skills/sources", p
		case len(args) == 4 && args[2] == "rm":
			method, path = "DELETE", "/skills/sources/"+url.PathEscape(args[3])
		default:
			return usage
		}
	case "upload":
		if len(args) != 4 {
			return usage
		}
		return uploadSkills(ctx, api, args[2], args[3])
	case "sync":
		if len(args) != 3 {
			return usage
		}
		method, path = "POST", "/skills/sources/"+url.PathEscape(args[2])+"/sync"
	case "library":
		path = "/skills/library"
		if len(args) == 3 {
			path += "?q=" + url.QueryEscape(args[2])
		}
	case "installed":
		fs := flag.NewFlagSet("installed", flag.ContinueOnError)
		team := fs.String("team", "", "only the skills this team's agents may use")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		path = "/skills/installed"
		if *team != "" {
			path += "?team=" + url.QueryEscape(*team)
		}
	case "install", "uninstall":
		p := core.SkillInstall{}
		rest := args[2:]
		if args[1] == "install" {
			if len(rest) < 2 {
				return usage
			}
			p.Source, p.Name, rest = rest[0], rest[1], rest[2:]
		} else {
			if len(rest) != 1 {
				return usage
			}
			p.Name, rest = rest[0], nil
		}
		fs := flag.NewFlagSet("install", flag.ContinueOnError)
		fs.StringVar(&p.Scope, "scope", "", "team:NAME or agent:NAME")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		var cur struct {
			Version int64 `json:"version"`
		}
		if err := api.Do(ctx, "GET", "/catalog", nil, &cur); err != nil {
			return err
		}
		p.Base = cur.Version
		method, path, body = "POST", "/skills/"+args[1], p
	default:
		return usage
	}
	var v any
	if err := api.Do(ctx, method, path, body, &v); err != nil {
		return err
	}
	return printJSON(v)
}

// uploadSkills uploads an archive (zip or tar.gz) as source name.
func uploadSkills(ctx context.Context, api worker.API, name, archive string) error {
	data, err := os.ReadFile(archive)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("name", name)
	fw, err := mw.CreateFormFile("archive", filepath.Base(archive))
	if err != nil {
		return err
	}
	_, _ = fw.Write(data)
	if err = mw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(api.URL, "/")+"/skills/sources/upload", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+api.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return &worker.HTTPError{Status: res.StatusCode, Message: string(out)}
	}
	_, err = os.Stdout.Write(out)
	return err
}
