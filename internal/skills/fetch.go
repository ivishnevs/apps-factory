package skills

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Public sources fetched by Core (skill-registry D3): https only, public addresses only (checked when connecting, so
// a name that resolves to a private address later is refused too), a shallow clone without submodules, LFS or hooks
// (go-git runs no hooks), within time and size limits, into a fresh directory.

// Fetch limits.
var (
	FetchTimeout  = 2 * time.Minute
	MaxSourceSize = int64(64 << 20)
)

// ErrNotPublic refuses a source address that is not public.
var ErrNotPublic = errors.New("source address is not public")

// AllowPrivate lets tests fetch from a local server; never set in Core.
var AllowPrivate = false

// publicDialer refuses connections to loopback, private, link-local and other non-public addresses.
func publicDialer() *net.Dialer {
	return &net.Dialer{Timeout: 10 * time.Second, Control: func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || !AllowPrivate && !publicIP(ip) {
			return fmt.Errorf("%w: %s", ErrNotPublic, host)
		}
		return nil
	}}
}

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() ||
		ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() || ip.Equal(net.IPv4bcast) ||
		ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64) // carrier-grade NAT
}

var installOnce sync.Once

// publicClient is the HTTP client of public fetches: the public dialer, no redirects to other schemes.
func publicClient() *http.Client {
	tr := &http.Transport{DialContext: publicDialer().DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, Proxy: nil}
	return &http.Client{Transport: tr, Timeout: FetchTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" && !AllowPrivate || len(via) > 5 {
			return errors.New("redirect refused")
		}
		return nil
	}}
}

// CheckSourceURL accepts an https URL without credentials or a fragment.
func CheckSourceURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	switch {
	case u.Scheme != "https" && !(AllowPrivate && u.Scheme == "http"):
		return nil, errors.New("source URL must be https")
	case u.User != nil:
		return nil, errors.New("source URL must not carry credentials (private sources are fetched by a node)")
	case u.Host == "" || u.Fragment != "":
		return nil, errors.New("invalid source URL")
	}
	return u, nil
}

// FetchPublic shallow-clones url at ref (a branch, tag or empty for the default branch) into dir and returns the
// commit; the clone is refused when it exceeds MaxSourceSize.
func FetchPublic(ctx context.Context, raw, ref, dir string) (string, error) {
	if _, err := CheckSourceURL(raw); err != nil {
		return "", err
	}
	installOnce.Do(func() {
		c := githttp.NewClient(publicClient())
		client.InstallProtocol("https", c)
		client.InstallProtocol("http", c) // refused by CheckSourceURL outside tests
	})
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	opts := &git.CloneOptions{URL: raw, Depth: 1, SingleBranch: true, NoCheckout: false, RecurseSubmodules: git.NoRecurseSubmodules, Tags: git.NoTags}
	if ref != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(ref)
	}
	repo, err := git.PlainCloneContext(ctx, dir, false, opts)
	if err != nil && ref != "" && errors.Is(err, git.NoMatchingRefSpecError{}) {
		os.RemoveAll(dir)
		opts.ReferenceName = plumbing.NewTagReferenceName(ref)
		repo, err = git.PlainCloneContext(ctx, dir, false, opts)
	}
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", raw, err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	if size, err := treeSize(dir); err != nil {
		return "", err
	} else if size > MaxSourceSize {
		return "", fmt.Errorf("source larger than %d MiB", MaxSourceSize>>20)
	}
	return head.Hash().String(), nil
}

func treeSize(dir string) (int64, error) {
	var n int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" && d.IsDir() {
			return filepath.SkipDir
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			n += info.Size()
		}
		return nil
	})
	return n, err
}

// SourceRoot is dir/subpath, refusing a subpath that leaves dir.
func SourceRoot(dir, subpath string) (string, error) {
	subpath = strings.Trim(filepath.ToSlash(subpath), "/")
	if subpath == "" {
		return dir, nil
	}
	return inside(dir, subpath)
}

// MaxSourceFiles bounds the files of a source tree a node uploads or an owner uploads.
const MaxSourceFiles = 20000

// PackTree tars the regular files of a source tree (a node's clone; .git and symlinks are left out) for Core.
func PackTree(dir string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	var total int64
	n := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil // directories, symlinks
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if total += info.Size(); total > MaxSourceSize || n >= MaxSourceFiles {
			return fmt.Errorf("source larger than %d MiB or %d files", MaxSourceSize>>20, MaxSourceFiles)
		}
		n++
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err = tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: 0o644, Size: int64(len(b)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
			return err
		}
		_, err = tw.Write(b)
		return err
	})
	if err == nil {
		err = tw.Close()
	}
	return buf.Bytes(), err
}

// UnpackTree extracts a source tree (PackTree's tar, or an uploaded tar) into dst: regular files only, inside dst,
// within the source limits.
func UnpackTree(r io.Reader, dst string) error {
	tr := tar.NewReader(r)
	var total int64
	n := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if err = writeTreeFile(dst, h.Name, h.Typeflag == tar.TypeReg, h.Size, io.LimitReader(tr, h.Size), &total, &n); err != nil {
			return err
		}
	}
}

func writeTreeFile(dst, name string, regular bool, size int64, r io.Reader, total *int64, n *int) error {
	clean := path.Clean(strings.TrimPrefix(filepath.ToSlash(name), "./"))
	switch {
	case !regular:
		return nil // symlinks and devices are left out
	case clean == "." || strings.HasPrefix(clean, "../") || path.IsAbs(clean):
		return fmt.Errorf("archive entry %s leaves the source", name)
	}
	if *total += size; *total > MaxSourceSize || *n >= MaxSourceFiles {
		return fmt.Errorf("source larger than %d MiB or %d files", MaxSourceSize>>20, MaxSourceFiles)
	}
	*n++
	p := filepath.Join(dst, filepath.FromSlash(clean))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(r, size))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// UnpackUpload extracts an uploaded archive (zip, tar.gz or tar) into dst with UnpackTree's rules.
func UnpackUpload(data []byte, dst string) error {
	switch {
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		var total int64
		n := 0
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			err = writeTreeFile(dst, f.Name, f.Mode().IsRegular(), int64(f.UncompressedSize64), rc, &total, &n)
			rc.Close()
			if err != nil {
				return err
			}
		}
		return nil
	case bytes.HasPrefix(data, []byte{0x1f, 0x8b}):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		defer gz.Close()
		return UnpackTree(io.LimitReader(gz, MaxSourceSize*2), dst)
	}
	return UnpackTree(bytes.NewReader(data), dst)
}

// WriteUploadedFile adds one file of an uploaded folder (its relative path) to dst.
func WriteUploadedFile(dst, name string, data []byte, total *int64, n *int) error {
	return writeTreeFile(dst, name, true, int64(len(data)), bytes.NewReader(data), total, n)
}
