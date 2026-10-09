package rng

import (
	"math"
	"testing"
)

func TestDeterministic(t *testing.T) {
	a := New(42).Derive(LabelSim, 7)
	b := New(42).Derive(LabelSim, 7)
	for i := 0; i < 1000; i++ {
		if a.Uint64() != b.Uint64() {
			t.Fatal("same seed and path must give the same sequence")
		}
	}
}

func TestDeriveEquivalence(t *testing.T) {
	a := New(9).Derive(1).Derive(2)
	b := New(9).Derive(1, 2)
	if a.State() != b.State() {
		t.Fatal("Derive(a).Derive(b) must equal Derive(a,b)")
	}
}

func TestStreamsDiffer(t *testing.T) {
	seen := map[uint64]bool{}
	root := New(1)
	for i := uint64(0); i < 10000; i++ {
		s := root.Derive(LabelSim, i)
		v := s.Uint64()
		if seen[v] {
			t.Fatalf("collision at %d", i)
		}
		seen[v] = true
	}
}

func TestCopyForks(t *testing.T) {
	a := New(3)
	a.Uint64()
	b := a
	if a.Uint64() != b.Uint64() {
		t.Fatal("copy must fork the stream")
	}
}

func TestUniformMoments(t *testing.T) {
	r := New(5)
	const n = 200000
	var sum, sum2 float64
	for i := 0; i < n; i++ {
		x := r.Float64()
		if x < 0 || x >= 1 {
			t.Fatalf("Float64 out of range: %v", x)
		}
		sum += x
		sum2 += x * x
	}
	mean := sum / n
	vr := sum2/n - mean*mean
	if math.Abs(mean-0.5) > 0.005 || math.Abs(vr-1.0/12) > 0.003 {
		t.Fatalf("uniform moments off: mean=%v var=%v", mean, vr)
	}
}

func TestNormMoments(t *testing.T) {
	r := New(11)
	const n = 400000
	var sum, sum2 float64
	for i := 0; i < n; i++ {
		x := r.Norm()
		if math.IsNaN(x) || math.Abs(x) > normClip {
			t.Fatalf("Norm out of range: %v", x)
		}
		sum += x
		sum2 += x * x
	}
	mean := sum / n
	vr := sum2/n - mean*mean
	if math.Abs(mean) > 0.01 || math.Abs(vr-1) > 0.02 {
		t.Fatalf("normal moments off: mean=%v var=%v", mean, vr)
	}
}

func TestIntN(t *testing.T) {
	r := New(2)
	counts := make([]int, 7)
	for i := 0; i < 70000; i++ {
		v := r.IntN(7)
		if v < 0 || v >= 7 {
			t.Fatalf("IntN out of range %d", v)
		}
		counts[v]++
	}
	for _, c := range counts {
		if c < 9500 || c > 10500 {
			t.Fatalf("IntN not uniform: %v", counts)
		}
	}
	if r.IntN(0) != 0 || r.IntN(-3) != 0 {
		t.Fatal("IntN(<=0) must be 0")
	}
}

func TestInvNormCDF(t *testing.T) {
	cases := map[float64]float64{0.5: 0, 0.975: 1.959964, 0.025: -1.959964, 0.8413447: 1}
	for p, want := range cases {
		if got := InvNormCDF(p); math.Abs(got-want) > 1e-5 {
			t.Errorf("InvNormCDF(%v)=%v want %v", p, got, want)
		}
	}
	if !math.IsInf(InvNormCDF(0), -1) || !math.IsInf(InvNormCDF(1), 1) {
		t.Error("bounds must map to infinities")
	}
}

func TestSeedFromString(t *testing.T) {
	if SeedFromString("42") != 42 {
		t.Fatal("digits must parse")
	}
	a, b := "NIGHT-42", "NIGHT-"+"42"
	if SeedFromString(a) != SeedFromString(b) {
		t.Fatal("hash must be stable")
	}
	if SeedFromString("") == SeedFromString("a") {
		t.Fatal("unexpected collision")
	}
	// 20 digits overflow uint64: must hash, not wrap.
	if SeedFromString("99999999999999999999") == 0 {
		t.Fatal("overflowing digits must hash")
	}
}

func BenchmarkNorm(b *testing.B) {
	r := New(1)
	var s float64
	for i := 0; i < b.N; i++ {
		s += r.Norm()
	}
	_ = s
}
