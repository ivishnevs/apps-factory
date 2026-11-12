package config

import (
	"strings"
	"testing"
)

// throughput-signals 2.1: an agent without execution.max_concurrent_runs gets its role's work-run limit from
// company.yaml concurrency (default developer 2, manager 2, others 1); max_concurrent_runs overrides it.
func TestRoleConcurrencyDefaults(t *testing.T) {
	root := fixture(t)
	base := mustRead(t, root, "configs/profiles/base.yaml")
	put(t, root, "configs/profiles/base.yaml", strings.Replace(base, "    max_concurrent_runs: 1\n", "", 1))
	c, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	if a := c.Agents["agent"]; a.Spec.Execution.MaxConcurrentRuns != 2 || !a.LimitFromRole {
		t.Fatalf("developer default: %d (from role %v)", a.Spec.Execution.MaxConcurrentRuns, a.LimitFromRole)
	}
	if c.RoleLimit("qa") != 1 || c.RoleLimit("manager") != 2 || c.RoleLimit("") != 1 {
		t.Fatalf("defaults %v", c.Concurrency)
	}
	// The company's own table: listed roles and the default for the rest.
	put(t, root, "configs/company.yaml", "concurrency:\n  developer: 3\n  default: 2\n")
	if c, err = LoadCatalog(root); err != nil {
		t.Fatal(err)
	}
	if a := c.Agents["agent"]; a.Spec.Execution.MaxConcurrentRuns != 3 || c.RoleLimit("qa") != 2 || c.RoleLimit("manager") != 2 {
		t.Fatalf("company table: %d %v", a.Spec.Execution.MaxConcurrentRuns, c.Concurrency)
	}
	// max_concurrent_runs overrides the role.
	put(t, root, "configs/profiles/base.yaml", base)
	if c, err = LoadCatalog(root); err != nil {
		t.Fatal(err)
	}
	if a := c.Agents["agent"]; a.Spec.Execution.MaxConcurrentRuns != 1 || a.LimitFromRole {
		t.Fatalf("override: %d (from role %v)", a.Spec.Execution.MaxConcurrentRuns, a.LimitFromRole)
	}
	for _, bad := range []string{"concurrency:\n  developer: 0\n", "concurrency:\n  developer: 9\n", "concurrency:\n  Bad_Role: 1\n", "concurrency: 2\n"} {
		put(t, root, "configs/company.yaml", bad)
		if _, err = LoadCatalog(root); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
