package compare

import (
	"math"
	"sort"
)

// median returns the median of vals (mean of the two middle values for an
// even count). It returns 0 for an empty slice. The input is not modified.
func median(vals []float64) float64 {
	n := len(vals)
	if n == 0 {
		return 0
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// minMax returns the smallest and largest value. It returns 0, 0 when empty.
func minMax(vals []float64) (lo, hi float64) {
	if len(vals) == 0 {
		return 0, 0
	}
	lo, hi = vals[0], vals[0]
	for _, v := range vals[1:] {
		lo = math.Min(lo, v)
		hi = math.Max(hi, v)
	}
	return lo, hi
}

// inRange reports whether v lies within [lo, hi].
func inRange(v, lo, hi float64) bool { return v >= lo && v <= hi }

// rangesOverlap reports whether [alo,ahi] and [blo,bhi] intersect.
func rangesOverlap(alo, ahi, blo, bhi float64) bool {
	return alo <= bhi && blo <= ahi
}
