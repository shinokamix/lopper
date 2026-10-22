package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The generated trees. walk holds no repository, so a scan of it times the
// walk alone. worktrees holds repositories whose worktrees each carry an
// ignored dependency tree, as real projects do.
const (
	walkDirs    = 300
	walkSubdirs = 10
	walkFiles   = 10
	repos       = 12
	depDirs     = 40
	depFiles    = 25
)

// linked are the worktrees every repository gets. Their mix runs each path
// of inspect: clean, dirty, unpushed and merged by squash.
var linked = []string{"merged", "dirty", "unpushed", "squashed"}

// BenchmarkScan times `lopper scan` as a user runs it. It runs the binary
// that LOPPER_BENCH_BIN names, so CI can time the base branch and a pull
// request on the same trees, or else lopper built from this checkout.
// LOPPER_BENCH_FIXTURE keeps the trees between runs.
func BenchmarkScan(b *testing.B) {
	bin := lopperBinary(b)
	root := fixture(b)
	for _, tc := range []struct {
		name      string
		worktrees int
	}{
		{"walk", 0},
		{"worktrees", repos * len(linked)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			dir := filepath.Join(root, tc.name)
			git := gitCalls(b, bin, root, dir, tc.worktrees)
			var user, sys time.Duration
			for b.Loop() {
				state := scan(b, bin, root, dir, tc.worktrees)
				user += state.UserTime()
				sys += state.SystemTime()
			}
			b.ReportMetric(float64(git), "git/op")
			b.ReportMetric(float64(user.Milliseconds())/float64(b.N), "user-ms/op")
			b.ReportMetric(float64(sys.Milliseconds())/float64(b.N), "sys-ms/op")
		})
	}
}

func scan(b *testing.B, bin, root, dir string, worktrees int, env ...string) *os.ProcessState {
	b.Helper()
	cmd := exec.Command(bin, "scan", "--json", dir)
	cmd.Env = append(os.Environ(), gitEnv(root)...)
	cmd.Env = append(cmd.Env, "LOPPER_NO_CACHE=1", "LOPPER_NO_UPDATE_CHECK=1")
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.Output()
	if err != nil {
		b.Fatalf("lopper scan: %v\n%s", err, out)
	}
	checkScan(b, out, worktrees)
	return cmd.ProcessState
}

// gitCalls counts the git processes one scan starts, through a git on PATH
// that logs each call. Unlike the time, the count does not depend on the
// runner, so a rise is never noise.
func gitCalls(b *testing.B, bin, root, dir string, worktrees int) int {
	b.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		b.Fatal(err)
	}
	tmp := b.TempDir()
	calls := filepath.Join(tmp, "calls")
	wrapper := "#!/bin/sh\necho >>\"$LOPPER_BENCH_GIT_CALLS\"\nexec \"$LOPPER_BENCH_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(tmp, "git"), []byte(wrapper), 0o700); err != nil {
		b.Fatal(err)
	}
	scan(b, bin, root, dir, worktrees,
		"PATH="+tmp+string(os.PathListSeparator)+os.Getenv("PATH"),
		"LOPPER_BENCH_GIT="+git,
		"LOPPER_BENCH_GIT_CALLS="+calls,
	)
	log, err := os.ReadFile(calls)
	if err != nil && !os.IsNotExist(err) {
		b.Fatal(err)
	}
	return strings.Count(string(log), "\n")
}

// checkScan fails a scan that missed worktrees or could not inspect them,
// because its time would mean nothing.
func checkScan(b *testing.B, out []byte, want int) {
	b.Helper()
	var records []struct {
		Path  string `json:"path"`
		Facts struct {
			Errors []string `json:"errors"`
		} `json:"facts"`
	}
	if err := json.Unmarshal(out, &records); err != nil {
		b.Fatalf("parse scan output: %v", err)
	}
	if len(records) != want {
		b.Fatalf("scan found %d worktrees, want %d", len(records), want)
	}
	for _, r := range records {
		if len(r.Facts.Errors) > 0 {
			b.Fatalf("%s: %s", r.Path, strings.Join(r.Facts.Errors, "; "))
		}
	}
}

func lopperBinary(b *testing.B) string {
	b.Helper()
	if bin := os.Getenv("LOPPER_BENCH_BIN"); bin != "" {
		return bin
	}
	bin := filepath.Join(b.TempDir(), "lopper")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		b.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func fixture(b *testing.B) string {
	b.Helper()
	root := os.Getenv("LOPPER_BENCH_FIXTURE")
	if root == "" {
		root = b.TempDir()
	}
	ready := filepath.Join(root, ".ready")
	if _, err := os.Stat(ready); err == nil {
		return root
	}
	// git records absolute paths, so an unfinished fixture cannot be moved
	// aside and has to be removed by hand.
	if entries, _ := os.ReadDir(root); len(entries) > 0 {
		b.Fatalf("%s holds an unfinished fixture: remove it", root)
	}
	if err := os.MkdirAll(filepath.Join(root, "worktrees"), 0o755); err != nil {
		b.Fatal(err)
	}
	config := "[user]\n\tname = lopper\n\temail = bench@lopper.invalid\n[init]\n\tdefaultBranch = main\n"
	writeFile(b, filepath.Join(root, ".gitconfig"), config)

	for i := range walkDirs {
		for j := range walkSubdirs {
			files(b, filepath.Join(root, "walk", fmt.Sprintf("d%03d", i), fmt.Sprintf("s%02d", j)), walkFiles)
		}
	}
	for i := range repos {
		repo(b, root, filepath.Join(root, "worktrees", fmt.Sprintf("repo%02d", i)))
	}
	writeFile(b, ready, "")
	return root
}

func repo(b *testing.B, root, dir string) {
	b.Helper()
	git := func(dir string, args ...string) {
		b.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), gitEnv(root)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	text := func(edits map[int]string) string {
		out := append([]string(nil), lines...)
		for i, l := range edits {
			out[i] = l
		}
		return strings.Join(out, "\n") + "\n"
	}

	git(filepath.Dir(dir), "init", "-q", filepath.Base(dir))
	writeFile(b, filepath.Join(dir, ".gitignore"), "node_modules/\n")
	writeFile(b, filepath.Join(dir, "notes.txt"), text(nil))
	files(b, filepath.Join(dir, "src"), 20)
	git(dir, "add", "-A")
	git(dir, "commit", "-q", "-m", "init")

	path := func(kind string) string { return dir + "-" + kind }
	for _, kind := range linked {
		git(dir, "worktree", "add", "-q", "-b", kind, path(kind))
	}
	writeFile(b, filepath.Join(path("dirty"), "draft.txt"), "draft\n")
	writeFile(b, filepath.Join(path("unpushed"), "notes.txt"), text(map[int]string{50: "work"}))
	git(path("unpushed"), "commit", "-q", "-am", "work")
	// main takes the squashed change with one of its own, so lopper has to
	// merge the file to see that the branch is in main.
	writeFile(b, filepath.Join(path("squashed"), "notes.txt"), text(map[int]string{10: "fix"}))
	git(path("squashed"), "commit", "-q", "-am", "fix")
	writeFile(b, filepath.Join(dir, "notes.txt"), text(map[int]string{10: "fix", 90: "more"}))
	git(dir, "commit", "-q", "-am", "fix and more")

	for _, checkout := range append([]string{dir}, path("merged"), path("dirty"), path("unpushed"), path("squashed")) {
		for k := range depDirs {
			files(b, filepath.Join(checkout, "node_modules", fmt.Sprintf("pkg%02d", k)), depFiles)
		}
	}
}

// gitEnv keeps the runner's git config out of the fixture and of the scan.
func gitEnv(root string) []string {
	return []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(root, ".gitconfig"),
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
	}
}

func files(b *testing.B, dir string, n int) {
	b.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatal(err)
	}
	for i := range n {
		writeFile(b, filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), strings.Repeat("x", 512))
	}
}

func writeFile(b *testing.B, path, content string) {
	b.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		b.Fatal(err)
	}
}
