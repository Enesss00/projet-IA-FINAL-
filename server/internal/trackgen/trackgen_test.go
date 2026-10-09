package trackgen

import (
	"encoding/json"
	"math"
	"testing"
)

func TestDeterministic(t *testing.T) {
	a, _ := json.Marshal(Generate(42))
	b, _ := json.Marshal(Generate(42))
	if string(a) != string(b) {
		t.Fatal("same seed must give the same track")
	}
}

func TestBoundsManySeeds(t *testing.T) {
	names := map[string]bool{}
	for s := uint64(0); s < 300; s++ {
		tr := Generate(s)
		names[tr.Name] = true
		check := func(cond bool, msg string, v any) {
			if !cond {
				t.Fatalf("seed %d: %s (%v)", s, msg, v)
			}
		}
		check(tr.LengthM >= 3900 && tr.LengthM <= 6300, "length", tr.LengthM)
		check(tr.Laps >= 38 && tr.Laps <= 66, "laps", tr.Laps)
		check(tr.BaseLapS >= 60 && tr.BaseLapS <= 125, "base lap", tr.BaseLapS)
		check(tr.WearFactor >= 0.75 && tr.WearFactor <= 1.35, "wear", tr.WearFactor)
		check(tr.PitLossS >= 16 && tr.PitLossS <= 27, "pit loss", tr.PitLossS)
		check(tr.OvertakeEase >= 0.4 && tr.OvertakeEase <= 1.6, "overtake", tr.OvertakeEase)
		np := len(tr.Points)
		check(np == len(tr.TimeFrac), "time array", np)
		check(np == len(tr.DistFrac), "dist array", np)
		check(len(tr.Corners) >= 4, "corners", len(tr.Corners))
		check(len(tr.PitLane) > 3, "pit lane", len(tr.PitLane))
		for i := 1; i < len(tr.TimeFrac); i++ {
			check(tr.TimeFrac[i] > tr.TimeFrac[i-1] && tr.DistFrac[i] > tr.DistFrac[i-1], "fractions monotonic", i)
		}
		for _, p := range tr.Points {
			check(p.X >= 0 && p.X <= 1 && p.Y >= 0 && p.Y <= 1 && !math.IsNaN(p.X), "normalised", p)
		}
		check(tr.Sectors[0] > 0 && tr.Sectors[0] < tr.Sectors[1] && tr.Sectors[1] < 1, "sectors", tr.Sectors)
	}
	if len(names) < 100 {
		t.Fatalf("names not varied enough: %d", len(names))
	}
}

func FuzzGenerate(f *testing.F) {
	f.Add(uint64(0))
	f.Add(uint64(1<<63 + 12345))
	f.Fuzz(func(t *testing.T, s uint64) {
		tr := Generate(s)
		if tr == nil || tr.Laps <= 0 || math.IsNaN(tr.BaseLapS) {
			t.Fatalf("invalid track for seed %d", s)
		}
	})
}
