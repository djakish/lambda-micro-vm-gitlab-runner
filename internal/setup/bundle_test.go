package setup

import (
	"archive/zip"
	"os"
	"testing"
)

func TestFindRepoRoot(t *testing.T) {
	// The test runs inside internal/setup; walking up must find the module root.
	root := findRepoRoot(mustGetwd())
	if root == "" {
		t.Fatal("findRepoRoot returned empty from inside the repo")
	}
	if !fileExists(root + "/go.mod") {
		t.Errorf("resolved root %q has no go.mod", root)
	}
}

func TestFindRepoRootMissing(t *testing.T) {
	if got := findRepoRoot(t.TempDir()); got != "" {
		t.Errorf("expected empty root for a non-repo dir, got %q", got)
	}
}

func TestZipContext(t *testing.T) {
	root := findRepoRoot(mustGetwd())
	if root == "" {
		t.Skip("not in a repo checkout")
	}
	zipPath, err := zipContext(root)
	if err != nil {
		t.Fatalf("zipContext: %v", err)
	}
	defer os.Remove(zipPath)

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	found := map[string]bool{}
	for _, f := range zr.File {
		found[f.Name] = true
	}
	for _, want := range []string{"Dockerfile", "go.mod", "cmd/microvm-agent/main.go", "internal/agent/agent.go"} {
		if !found[want] {
			t.Errorf("zip missing %q", want)
		}
	}
}
