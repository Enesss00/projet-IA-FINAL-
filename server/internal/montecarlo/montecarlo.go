// Package montecarlo runs N independent race simulations in parallel and
// streams aggregated results.
//
// Determinism contract: simulation i always uses the stream
// rng(seed).Derive(LabelSim, i), whatever the worker that runs it, and every
// strategy of a job is evaluated on the same N streams (common random
// numbers: the comparison between strategies is paired, which removes most of
// the noise). Aggregates are computed over the contiguous prefix of finished
// simulations, at a fixed schedule of checkpoints, so that the progress stream
// itself — not only the final result — is identical with 1 or 16 workers.
package montecarlo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"

	"pitwall/internal/race"
	"pitwall/internal/rng"
	"pitwall/internal/stats"
)

// Limits.
const (
	MaxSims       = 50_000 // per strategy and per job
	MinSims       = 1
	MaxStrategies = 4
	MaxWorkers    = 64
	BatchSize     = 25
	// MaxResultBytes bounds the memory of a job's raw results.
	MaxResultBytes = MaxSims * MaxStrategies * 16
)

// Job is a Monte Carlo request.
type Job struct {
	Scenario   *race.Scenario
	Strategies []race.Strategy // must be validated by the caller
	Sims       int
	Workers    int // <= 0 means GOMAXPROCS
}

// Stats summarises one strategy over the simulations done so far.
type Stats struct {
	Name     string         `json:"name"`
	Plan     string         `json:"plan"`
	N        int            `json:"n"`
	Hist     []int          `json:"hist"` // Hist[p] = finishes in position p+1; Hist[cars] = retirements
	Win      stats.Interval `json:"win"`
	Podium   stats.Interval `json:"podium"`
	Points   stats.Interval `json:"points"`
	DNF      stats.Interval `json:"dnf"`
	MeanPos  stats.Interval `json:"meanPos"`
	MedPos   float64        `json:"medPos"`
	P95Pos   float64        `json:"p95Pos"`  // 95th percentile of the finishing position (risk)
	CVaRPos  float64        `json:"cvarPos"` // mean of the worst 5% positions
	WorstPos int            `json:"worstPos"`
	BestPos  int            `json:"bestPos"`
	// Race time of finishers, s.
	TimeMed stats.Interval `json:"timeMed"`
	TimeP5  float64        `json:"timeP5"`
	TimeP95 float64        `json:"timeP95"`
	// Ahead[j] = fraction of simulations where this strategy finished ahead
	// of strategy j in the same universe (paired comparison).
	Ahead []float64 `json:"ahead"`
}

// Progress is a snapshot of a running job.
type Progress struct {
	Done      int     `json:"done"`
	Total     int     `json:"total"`
	Races     int     `json:"races"` // simulated races (= Done × strategies)
	Stats     []Stats `json:"strategies"`
	Final     bool    `json:"final"`
	Truncated bool    `json:"truncated"` // stopped early (deadline or cancel)
	PointsTop int     `json:"pointsTop"`
}

// Checkpoints returns the deterministic schedule of prefix sizes at which
// progress is reported: roughly geometric (×1.25) so that the early, noisy
// phase is shown in detail, always ending on total.
func Checkpoints(total int) []int {
	var cps []int
	x := float64(BatchSize)
	for {
		c := int(math.Ceil(x/BatchSize)) * BatchSize
		if c >= total {
			break
		}
		if len(cps) == 0 || c > cps[len(cps)-1] {
			cps = append(cps, c)
		}
		x *= 1.25
	}
	return append(cps, total)
}

// ErrInvalidJob is returned for malformed jobs.
var ErrInvalidJob = errors.New("invalid job")

// PointsPositions returns how many positions score points for a grid size.
func PointsPositions(cars int) int { return min(10, (cars+1)/2) }

// Run executes the job. emit is called synchronously, from a single
// goroutine, at every checkpoint (and once with Final=true). If ctx ends
// early, the last emitted progress has Truncated=true and Run returns it with
// ctx.Err().
func Run(ctx context.Context, job Job, emit func(Progress)) (Progress, error) {
	sc := job.Scenario
	ns := len(job.Strategies)
	if sc == nil || ns == 0 || ns > MaxStrategies || job.Sims < MinSims || job.Sims > MaxSims {
		return Progress{}, fmt.Errorf("%w: %d strategies, %d sims", ErrInvalidJob, ns, job.Sims)
	}
	for _, s := range job.Strategies {
		if err := s.Validate(sc.Laps()); err != nil {
			return Progress{}, fmt.Errorf("%w: %w", ErrInvalidJob, err)
		}
	}
	workers := job.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	workers = min(workers, MaxWorkers)

	total := job.Sims
	nb := (total + BatchSize - 1) / BatchSize
	pos := make([][]uint8, ns)     // finishing position 1..N, or N+1 for a retirement
	times := make([][]float64, ns) // race time, s (NaN for a retirement)
	for s := range pos {
		pos[s] = make([]uint8, total)
		times[s] = make([]float64, total)
	}
	root := rng.New(sc.Seed)
	cars := sc.N()

	var next atomic.Int64
	done := make(chan int, nb)
	var wg sync.WaitGroup
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var panicErr atomic.Value
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicErr.Store(fmt.Errorf("simulation worker panic: %v", r))
					cancel()
				}
			}()
			for {
				b := int(next.Add(1) - 1)
				if b >= nb || wctx.Err() != nil {
					return
				}
				lo, hi := b*BatchSize, min(total, (b+1)*BatchSize)
				for i := lo; i < hi; i++ {
					simRNG := root.Derive(rng.LabelSim, uint64(i))
					for s := 0; s < ns; s++ {
						st := sc.NewState(simRNG, job.Strategies[s])
						sc.Run(&st, nil)
						p := sc.Player
						if st.Status[p] == race.Running {
							pos[s][i] = uint8(st.Position(p))
							times[s][i] = st.Time[p]
						} else {
							pos[s][i] = uint8(cars + 1)
							times[s][i] = math.NaN()
						}
					}
				}
				done <- b
			}
		}()
	}
	go func() { wg.Wait(); close(done) }()

	agg := newAggregator(job, cars)
	cps := Checkpoints(total)
	ci := 0
	finished := make([]bool, nb)
	prefixB := 0 // batches [0, prefixB) are done
	var last Progress
	for b := range done {
		finished[b] = true
		for prefixB < nb && finished[prefixB] {
			prefixB++
		}
		prefix := min(total, prefixB*BatchSize)
		for ci < len(cps) && cps[ci] <= prefix {
			agg.advance(pos, cps[ci])
			last = agg.snapshot(total, pos, times)
			last.Final = cps[ci] == total
			if emit != nil {
				emit(last)
			}
			ci++
		}
	}
	if err, ok := panicErr.Load().(error); ok {
		return last, err
	}
	if last.Final {
		return last, nil
	}
	// Interrupted: report what was computed, flagged as truncated.
	agg.advance(pos, min(total, prefixB*BatchSize))
	last = agg.snapshot(total, pos, times)
	last.Truncated = true
	last.Final = true
	if emit != nil {
		emit(last)
	}
	err := ctx.Err()
	if err == nil {
		err = context.Canceled
	}
	return last, err
}

// aggregator maintains incremental counters over the done prefix. Counters
// are updated in simulation-index order only, so floating-point sums are
// reproducible.
type aggregator struct {
	job    Job
	cars   int
	upTo   int
	hist   [][]int
	sumPos []float64
	sumSq  []float64
	ahead  [][]int
}

func newAggregator(job Job, cars int) *aggregator {
	ns := len(job.Strategies)
	a := &aggregator{job: job, cars: cars, hist: make([][]int, ns), sumPos: make([]float64, ns), sumSq: make([]float64, ns), ahead: make([][]int, ns)}
	for s := range a.hist {
		a.hist[s] = make([]int, cars+1)
		a.ahead[s] = make([]int, ns)
	}
	return a
}

func (a *aggregator) advance(pos [][]uint8, upTo int) {
	ns := len(pos)
	for i := a.upTo; i < upTo; i++ {
		for s := 0; s < ns; s++ {
			p := int(pos[s][i])
			a.hist[s][p-1]++
			a.sumPos[s] += float64(p)
			a.sumSq[s] += float64(p * p)
			for t := 0; t < ns; t++ {
				if p < int(pos[t][i]) {
					a.ahead[s][t]++
				}
			}
		}
	}
	a.upTo = upTo
}

// snapshot summarises the prefix [0, a.upTo). Every float in the result is
// finite (JSON-safe), including for an empty prefix.
func (a *aggregator) snapshot(total int, pos [][]uint8, times [][]float64) Progress {
	src := a
	n := a.upTo
	ns := len(pos)
	top := PointsPositions(a.cars)
	pr := Progress{Done: n, Total: total, Races: n * ns, PointsTop: top}
	for s := 0; s < ns; s++ {
		h := append([]int(nil), src.hist[s]...)
		win := h[0]
		pod, pts := 0, 0
		for p := 0; p < a.cars; p++ {
			if p < 3 {
				pod += h[p]
			}
			if p < top {
				pts += h[p]
			}
		}
		ps := make([]float64, n)
		ft := make([]float64, 0, n)
		for i := 0; i < n; i++ {
			ps[i] = float64(pos[s][i])
			if !math.IsNaN(times[s][i]) {
				ft = append(ft, times[s][i])
			}
		}
		sort.Float64s(ps)
		sort.Float64s(ft)
		if n == 0 {
			pr.Stats = append(pr.Stats, Stats{Name: a.job.Strategies[s].Name, Plan: a.job.Strategies[s].String(), Hist: h, Win: stats.Wilson(0, 0), Podium: stats.Wilson(0, 0), Points: stats.Wilson(0, 0), DNF: stats.Wilson(0, 0), Ahead: make([]float64, ns)})
			continue
		}
		st := Stats{
			Name:     a.job.Strategies[s].Name,
			Plan:     a.job.Strategies[s].String(),
			N:        n,
			Hist:     h,
			Win:      stats.Wilson(win, n),
			Podium:   stats.Wilson(min(pod, n), n),
			Points:   stats.Wilson(min(pts, n), n),
			DNF:      stats.Wilson(h[a.cars], n),
			MeanPos:  stats.MeanCI(src.sumPos[s], src.sumSq[s], n),
			MedPos:   stats.Quantile(ps, 0.5),
			P95Pos:   stats.Quantile(ps, 0.95),
			CVaRPos:  stats.CVaRHigh(ps, 0.05),
			BestPos:  int(ps[0]),
			WorstPos: int(ps[n-1]),
			Ahead:    make([]float64, ns),
		}
		if len(ft) > 0 {
			st.TimeMed = stats.MedianCI(ft)
			st.TimeP5 = stats.Quantile(ft, 0.05)
			st.TimeP95 = stats.Quantile(ft, 0.95)
		} else {
			st.TimeMed = stats.Interval{}
		}
		for t := 0; t < ns; t++ {
			st.Ahead[t] = float64(src.ahead[s][t]) / float64(n)
		}
		pr.Stats = append(pr.Stats, st)
	}
	return pr
}
