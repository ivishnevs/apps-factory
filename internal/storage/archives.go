package storage

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"appfactory/internal/artifacts"
)

// Images inside archives (artifact-duplicates 1.2): a zip or tar artifact version lists its images, so the Library
// shows them as part of that version without unpacking them into artifacts of their own.

const (
	maxArchiveImages = 1000
	maxArchiveBytes  = 512 << 20
)

var imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true, ".svg": true}

// IsArchive says whether a file name is a zip or tar archive this lists.
func IsArchive(name string) bool {
	n := strings.ToLower(name)
	return strings.HasSuffix(n, ".zip") || strings.HasSuffix(n, ".tar") || strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".tgz")
}

// safeEntry is an archive path that stays inside the archive: relative, no "..", no backslashes.
func safeEntry(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.HasPrefix(p, "__MACOSX/") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// archiveFile copies a blob to a temporary file (zip needs random access); the caller removes it.
func (s *Store) archiveFile(ctx context.Context, company, sha string) (*os.File, int64, error) {
	rc, b, err := s.OpenBlob(ctx, company, sha)
	if err != nil {
		return nil, 0, err
	}
	defer rc.Close()
	if b.Size > maxArchiveBytes {
		return nil, 0, fmt.Errorf("archive larger than %d MiB", maxArchiveBytes>>20)
	}
	f, err := os.CreateTemp("", "factory-archive-*")
	if err != nil {
		return nil, 0, err
	}
	n, err := io.Copy(f, io.LimitReader(rc, maxArchiveBytes+1))
	if err == nil && n > maxArchiveBytes {
		err = fmt.Errorf("archive larger than %d MiB", maxArchiveBytes>>20)
	}
	if err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, 0, err
	}
	return f, n, nil
}

// walkArchive calls fn for each safe regular file of the archive name in f; fn's reader is valid during the call.
func walkArchive(f *os.File, size int64, name string, fn func(p string, size int64, r io.Reader) (stop bool, err error)) error {
	if strings.HasSuffix(strings.ToLower(name), ".zip") {
		z, err := zip.NewReader(f, size)
		if err != nil {
			return err
		}
		for _, e := range z.File {
			if e.FileInfo().IsDir() || !safeEntry(e.Name) {
				continue
			}
			r, err := e.Open()
			if err != nil {
				return err
			}
			stop, err := fn(e.Name, int64(e.UncompressedSize64), r)
			r.Close()
			if err != nil || stop {
				return err
			}
		}
		return nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var src io.Reader = f
	if n := strings.ToLower(name); strings.HasSuffix(n, ".gz") || strings.HasSuffix(n, ".tgz") {
		g, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer g.Close()
		src = g
	}
	t := tar.NewReader(src)
	for {
		h, err := t.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || !safeEntry(strings.TrimPrefix(h.Name, "./")) {
			continue
		}
		if stop, err := fn(strings.TrimPrefix(h.Name, "./"), h.Size, t); err != nil || stop {
			return err
		}
	}
}

// ListArchiveImages records the images of an uploaded archive version on it (best effort: an unreadable archive
// lists none).
func (s *Store) ListArchiveImages(ctx context.Context, company, project string, rec artifacts.Record) (artifacts.Record, error) {
	name := cmpOrStr(rec.Name, path.Base(rec.URI))
	if rec.SHA256 == "" || !IsArchive(name) || len(rec.ArchiveImages) > 0 {
		return rec, nil
	}
	f, size, err := s.archiveFile(ctx, company, rec.SHA256)
	if err != nil {
		return rec, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	var images []artifacts.ArchiveEntry
	err = walkArchive(f, size, name, func(p string, n int64, _ io.Reader) (bool, error) {
		if imageExt[strings.ToLower(path.Ext(p))] {
			images = append(images, artifacts.ArchiveEntry{Path: p, Size: n})
		}
		return len(images) >= maxArchiveImages, nil
	})
	if err != nil || len(images) == 0 {
		return rec, err
	}
	rec.ArchiveImages = images
	b, _ := json.Marshal(rec)
	_, err = s.DB.ExecContext(ctx, "UPDATE artifact_versions SET body=? WHERE company_id=? AND project_id=? AND id=? AND version=?", b, company, project, rec.ID, rec.Version)
	return rec, err
}

// CopyArchiveEntry writes one file of an archive version to out (the Library's preview of an archived image).
func (s *Store) CopyArchiveEntry(ctx context.Context, company, project, id, version, entry string, out io.Writer) error {
	rec, err := s.Artifact(ctx, company, project, id, version)
	if err != nil {
		return err
	}
	name := cmpOrStr(rec.Name, path.Base(rec.URI))
	if rec.SHA256 == "" || !IsArchive(name) || !safeEntry(entry) {
		return ErrNotFound
	}
	f, size, err := s.archiveFile(ctx, company, rec.SHA256)
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	found := false
	err = walkArchive(f, size, name, func(p string, _ int64, r io.Reader) (bool, error) {
		if p != entry {
			return false, nil
		}
		found = true
		_, err := io.Copy(out, io.LimitReader(r, maxArchiveBytes))
		return true, err
	})
	if err == nil && !found {
		return ErrNotFound
	}
	return err
}

func cmpOrStr(v ...string) string {
	for _, s := range v {
		if s != "" && s != "." && s != "/" {
			return s
		}
	}
	return ""
}
