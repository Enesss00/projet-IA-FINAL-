package model

import (
	"math"
	"testing"
)

func TestTyreDeltaMonotoneAndCliff(t *testing.T) {
	for c := Compound(0); c < NumCompounds; c++ {
		prev := math.Inf(-1)
		for w := 0.0; w <= 1.6; w += 0.01 {
			d := TyreDeltaS(c, w)
			if d < prev-1e-12 || math.IsNaN(d) {
				t.Fatalf("%v: delta not monotone at w=%v", c, w)
			}
			prev = d
		}
		// The cliff must really be a cliff: the slope past it is much
		// steeper than before it.
		cl := Tyres[c].Cliff
		before := TyreDeltaS(c, cl) - TyreDeltaS(c, cl-0.1)
		after := TyreDeltaS(c, cl+0.3) - TyreDeltaS(c, cl+0.2)
		if after < 4*before {
			t.Fatalf("%v: no cliff (before %v, after %v)", c, before, after)
		}
	}
}

func TestCompoundOrdering(t *testing.T) {
	// Fresh: soft fastest. Long stint: hard ends up fastest.
	if !(TyreDeltaS(Soft, 0) < TyreDeltaS(Medium, 0) && TyreDeltaS(Medium, 0) < TyreDeltaS(Hard, 0)) {
		t.Fatal("fresh pace ordering")
	}
	age := 30
	s := TyreDeltaS(Soft, Wear(Soft, age, 1))
	h := TyreDeltaS(Hard, Wear(Hard, age, 1))
	if h >= s {
		t.Fatal("after 30 laps hards must beat softs")
	}
	if cl := CliffLap(Soft, 1); cl < 12 || cl > 18 {
		t.Fatalf("soft cliff lap %v", cl)
	}
	if cl := CliffLap(Hard, 1); cl < 30 || cl > 40 {
		t.Fatalf("hard cliff lap %v", cl)
	}
}

func TestParseCompound(t *testing.T) {
	cases := []struct {
		in   string
		want Compound
	}{{"s", Soft}, {"MEDIUM", Medium}, {"\th\n", Hard}}
	for _, c := range cases {
		got, err := ParseCompound(c.in)
		if err != nil || got != c.want {
			t.Fatalf("%q -> %v %v", c.in, got, err)
		}
	}
	for _, bad := range []string{"", "X", "INTER", "soft-ish", "\x00"} {
		if _, err := ParseCompound(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if Compound(99).String() != "?" || Compound(99).Name() != "UNKNOWN" {
		t.Fatal("out-of-range compound")
	}
}

func TestDirtyAirAndPass(t *testing.T) {
	if DirtyAirDeltaS(5) != 0 || DirtyAirDeltaS(-1) != 0 {
		t.Fatal("no dirty air far away")
	}
	if math.Abs(DirtyAirDeltaS(0)-DirtyAirS) > 1e-12 {
		t.Fatal("max dirty air at zero gap")
	}
	if PassProbability(2, 1) <= PassProbability(0.5, 1) {
		t.Fatal("bigger advantage, better odds")
	}
	if PassProbability(1, 1.5) <= PassProbability(1, 0.5) {
		t.Fatal("easier track, better odds")
	}
	for _, adv := range []float64{-10, 0, 10, math.NaN()} {
		p := PassProbability(adv, 1)
		if p < 0 || p > 1 || math.IsNaN(p) {
			t.Fatalf("bad probability %v", p)
		}
	}
	if PassProbability(1, 0) != 0 {
		t.Fatal("zero ease")
	}
}

func TestLogisticTable(t *testing.T) {
	for x := -15.0; x <= 15; x += 0.0137 {
		want := 1 / (1 + math.Exp(-x))
		if math.Abs(logistic(x)-want) > 1e-5 {
			t.Fatalf("logistic(%v)=%v want %v", x, logistic(x), want)
		}
	}
	if logistic(math.NaN()) != 0 || logistic(math.Inf(1)) != 1 || logistic(math.Inf(-1)) != 0 {
		t.Fatal("logistic edge cases")
	}
}
