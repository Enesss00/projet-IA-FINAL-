package stats

import (
	"math"
	"testing"
)

func TestWilson(t *testing.T) {
	iv := Wilson(0, 100)
	if iv.P != 0 || iv.Lo != 0 || iv.Hi <= 0 || iv.Hi > 0.05 {
		t.Fatalf("wilson(0,100)=%+v", iv)
	}
	iv = Wilson(50, 100)
	if math.Abs(iv.Lo-0.4038) > 1e-3 || math.Abs(iv.Hi-0.5962) > 1e-3 {
		t.Fatalf("wilson(50,100)=%+v", iv)
	}
	if iv := Wilson(0, 0); iv.Lo != 0 || iv.Hi != 1 {
		t.Fatal("empty")
	}
	// width shrinks as 1/sqrt(n)
	w1 := Wilson(250, 1000)
	w2 := Wilson(4000, 16000)
	r := (w1.Hi - w1.Lo) / (w2.Hi - w2.Lo)
	if math.Abs(r-4) > 0.05 {
		t.Fatalf("width ratio %v, want 4", r)
	}
}

func TestQuantile(t *testing.T) {
	x := []float64{1, 2, 3, 4, 5}
	if Quantile(x, 0.5) != 3 || Quantile(x, 0) != 1 || Quantile(x, 1) != 5 || Quantile(x, 0.25) != 2 {
		t.Fatal("quantiles")
	}
	if !math.IsNaN(Quantile(nil, 0.5)) {
		t.Fatal("empty")
	}
	m := MedianCI(x)
	if m.P != 3 || m.Lo > 3 || m.Hi < 3 {
		t.Fatalf("median ci %+v", m)
	}
	if c := CVaRHigh([]float64{1, 2, 3, 4, 10}, 0.2); c != 10 {
		t.Fatalf("cvar %v", c)
	}
	mc := MeanCI(15, 55, 5)
	if mc.P != 3 || mc.Lo >= 3 || mc.Hi <= 3 {
		t.Fatalf("mean ci %+v", mc)
	}
}
