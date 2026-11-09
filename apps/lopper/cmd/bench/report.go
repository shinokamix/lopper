package main

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

type report struct {
	runner      string
	base        string
	rounds      int
	maxSlowdown float64
	results     []result
}

type result struct {
	name string
	git  [2]gitStats
	wall runs
	cpu  runs
}

type gitStats map[string]gitCommand

type gitCommand struct {
	processes int
	time      time.Duration
}

func (s gitStats) total() gitCommand {
	var all gitCommand
	for _, c := range s {
		all.processes += c.processes
		all.time += c.time
	}
	return all
}

func (c gitCommand) String() string { return fmt.Sprintf("%d in %s", c.processes, inMs(c.time)) }

func (r result) moreGit() bool { return r.git[1].total().processes > r.git[0].total().processes }

func (rep report) tooSlow(r result) bool {
	return rep.maxSlowdown > 0 && r.wall.slowdown() > rep.maxSlowdown && r.wall.differ()
}

func (rep report) failed() bool {
	for _, r := range rep.results {
		if r.moreGit() || rep.tooSlow(r) {
			return true
		}
	}
	return false
}

func (rep report) title() string {
	if rep.runner == "" {
		return "### Scan speed\n\n"
	}
	return "### Scan speed on " + rep.runner + "\n\n"
}

func (rep report) nothingToCompare() string {
	return rep.title() + "lopper builds the same from base " + rep.base + " and from this change, so there is nothing to compare.\n\n"
}

func (rep report) markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s\n\n", rep.title(), rep.verdict())
	fmt.Fprintf(&b, "| | base %s | this change | Change |\n|---|--:|--:|---|\n", rep.base)
	for _, r := range rep.results {
		before, after := r.git[0].total().processes, r.git[1].total().processes
		git := "no change"
		if after != before {
			git = fmt.Sprintf("%+d", after-before)
		}
		if r.moreGit() {
			git += " :x:"
		}
		took := r.wall.change()
		if rep.tooSlow(r) {
			took += " :x:"
		}
		fmt.Fprintf(&b, "| %s: git processes | %d | %d | %s |\n", r.name, before, after, git)
		fmt.Fprintf(&b, "| %s: time | %s | %s | %s |\n", r.name, inMs(r.wall.median(0)), inMs(r.wall.median(1)), took)
		fmt.Fprintf(&b, "| %s: CPU time | %s | %s | %s |\n", r.name, inMs(r.cpu.median(0)), inMs(r.cpu.median(1)), r.cpu.change())
	}
	fmt.Fprintf(&b, "\nEach time is the median of %d runs. CPU time is user plus system time, and on Windows it leaves out the git processes. A change is within noise when a Mann–Whitney test cannot tell the runs of the two builds apart at p < 0.05.\n\n<details><summary>Where the time goes</summary>\n\n", rep.rounds)
	for _, r := range rep.results {
		fmt.Fprintf(&b, "| %s | base | this change |\n|---|--:|--:|\n", r.name)
		fmt.Fprintf(&b, "| scan, fastest to slowest | %s | %s |\n", r.spread(0), r.spread(1))
		fmt.Fprintf(&b, "| git, all commands | %s | %s |\n", r.git[0].total(), r.git[1].total())
		for _, name := range r.commands() {
			fmt.Fprintf(&b, "| git %s | %s | %s |\n", name, r.git[0].describe(name), r.git[1].describe(name))
		}
		b.WriteString("\n")
	}
	b.WriteString("A git row is how many processes ran that command and how long they ran in total. They can run at the same time, so the total can be longer than the scan. It starts when git does, so it leaves out what runs before, such as the xcrun shim of /usr/bin/git on macOS.\n\n</details>\n\n")
	return b.String()
}

func (r result) spread(i int) string {
	return fmt.Sprintf("%d–%s", slices.Min(r.wall[i]).Milliseconds(), inMs(slices.Max(r.wall[i])))
}

func (r result) commands() []string {
	longest := func(name string) time.Duration { return max(r.git[0][name].time, r.git[1][name].time) }
	names := slices.Collect(maps.Keys(r.git[0]))
	for name := range r.git[1] {
		if _, ok := r.git[0][name]; !ok {
			names = append(names, name)
		}
	}
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(cmp.Compare(longest(b), longest(a)), cmp.Compare(a, b))
	})
	return names
}

func (s gitStats) describe(name string) string {
	c, ok := s[name]
	if !ok {
		return "none"
	}
	return c.String()
}

func inMs(d time.Duration) string { return fmt.Sprintf("%d ms", d.Milliseconds()) }

func (rep report) verdict() string {
	var moreGit, tooSlow bool
	for _, r := range rep.results {
		moreGit = moreGit || r.moreGit()
		tooSlow = tooSlow || rep.tooSlow(r)
	}
	limit := fmt.Sprintf("more than %.0f%% slower", rep.maxSlowdown)
	switch {
	case moreGit && tooSlow:
		return ":x: This change starts more git processes, and makes a scan " + limit + "."
	case moreGit:
		return ":x: This change starts more git processes."
	case tooSlow:
		return ":x: This change makes a scan " + limit + "."
	case rep.maxSlowdown > 0:
		return ":white_check_mark: No more git processes, and no scan " + limit + " by more than noise."
	default:
		return ":white_check_mark: No more git processes. Time is not checked on this runner."
	}
}
