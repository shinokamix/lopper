package main

import (
	"math"
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
		wall        runs
		maxSlowdown float64
		want        bool
	}{
		{"same", [2]int{10, 10}, runs{around(100), around(100)}, 20, false},
		{"one more git process", [2]int{10, 11}, runs{around(100), around(100)}, 20, true},
		{"fewer git processes", [2]int{10, 9}, runs{around(100), around(100)}, 20, false},
		{"slower within the limit", [2]int{10, 10}, runs{around(100), around(119)}, 20, false},
		{"slower past the limit", [2]int{10, 10}, runs{around(100), around(122)}, 20, true},
		{"slower past the limit, within noise", [2]int{10, 10}, runs{{60 * ms, 100 * ms, 140 * ms, 180 * ms, 220 * ms}, {80 * ms, 120 * ms, 175 * ms, 200 * ms, 230 * ms}}, 20, false},
		{"slower with time unchecked", [2]int{10, 10}, runs{around(100), around(300)}, 0, false},
		{"more git with time unchecked", [2]int{10, 11}, runs{around(100), around(100)}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := result{
				name: "worktrees",
				git:  [2]gitStats{{"status": {processes: tc.git[0]}}, {"status": {processes: tc.git[1]}}},
				wall: tc.wall,
				cpu:  tc.wall,
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
		wall: runs{{1000 * ms, 1200 * ms}, {1100 * ms, 1300 * ms}},
		cpu:  runs{{400 * ms, 500 * ms}, {400 * ms, 500 * ms}},
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
		name: "walk",
		git:  [2]gitStats{{"version": {processes: 1}}, {"version": {processes: 1}}},
		wall: runs{{100 * ms}, {100 * ms}},
		cpu:  runs{{50 * ms}, {50 * ms}},
	}
	linux := report{runner: "Linux", maxSlowdown: 20, results: []result{r}}.markdown()
	macOS := report{runner: "macOS", results: []result{r}}.markdown()
	want := "</details>\n\n### Scan speed on macOS"
	if !strings.Contains(linux+macOS, want) {
		t.Errorf("the second report's heading does not start after a blank line, so GitHub shows it inside the HTML block:\n%s", linux+macOS)
	}
}

func around(center int) []time.Duration {
	c := time.Duration(center) * ms
	return []time.Duration{c - 2*ms, c - ms, c, c + ms, c + 2*ms}
}

func TestReportMarksChangesWithinNoise(t *testing.T) {
	r := result{
		name: "walk",
		wall: runs{around(65), around(78)},
		cpu:  runs{{60 * ms, 64 * ms, 66 * ms, 70 * ms, 140 * ms}, {62 * ms, 65 * ms, 68 * ms, 72 * ms, 80 * ms}},
	}
	got := report{results: []result{r}}.markdown()
	for _, want := range []string{
		"| walk: time | 65 ms | 78 ms | +20% |",
		"| walk: CPU time | 66 ms | 68 ms | +3%, within noise |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
}

func TestReportShowsCPUTimeTooShortToMeasure(t *testing.T) {
	zero := []time.Duration{0, 0, 0, 0, 0}
	idle := result{name: "idle", wall: runs{around(20), around(20)}, cpu: runs{zero, zero}}
	busier := result{name: "busier", wall: runs{around(20), around(20)}, cpu: runs{zero, around(16)}}
	got := report{results: []result{idle, busier}}.markdown()
	for _, want := range []string{
		"| idle: CPU time | 0 ms | 0 ms | no change |",
		"| busier: CPU time | 0 ms | 16 ms | up from 0 ms |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
}

func TestMannWhitneyP(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b []time.Duration
		want float64
	}{
		{"apart", around(3), around(8), 0.0079},
		{"interleaved", []time.Duration{1, 3, 5, 7, 9}, []time.Duration{2, 4, 6, 8, 10}, 0.6905},
		{"partly tied", []time.Duration{15, 15, 15, 31, 31}, []time.Duration{31, 31, 31, 46, 46}, 0.0794},
		{"all tied", []time.Duration{5, 5, 5}, []time.Duration{5, 5, 5}, 1},
	} {
		if got := mannWhitneyP(tc.a, tc.b); math.Abs(got-tc.want) > 0.0005 {
			t.Errorf("%s: p = %.4f, want %.4f", tc.name, got, tc.want)
		}
	}
}
