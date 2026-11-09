package main

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"time"
)

type runs [2][]time.Duration

func (r runs) median(i int) time.Duration {
	sorted := slices.Sorted(slices.Values(r[i]))
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func (r runs) slowdown() float64 {
	return 100 * (float64(r.median(1))/float64(r.median(0)) - 1)
}

func (r runs) differ() bool { return mannWhitneyP(r[0], r[1]) < 0.05 }

func (r runs) change() string {
	change := math.Round(r.slowdown())
	switch {
	case change == 0:
		return "no change"
	case r.differ():
		return fmt.Sprintf("%+.0f%%", change)
	default:
		return fmt.Sprintf("%+.0f%%, within noise", change)
	}
}

func mannWhitneyP(a, b []time.Duration) float64 {
	type run struct {
		took  time.Duration
		first bool
	}
	all := make([]run, 0, len(a)+len(b))
	for _, d := range a {
		all = append(all, run{d, true})
	}
	for _, d := range b {
		all = append(all, run{d, false})
	}
	slices.SortFunc(all, func(x, y run) int { return cmp.Compare(x.took, y.took) })

	var rankSum, ties float64
	for i := 0; i < len(all); {
		j := i
		for j < len(all) && all[j].took == all[i].took {
			j++
		}
		rank := float64(i+j+1) / 2
		for _, r := range all[i:j] {
			if r.first {
				rankSum += rank
			}
		}
		t := float64(j - i)
		ties += t*t*t - t
		i = j
	}

	n1, n2 := float64(len(a)), float64(len(b))
	n := n1 + n2
	variance := n1 * n2 / 12 * (n + 1 - ties/(n*(n-1)))
	if variance <= 0 {
		return 1
	}
	u := rankSum - n1*(n1+1)/2
	z := (math.Abs(u-n1*n2/2) - 0.5) / math.Sqrt(variance)
	if z <= 0 {
		return 1
	}
	return math.Erfc(z / math.Sqrt2)
}
