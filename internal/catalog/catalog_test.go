package catalog

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A scan of some roots replaces what was kept below them, met again or
// gone, and keeps what other scans met elsewhere.
func TestRememberReplacesOnlyBelowRoots(t *testing.T) {
	dir := t.TempDir()
	c := Catalog{filepath.Join(dir, "cache", "repos.json")}
	code, other := filepath.Join(dir, "code"), filepath.Join(dir, "other")
	gone := filepath.Join(code, "gone", ".git")
	kept := filepath.Join(code, "kept", ".git")
	elsewhere := filepath.Join(other, "repo", ".git")
	if err := c.Remember([]string{dir}, []string{gone, kept, elsewhere}); err != nil {
		t.Fatal(err)
	}

	added := filepath.Join(code, "added", ".git")
	if err := c.Remember([]string{code}, []string{kept, added}); err != nil {
		t.Fatal(err)
	}

	want := []string{added, kept, elsewhere}
	slices.Sort(want)
	if got := c.Known(); !slices.Equal(got, want) {
		t.Errorf("Known() = %v, want %v", got, want)
	}
}

// A file lopper cannot use is no reason to fail: the scan walks as if
// nothing were known, and replaces the file.
func TestKnownIgnoresUnusableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.json")
	c := Catalog{path}
	for name, content := range map[string]string{
		"damaged":       `{"version": 1, "places": [`,
		"other version": `{"version": 2, "places": ["/repo/.git"]}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := c.Known(); got != nil {
			t.Errorf("%s file: Known() = %v, want none", name, got)
		}
	}

	place := filepath.Join(t.TempDir(), ".git")
	if err := c.Remember(nil, []string{place}); err != nil {
		t.Fatal(err)
	}
	if got := c.Known(); !slices.Equal(got, []string{place}) {
		t.Errorf("Known() = %v after Remember, want [%s]", got, place)
	}
}
