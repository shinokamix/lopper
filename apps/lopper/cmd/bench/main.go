// Command bench times `lopper scan` built from a base commit and from this
// checkout, on the same generated repositories, and counts the git processes
// each scan starts. It prints a Markdown report and exits with 1 when a scan
// starts more git processes, or when its median time grows by more than
// -max-slowdown percent and a Mann–Whitney test tells the two builds apart.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

var errRegressed = errors.New("this change makes a scan slower or start more git processes")

func main() {
	rounds := flag.Int("rounds", 10, "timed scans of each build")
	maxSlowdown := flag.Float64("max-slowdown", 20, "slowdown in percent that fails the run, or 0 to only report time")
	runner := flag.String("name", "", "the runner, for the report title")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: bench [flags] [base]\n\nbase defaults to where HEAD leaves origin/main.")
		flag.PrintDefaults()
	}
	flag.Parse()
	if err := run(flag.Arg(0), report{runner: *runner, rounds: *rounds, maxSlowdown: *maxSlowdown}); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}

func run(base string, rep report) error {
	if rep.rounds < 4 {
		return errors.New("-rounds must be at least 4, the fewest that can tell two builds apart at p < 0.05")
	}
	if base == "" {
		var err error
		if base, err = git("merge-base", "HEAD", "origin/main"); err != nil {
			return err
		}
	}
	base, err := git("rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return err
	}
	if rep.base, err = git("rev-parse", "--short", base); err != nil {
		return err
	}

	work, err := os.MkdirTemp("", "lopper-bench-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	fmt.Fprintf(os.Stderr, "building lopper from %s and from this checkout\n", rep.base)
	baseBin, err := buildBase(work, base)
	if err != nil {
		return err
	}
	headBin, err := build(".", filepath.Join(work, "head"))
	if err != nil {
		return err
	}
	same, err := sameFile(baseBin, headBin)
	if err != nil {
		return err
	}
	if same {
		fmt.Print(rep.nothingToCompare())
		return nil
	}
	fmt.Fprintln(os.Stderr, "generating repositories")
	root := filepath.Join(work, "fixture")
	if err := makeFixture(root); err != nil {
		return err
	}
	for _, c := range cases(root) {
		fmt.Fprintf(os.Stderr, "scanning %s\n", c.name)
		r, err := measure(c, root, [2]string{baseBin, headBin}, rep.rounds)
		if err != nil {
			return err
		}
		rep.results = append(rep.results, r)
	}

	fmt.Print(rep.markdown())
	if rep.failed() {
		return errRegressed
	}
	return nil
}

func buildBase(work, base string) (string, error) {
	src := filepath.Join(work, "src")
	if _, err := git("worktree", "add", "--quiet", "--detach", src, base); err != nil {
		return "", err
	}
	defer func() { _, _ = git("worktree", "remove", "--force", src) }()
	module, err := git("rev-parse", "--show-prefix")
	if err != nil {
		return "", err
	}
	return build(filepath.Join(src, module), filepath.Join(work, "base"))
}

func build(module, out string) (string, error) {
	into := out + string(filepath.Separator)
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", into, "./cmd/lopper")
	cmd.Dir = module
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build in %s: %w\n%s", module, err, out)
	}
	built, err := os.ReadDir(out)
	if err != nil || len(built) != 1 {
		return "", fmt.Errorf("go build in %s left %v in %s: %w", module, built, out, err)
	}
	return filepath.Join(out, built[0].Name()), nil
}

func sameFile(a, b string) (bool, error) {
	x, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	y, err := os.ReadFile(b)
	return bytes.Equal(x, y), err
}

type scanCase struct {
	name      string
	dir       string
	worktrees int
}

func measure(c scanCase, root string, bins [2]string, rounds int) (result, error) {
	r := result{name: c.name}
	for i, bin := range bins {
		g, err := traceGit(bin, root, c)
		if err != nil {
			return r, err
		}
		r.git[i] = g
	}
	for round := range rounds {
		for k := range bins {
			i := (k + round) % len(bins)
			bin := bins[i]
			wall, cpu, err := scan(bin, root, c)
			if err != nil {
				return r, err
			}
			r.wall[i] = append(r.wall[i], wall)
			r.cpu[i] = append(r.cpu[i], cpu)
		}
	}
	return r, nil
}

// traceGit relies on git writing one trace file per process when
// GIT_TRACE2_EVENT names a directory. The file holds the command name in a
// cmd_name event and the seconds since the process started in each t_abs.
func traceGit(bin, root string, c scanCase) (gitStats, error) {
	traces, err := os.MkdirTemp("", "lopper-bench-trace-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(traces)
	if _, _, err := scan(bin, root, c, "GIT_TRACE2_EVENT="+traces); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(traces)
	if err != nil {
		return nil, err
	}
	stats := gitStats{}
	for _, e := range entries {
		name, ran, err := readTrace(filepath.Join(traces, e.Name()))
		if err != nil {
			return nil, err
		}
		cmd := stats[name]
		cmd.processes++
		cmd.time += ran
		stats[name] = cmd
	}
	return stats, nil
}

func readTrace(path string) (string, time.Duration, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	name, seconds := "unknown", 0.0
	for dec := json.NewDecoder(f); ; {
		var event struct {
			Event string  `json:"event"`
			Name  string  `json:"name"`
			TAbs  float64 `json:"t_abs"`
		}
		err := dec.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", 0, fmt.Errorf("%s: %w", path, err)
		}
		if event.Event == "cmd_name" {
			name = event.Name
		}
		seconds = max(seconds, event.TAbs)
	}
	return name, time.Duration(seconds * float64(time.Second)), nil
}

func scan(bin, root string, c scanCase, env ...string) (wall, cpu time.Duration, err error) {
	cmd := exec.Command(bin, "scan", "--json", c.dir) //nolint:gosec // bin is the lopper this run built
	cmd.Env = slices.Concat(os.Environ(), gitEnv(root), []string{"LOPPER_NO_CACHE=1", "LOPPER_NO_UPDATE_CHECK=1"}, env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	if err := cmd.Run(); err != nil {
		return 0, 0, fmt.Errorf("%s scan %s: %w\n%s", bin, c.dir, err, stderr.Bytes())
	}
	wall = time.Since(start)
	cpu = cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()
	return wall, cpu, checkScan(stdout.Bytes(), c.worktrees)
}

func checkScan(out []byte, want int) error {
	var records []struct {
		Path  string `json:"path"`
		Facts struct {
			Errors []string `json:"errors"`
		} `json:"facts"`
	}
	if err := json.Unmarshal(out, &records); err != nil {
		return fmt.Errorf("parse scan output: %w", err)
	}
	if len(records) != want {
		return fmt.Errorf("scan found %d worktrees, want %d", len(records), want)
	}
	for _, r := range records {
		if len(r.Facts.Errors) > 0 {
			return fmt.Errorf("%s: %s", r.Path, strings.Join(r.Facts.Errors, "; "))
		}
	}
	return nil
}

func git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, stderr.Bytes())
	}
	return strings.TrimSpace(string(out)), nil
}
