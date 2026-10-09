package race

import (
	"math"
	"testing"

	"pitwall/internal/model"
	"pitwall/internal/rng"
)

func mustScenario(t testing.TB, seed uint64, cars int) *Scenario {
	t.Helper()
	sc, err := NewScenario(seed, Options{Cars: cars})
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func oneStop(laps int) Strategy {
	return Strategy{Name: "1S", Start: model.Medium, Stops: []Stop{{Lap: laps / 2, Compound: model.Hard}}}
}

// checkInvariants verifies the properties that must hold after every lap.
func checkInvariants(t *testing.T, st *State, prev *State) {
	t.Helper()
	seen := [MaxCars]bool{}
	for p := 0; p < st.N; p++ {
		c := st.Order[p]
		if int(c) >= st.N || seen[c] {
			t.Fatalf("lap %d: order is not a permutation: %v", st.Lap, st.Order[:st.N])
		}
		seen[c] = true
	}
	retiredSeen := false
	for p := 0; p < st.N; p++ {
		c := int(st.Order[p])
		if st.Status[c] == Retired {
			retiredSeen = true
		} else if retiredSeen {
			t.Fatalf("lap %d: running car behind a retired car", st.Lap)
		}
	}
	for c := 0; c < st.N; c++ {
		if st.Fuel[c] < 0 || math.IsNaN(st.Fuel[c]) {
			t.Fatalf("negative fuel car %d", c)
		}
		if st.Time[c] < 0 || math.IsNaN(st.Time[c]) || math.IsInf(st.Time[c], 0) {
			t.Fatalf("bad time car %d: %v", c, st.Time[c])
		}
		if st.Status[c] != Running {
			continue
		}
		if st.Lap > 0 && st.LastLap[c] <= 0 {
			t.Fatalf("non-positive lap time car %d: %v", c, st.LastLap[c])
		}
		if prev != nil {
			if st.Time[c] <= prev.Time[c] {
				t.Fatalf("time not increasing car %d", c)
			}
			if st.Pits[c] > prev.Pits[c]+1 {
				t.Fatalf("car %d pitted twice in lap %d", c, st.Lap)
			}
		}
	}
	// running cars are sorted by time
	for p := 1; p < st.N; p++ {
		a, b := int(st.Order[p-1]), int(st.Order[p])
		if st.Status[a] == Running && st.Status[b] == Running && st.Time[a] > st.Time[b] {
			t.Fatalf("lap %d: classification not sorted by time", st.Lap)
		}
	}
}

func TestPropertiesManySeeds(t *testing.T) {
	for seed := uint64(0); seed < 40; seed++ {
		for _, cars := range []int{1, 2, 10, 20} {
			sc := mustScenario(t, seed, cars)
			for sim := uint64(0); sim < 5; sim++ {
				st := sc.NewState(rng.New(seed).Derive(rng.LabelSim, sim), oneStop(sc.Laps()))
				checkInvariants(t, &st, nil)
				for !st.Done() {
					prev := st
					sc.Step(&st, nil)
					checkInvariants(t, &st, &prev)
				}
				if st.Lap != sc.Laps() {
					t.Fatal("race did not reach the flag")
				}
			}
		}
	}
}

func TestRivalStrategiesValid(t *testing.T) {
	for seed := uint64(0); seed < 300; seed++ {
		sc := mustScenario(t, seed, 20)
		for i, s := range sc.Rivals {
			if err := s.Validate(sc.Laps()); err != nil {
				t.Fatalf("seed %d car %d: %v (%s)", seed, i, err, s)
			}
		}
	}
}

func TestStepAfterFinishIsNoop(t *testing.T) {
	sc := mustScenario(t, 1, 10)
	st := sc.NewState(rng.New(1), oneStop(sc.Laps()))
	sc.Run(&st, nil)
	snap := st
	sc.Step(&st, nil)
	if snap != st {
		t.Fatal("stepping a finished race must not change it")
	}
}

func TestSnapshotReplay(t *testing.T) {
	sc := mustScenario(t, 7, 20)
	st := sc.NewState(rng.New(7).Derive(rng.LabelSim, 3), oneStop(sc.Laps()))
	for i := 0; i < 10; i++ {
		sc.Step(&st, nil)
	}
	snap := st
	a, b := st, snap
	sc.Run(&a, nil)
	sc.Run(&b, nil)
	if a != b {
		t.Fatal("re-running a snapshot must reproduce the same race")
	}
}

func TestDeterministicEvents(t *testing.T) {
	sc := mustScenario(t, 99, 20)
	run := func() []Event {
		st := sc.NewState(rng.New(99), oneStop(sc.Laps()))
		var log Log
		sc.Run(&st, &log)
		return log.Events
	}
	a, b := run(), run()
	if len(a) != len(b) || len(a) == 0 {
		t.Fatalf("event logs differ in length: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("event %d differs", i)
		}
	}
}

// Analytic case: a single car with no variability. Its race time must equal,
// exactly, the sum of the model lap times computed independently here.
func TestSingleCarAnalytic(t *testing.T) {
	sc, err := NewScenario(5, Options{Cars: 1, NoVariability: true})
	if err != nil {
		t.Fatal(err)
	}
	tr := sc.Track
	strat := Strategy{Start: model.Soft, Stops: []Stop{{Lap: 12, Compound: model.Hard}, {Lap: 30, Compound: model.Medium}}}
	if err := strat.Validate(tr.Laps); err != nil {
		t.Fatal(err)
	}
	st := sc.NewState(rng.New(1), strat)
	sc.Run(&st, nil)

	d := sc.Drivers[0]
	fuel := float64(tr.Laps)*tr.FuelPerLapKg + model.FuelMarginKg
	comp, age, total := strat.Start, 0, 0.0
	next := 0
	for lap := 1; lap <= tr.Laps; lap++ {
		w := float64(age) * model.Tyres[comp].WearPerLap * tr.WearFactor * d.TyreSave
		lt := tr.BaseLapS + d.PaceS + model.TyreDeltaS(comp, w) + fuel*model.FuelSecPerKg
		if lap == 1 {
			lt += model.StartPenaltyS
		}
		if next > 0 && strat.Stops[next-1].Lap == lap-1 {
			lt += model.Tyres[comp].WarmupS
		}
		fuel = math.Max(0, fuel-tr.FuelPerLapKg)
		if next < len(strat.Stops) && strat.Stops[next].Lap == lap {
			lt += tr.PitLossS + model.PitStationaryS
			comp = strat.Stops[next].Compound
			age = 0
			next++
		} else {
			age++
		}
		total += lt
	}
	if math.Abs(st.Time[0]-total) > 1e-9 {
		t.Fatalf("race time %v, analytic %v", st.Time[0], total)
	}
	if st.Pits[0] != 2 {
		t.Fatalf("pits %d", st.Pits[0])
	}
}

// Without variability and with identical cars, a car on the better strategy
// must finish ahead: sanity of the strategic signal.
func TestCliffHurts(t *testing.T) {
	sc, _ := NewScenario(3, Options{Cars: 1, NoVariability: true})
	laps := sc.Laps()
	good := Strategy{Start: model.Medium, Stops: []Stop{{Lap: laps / 2, Compound: model.Hard}}}
	bad := Strategy{Start: model.Soft, Stops: []Stop{{Lap: laps - 2, Compound: model.Hard}}} // soft way past its cliff
	a := sc.NewState(rng.New(1), good)
	b := sc.NewState(rng.New(1), bad)
	sc.Run(&a, nil)
	sc.Run(&b, nil)
	if a.Time[0] >= b.Time[0] {
		t.Fatalf("running softs past the cliff should cost time: %v vs %v", a.Time[0], b.Time[0])
	}
}

func TestScenarioBounds(t *testing.T) {
	for _, n := range []int{0, -1, 21, 1000} {
		if _, err := NewScenario(1, Options{Cars: n}); err == nil {
			t.Fatalf("grid size %d accepted", n)
		}
	}
	sc := mustScenario(t, 1, 20)
	codes := map[string]bool{}
	for _, d := range sc.Drivers {
		if codes[d.Code] {
			t.Fatalf("duplicate code %s", d.Code)
		}
		codes[d.Code] = true
	}
}

func BenchmarkRace20x50(b *testing.B) {
	sc := mustScenario(b, 1, 20)
	sc.env.laps = 50
	s := oneStop(50)
	root := rng.New(1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st := sc.NewState(root.Derive(uint64(i)), s)
		st.Laps = 50
		sc.Run(&st, nil)
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "races/s")
}

func TestJitterKeepsPlansValid(t *testing.T) {
	r := rng.New(4)
	for laps := 3; laps < 70; laps++ {
		for k := 0; k < 200; k++ {
			base := baselineStrategy(&r, laps, 0.75+r.Float64()*0.6)
			p := base.ToPlan()
			jitter(&r, &p, laps)
			s := Strategy{Start: base.Start}
			for i := 0; i < int(p.N); i++ {
				s.Stops = append(s.Stops, p.Stops[i])
			}
			if err := s.Validate(laps); err != nil {
				t.Fatalf("laps %d: %v (%s -> %s)", laps, err, base, s)
			}
		}
	}
}

func TestParseStrategy(t *testing.T) {
	s, err := ParseStrategy("S-14-M-35-H")
	if err != nil || s.Start != model.Soft || len(s.Stops) != 2 || s.Stops[1].Lap != 35 || s.String() != "S-14-M-35-H" {
		t.Fatalf("%+v %v", s, err)
	}
	for _, bad := range []string{"", "M-", "M-x-H", "Q", "M-12", "M-12-Q", "M--H", "M-99999-H", "M-1-H-2-S-3-M-4-H-5-S-6-M"} {
		if _, err := ParseStrategy(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func FuzzValidateStrategy(f *testing.F) {
	f.Add(uint8(1), 20, uint8(2), 40, uint8(0), 50)
	f.Add(uint8(9), -1, uint8(255), 0, uint8(1), 0)
	f.Fuzz(func(t *testing.T, start uint8, l1 int, c1 uint8, l2 int, c2 uint8, laps int) {
		s := Strategy{Start: model.Compound(start), Stops: []Stop{{l1, model.Compound(c1)}, {l2, model.Compound(c2)}}}
		err := s.Validate(laps)
		if err != nil {
			return
		}
		// a strategy accepted by Validate must run without breaking invariants
		if laps > 80 {
			return
		}
		sc, e := NewScenario(1, Options{Cars: 4})
		if e != nil {
			t.Fatal(e)
		}
		if laps != sc.Laps() {
			return
		}
		st := sc.NewState(rng.New(1), s)
		sc.Run(&st, nil)
		if st.Pits[sc.Player] > 2 {
			t.Fatal("more pits than planned")
		}
	})
}

func FuzzParseStrategy(f *testing.F) {
	for _, s := range []string{"M-23-H", "S-14-M-35-H", "", "-", "H-0-S", "M-1-M"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		s, err := ParseStrategy(in)
		if err != nil {
			return
		}
		_ = s.Validate(50)
		_ = s.String()
	})
}
