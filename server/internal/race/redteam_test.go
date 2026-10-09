package race

// Red-team attack tests on the race engine and strategy parsing.

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"pitwall/internal/model"
	"pitwall/internal/rng"
)

var rtExtremeSeeds = []uint64{0, 1, math.MaxUint64, math.MaxUint64 - 1, 1 << 63, (1 << 63) - 1,
	rng.SeedFromString("18446744073709551616"), rng.SeedFromString("-"), 0x5851f42d4c957f2d}

// boundary strategies for a race of `laps` laps (laps >= 7)
func rtBoundaryStrategies(laps int) []Strategy {
	return []Strategy{
		{Name: "lap1", Start: model.Medium, Stops: []Stop{{Lap: 1, Compound: model.Hard}}},
		{Name: "last", Start: model.Medium, Stops: []Stop{{Lap: laps - 1, Compound: model.Soft}}},
		{Name: "5 early", Start: model.Soft, Stops: []Stop{{1, model.Medium}, {2, model.Hard}, {3, model.Soft}, {4, model.Medium}, {5, model.Hard}}},
		{Name: "5 late", Start: model.Hard, Stops: []Stop{{laps - 5, model.Soft}, {laps - 4, model.Soft}, {laps - 3, model.Soft}, {laps - 2, model.Soft}, {laps - 1, model.Soft}}},
		{Name: "no stop never", Start: model.Hard, Stops: []Stop{{laps / 2, model.Hard}, {laps/2 + 1, model.Medium}}},
	}
}

func TestRedteamEveryGridSizeExtremeSeeds(t *testing.T) {
	for _, seed := range rtExtremeSeeds {
		for cars := MinCars; cars <= MaxCars; cars++ {
			sc, err := NewScenario(seed, Options{Cars: cars})
			if err != nil {
				t.Fatalf("seed %d cars %d: %v", seed, cars, err)
			}
			if sc.N() != cars || len(sc.Grid) != cars || len(sc.Drivers) != cars || len(sc.Rivals) != cars || sc.Player >= cars {
				t.Fatalf("seed %d cars %d: incoherent scenario", seed, cars)
			}
			codes := map[string]bool{}
			for _, d := range sc.Drivers {
				if codes[d.Code] || utf8.RuneCountInString(d.Code) != 3 {
					t.Fatalf("seed %d cars %d: driver code %q duplicated or malformed", seed, cars, d.Code)
				}
				codes[d.Code] = true
			}
			if _, err := json.Marshal(sc); err != nil {
				t.Fatalf("scenario not JSON-safe: %v", err)
			}
			laps := sc.Laps()
			if laps < 7 {
				t.Fatalf("race too short: %d", laps)
			}
			for si, strat := range rtBoundaryStrategies(laps) {
				if err := strat.Validate(laps); err != nil {
					t.Fatalf("boundary strategy %s invalid: %v", strat.Name, err)
				}
				for sim := uint64(0); sim < 2; sim++ {
					st := sc.NewState(rng.New(seed).Derive(rng.LabelSim, sim, uint64(si)), strat)
					checkInvariants(t, &st, nil)
					var log Log
					for !st.Done() {
						prev := st
						sc.Step(&st, &log)
						checkInvariants(t, &st, &prev)
					}
					p := sc.Player
					if st.Status[p] == Running {
						if int(st.Pits[p]) != len(strat.Stops) {
							t.Fatalf("seed %d cars %d %s: player made %d stops, plan has %d", seed, cars, strat.Name, st.Pits[p], len(strat.Stops))
						}
						if popcount(int(st.Used[p])) < 2 {
							t.Fatal("player finished on a single compound")
						}
					}
					for c := 0; c < st.N; c++ {
						if st.Status[c] == Running && popcount(int(st.Used[c])) < 2 {
							t.Fatalf("seed %d cars %d: car %d finished on a single compound (plan %v)", seed, cars, c, st.Plan[c])
						}
					}
					if _, err := json.Marshal(log.Events); err != nil {
						t.Fatalf("events not JSON-safe: %v", err)
					}
					for _, e := range log.Events {
						if math.IsNaN(e.Value) || math.IsInf(e.Value, 0) || e.Lap < 1 || e.Lap > laps || e.Car < 0 || e.Car >= cars {
							t.Fatalf("bad event %+v", e)
						}
					}
				}
			}
		}
	}
}

// Determinism under extreme seeds: same inputs, bit-identical final state.
func TestRedteamDeterminismExtremeSeeds(t *testing.T) {
	for _, seed := range rtExtremeSeeds {
		a, _ := NewScenario(seed, Options{Cars: 20})
		b, _ := NewScenario(seed, Options{Cars: 20})
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if string(ja) != string(jb) {
			t.Fatalf("seed %d: scenario not deterministic", seed)
		}
		s1 := a.NewState(rng.New(seed), oneStop(a.Laps()))
		s2 := b.NewState(rng.New(seed), oneStop(b.Laps()))
		a.Run(&s1, nil)
		b.Run(&s2, nil)
		if s1 != s2 {
			t.Fatalf("seed %d: race not deterministic", seed)
		}
	}
}

// Whatever ParseStrategy accepts, and whose stops are valid for the race,
// Validate must accept too: the name the parser derives from the input must
// not make the strategy invalid.
func TestRedteamParseThenValidateConsistent(t *testing.T) {
	inputs := []string{
		"M-10-H",
		"M\t-10-H",                    // tab inside: parsed, name keeps the tab
		"MEDIUM-1-HARD-2-SOFT-3-ſoft", // name truncated to 24 BYTES in the middle of "ſ"
		"medium-10-hard-20-medium-30-hard-31-soft-32-ſoft",
		" M-10-H ",
		"S-1-M-2-H-3-S-4-M-5-H",
	}
	for _, in := range inputs {
		s, err := ParseStrategy(in)
		if err != nil {
			continue
		}
		if err := s.Validate(50); err != nil {
			t.Errorf("ParseStrategy(%q) accepted, Validate rejects: %v (name %q)", in, err, s.Name)
		}
	}
}

func TestRedteamParseStrategyHostile(t *testing.T) {
	hostile := []string{
		"", "-", "--", "M-", "-M", "M--H", strings.Repeat("M-1-", 2500) + "H",
		strings.Repeat("9", 10_000), "M-99999-H", "M-0-H", "M--1-H", "M-+1-H", "M-1e1-H", "M-٣-H",
		"M-１-H", "M-1-H-", "\x00", "M-1-\xff", strings.Repeat("ſ", 5000),
	}
	for _, in := range hostile {
		s, err := ParseStrategy(in)
		if err == nil {
			if verr := s.Validate(50); verr == nil && (len(s.Stops) == 0 || len(s.Stops) > MaxStops) {
				t.Errorf("ParseStrategy(%.30q) produced an impossible valid strategy", in)
			}
			if !utf8.ValidString(s.Name) {
				t.Errorf("ParseStrategy(%.30q) produced a non UTF-8 name", in)
			}
		}
	}
}

func TestRedteamValidateBoundaries(t *testing.T) {
	cases := []struct {
		s    Strategy
		laps int
		ok   bool
	}{
		{Strategy{Start: model.Medium, Stops: []Stop{{1, model.Hard}}}, 2, true},
		{Strategy{Start: model.Medium, Stops: []Stop{{1, model.Hard}}}, 1, false},
		{Strategy{Start: model.Medium, Stops: []Stop{{1, model.Hard}}}, 0, false},
		{Strategy{Start: model.Medium, Stops: []Stop{{1, model.Hard}}}, math.MinInt, false},
		{Strategy{Start: model.Medium, Stops: []Stop{{math.MaxInt, model.Hard}}}, math.MaxInt, false},
		{Strategy{Start: model.Medium, Stops: []Stop{{math.MinInt, model.Hard}}}, 50, false},
		{Strategy{Start: model.Compound(200), Stops: []Stop{{3, model.Hard}}}, 50, false},
		{Strategy{Start: model.Medium, Stops: []Stop{{3, model.Compound(3)}}}, 50, false},
		{Strategy{Start: model.Medium, Stops: make([]Stop, 6)}, 50, false},
		{Strategy{Name: strings.Repeat("é", 24), Start: model.Medium, Stops: []Stop{{3, model.Hard}}}, 50, true},
		{Strategy{Name: "\xff", Start: model.Medium, Stops: []Stop{{3, model.Hard}}}, 50, false},
	}
	for i, c := range cases {
		err := c.s.Validate(c.laps)
		if (err == nil) != c.ok {
			t.Errorf("case %d: Validate(%d) = %v, want ok=%v", i, c.laps, err, c.ok)
		}
	}
}

// Short races built with WithLaps (used by tests and benchmarks): rivals'
// plans must stay valid and the race must still respect the invariants.
func TestRedteamWithLapsTiny(t *testing.T) {
	sc := mustScenario(t, 42, 20)
	for laps := 2; laps <= 8; laps++ {
		s := sc.WithLaps(laps)
		for i, r := range s.Rivals {
			if err := r.Validate(laps); err != nil {
				t.Fatalf("laps %d car %d: rival plan invalid: %v", laps, i, err)
			}
		}
		st := s.NewState(rng.New(1), Strategy{Start: model.Medium, Stops: []Stop{{1, model.Hard}}})
		for !st.Done() {
			prev := st
			s.Step(&st, nil)
			checkInvariants(t, &st, &prev)
		}
	}
}
