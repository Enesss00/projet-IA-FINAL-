// Package stats holds the small, exact statistical helpers used to report
// Monte Carlo results: Wilson intervals, order-statistic quantiles and
// distribution-free confidence intervals for the median, and CVaR.
package stats

import (
	"math"
	"sort"
)

// Z95 is the two-sided 95% normal quantile.
const Z95 = 1.959963984540054

// Interval is an estimate with a 95% confidence interval.
type Interval struct {
	P  float64 `json:"p"`
	Lo float64 `json:"lo"`
	Hi float64 `json:"hi"`
}

// Wilson returns the Wilson score interval for k successes out of n trials.
// It is well behaved at p = 0 and p = 1, unlike the Wald interval.
func Wilson(k, n int) Interval {
	if n <= 0 {
		return Interval{0, 0, 1}
	}
	p := float64(k) / float64(n)
	z2 := Z95 * Z95
	nf := float64(n)
	den := 1 + z2/nf
	centre := (p + z2/(2*nf)) / den
	half := Z95 * math.Sqrt(p*(1-p)/nf+z2/(4*nf*nf)) / den
	return Interval{P: p, Lo: math.Max(0, centre-half), Hi: math.Min(1, centre+half)}
}

// Quantile returns the q-quantile of an already sorted slice (type 7,
// linear interpolation). It returns NaN for an empty slice.
func Quantile(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return math.NaN()
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[n-1]
	}
	h := q * float64(n-1)
	i := int(h)
	if i >= n-1 {
		return sorted[n-1]
	}
	return sorted[i] + (h-float64(i))*(sorted[i+1]-sorted[i])
}

// MedianCI returns the median of a sorted slice with a distribution-free 95%
// confidence interval from binomial order statistics.
func MedianCI(sorted []float64) Interval {
	n := len(sorted)
	if n == 0 {
		return Interval{math.NaN(), math.NaN(), math.NaN()}
	}
	half := Z95 * math.Sqrt(float64(n)) / 2
	lo := int(math.Floor(float64(n)/2 - half))
	hi := int(math.Ceil(float64(n)/2 + half))
	lo = max(0, min(n-1, lo))
	hi = max(0, min(n-1, hi))
	return Interval{P: Quantile(sorted, 0.5), Lo: sorted[lo], Hi: sorted[hi]}
}

// MeanCI returns the mean with a normal-approximation 95% interval.
func MeanCI(sum, sumSq float64, n int) Interval {
	if n == 0 {
		return Interval{math.NaN(), math.NaN(), math.NaN()}
	}
	nf := float64(n)
	m := sum / nf
	if n == 1 {
		return Interval{m, m, m}
	}
	v := math.Max(0, (sumSq-nf*m*m)/(nf-1))
	h := Z95 * math.Sqrt(v/nf)
	return Interval{m, m - h, m + h}
}

// SortedCopy returns a sorted copy.
func SortedCopy(x []float64) []float64 {
	c := append([]float64(nil), x...)
	sort.Float64s(c)
	return c
}

// CVaRHigh returns the mean of the worst (highest) alpha fraction of a
// sorted slice, e.g. alpha = 0.05 for CVaR95 of a finishing position.
func CVaRHigh(sorted []float64, alpha float64) float64 {
	n := len(sorted)
	if n == 0 {
		return math.NaN()
	}
	k := int(math.Ceil(alpha * float64(n)))
	k = max(1, min(n, k))
	s := 0.0
	for _, v := range sorted[n-k:] {
		s += v
	}
	return s / float64(k)
}
