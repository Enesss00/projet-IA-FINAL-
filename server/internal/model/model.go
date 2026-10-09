// Package model holds the physical and statistical models of PIT WALL.
//
// Every quantity carries its unit in its name or comment, and every tunable
// parameter has documented bounds (see docs/MODELS.md, which mirrors this
// file). The functions here are pure and allocation-free: they are called
// hundreds of millions of times per second by the Monte Carlo engine.
package model

import (
	"fmt"
	"math"
	"strings"
)

// Compound identifies a tyre compound.
type Compound uint8

// Dry compounds (palier 1). Wet compounds are added with the weather model.
const (
	Soft Compound = iota
	Medium
	Hard
	NumCompounds
)

var compoundCodes = [NumCompounds]string{"S", "M", "H"}
var compoundNames = [NumCompounds]string{"SOFT", "MEDIUM", "HARD"}

// String returns the one-letter code.
func (c Compound) String() string {
	if c < NumCompounds {
		return compoundCodes[c]
	}
	return "?"
}

// Name returns the full name.
func (c Compound) Name() string {
	if c < NumCompounds {
		return compoundNames[c]
	}
	return "UNKNOWN"
}

// ParseCompound accepts "S", "M", "H" or the full names (case-insensitive).
func ParseCompound(s string) (Compound, error) {
	u := strings.ToUpper(strings.TrimSpace(s))
	for i := Compound(0); i < NumCompounds; i++ {
		if u == compoundCodes[i] || u == compoundNames[i] {
			return i, nil
		}
	}
	return 0, fmt.Errorf("unknown tyre compound %q (expected S, M or H)", truncate(s, 16))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// TyreSpec describes one compound. Wear is expressed as a dimensionless
// fraction of usable life: w = age(laps) × WearPerLap × track.WearFactor.
type TyreSpec struct {
	PaceS      float64 // s/lap, pace offset vs medium on fresh tyres       [-1.5, 1.5]
	WearPerLap float64 // 1/lap, fraction of life used per lap               [0.005, 0.1]
	DegS       float64 // s, linear degradation at w = 1                     [0, 4]
	Cliff      float64 // dimensionless wear at which the cliff starts       [0.4, 1]
	CliffS     float64 // s, extra lap time per (0.1 wear past the cliff)²   [0, 3]
	WarmupS    float64 // s, out-lap penalty on cold tyres                   [0, 2]
}

// Tyres is the compound table. The numbers are chosen so that, on an
// average circuit (WearFactor = 1), the soft reaches its cliff after ≈ 15
// laps, the medium ≈ 23 and the hard ≈ 35 — the classic "one-stop is
// possible but the two-stop is close" balance.
var Tyres = [NumCompounds]TyreSpec{
	Soft:   {PaceS: -0.70, WearPerLap: 0.046, DegS: 1.6, Cliff: 0.70, CliffS: 0.65, WarmupS: 0.35},
	Medium: {PaceS: 0.00, WearPerLap: 0.030, DegS: 1.3, Cliff: 0.70, CliffS: 0.65, WarmupS: 0.65},
	Hard:   {PaceS: 0.55, WearPerLap: 0.020, DegS: 1.1, Cliff: 0.72, CliffS: 0.65, WarmupS: 1.05},
}

// Wear returns the dimensionless wear after age laps on a circuit with the
// given wear factor.
func Wear(c Compound, age int, trackWear float64) float64 {
	return float64(age) * Tyres[c].WearPerLap * trackWear
}

// TyreDeltaS returns the lap-time delta (s) of compound c with the given
// dimensionless wear w, relative to a fresh medium. It is continuous,
// non-decreasing in w, and convex past the cliff:
//
//	Δ(w) = PaceS + DegS·w + CliffS·max(0, (w − Cliff)/0.1)²
func TyreDeltaS(c Compound, w float64) float64 {
	t := &Tyres[c]
	d := t.PaceS + t.DegS*w
	if w > t.Cliff {
		x := (w - t.Cliff) * 10
		d += t.CliffS * x * x
	}
	return d
}

// CliffLap returns the tyre age (laps) at which the cliff starts.
func CliffLap(c Compound, trackWear float64) float64 {
	return Tyres[c].Cliff / (Tyres[c].WearPerLap * trackWear)
}

// Race constants. Each comment reads "unit, meaning [lo, hi]"; the bounds
// are enforced by TestParametersHaveUnitsAndBounds and documented in
// docs/MODELS.md.
const (
	FuelSecPerKg   = 0.032  // s/kg, lap-time cost of fuel mass [0.02, 0.045]
	DayFormSigmaS  = 0.14   // s/lap, σ of a car's race-day pace (set-up, track fit) [0, 0.4]
	FuelMarginKg   = 1.2    // kg, safety margin loaded on top of the race need [0, 5]
	StartPenaltyS  = 4.6    // s, standing-start extra time on lap 1 [0, 10]
	GridSlotS      = 0.22   // s, time gap per grid slot at the start line [0, 0.5]
	StartSigmaS    = 0.35   // s, launch variability σ (half-normal) [0, 1.5]
	DirtyAirGapS   = 1.2    // s, interval below which a follower suffers dirty air [0.3, 3]
	DirtyAirS      = 0.45   // s/lap, dirty-air loss at zero interval [0, 1.5]
	MinGapS        = 0.25   // s, interval kept behind a car that could not be passed [0.05, 1]
	PassFightS     = 0.18   // s, time both cars lose in a successful pass [0, 1]
	MaxPassesLap   = 3      // count, passes one car may make in one lap [1, 5]
	DNFPerLap      = 0.0005 // 1/lap, mechanical failure hazard [0, 0.01]
	PitStationaryS = 2.4    // s, nominal stationary time [1.5, 6]
	PitSigmaS      = 0.22   // s, σ of the stationary time (half-normal) [0, 1]
	SlowStopP      = 0.03   // probability per stop, slow stop [0, 0.2]
	SlowStopMeanS  = 2.5    // s, mean extra time of a slow stop (exponential) [0, 10]
	SlowStopMaxS   = 12.0   // s, cap of the slow-stop extra time [0, 30]
	MistakeMeanS   = 1.6    // s, mean time lost in a driver mistake (exponential) [0, 5]
	MistakeMaxS    = 7.0    // s, cap of a single mistake [0, 20]
)

// FuelDeltaS returns the lap-time cost of carrying fuelKg.
func FuelDeltaS(fuelKg float64) float64 { return fuelKg * FuelSecPerKg }

// DirtyAirDeltaS returns the dirty-air loss for a car following at the given
// interval (s). Zero at or beyond DirtyAirGapS, linear below.
func DirtyAirDeltaS(intervalS float64) float64 {
	if intervalS >= DirtyAirGapS || intervalS < 0 {
		return 0
	}
	return DirtyAirS * (1 - intervalS/DirtyAirGapS)
}

// PassProbability is the probability that an attacker whose potential lap is
// paceAdvS seconds faster than the defender's completes the pass this lap.
// It is a logistic curve centred on a threshold that grows when overtaking is
// hard on this circuit (ease < 1) and that is offset by tyre-age advantage.
//
//	p = 0.92 / (1 + exp(−(adv − thr)/0.28)),  thr = 0.55 / ease
func PassProbability(paceAdvS, ease float64) float64 {
	if ease <= 0 || math.IsNaN(paceAdvS) {
		return 0
	}
	thr := 0.55 / ease
	return 0.92 * logistic((paceAdvS-thr)/0.28)
}

// logistic is 1/(1+e^-x) from a 2049-entry table on [-12, 12] with linear
// interpolation (absolute error < 2e-6), saturated outside. It keeps the
// hot loop free of transcendental calls.
func logistic(x float64) float64 {
	if !(x > -logisticSpan) { // also catches NaN
		return 0
	}
	if x >= logisticSpan {
		return 1
	}
	f := (x + logisticSpan) * (logisticN / (2 * logisticSpan))
	i := int(f)
	if i >= logisticN {
		return logisticTab[logisticN]
	}
	a := logisticTab[i]
	return a + (logisticTab[i+1]-a)*(f-float64(i))
}

const (
	logisticN    = 2048
	logisticSpan = 12.0
)

var logisticTab = func() (t [logisticN + 1]float64) {
	for i := range t {
		x := -logisticSpan + 2*logisticSpan*float64(i)/logisticN
		t[i] = 1 / (1 + math.Exp(-x))
	}
	return
}()

// Driver is a fictional driver and car combination.
type Driver struct {
	Name      string  `json:"name"`
	Code      string  `json:"code"`
	Team      string  `json:"team"`
	TeamIdx   int     `json:"teamIdx"`
	Number    int     `json:"number"`
	PaceS     float64 `json:"paceS"`     // s/lap, car+driver offset vs reference   [-1.2, 1.8]
	SigmaS    float64 `json:"sigmaS"`    // s, lap-to-lap σ                        [0.08, 0.6]
	MistakeP  float64 `json:"mistakeP"`  // probability of a mistake per lap       [0, 0.03]
	TyreSave  float64 `json:"tyreSave"`  // wear multiplier (<1 gentle on tyres)   [0.85, 1.15]
	Defending float64 `json:"defending"` // multiplier on attacker threshold       [0.8, 1.25]
}

// MarshalText encodes the compound as its one-letter code.
func (c Compound) MarshalText() ([]byte, error) {
	if c >= NumCompounds {
		return nil, fmt.Errorf("invalid compound %d", c)
	}
	return []byte(compoundCodes[c]), nil
}

// UnmarshalText decodes a compound code or name.
func (c *Compound) UnmarshalText(b []byte) error {
	v, err := ParseCompound(string(b))
	if err != nil {
		return err
	}
	*c = v
	return nil
}
