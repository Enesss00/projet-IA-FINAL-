package race

import (
	"math"

	"pitwall/internal/model"
	"pitwall/internal/rng"
)

// Car status.
const (
	Running uint8 = iota
	Retired
)

// State is the complete mutable state of a race. It is a value type with no
// pointers or slices: `snap := *st` is an exact snapshot, including the random
// streams, so re-stepping a copy reproduces the same future bit for bit.
type State struct {
	Lap  int // laps completed
	Laps int // race distance
	N    int

	Order    [MaxCars]uint8   // Order[p] = car at position p (0 = leader)
	Time     [MaxCars]float64 // s, cumulative race time at the end of the last completed lap
	LastLap  [MaxCars]float64 // s, last lap time
	BestLap  [MaxCars]float64 // s
	Fuel     [MaxCars]float64 // kg
	Age      [MaxCars]int16   // laps completed on the current tyre set
	Comp     [MaxCars]model.Compound
	Used     [MaxCars]uint8 // bitmask of compounds used
	NextStop [MaxCars]uint8 // index of the next planned stop in Plan
	Pits     [MaxCars]uint8 // stops made
	PitLap   [MaxCars]int16 // lap of the last stop (0 = none)
	Status   [MaxCars]uint8
	OutLap   [MaxCars]int16 // lap at which the car retired
	Plan     [MaxCars]Plan
	R        [MaxCars]rng.Stream
	Form     [MaxCars]float64 // s/lap, race-day pace offset drawn at the start

	// scratch (kept in the value so that Step allocates nothing)
	pot [MaxCars]float64
	arr [MaxCars]float64
}

// EventKind classifies race events.
type EventKind uint8

// Event kinds.
const (
	EvPit EventKind = iota + 1
	EvPass
	EvRetire
	EvMistake
	EvSlowStop
	EvFastestLap
)

var eventNames = map[EventKind]string{EvPit: "pit", EvPass: "pass", EvRetire: "retire", EvMistake: "mistake", EvSlowStop: "slowstop", EvFastestLap: "fastest"}

// String names the kind.
func (k EventKind) String() string { return eventNames[k] }

// Event is a time-stamped race event (event sourcing: a race is fully
// described by its initial state and its events).
type Event struct {
	Lap      int            `json:"lap"`
	Kind     EventKind      `json:"-"`
	KindName string         `json:"kind"`
	Car      int            `json:"car"`
	Other    int            `json:"other"` // defender for a pass, -1 otherwise
	Value    float64        `json:"value"` // s: stationary time, time lost, lap time...
	Compound model.Compound `json:"compound"`
}

// Log collects events when non-nil.
type Log struct {
	Events  []Event
	fastest float64
}

func (l *Log) add(e Event) {
	if l != nil {
		e.KindName = e.Kind.String()
		l.Events = append(l.Events, e)
	}
}

// NewState returns the state on the grid, before lap 1. sim is the random
// stream of this simulation; every car gets its own child stream so that the
// player's choices never shift the rivals' random draws (common random
// numbers between strategies).
func (sc *Scenario) NewState(sim rng.Stream, player Strategy) State {
	e := &sc.env
	var st State
	st.Laps = e.laps
	st.N = e.n
	for p, c := range sc.Grid {
		st.Order[p] = uint8(c)
	}
	for c := 0; c < e.n; c++ {
		st.R[c] = sim.Derive(rng.LabelCar, uint64(c))
		st.Fuel[c] = e.startFuel
		if e.variability {
			st.Form[c] = model.DayFormSigmaS * st.R[c].Norm()
		}
		if c == sc.Player {
			st.Comp[c] = player.Start
			st.Plan[c] = player.ToPlan()
		} else {
			st.Comp[c] = sc.Rivals[c].Start
			st.Plan[c] = sc.rivalPlans[c]
			if e.variability {
				jitter(&st.R[c], &st.Plan[c], e.laps)
			}
		}
		st.Used[c] = 1 << st.Comp[c]
	}
	return st
}

// jitter moves each rival stop by up to ±2 laps for this simulation: the
// player does not know the exact laps the rivals will pit on. Stops stay
// strictly increasing and inside the race; compounds are unchanged, so the
// two-compound rule still holds.
func jitter(r *rng.Stream, p *Plan, laps int) {
	prev := 0
	for i := 0; i < int(p.N); i++ {
		l := p.Stops[i].Lap + r.IntN(5) - 2
		// leave room for the remaining stops before the flag
		maxL := laps - (int(p.N) - i)
		if l > maxL {
			l = maxL
		}
		if l <= prev {
			l = prev + 1
		}
		p.Stops[i].Lap = l
		prev = l
	}
}

// Done reports whether the race is over.
func (st *State) Done() bool { return st.Lap >= st.Laps }

// Running reports the number of cars still running.
func (st *State) Running() int {
	n := 0
	for c := 0; c < st.N; c++ {
		if st.Status[c] == Running {
			n++
		}
	}
	return n
}

// Position returns the 1-based position of car c.
func (st *State) Position(c int) int {
	for p := 0; p < st.N; p++ {
		if int(st.Order[p]) == c {
			return p + 1
		}
	}
	return st.N
}

// Run steps the race to the finish.
func (sc *Scenario) Run(st *State, log *Log) {
	for !st.Done() {
		sc.Step(st, log)
	}
}

// Step simulates one lap for the whole field.
//
//  1. potential lap time of every running car (pace, tyres, fuel, noise,
//     mistakes, start, dirty air from the interval at the start of the lap);
//  2. line-crossing times resolved front to back with probabilistic passes;
//  3. pit stops at the end of the in-lap;
//  4. new classification.
func (sc *Scenario) Step(st *State, log *Log) {
	if st.Done() {
		return
	}
	e := &sc.env
	lap := st.Lap + 1
	n := st.N

	// 1. potential lap times
	for p := 0; p < n; p++ {
		c := int(st.Order[p])
		if st.Status[c] != Running {
			continue
		}
		r := &st.R[c]
		comp := st.Comp[c]
		w := float64(st.Age[c]) * model.Tyres[comp].WearPerLap * e.wear * e.tyreSave[c]
		lt := e.baseS + e.pace[c] + st.Form[c] + model.TyreDeltaS(comp, w) + st.Fuel[c]*model.FuelSecPerKg
		if st.PitLap[c] == int16(lap-1) && lap > 1 {
			lt += model.Tyres[comp].WarmupS
		}
		if e.variability {
			lt += e.sigma[c] * r.Norm()
			if r.Float64() < e.mistakeP[c] {
				loss := math.Min(r.Exp(model.MistakeMeanS), model.MistakeMaxS)
				lt += loss
				log.add(Event{Lap: lap, Kind: EvMistake, Car: c, Other: -1, Value: round2(loss)})
			}
			if r.Float64() < e.dnfP {
				st.Status[c] = Retired
				st.OutLap[c] = int16(lap)
				log.add(Event{Lap: lap, Kind: EvRetire, Car: c, Other: -1})
				continue
			}
		}
		if lap == 1 {
			lt += model.StartPenaltyS + model.GridSlotS*float64(e.gridPos[c])
			if e.variability {
				lt += model.StartSigmaS * math.Abs(r.Norm())
			}
		} else if p > 0 {
			ahead := int(st.Order[p-1])
			if st.Status[ahead] == Running {
				lt += model.DirtyAirDeltaS(st.Time[c] - st.Time[ahead])
			}
		}
		st.pot[c] = lt
	}

	// 2. line crossing, front to back. list holds running cars in their new
	// order; arr their crossing time.
	var list [MaxCars]uint8
	k := 0
	for p := 0; p < n; p++ {
		c := int(st.Order[p])
		if st.Status[c] != Running {
			continue
		}
		st.arr[c] = st.Time[c] + st.pot[c]
		list[k] = uint8(c)
		j := k
		k++
		passes := 0
		for j > 0 {
			a := int(list[j-1])
			if st.arr[c] >= st.arr[a]+model.MinGapS {
				break // not close enough to attack
			}
			adv := st.pot[a] - st.pot[c]
			ok := false
			if passes < model.MaxPassesLap {
				prob := model.PassProbability(adv, e.ease/e.defending[a])
				if !e.variability {
					ok = prob >= 0.5
				} else {
					ok = st.R[c].Float64() < prob
				}
			}
			if !ok {
				st.arr[c] = st.arr[a] + model.MinGapS
				break
			}
			passes++
			old := st.arr[a]
			st.arr[c] = minf(st.arr[c]+model.PassFightS, old-0.05)
			st.arr[a] = maxf(old+model.PassFightS, st.arr[c]+model.MinGapS)
			list[j-1], list[j] = list[j], list[j-1]
			log.add(Event{Lap: lap, Kind: EvPass, Car: c, Other: a, Value: round2(adv)})
			j--
		}
		// cars passed may now be held up by each other
		for q := j + 1; q < k; q++ {
			x, y := int(list[q-1]), int(list[q])
			if st.arr[y] < st.arr[x]+model.MinGapS*0.5 {
				st.arr[y] = st.arr[x] + model.MinGapS*0.5
			}
		}
	}

	// 3. pit stops at the end of the in-lap, tyres and fuel
	for q := 0; q < k; q++ {
		c := int(list[q])
		r := &st.R[c]
		pl := &st.Plan[c]
		st.Fuel[c] = maxf(0, st.Fuel[c]-e.fuelPerLap)
		if int(st.NextStop[c]) < int(pl.N) && pl.Stops[st.NextStop[c]].Lap == lap && lap < st.Laps {
			stop := pl.Stops[st.NextStop[c]]
			stat := model.PitStationaryS
			if e.variability {
				stat += model.PitSigmaS * math.Abs(r.Norm())
				if r.Float64() < model.SlowStopP {
					extra := math.Min(r.Exp(model.SlowStopMeanS), model.SlowStopMaxS)
					stat += extra
					log.add(Event{Lap: lap, Kind: EvSlowStop, Car: c, Other: -1, Value: round2(extra)})
				}
			}
			st.arr[c] += e.pitLossS + stat
			st.Comp[c] = stop.Compound
			st.Used[c] |= 1 << stop.Compound
			st.Age[c] = 0
			st.PitLap[c] = int16(lap)
			st.Pits[c]++
			st.NextStop[c]++
			log.add(Event{Lap: lap, Kind: EvPit, Car: c, Other: -1, Value: round2(stat), Compound: stop.Compound})
		} else {
			st.Age[c]++
		}
		lt := st.arr[c] - st.Time[c]
		st.LastLap[c] = lt
		if st.BestLap[c] == 0 || lt < st.BestLap[c] {
			st.BestLap[c] = lt
		}
		if log != nil && (log.fastest == 0 || lt < log.fastest) {
			log.fastest = lt
			log.add(Event{Lap: lap, Kind: EvFastestLap, Car: c, Other: -1, Value: round3(lt)})
		}
		st.Time[c] = st.arr[c]
	}

	// 4. classification: running cars by crossing time (insertion sort,
	// the list is nearly sorted), then retired cars by laps completed.
	for q := 1; q < k; q++ {
		x := list[q]
		j := q
		for j > 0 && st.arr[list[j-1]] > st.arr[x] {
			list[j] = list[j-1]
			j--
		}
		list[j] = x
	}
	var out [MaxCars]uint8
	m := 0
	for q := 0; q < k; q++ {
		out[m] = list[q]
		m++
	}
	// retired cars keep their relative order, most recent retirements first
	// (they completed more laps).
	for back := lap; back >= 1 && m < n; back-- {
		for p := 0; p < n; p++ {
			c := st.Order[p]
			if st.Status[c] == Retired && int(st.OutLap[c]) == back {
				out[m] = c
				m++
			}
		}
	}
	st.Order = out
	st.Lap = lap
}

// minf/maxf are NaN-unaware min/max (inputs are always finite here); they
// are measurably faster than math.Min/Max in the hot loop.
func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }
