package config

import (
	"os"
	"strings"
	"testing"
)

// loadStored loads company's catalog from docs over the factory root at root.
func loadStored(t *testing.T, root, company string, docs map[string]string) (*Catalog, error) {
	t.Helper()
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fsys, err := CatalogFS(r.FS(), company, docs)
	if err != nil {
		t.Fatal(err)
	}
	return LoadCompanyCatalogFS(fsys, root, company)
}

// TestSeedRoundTrip (company-self-service 3.3): the seed of the repository's catalogs loads as a stored catalog
// to the same agents and instructions as the disk catalog.
func TestSeedRoundTrip(t *testing.T) {
	for _, company := range []string{DefaultCompany, "smoke"} {
		disk, err := LoadCompanyCatalog("../..", company)
		if err != nil {
			t.Fatal(err)
		}
		docs, err := SeedDocuments("../..", company)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := loadStored(t, "../..", company, docs)
		if err != nil {
			t.Fatal(company, err)
		}
		if len(stored.Agents) != len(disk.Agents) || len(stored.NodeHooks) != len(disk.NodeHooks) || len(stored.Teams) != len(disk.Teams) {
			t.Fatalf("%s: %d/%d agents", company, len(stored.Agents), len(disk.Agents))
		}
		for name, a := range disk.Agents {
			if stored.Agents[name].Instructions != a.Instructions {
				t.Fatalf("%s/%s: instructions differ", company, name)
			}
		}
	}
}

// TestStoredCatalogOwnsItsSections: inside the company's sections only documents count (a file on disk that
// the version does not hold is gone); a stored prompt overrides the disk's; invalid paths are refused.
func TestStoredCatalogOwnsItsSections(t *testing.T) {
	root := fixture(t)
	docs, err := SeedDocuments(root, DefaultCompany)
	if err != nil {
		t.Fatal(err)
	}
	// An agent added on disk after the seed is not part of the stored version.
	for p, c := range docs {
		if strings.HasPrefix(p, "configs/agents/") {
			name := strings.TrimSuffix(strings.TrimPrefix(p, "configs/agents/"), ".yaml")
			put(t, root, "configs/agents/disk-only.yaml", strings.Replace(strings.Replace(c, "name: "+name, "name: disk-only", 1), "spec:\n", "spec:\n  reports_to: "+name+"\n", 1))
			break
		}
	}
	disk, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	docs["prompts/agent.md"] = "STORED"
	c, err := loadStored(t, root, DefaultCompany, docs)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := disk.Agents["disk-only"]; !ok || len(c.Agents) != len(disk.Agents)-1 {
		t.Fatalf("%d agents, disk %d", len(c.Agents), len(disk.Agents))
	}
	for name, a := range c.Agents {
		if !strings.HasPrefix(a.Instructions, "STORED") {
			t.Fatal(name, a.Instructions)
		}
	}
	for _, p := range []string{"configs/prices.yaml", "configs/smoke/agents/x.yaml", "configs/agents/sub/x.yaml", "../x.md", "skills/x/SKILL.md", "prompts/x.txt", "configs/agents/x.yml"} {
		if ValidCatalogPath(DefaultCompany, p) == nil {
			t.Fatal("accepted", p)
		}
	}
	for _, p := range []string{"configs/smoke/agents/x.yaml", "configs/smoke/company.yaml", "prompts/team/x.md"} {
		if err := ValidCatalogPath("smoke", p); err != nil {
			t.Fatal(err)
		}
	}
	if ValidCatalogDocuments(DefaultCompany, map[string]string{"prompts/a.md": strings.Repeat("x", MaxCatalogDocument+1)}) == nil {
		t.Fatal("oversized document accepted")
	}
}

// TestTemplatesNotACompany: "templates" stays a reserved company name.
func TestTemplatesNotACompany(t *testing.T) {
	if _, err := CompanyDir("templates"); err == nil {
		t.Fatal("company named templates accepted")
	}
}
