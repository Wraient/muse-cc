// Package metrics provides tiny aggregation helpers used to compute panel
// statistics (percentiles, rates) from raw store data.
package metrics

import "sort"

// Percentile returns the p-th percentile (0..100) of the given values using
// nearest-rank. An empty input yields 0.
func Percentile(vals []int64, p float64) int64 {
	if len(vals) == 0 {
		return 0
	}
	cp := append([]int64(nil), vals...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	if p <= 0 {
		return cp[0]
	}
	if p >= 100 {
		return cp[len(cp)-1]
	}
	// nearest-rank
	idx := int(float64(len(cp)-1) * p / 100)
	return cp[idx]
}

// Rate returns the per-second rate of `count` events over `secs`.
func Rate(count int64, secs float64) float64 {
	if secs <= 0 {
		return 0
	}
	return float64(count) / secs
}
