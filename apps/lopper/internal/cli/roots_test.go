package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestDefaultRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)        // Unix
	t.Setenv("USERPROFILE", home) // Windows

	roots, err := defaultRoots()
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) == 0 || roots[0] != home {
		t.Errorf("roots = %v, want %s first", roots, home)
	}
}

func TestDefaultRootsWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := defaultRoots(); err == nil {
		t.Error("defaultRoots() succeeded without a home directory")
	}
}

// TestTempDirs checks that the temporary directory the environment names is
// a candidate. TestTempRoots decides whether it becomes a root. Under /tmp,
// where t.TempDir is on Linux, it does not.
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
	nested := filepath.Join(out, "sub")
	for _, dir := range []string{appTemp, inHome, nested} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	locked := filepath.Join(out, "locked")
	if err := os.Mkdir(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) }) // or t.TempDir cannot remove it
	// Windows ignores the mode bits, and root reads everything.
	unreadable := runtime.GOOS != "windows" && os.Geteuid() != 0
	link := filepath.Join(t.TempDir(), "link")
	linked := os.Symlink(out, link) == nil // needs a privilege on Windows

	cases := []struct {
		name  string
		temps []string
		want  []string
	}{
		{"outside home", []string{out}, []string{home, out}},
		{"unset or missing", []string{"", filepath.Join(out, "missing")}, []string{home}},
		{"reached from home", []string{inHome, appTemp}, []string{home}},
		{"inner first", []string{nested, out}, []string{home, out}},
		{"outer first", []string{out, nested}, []string{home, out}},
	}
	if unreadable {
		cases = append(cases, struct {
			name  string
			temps []string
			want  []string
		}{"unreadable", []string{locked}, []string{home}})
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
			if got := withTemps([]string{home}, tc.temps); !slices.Equal(got, tc.want) {
				t.Errorf("withTemps() = %v, want %v", got, tc.want)
			}
		})
	}
}

// realDir resolves symlinks in a test directory. On macOS the temporary
// directory lies under /var, a link to /private/var.
func realDir(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
