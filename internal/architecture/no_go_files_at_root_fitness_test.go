package architecture

// Repo-root layout sensor (fleet rollout 2026-10-17).
//
//   - TestNoGoFilesAtRepoRoot: the repository root holds no .go file at all,
//     production or test. The godog runner and its step definitions live next
//     to the .feature files in features/; every binary lives under cmd/ and
//     every package under internal/. A Go file at the root turns the module
//     path itself into a package, hides the layout the guide documents and was
//     the reason the BDD files used to sit next to go.mod.
//   - TestNoGoFilesAtRepoRootSensorBites proves the sensor fails when a .go
//     file (test or not) is added to the root and passes on a root that only
//     has directories and non-Go files.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// goFilesAtRoot returns the names of the regular files directly in dir whose
// name ends in .go (nothing below the root is looked at), sorted.
func goFilesAtRoot(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

func rootGoFileViolation(name string) string {
	return fmt.Sprintf("ARCHITECTURE VIOLATION (layout rule)\n"+
		"  RULE:    no .go file lives at the repository root\n"+
		"  WHAT:    %s sits next to go.mod\n"+
		"  WHY:     the module root must stay free of Go code: the godog runner and steps belong in features/ "+
		"(package features_test, Paths \".\"), binaries in cmd/<name>/, everything else in internal/.\n"+
		"  FIX:     git mv %s into features/ (BDD) or the matching internal/ or cmd/ package. Do NOT loosen this test.\n",
		name, name)
}

func TestNoGoFilesAtRepoRoot(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected the module root two levels above internal/architecture: %v", err)
	}
	files, err := goFilesAtRoot(root)
	if err != nil {
		t.Fatalf("read repository root: %v", err)
	}
	for _, name := range files {
		t.Errorf("%s", rootGoFileViolation(name))
	}
}

// rootFixture builds a fake repository root: an entry ending in "/" is a
// directory, any other entry a file (its parent directories are created).
func rootFixture(t *testing.T, entries []string) string {
	t.Helper()
	dir := t.TempDir()
	for _, e := range entries {
		path := filepath.Join(dir, filepath.FromSlash(e))
		if strings.HasSuffix(e, "/") {
			if err := os.MkdirAll(path, 0o750); err != nil {
				t.Fatalf("mkdir %s: %v", e, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir for %s: %v", e, err)
		}
		if err := os.WriteFile(path, []byte("package x\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", e, err)
		}
	}
	return dir
}

func TestNoGoFilesAtRepoRootSensorBites(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		want    string
	}{
		{name: "empty root", want: ""},
		{name: "directories and non-Go files only", entries: []string{"go.mod", "Makefile", "README.md", "features/", "cmd/"}, want: ""},
		{name: "Go files below the root are fine", entries: []string{"features/features_test.go", "cmd/facility/main.go"}, want: ""},
		{name: "a production file", entries: []string{"main.go"}, want: "main.go"},
		{name: "a test file", entries: []string{"features_test.go"}, want: "features_test.go"},
		{name: "a directory named like a Go file is not a file", entries: []string{"odd.go/"}, want: ""},
		{name: "several files are all reported in order", entries: []string{"b_test.go", "a.go", "go.mod"}, want: "a.go,b_test.go"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := goFilesAtRoot(rootFixture(t, tc.entries))
			if err != nil {
				t.Fatalf("goFilesAtRoot: %v", err)
			}
			if strings.Join(got, ",") != tc.want {
				t.Fatalf("goFilesAtRoot = %v, want %q", got, tc.want)
			}
		})
	}
}

func TestNoGoFilesAtRepoRootUnreadableRootFails(t *testing.T) {
	if _, err := goFilesAtRoot(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a root that cannot be read must be an error, not a silent pass")
	}
}
