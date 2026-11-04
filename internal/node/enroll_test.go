package node

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"appfactory/internal/nodesig"
)

// fakeEnrollCore checks what a real Core checks and answers from a script of statuses.
type fakeEnrollCore struct {
	mu       sync.Mutex
	t        *testing.T
	code     int
	statuses []int
	pub      ed25519.PublicKey
	fp, node string
	polls    int
	active   bool
}

func (f *fakeEnrollCore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodPost && r.URL.Path == "/nodes/enroll" {
		var in map[string]string
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			f.t.Error(err)
		}
		if f.code != 0 {
			http.Error(w, "enrollment code is not valid", f.code)
			return
		}
		sign, box, err := nodesig.DecodePublicKeys(in["sign_pub"], in["box_pub"])
		if err != nil {
			f.t.Error(err)
		}
		proof, _ := hex.DecodeString(in["proof"])
		if !ed25519.Verify(sign, nodesig.EnrollMessage(in["code"], in["node_id"], in["sign_pub"], in["box_pub"]), proof) {
			f.t.Error("proof does not verify")
		}
		f.pub, f.node, f.fp = sign, in["node_id"], nodesig.Fingerprint(sign, box)
		_ = json.NewEncoder(w).Encode(EnrollmentStatus{NodeID: f.node, Company: "acme", Name: "mini", Fingerprint: f.fp, Status: "pending"})
		return
	}
	if _, err := nodesig.Verify(r, f.pub, nil, time.Now()); err != nil || r.URL.Path != "/nodes/"+f.node+"/enrollment" {
		f.t.Error("status request:", r.URL.Path, err)
	}
	f.polls++
	if len(f.statuses) > 0 {
		s := f.statuses[0]
		f.statuses = f.statuses[1:]
		if s != http.StatusOK {
			http.Error(w, "no", s)
			return
		}
	}
	status := "pending"
	if len(f.statuses) == 0 {
		status = "active"
	}
	_ = json.NewEncoder(w).Encode(EnrollmentStatus{NodeID: f.node, Company: "acme", Name: "mini", Fingerprint: f.fp, Status: status})
}

// TestEnrollmentWaitsForApproval (node-identity 1.3): keys are created, the fingerprint is shown, a passing Core
// failure does not end the wait, and the company from the answer is stored.
func TestEnrollmentWaitsForApproval(t *testing.T) {
	f := &fakeEnrollCore{t: t, statuses: []int{200, 503, 200}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	dir := t.TempDir()
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := Enrollment{URL: srv.URL, Code: "fne_x", StateDir: dir, Out: &out, Poll: time.Millisecond}.Run(ctx)
	if err != nil || got.Status != "active" {
		t.Fatal(got, err)
	}
	keys, err := LoadKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), keys.Fingerprint()) || !strings.Contains(out.String(), "approved") {
		t.Fatal(out.String())
	}
	if strings.Contains(out.String(), "fne_x") {
		t.Fatal("code printed")
	}
	if c, err := LoadCompany(dir); err != nil || c != "acme" {
		t.Fatal(c, err)
	}
	if f.polls != 3 {
		t.Fatal(f.polls)
	}
}

// TestEnrollmentRefused: a refused code keeps the identity for the next code; rejected keys end the wait.
func TestEnrollmentRefused(t *testing.T) {
	f := &fakeEnrollCore{t: t, code: http.StatusForbidden}
	srv := httptest.NewServer(f)
	defer srv.Close()
	dir := t.TempDir()
	e := Enrollment{URL: srv.URL, Code: "fne_x", StateDir: dir, Poll: time.Millisecond}
	if _, err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatal(err)
	}
	first, err := LoadKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := LoadCompany(dir); err != nil || c != "" {
		t.Fatal(c, err)
	}

	f.code, f.statuses = 0, []int{http.StatusUnauthorized}
	if _, err = e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "new code") {
		t.Fatal(err)
	}
	second, err := LoadKeys(dir)
	if err != nil || !second.Sign.Equal(first.Sign) {
		t.Fatal("identity changed", err)
	}
}

// TestEnrollmentStops: the wait ends with ctx.
func TestEnrollmentStops(t *testing.T) {
	f := &fakeEnrollCore{t: t, statuses: []int{200, 200, 200, 200, 200, 200, 200, 200, 200, 200}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := (Enrollment{URL: srv.URL, Code: "fne_x", StateDir: t.TempDir(), Poll: time.Hour}).Run(ctx); err == nil {
		t.Fatal("no error")
	}
}

func TestCheckCoreURL(t *testing.T) {
	for _, ok := range []string{"https://core.example", "http://127.0.0.1:8080", "http://localhost:8080"} {
		if err := checkCoreURL(ok); err != nil {
			t.Fatal(ok, err)
		}
	}
	for _, bad := range []string{"http://core.example", "ftp://core.example", "not a url", ""} {
		if err := checkCoreURL(bad); err == nil {
			t.Fatal(bad)
		}
	}
}

func TestLoadCompany(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, CompanyFile), []byte("../evil\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCompany(dir); err == nil {
		t.Fatal("invalid company accepted")
	}
}

// TestClockOffsetCheck (node-identity 2.3): Core refuses signatures beyond its window, so the doctor warns before it.
func TestClockOffsetCheck(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	at := func(d time.Duration) string { return now.Add(d).UTC().Format(http.TimeFormat) }
	if _, bad := clockOffsetCheck(at(30*time.Second), now); bad {
		t.Fatal("small offset reported")
	}
	if c, bad := clockOffsetCheck(at(-90*time.Second), now); !bad || c.Status != Warn {
		t.Fatal(c)
	}
	if c, bad := clockOffsetCheck(at(5*time.Minute), now); !bad || c.Status != Fail || !strings.Contains(c.Hint, "clock") {
		t.Fatal(c)
	}
	if _, bad := clockOffsetCheck("", now); bad {
		t.Fatal("no Date header reported")
	}
}
