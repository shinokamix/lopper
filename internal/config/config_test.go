package config

import (
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)        // Unix
	t.Setenv("USERPROFILE", home) // Windows

	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Roots) != 1 || cfg.Roots[0] != home {
		t.Errorf("Roots = %v, want [%s]", cfg.Roots, home)
	}
	if !cfg.SkipPaths[filepath.Join(home, "Library")] || !cfg.SkipPaths[filepath.Join(home, "go", "pkg")] {
		t.Errorf("SkipPaths = %v, want ~/Library and ~/go/pkg", cfg.SkipPaths)
	}
	for _, name := range []string{"Library", "venv", "go", "pkg"} {
		if cfg.SkipNames[name] {
			t.Errorf("SkipNames contains %q: it would hide projects of that name", name)
		}
	}
	if !cfg.SkipNames["node_modules"] {
		t.Error("SkipNames misses node_modules")
	}
}

func TestDefaultWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := Default(); err == nil {
		t.Error("Default() succeeded without a home directory")
	}
}
