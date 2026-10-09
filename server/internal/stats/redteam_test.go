package stats

// Red-team attack tests on the statistical helpers.

import (
	"fmt"
	"math"
	"testing"
)

// A confidence interval must contain its point estimate: Lo <= P <= Hi,
// within [0, 1]. At k = n, floating-point rounding gives Hi = 0.9999999999999999 < P = 1.
func TestRedteamWilsonContainsEstimate(t *testing.T) {
	bad := 0
	for n := 1; n <= 5000; n++ {
		for _, k := range []int{0, 1, n / 3, n / 2, n - 1, n} {
			if k < 0 || k > n {
				continue
			}
			iv := Wilson(k, n)
			if !(iv.Lo <= iv.P && iv.P <= iv.Hi && iv.Lo >= 0 && iv.Hi <= 1) {
				if bad < 5 {
					t.Errorf("Wilson(%d, %d) = %+v does not contain its estimate", k, n, iv)
				}
				bad++
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d incoherent intervals in total", bad)
	}
}

// Degenerate inputs must not panic.
func TestRedteamStatsNoPanic(t *testing.T) {
	try := func(name string, f func()) {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic: %v", r)
				}
			}()
			f()
		})
	}
	xs := []float64{1, 2, 3}
	for _, q := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), math.Copysign(0, -1), 1e-300, 1 - 1e-17} {
		try(fmt.Sprintf("Quantile q=%v", q), func() { _ = Quantile(xs, q) })
		try(fmt.Sprintf("CVaRHigh a=%v", q), func() { _ = CVaRHigh(xs, q) })
	}
	try("Wilson(-1,10)", func() { _ = Wilson(-1, 10) })
	try("Wilson(11,10)", func() { _ = Wilson(11, 10) })
	try("Wilson(0,MinInt)", func() { _ = Wilson(0, math.MinInt) })
	try("MeanCI n=-1", func() { _ = MeanCI(0, 0, -1) })
	try("MedianCI nil", func() { _ = MedianCI(nil) })
	try("MedianCI one", func() { _ = MedianCI([]float64{7}) })
}

func TestRedteamMeanCIConstantSample(t *testing.T) {
	for n := 2; n < 3000; n += 7 {
		for _, x := range []float64{1, 3, 7, 19, 21} {
			iv := MeanCI(x*float64(n), x*x*float64(n), n)
			if iv.P != x || iv.Lo > x || iv.Hi < x || iv.Hi-iv.Lo > 1e-6 {
				t.Fatalf("constant sample %v×%d: %+v", x, n, iv)
			}
		}
	}
}
