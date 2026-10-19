package node

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"appfactory/internal/protocol"
)

// TestStackChecks: a check runs only on a node with all its labels; a failing command fails with its stderr
// and hint; the doctor shows the stack checks of the NodeHooks copy in state_dir.
func TestStackChecks(t *testing.T) {
	ctx := context.Background()
	h := Host{Run: func(_ context.Context, name string, args ...string) (string, string, error) {
		if name != "/bin/sh" || args[0] != "-c" {
			t.Fatalf("%s %v", name, args)
		}
		if args[1] == "xcrun simctl list" {
			return "", "xcrun: error: no simctl\nmore", errors.New("exit status 72")
		}
		return "Xcode 26.0\nBuild 1", "", nil
	}}
	checks := []protocol.NodeStackCheck{{Name: "xcode", Command: "xcodebuild -version", Labels: []string{"macos"}},
		{Name: "simctl", Command: "xcrun simctl list", Hint: "sudo xcodebuild -runFirstLaunch"},
		{Name: "adb", Command: "adb devices", Labels: []string{"macos", "android"}}}
	r := StackChecks(ctx, h, []string{"macos"}, checks)
	if len(r) != 3 || r[0] != (CheckResult{Check: "stack.xcode", Status: Pass, Detail: "Xcode 26.0"}) ||
		r[1] != (CheckResult{Check: "stack.simctl", Status: Fail, Detail: "exit status 72: xcrun: error: no simctl", Hint: "sudo xcodebuild -runFirstLaunch"}) ||
		r[2].Check != "stack.adb" || r[2].Status != Skip {
		t.Fatalf("%+v", r)
	}

	dir := t.TempDir()
	if _, ok, err := LoadHooks(dir); ok || err != nil {
		t.Fatal(ok, err)
	}
	d := protocol.NodeHooksDelivery{Name: "ios", Hash: "h", Spec: protocol.NodeHooks{Checks: checks[1:2]}}
	if err := SaveHooks(dir, d); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadHooks(dir)
	if !ok || err != nil || got.Name != "ios" || len(got.Spec.Checks) != 1 {
		t.Fatal(got, ok, err)
	}
	doc := Doctor{Host: h, Stack: got.Spec.Checks}
	var b bytes.Buffer
	if err = Render(&b, StackChecks(ctx, doc.Host, doc.Labels, doc.Stack)); err != nil {
		t.Fatal(err)
	}
	if out := b.String(); !strings.Contains(out, "FAIL    stack.simctl") || !strings.Contains(out, "-> sudo xcodebuild -runFirstLaunch") {
		t.Fatal(out)
	}
}
