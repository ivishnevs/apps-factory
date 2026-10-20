package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestCoreContainerHasNoHostMounts (core-owned-project-data 3.1): Compose gives Core named volumes only (its
// data and the blob store, which the image creates for the nonroot user), no workspace bind mount and no
// host/container path mapping; no Core, storage or OpenSpec code opens project files.
func TestCoreContainerHasNoHostMounts(t *testing.T) {
	b, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var compose struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
			Volumes     []any             `yaml:"volumes"`
		} `yaml:"services"`
		Volumes map[string]any `yaml:"volumes"`
	}
	if err = yaml.Unmarshal(b, &compose); err != nil {
		t.Fatal(err)
	}
	core := compose.Services["core"]
	mounts := map[string]bool{}
	for _, v := range core.Volumes {
		s, ok := v.(string) // the long syntax is how a bind mount was declared
		name, target, found := strings.Cut(s, ":")
		if _, named := compose.Volumes[name]; !ok || !found || !named {
			t.Fatalf("Core volume %v is not a named volume", v)
		}
		mounts[target] = true
	}
	if dir := core.Environment["FACTORY_BLOB_DIR"]; dir == "" || !mounts[dir] {
		t.Fatalf("blob store %q not on a named volume %v", dir, mounts)
	}
	for k := range core.Environment {
		if strings.Contains(k, "WORKSPACE") {
			t.Fatal("workspace mapping left:", k)
		}
	}
	docker, _ := os.ReadFile("../../Dockerfile")
	if !strings.Contains(string(docker), "--chown=65532:65532 /blobs /blobs") {
		t.Fatal("image does not prepare /blobs for the nonroot user")
	}
	for _, dir := range []string{"internal/core", "internal/storage", "internal/openspec", "internal/blobstore"} {
		files, _ := filepath.Glob(filepath.Join("../..", dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, _ := os.ReadFile(f)
			for _, banned := range []string{"os.OpenRoot", "FACTORY_WORKSPACE_"} {
				if strings.Contains(string(src), banned) {
					t.Fatalf("%s uses %s", f, banned)
				}
			}
		}
	}
}
