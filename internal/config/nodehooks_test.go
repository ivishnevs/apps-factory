package config

import (
	"strings"
	"testing"
)

const iosHooks = `apiVersion: factory/v1
kind: NodeHooks
metadata:
  name: ios
spec:
  on_run_stop: ["xcrun simctl shutdown all || true"]
  on_task_closed: ['xcrun simctl delete "factory-$FACTORY_TASK_ID" || true']
  prune:
    - command: rm -rf ~/Library/Developer/Xcode/DerivedData
      older_than: 72h
  checks:
    - name: simctl
      command: xcrun simctl list devices available
      hint: sudo xcodebuild -runFirstLaunch
      labels: [macos]
`

// TestNodeHooksCatalog: NodeHooks documents load from configs/node-hooks; a node gets the named one (default
// when unnamed), an unknown name is delivered missing; invalid documents are refused.
func TestNodeHooksCatalog(t *testing.T) {
	root := fixture(t)
	put(t, root, "configs/node-hooks/ios.yaml", iosHooks)
	c, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	d := c.NodeHooksFor("ios")
	if d.Missing || d.Name != "ios" || len(d.Spec.OnRunStop) != 1 || d.Spec.Prune[0].OlderThan != "72h" || d.Spec.Checks[0].Labels[0] != "macos" || d.Hash != d.Spec.Hash() || len(d.Hash) != 16 {
		t.Fatalf("%+v", d)
	}
	if d = c.NodeHooksFor(""); !d.Missing || d.Name != DefaultNodeHooks || !d.Spec.Empty() {
		t.Fatalf("%+v", d)
	}
	for name, doc := range map[string]string{
		"bad duration":    strings.Replace(iosHooks, "72h", "three days", 1),
		"empty command":   strings.Replace(iosHooks, `["xcrun simctl shutdown all || true"]`, `[""]`, 1),
		"bad check name":  strings.Replace(iosHooks, "name: simctl", "name: Sim_Ctl", 1),
		"duplicate check": iosHooks + "    - name: simctl\n      command: true\n",
		"wrong kind":      strings.Replace(iosHooks, "kind: NodeHooks", "kind: Agent", 1),
		"unknown field":   iosHooks + "  extra: 1\n",
	} {
		put(t, root, "configs/node-hooks/ios.yaml", doc)
		if _, err = LoadCatalog(root); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
