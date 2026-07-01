package setup

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// findRepoRoot walks up from start looking for the module root (a directory that
// has both go.mod and image/Dockerfile), so the wizard can assemble the image
// build context. Returns "" if not found.
func findRepoRoot(start string) string {
	dir := start
	for {
		if fileExists(filepath.Join(dir, "go.mod")) && fileExists(filepath.Join(dir, "image", "Dockerfile")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// zipContext assembles the MicroVM image build context (Dockerfile + Go source)
// into a temporary zip and returns its path. The caller removes it.
func zipContext(repoRoot string) (string, error) {
	f, err := os.CreateTemp("", "microvm-image-*.zip")
	if err != nil {
		return "", err
	}
	defer f.Close()

	zw := zip.NewWriter(f)

	// The Dockerfile must sit at the archive root.
	if err := addFile(zw, filepath.Join(repoRoot, "image", "Dockerfile"), "Dockerfile"); err != nil {
		return "", err
	}
	if err := addFile(zw, filepath.Join(repoRoot, "go.mod"), "go.mod"); err != nil {
		return "", err
	}
	if sum := filepath.Join(repoRoot, "go.sum"); fileExists(sum) {
		if err := addFile(zw, sum, "go.sum"); err != nil {
			return "", err
		}
	}
	for _, dir := range []string{"internal", "cmd"} {
		if err := addTree(zw, repoRoot, dir); err != nil {
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func addTree(zw *zip.Writer, root, sub string) error {
	base := filepath.Join(root, sub)
	return filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return addFile(zw, path, filepath.ToSlash(rel))
	})
}

func addFile(zw *zip.Writer, srcPath, zipName string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", srcPath, err)
	}
	defer src.Close()
	w, err := zw.Create(zipName)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
