package api

import (
	"math"

	"pitwall/internal/model"
	"pitwall/internal/montecarlo"
	"pitwall/internal/race"
	"pitwall/internal/trackgen"
)

// TyreView describes a compound and its degradation curve for this circuit
// (lap-time delta vs a fresh medium, s, for tyre ages 0..len-1).
type TyreView struct {
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	PaceS      float64   `json:"paceS"`
	WearPerLap float64   `json:"wearPerLap"`
	CliffLap   float64   `json:"cliffLap"`
	Curve      []float64 `json:"curve"`
}

// ScenarioView is the "scenario" message payload.
type ScenarioView struct {
	Seed      string          `json:"seed"`
	Cars      int             `json:"cars"`
	Laps      int             `json:"laps"`
	PointsTop int             `json:"pointsTop"`
	Track     *trackgen.Track `json:"track"`
	Drivers   []model.Driver  `json:"drivers"`
	Teams     []string        `json:"teams"`
	Grid      []int           `json:"grid"`
	Player    int             `json:"player"`
	Suggested StrategyDTO     `json:"suggested"`
	Tyres     []TyreView      `json:"tyres"`
	Limits    LimitsView      `json:"limits"`
}

// LimitsView advertises the server limits to the client.
type LimitsView struct {
	MaxSims       int     `json:"maxSims"`
	MaxStrategies int     `json:"maxStrategies"`
	MaxStops      int     `json:"maxStops"`
	MinSpeed      float64 `json:"minSpeed"`
	MaxSpeed      float64 `json:"maxSpeed"`
	MaxCars       int     `json:"maxCars"`
}

func limitsView() LimitsView {
	return LimitsView{MaxSims: montecarlo.MaxSims, MaxStrategies: montecarlo.MaxStrategies, MaxStops: race.MaxStops, MinSpeed: MinSpeed, MaxSpeed: MaxSpeed, MaxCars: race.MaxCars}
}

// StrategyToDTO converts a strategy to its wire form.
func StrategyToDTO(s race.Strategy) StrategyDTO {
	d := StrategyDTO{Name: s.Name, Start: s.Start.String(), Stops: []StopDTO{}}
	for _, st := range s.Stops {
		d.Stops = append(d.Stops, StopDTO{Lap: itoaNum(st.Lap), Compound: st.Compound.String()})
	}
	return d
}

func newScenarioView(seed string, sc *race.Scenario) ScenarioView {
	v := ScenarioView{
		Seed: seed, Cars: sc.N(), Laps: sc.Laps(), PointsTop: montecarlo.PointsPositions(sc.N()),
		Track: sc.Track, Drivers: sc.Drivers, Teams: sc.Teams, Grid: sc.Grid, Player: sc.Player,
		Suggested: StrategyToDTO(sc.Rivals[sc.Player]), Limits: limitsView(),
	}
	wear := sc.Track.WearFactor * sc.Drivers[sc.Player].TyreSave
	maxAge := min(sc.Laps(), 70)
	for c := model.Compound(0); c < model.NumCompounds; c++ {
		t := model.Tyres[c]
		tv := TyreView{Code: c.String(), Name: c.Name(), PaceS: t.PaceS, WearPerLap: t.WearPerLap, CliffLap: round(model.CliffLap(c, wear), 2)}
		for age := 0; age <= maxAge; age++ {
			tv.Curve = append(tv.Curve, round(model.TyreDeltaS(c, model.Wear(c, age, wear)), 3))
		}
		v.Tyres = append(v.Tyres, tv)
	}
	return v
}

// CarLapView is one car's line in a lap message.
type CarLapView struct {
	Car      int     `json:"car"`
	Pos      int     `json:"pos"`
	Time     float64 `json:"time"`     // s, cumulative at the end of this lap
	LapTime  float64 `json:"lapTime"`  // s
	Best     float64 `json:"best"`     // s
	Gap      float64 `json:"gap"`      // s to the leader
	Interval float64 `json:"interval"` // s to the car ahead
	Compound string  `json:"compound"`
	Age      int     `json:"age"`
	Pits     int     `json:"pits"`
	Pitted   bool    `json:"pitted"` // stopped at the end of this lap
	Out      bool    `json:"out"`
	Wear     float64 `json:"wear"` // dimensionless
}

// LapView is the "race.lap" payload.
type LapView struct {
	Lap    int          `json:"lap"`
	Laps   int          `json:"laps"`
	Flag   string       `json:"flag"` // green | chequered
	Cars   []CarLapView `json:"cars"`
	Events []race.Event `json:"events"`
}

func newLapView(sc *race.Scenario, st *race.State, events []race.Event) LapView {
	lv := LapView{Lap: st.Lap, Laps: st.Laps, Flag: "green", Events: events}
	if lv.Events == nil {
		lv.Events = []race.Event{}
	}
	if st.Done() {
		lv.Flag = "chequered"
	}
	lead := st.Time[st.Order[0]]
	prev := lead
	for p := 0; p < st.N; p++ {
		c := int(st.Order[p])
		cv := CarLapView{
			Car: c, Pos: p + 1, Time: round(st.Time[c], 3), LapTime: round(st.LastLap[c], 3), Best: round(st.BestLap[c], 3),
			Gap: round(st.Time[c]-lead, 3), Interval: round(st.Time[c]-prev, 3),
			Compound: st.Comp[c].String(), Age: int(st.Age[c]), Pits: int(st.Pits[c]),
			Pitted: int(st.PitLap[c]) == st.Lap && st.Lap > 0, Out: st.Status[c] != race.Running,
			Wear: round(model.Wear(st.Comp[c], int(st.Age[c]), sc.Track.WearFactor*sc.Drivers[c].TyreSave), 3),
		}
		if cv.Out {
			cv.Gap, cv.Interval = 0, 0
		}
		prev = st.Time[c]
		lv.Cars = append(lv.Cars, cv)
	}
	return lv
}

func round(x float64, d int) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	p := math.Pow(10, float64(d))
	return math.Round(x*p) / p
}
