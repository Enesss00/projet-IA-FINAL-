// Package race is the lap-by-lap race engine.
//
// A race is a pure function of (Scenario, simulation stream, player plan).
// The whole mutable state lives in State, a fixed-size value type: copying it
// is a snapshot, and stepping a copy is a branch (parallel universe).
package race

import (
	"fmt"
	"math"
	"sort"

	"pitwall/internal/model"
	"pitwall/internal/rng"
	"pitwall/internal/trackgen"
)

// MaxCars bounds the grid size.
const MaxCars = 20

// MinCars is the smallest grid size.
const MinCars = 1

// Scenario is everything that is fixed for a given seed: the circuit, the
// drivers, the starting grid, the player's car and the rivals' baseline
// strategies.
type Scenario struct {
	Seed       uint64          `json:"-"`
	Track      *trackgen.Track `json:"track"`
	Drivers    []model.Driver  `json:"drivers"`
	Grid       []int           `json:"grid"` // Grid[p] = car starting at position p
	Player     int             `json:"player"`
	Rivals     []Strategy      `json:"rivals"` // baseline plan of every car (player's is a suggestion)
	Teams      []string        `json:"teams"`
	env        env
	rivalPlans [MaxCars]Plan
}

// env caches per-car parameters in contiguous arrays for the hot loop.
type env struct {
	n           int
	laps        int
	baseS       float64
	wear        float64
	pitLossS    float64
	ease        float64
	fuelPerLap  float64
	startFuel   float64
	pace        [MaxCars]float64
	sigma       [MaxCars]float64
	mistakeP    [MaxCars]float64
	tyreSave    [MaxCars]float64
	defending   [MaxCars]float64
	gridPos     [MaxCars]int
	dnfP        float64
	variability bool
}

var (
	teamA = []string{"Halcyon", "Meridian", "Kestrel", "Nadir", "Solace", "Ironveil", "Corvid", "Lumen", "Tessera", "Obsidian", "Arclight", "Vantor", "Quill", "Basalt"}
	teamB = []string{"Dynamics", "Racing", "Motorsport", "Works", "Engineering", "Competition", "Systems", "Velocity"}
	first = []string{"A.", "B.", "C.", "D.", "E.", "F.", "G.", "H.", "I.", "J.", "K.", "L.", "M.", "N.", "O.", "P.", "R.", "S.", "T.", "V.", "Y.", "Z."}
	sur1  = []string{"Var", "Kel", "Mor", "Dan", "Ost", "Bri", "Sav", "Lun", "Tor", "Hel", "Rav", "Cas", "Fen", "Ilo", "Jar", "Nev", "Pal", "Quin", "Sor", "Wes"}
	sur2  = []string{"ani", "ström", "ez", "ova", "ellis", "dek", "aro", "inen", "ward", "elli", "quist", "ovic", "berg", "ard", "ato", "ski", "enko", "mont"}
)

// Options tunes scenario generation.
type Options struct {
	Cars int // grid size, [MinCars, MaxCars]
	// NoVariability removes every random term (driver noise, mistakes,
	// failures, slow stops, start variability, rival jitter). Used by the
	// analytic tests.
	NoVariability bool
}

// NewScenario builds the deterministic scenario for a seed.
func NewScenario(seed uint64, opt Options) (*Scenario, error) {
	if opt.Cars < MinCars || opt.Cars > MaxCars {
		return nil, fmt.Errorf("grid size %d out of [%d, %d]", opt.Cars, MinCars, MaxCars)
	}
	tr := trackgen.Generate(seed)
	return NewScenarioOnTrack(seed, tr, opt)
}

// NewScenarioOnTrack builds a scenario on a given track.
func NewScenarioOnTrack(seed uint64, tr *trackgen.Track, opt Options) (*Scenario, error) {
	if opt.Cars < MinCars || opt.Cars > MaxCars {
		return nil, fmt.Errorf("grid size %d out of [%d, %d]", opt.Cars, MinCars, MaxCars)
	}
	n := opt.Cars
	r := rng.New(seed).Derive(rng.LabelGrid)
	sc := &Scenario{Seed: seed, Track: tr}

	nTeams := (n + 1) / 2
	perm := permutation(&r, len(teamA))
	usedNames := map[string]bool{}
	for t := 0; t < nTeams; t++ {
		sc.Teams = append(sc.Teams, teamA[perm[t]]+" "+teamB[r.IntN(len(teamB))])
	}
	// Team pace: evenly spread with jitter so that every seed has a clear
	// front, midfield and back.
	teamPace := make([]float64, nTeams)
	for t := range teamPace {
		frac := 0.5
		if nTeams > 1 {
			frac = float64(t) / float64(nTeams-1)
		}
		teamPace[t] = -0.45 + 1.0*frac + (r.Float64()-0.5)*0.2
	}
	numbers := permutation(&r, 98)
	for i := 0; i < n; i++ {
		t := i / 2
		var nm string
		for k := 0; ; k++ {
			nm = first[r.IntN(len(first))] + " " + sur1[r.IntN(len(sur1))] + sur2[r.IntN(len(sur2))]
			if !usedNames[nm] || k > 50 {
				break
			}
		}
		usedNames[nm] = true
		d := model.Driver{
			Name:      nm,
			Team:      sc.Teams[t],
			TeamIdx:   t,
			Number:    numbers[i] + 2,
			PaceS:     round3(teamPace[t] + (r.Float64()-0.5)*0.3),
			SigmaS:    round3(0.16 + r.Float64()*0.18),
			MistakeP:  round3(0.003 + r.Float64()*0.007),
			TyreSave:  round3(0.9 + r.Float64()*0.2),
			Defending: round3(0.9 + r.Float64()*0.25),
		}
		d.Code = code(nm)
		sc.Drivers = append(sc.Drivers, d)
	}
	dedupeCodes(sc.Drivers)

	// Qualifying: one flying lap each, fresh softs.
	type q struct {
		car int
		t   float64
	}
	qs := make([]q, n)
	for i := range qs {
		qs[i] = q{i, sc.Drivers[i].PaceS + r.Norm()*0.18}
	}
	sort.SliceStable(qs, func(a, b int) bool { return qs[a].t < qs[b].t })
	for _, x := range qs {
		sc.Grid = append(sc.Grid, x.car)
	}
	// The player drives the faster car of the second-best team: a podium is
	// expected, a win requires a great strategy (or luck).
	sc.Player = 0
	if n >= 4 {
		sc.Player = 2
		if sc.Drivers[3].PaceS < sc.Drivers[2].PaceS {
			sc.Player = 3
		}
	}

	rr := rng.New(seed).Derive(rng.LabelRivals)
	for i := 0; i < n; i++ {
		sc.Rivals = append(sc.Rivals, baselineStrategy(&rr, tr.Laps, tr.WearFactor*sc.Drivers[i].TyreSave))
	}
	sc.Rivals[sc.Player].Name = "Plan ingénieur"
	for i, s := range sc.Rivals {
		sc.rivalPlans[i] = s.ToPlan()
	}
	sc.initEnv(opt)
	return sc, nil
}

func (sc *Scenario) initEnv(opt Options) {
	e := &sc.env
	tr := sc.Track
	e.n = len(sc.Drivers)
	e.laps = tr.Laps
	e.baseS = tr.BaseLapS
	e.wear = tr.WearFactor
	e.pitLossS = tr.PitLossS
	e.ease = tr.OvertakeEase
	e.fuelPerLap = tr.FuelPerLapKg
	e.startFuel = float64(tr.Laps)*tr.FuelPerLapKg + model.FuelMarginKg
	e.dnfP = model.DNFPerLap
	e.variability = !opt.NoVariability
	for i, d := range sc.Drivers {
		e.pace[i] = d.PaceS
		e.sigma[i] = d.SigmaS
		e.mistakeP[i] = d.MistakeP
		e.tyreSave[i] = d.TyreSave
		e.defending[i] = d.Defending
	}
	for p, c := range sc.Grid {
		e.gridPos[c] = p
	}
	if opt.NoVariability {
		e.dnfP = 0
		for i := 0; i < e.n; i++ {
			e.sigma[i] = 0
			e.mistakeP[i] = 0
		}
	}
}

// Laps returns the race distance in laps.
func (sc *Scenario) Laps() int { return sc.env.laps }

// N returns the number of cars.
func (sc *Scenario) N() int { return sc.env.n }

// baselineStrategy picks a sensible plan for a car: the minimal number of
// stops that keeps every stint before the tyre cliff, with stint lengths
// proportional to each compound's life, and some randomness so that the
// field does not move as one.
func baselineStrategy(r *rng.Stream, laps int, wear float64) Strategy {
	type tmpl []model.Compound
	one := []tmpl{{model.Medium, model.Hard}, {model.Hard, model.Medium}, {model.Soft, model.Hard}, {model.Medium, model.Hard}}
	two := []tmpl{{model.Soft, model.Medium, model.Hard}, {model.Medium, model.Hard, model.Medium}, {model.Soft, model.Hard, model.Medium}, {model.Medium, model.Medium, model.Hard}, {model.Soft, model.Hard, model.Hard}}
	three := []tmpl{{model.Soft, model.Medium, model.Medium, model.Hard}, {model.Medium, model.Hard, model.Medium, model.Soft}}
	fits := func(t tmpl) ([]int, bool) {
		total := 0.0
		for _, c := range t {
			total += model.CliffLap(c, wear)
		}
		lens := make([]int, len(t))
		acc := 0
		ok := true
		for i, c := range t {
			l := int(math.Round(float64(laps) * model.CliffLap(c, wear) / total))
			if i == len(t)-1 {
				l = laps - acc
			}
			lens[i] = l
			acc += l
			if float64(l) > model.CliffLap(c, wear)*1.04 || l < 2 {
				ok = false
			}
		}
		return lens, ok
	}
	var choice tmpl
	var lens []int
	for _, set := range [][]tmpl{one, two, three} {
		t := set[r.IntN(len(set))]
		if l, ok := fits(t); ok {
			choice, lens = t, l
			// a third of the field gambles on one more stop
			if len(t) < 4 && r.Bernoulli(0.3) {
				next := two
				if len(t) == 3 {
					next = three
				}
				t2 := next[r.IntN(len(next))]
				if l2, ok2 := fits(t2); ok2 {
					choice, lens = t2, l2
				}
			}
			break
		}
	}
	if choice == nil { // extreme wear: as many stops as allowed
		choice = tmpl{model.Soft}
		for i := 0; i < MaxStops; i++ {
			choice = append(choice, model.Medium)
		}
		lens = nil
		for i := range choice {
			lens = append(lens, laps/len(choice))
			if i == len(choice)-1 {
				lens[i] = laps - (laps/len(choice))*(len(choice)-1)
			}
		}
	}
	s := Strategy{Name: "Plan", Start: choice[0]}
	lap := 0
	for i := 0; i < len(choice)-1; i++ {
		lap += lens[i]
		jit := r.IntN(5) - 2
		l := lap + jit
		if l < 1 {
			l = 1
		}
		if len(s.Stops) > 0 && l <= s.Stops[len(s.Stops)-1].Lap {
			l = s.Stops[len(s.Stops)-1].Lap + 1
		}
		if l >= laps {
			break
		}
		s.Stops = append(s.Stops, Stop{Lap: l, Compound: choice[i+1]})
	}
	if s.Validate(laps) != nil { // last resort: always-valid one-stop
		s = Strategy{Name: "Plan", Start: model.Medium, Stops: []Stop{{Lap: max(1, laps/2), Compound: model.Hard}}}
	}
	return s
}

func permutation(r *rng.Stream, n int) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	for i := n - 1; i > 0; i-- {
		j := r.IntN(i + 1)
		p[i], p[j] = p[j], p[i]
	}
	return p
}

func code(name string) string {
	// surname = after "X. "
	s := []rune(name)
	if len(s) > 3 {
		s = s[3:]
	}
	out := make([]rune, 0, 3)
	for _, c := range s {
		if len(out) == 3 {
			break
		}
		if c == 'ö' {
			c = 'o'
		}
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out)
}

// dedupeCodes makes the three-letter codes unique by picking other letters
// of the surname (as timing screens do), falling back to a digit.
func dedupeCodes(ds []model.Driver) {
	seen := map[string]bool{}
	for i := range ds {
		if !seen[ds[i].Code] {
			seen[ds[i].Code] = true
			continue
		}
		var sur []rune
		for _, r := range []rune(ds[i].Name)[min(3, len([]rune(ds[i].Name))):] {
			if r == 'ö' {
				r = 'o'
			}
			if r >= 'a' && r <= 'z' {
				r -= 'a' - 'A'
			}
			if r >= 'A' && r <= 'Z' {
				sur = append(sur, r)
			}
		}
		found := false
		for a := 1; a < len(sur) && !found; a++ {
			for b := a + 1; b < len(sur) && !found; b++ {
				c := string([]rune{sur[0], sur[a], sur[b]})
				if !seen[c] {
					ds[i].Code, found = c, true
				}
			}
		}
		for d := 1; !found; d++ {
			c := string([]rune(ds[i].Code)[:2]) + string(rune('0'+d%10))
			if !seen[c] || d > 10 {
				ds[i].Code, found = c, true
			}
		}
		seen[ds[i].Code] = true
	}
}

func round3(x float64) float64 { return math.Round(x*1000) / 1000 }

// WithLaps returns a copy of the scenario with a different race distance
// (benchmarks and tests). Rival plans are regenerated to stay valid.
func (sc *Scenario) WithLaps(laps int) *Scenario {
	if laps < 1 {
		laps = 1
	}
	c := *sc
	tr := *sc.Track
	tr.Laps = laps
	c.Track = &tr
	c.Rivals = nil
	rr := rng.New(sc.Seed).Derive(rng.LabelRivals)
	for i := range sc.Drivers {
		c.Rivals = append(c.Rivals, baselineStrategy(&rr, laps, tr.WearFactor*sc.Drivers[i].TyreSave))
	}
	for i, s := range c.Rivals {
		c.rivalPlans[i] = s.ToPlan()
	}
	c.initEnv(Options{Cars: len(sc.Drivers), NoVariability: !sc.env.variability})
	return &c
}
