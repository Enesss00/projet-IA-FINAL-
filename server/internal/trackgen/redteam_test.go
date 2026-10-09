package trackgen

// Red-team attack tests on the circuit generator.

import (
	"encoding/json"
	"math"
	"testing"

	"pitwall/internal/rng"
)

func rtFinite(t *testing.T, seed uint64, what string, xs ...float64) {
	t.Helper()
	for _, x := range xs {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			t.Fatalf("seed %d: non-finite %s", seed, what)
		}
	}
}

func TestRedteamExtremeSeedsTracks(t *testing.T) {
	seeds := []uint64{0, 1, math.MaxUint64, math.MaxUint64 - 1, 1 << 63, (1 << 63) - 1, 0x5851f42d4c957f2d,
		rng.SeedFromString("18446744073709551616"), rng.SeedFromString("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")}
	gen := rng.New(0xbad5eed)
	for i := 0; i < 1500; i++ {
		seeds = append(seeds, gen.Uint64())
	}
	for _, s := range seeds {
		tr := Generate(s)
		if tr == nil {
			t.Fatalf("seed %d: nil track", s)
		}
		b, err := json.Marshal(tr)
		if err != nil {
			t.Fatalf("seed %d: track not JSON-safe: %v", s, err)
		}
		if s < 10 || s > math.MaxUint64-10 {
			b2, _ := json.Marshal(Generate(s))
			if string(b) != string(b2) {
				t.Fatalf("seed %d: not deterministic", s)
			}
		}
		rtFinite(t, s, "scalars", tr.LengthM, tr.BaseLapS, tr.WearFactor, tr.PitLossS, tr.OvertakeEase, tr.FuelPerLapKg, tr.PitEntry, tr.Sectors[0], tr.Sectors[1])
		rtFinite(t, s, "speed", tr.SpeedKmh...)
		for _, p := range append(append([]Point{}, tr.Points...), tr.PitLane...) {
			rtFinite(t, s, "point", p.X, p.Y)
		}
		if tr.Laps < 2 || len(tr.Points) < 3 || len(tr.SpeedKmh) != len(tr.Points) {
			t.Fatalf("seed %d: degenerate track (laps %d, %d points, %d speeds)", s, tr.Laps, len(tr.Points), len(tr.SpeedKmh))
		}
		for _, v := range tr.SpeedKmh {
			if v <= 0 || v > 400 {
				t.Fatalf("seed %d: speed %v km/h", s, v)
			}
		}
		for _, z := range tr.Zones {
			rtFinite(t, s, "zone", z.From, z.To, z.Length)
		}
	}
}

// Generate's last-resort fallback (after 200 failed attempts) calls try()
// with a fixed stream and ignores its ok flag: if that attempt failed too,
// Generate would return nil and every caller would panic. The fallback
// stream must always succeed.
func TestRedteamFallbackStreamAlwaysSucceeds(t *testing.T) {
	for _, s := range []uint64{0, 1, 42, math.MaxUint64, 1 << 63} {
		fb := rng.New(0).Derive(rng.LabelTrack, 0)
		tr, ok := try(&fb, s)
		if !ok || tr == nil {
			t.Fatalf("seed %d: fallback attempt fails (ok=%v): Generate could return nil", s, ok)
		}
	}
}
