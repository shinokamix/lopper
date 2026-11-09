package main

import (
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
	if r.median(0) == 0 {
		if r.median(1) == 0 {
			return "no change"
		}
		return "up from 0 ms"
	}
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
	all := slices.Sorted(slices.Values(slices.Concat(a, b)))
	twiceRank := map[time.Duration]int{}
	for i := 0; i < len(all); {
		j := i
		for j < len(all) && all[j] == all[i] {
			j++
		}
		twiceRank[all[i]] = i + j + 1
		i = j
	}

	n, total := len(a), len(all)
	most := total * (total + 1)
	ways := make([][]float64, n+1)
	for k := range ways {
		ways[k] = make([]float64, most+1)
	}
	ways[0][0] = 1
	for i, d := range all {
		r := twiceRank[d]
		for k := min(i+1, n); k > 0; k-- {
			for s := most; s >= r; s-- {
				ways[k][s] += ways[k-1][s-r]
			}
		}
	}

	observed := 0
	for _, d := range a {
		observed += twiceRank[d]
	}
	mean := n * (total + 1)
	far := abs(observed - mean)
	var tail, every float64
	for s, w := range ways[n] {
		every += w
		if abs(s-mean) >= far {
			tail += w
		}
	}
	return tail / every
}

func abs(x int) int { return max(x, -x) }
