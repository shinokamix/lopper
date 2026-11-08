package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const ms = time.Millisecond

func TestReportFails(t *testing.T) {
	for _, tc := range []struct {
		name        string
		git         [2]int
		times       [2][]time.Duration
		maxSlowdown float64
		want        bool
	}{
		{"same", [2]int{10, 10}, [2][]time.Duration{{100 * ms}, {100 * ms}}, 20, false},
		{"one more git process", [2]int{10, 11}, [2][]time.Duration{{100 * ms}, {100 * ms}}, 20, true},
		{"fewer git processes", [2]int{10, 9}, [2][]time.Duration{{100 * ms}, {100 * ms}}, 20, false},
		{"slower within the limit", [2]int{10, 10}, [2][]time.Duration{{100 * ms}, {119 * ms}}, 20, false},
		{"slower past the limit", [2]int{10, 10}, [2][]time.Duration{{100 * ms}, {121 * ms}}, 20, true},
		{"one slow run of three", [2]int{10, 10}, [2][]time.Duration{{100 * ms, 100 * ms, 100 * ms}, {100 * ms, 300 * ms, 105 * ms}}, 20, false},
		{"slower with time unchecked", [2]int{10, 10}, [2][]time.Duration{{100 * ms}, {300 * ms}}, 0, false},
		{"more git with time unchecked", [2]int{10, 11}, [2][]time.Duration{{100 * ms}, {100 * ms}}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := result{
				name:  "worktrees",
				git:   [2]gitStats{{"status": {processes: tc.git[0]}}, {"status": {processes: tc.git[1]}}},
				times: tc.times,
			}
			rep := report{maxSlowdown: tc.maxSlowdown, results: []result{r}}
			if got := rep.failed(); got != tc.want {
				t.Errorf("failed() = %v, want %v\n%s", got, tc.want, rep.markdown())
			}
		})
	}
}

func TestReportShowsEachGitCommand(t *testing.T) {
	r := result{
		name: "worktrees",
		git: [2]gitStats{
			{"status": {processes: 48, time: 900 * ms}},
			{"status": {processes: 48, time: 800 * ms}, "log": {processes: 12, time: 300 * ms}},
		},
		times: [2][]time.Duration{{1000 * ms, 1200 * ms}, {1100 * ms, 1300 * ms}},
	}
	got := report{maxSlowdown: 20, results: []result{r}}.markdown()
	for _, want := range []string{
		"| worktrees: git processes | 48 | 60 | +12 :x: |",
		"| scan, fastest to slowest | 1000–1200 ms | 1100–1300 ms |",
		"| git, all commands | 48 in 900 ms | 60 in 1100 ms |\n| git status | 48 in 900 ms | 48 in 800 ms |\n| git log | none | 12 in 300 ms |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
}

func TestReadTraceOfRealGit(t *testing.T) {
	traces := t.TempDir()
	cmd := exec.Command("git", "version")
	cmd.Env = append(os.Environ(), "GIT_TRACE2_EVENT="+traces)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git version: %v\n%s", err, out)
	}
	files, err := os.ReadDir(traces)
	if err != nil || len(files) != 1 {
		t.Fatalf("want one trace file, got %v, %v", files, err)
	}
	name, ran, err := readTrace(filepath.Join(traces, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if name != "version" || ran <= 0 || ran > time.Minute {
		t.Errorf("readTrace = %q, %v; want version and its run time", name, ran)
	}
}

func TestReportsJoinIntoOneComment(t *testing.T) {
	r := result{
		name:  "walk",
		git:   [2]gitStats{{"version": {processes: 1}}, {"version": {processes: 1}}},
		times: [2][]time.Duration{{100 * ms}, {100 * ms}},
	}
	linux := report{runner: "Linux", maxSlowdown: 20, results: []result{r}}.markdown()
	macOS := report{runner: "macOS", results: []result{r}}.markdown()
	want := "</details>\n\n### Scan speed on macOS"
	if !strings.Contains(linux+macOS, want) {
		t.Errorf("the second report's heading does not start after a blank line, so GitHub shows it inside the HTML block:\n%s", linux+macOS)
	}
}

func TestReportMarksChangesWithinNoise(t *testing.T) {
	for _, tc := range []struct {
		name  string
		times [2][]time.Duration
		want  string
	}{
		{"runs overlap", [2][]time.Duration{{60 * ms, 65 * ms, 140 * ms}, {62 * ms, 70 * ms, 80 * ms}}, "| walk: time | 65 ms | 70 ms | +8%, within noise |"},
		{"runs apart", [2][]time.Duration{{60 * ms, 65 * ms, 66 * ms}, {75 * ms, 78 * ms, 80 * ms}}, "| walk: time | 65 ms | 78 ms | +20% |"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := result{name: "walk", times: tc.times}
			if got := (report{results: []result{r}}).markdown(); !strings.Contains(got, tc.want) {
				t.Errorf("report lacks %q:\n%s", tc.want, got)
			}
		})
	}
}
