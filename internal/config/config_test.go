package config

import (
	"os"
	"path/filepath"
	"slices"
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
	if len(cfg.Roots) == 0 || cfg.Roots[0] != home {
		t.Errorf("Roots = %v, want %s first", cfg.Roots, home)
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

// TestTempDirs checks that the temporary directory the environment names
// is a candidate. Whether it becomes a root is up to TestTempRoots' rules:
// under /tmp, as t.TempDir is on Linux, it does not.
func TestTempDirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tmp")
	t.Setenv("TMPDIR", dir) // Unix
	t.Setenv("TMP", dir)    // Windows
	t.Setenv("TEMP", dir)
	if got := tempDirs(); !slices.Contains(got, dir) {
		t.Errorf("tempDirs() = %v, want it to contain %s", got, dir)
	}
}

func TestTempRoots(t *testing.T) {
	home := realDir(t, t.TempDir())
	out := realDir(t, t.TempDir())
	appTemp := filepath.Join(home, "AppData", "Local", "Temp") // Windows' %TEMP%
	inHome := filepath.Join(home, "tmp")
	inSkippedName := filepath.Join(home, "code", "node_modules", "tmp")
	nested := filepath.Join(out, "sub")
	for _, dir := range []string{appTemp, inHome, inSkippedName, nested} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "link")
	linked := os.Symlink(out, link) == nil // needs a privilege on Windows

	cases := []struct {
		name  string
		temps []string
		want  []string
	}{
		{"outside home", []string{out}, []string{home, out}},
		{"unset or missing", []string{"", filepath.Join(out, "missing")}, []string{home}},
		{"under skipped AppData", []string{appTemp}, []string{home, appTemp}},
		{"under a skipped name", []string{inSkippedName}, []string{home, inSkippedName}},
		{"reached from home", []string{inHome}, []string{home}},
		{"inner first", []string{nested, out}, []string{home, out}},
		{"outer first", []string{out, nested}, []string{home, out}},
	}
	if linked {
		cases = append(cases, struct {
			name  string
			temps []string
			want  []string
		}{"one directory by two paths", []string{link, out}, []string{home, out}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaults(home, tc.temps).Roots; !slices.Equal(got, tc.want) {
				t.Errorf("Roots = %v, want %v", got, tc.want)
			}
		})
	}
}

// realDir resolves symlinks in a test directory: on macOS the temporary
// directory lies under /var, a link to /private/var.
func realDir(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
