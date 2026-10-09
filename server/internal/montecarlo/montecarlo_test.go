package montecarlo

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"pitwall/internal/model"
	"pitwall/internal/race"
)

func scenario(t testing.TB, seed uint64, cars int) *race.Scenario {
	t.Helper()
	sc, err := race.NewScenario(seed, race.Options{Cars: cars})
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func strategies(laps int) []race.Strategy {
	return []race.Strategy{
		{Name: "A", Start: model.Medium, Stops: []race.Stop{{Lap: laps / 2, Compound: model.Hard}}},
		{Name: "B", Start: model.Soft, Stops: []race.Stop{{Lap: laps / 3, Compound: model.Medium}, {Lap: 2 * laps / 3, Compound: model.Hard}}},
	}
}

func collect(t *testing.T, job Job) ([]Progress, Progress) {
	t.Helper()
	var ps []Progress
	final, err := Run(context.Background(), job, func(p Progress) { ps = append(ps, p) })
	if err != nil {
		t.Fatal(err)
	}
	return ps, final
}

// The central determinism guarantee: identical progress stream and final
// result with 1 worker and with 16 workers.
func TestDeterminismAcrossWorkers(t *testing.T) {
	sc := scenario(t, 2024, 20)
	job := Job{Scenario: sc, Strategies: strategies(sc.Laps()), Sims: 1200}
	job.Workers = 1
	p1, f1 := collect(t, job)
	job.Workers = 16
	p16, f16 := collect(t, job)
	if !reflect.DeepEqual(f1, f16) {
		t.Fatal("final result depends on the number of workers")
	}
	if !reflect.DeepEqual(p1, p16) {
		t.Fatal("progress stream depends on the number of workers")
	}
	a, _ := json.Marshal(f1)
	b, _ := json.Marshal(f16)
	if string(a) != string(b) {
		t.Fatal("JSON differs")
	}
}

func TestStatsCoherent(t *testing.T) {
	sc := scenario(t, 7, 10)
	_, f := collect(t, Job{Scenario: sc, Strategies: strategies(sc.Laps()), Sims: 800, Workers: 4})
	if !f.Final || f.Truncated || f.Done != 800 || f.Races != 1600 {
		t.Fatalf("bad final %+v", f)
	}
	for _, s := range f.Stats {
		sum := 0
		for _, h := range s.Hist {
			sum += h
		}
		if sum != 800 {
			t.Fatalf("histogram sums to %d", sum)
		}
		if !(s.Win.P <= s.Podium.P && s.Podium.P <= s.Points.P) {
			t.Fatalf("win <= podium <= points violated: %+v", s)
		}
		for _, iv := range []struct{ p, lo, hi float64 }{{s.Win.P, s.Win.Lo, s.Win.Hi}, {s.Podium.P, s.Podium.Lo, s.Podium.Hi}} {
			if iv.lo > iv.p || iv.p > iv.hi {
				t.Fatalf("interval does not contain estimate")
			}
		}
		if s.BestPos < 1 || s.WorstPos > 11 || s.MedPos < 1 {
			t.Fatalf("positions out of range %+v", s)
		}
		if s.TimeMed.P <= 0 || s.TimeP5 > s.TimeMed.P || s.TimeMed.P > s.TimeP95 {
			t.Fatalf("time quantiles %+v", s)
		}
	}
	// paired comparison: ahead + behind + same position = 1
	ab, ba := f.Stats[0].Ahead[1], f.Stats[1].Ahead[0]
	if ab+ba > 1+1e-12 || ab+ba <= 0 || f.Stats[0].Ahead[0] != 0 {
		t.Fatalf("ahead fractions %v %v", ab, ba)
	}
	if _, err := json.Marshal(f); err != nil {
		t.Fatal(err)
	}
}

// The 95% interval of a probability must shrink like 1/sqrt(N).
func TestConvergenceRate(t *testing.T) {
	sc := scenario(t, 11, 10)
	ps, _ := collect(t, Job{Scenario: sc, Strategies: strategies(sc.Laps())[:1], Sims: 6400, Workers: 4})
	width := func(n int) float64 {
		for _, p := range ps {
			if p.Done == n {
				return p.Stats[0].Podium.Hi - p.Stats[0].Podium.Lo
			}
		}
		t.Fatalf("no checkpoint at %d", n)
		return 0
	}
	cps := Checkpoints(6400)
	small := cps[3] // a few hundred
	r := width(small) / width(6400) / math.Sqrt(6400/float64(small))
	if r < 0.7 || r > 1.4 {
		t.Fatalf("interval width does not scale as 1/sqrt(N): normalised ratio %v", r)
	}
}

func TestCheckpoints(t *testing.T) {
	for _, total := range []int{1, 24, 25, 26, 1000, 50000} {
		cps := Checkpoints(total)
		if cps[len(cps)-1] != total {
			t.Fatalf("last checkpoint must be total")
		}
		for i := 1; i < len(cps); i++ {
			if cps[i] <= cps[i-1] {
				t.Fatalf("checkpoints not increasing: %v", cps)
			}
		}
		if len(cps) > 60 {
			t.Fatalf("too many checkpoints: %d", len(cps))
		}
	}
}

func TestCancel(t *testing.T) {
	sc := scenario(t, 3, 20)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	var last Progress
	f, err := Run(ctx, Job{Scenario: sc, Strategies: strategies(sc.Laps()), Sims: MaxSims, Workers: 2}, func(p Progress) { last = p })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline error, got %v", err)
	}
	if !f.Truncated || !last.Truncated || f.Done >= MaxSims {
		t.Fatalf("truncated result expected: %+v", f.Done)
	}
	if _, err := json.Marshal(f); err != nil {
		t.Fatalf("truncated result must be JSON-safe: %v", err)
	}
}

func TestCancelBeforeStart(t *testing.T) {
	sc := scenario(t, 3, 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f, err := Run(ctx, Job{Scenario: sc, Strategies: strategies(sc.Laps()), Sims: 1000}, nil)
	if err == nil || !f.Truncated {
		t.Fatal("expected cancellation")
	}
	if _, err := json.Marshal(f); err != nil {
		t.Fatalf("empty truncated result must be JSON-safe: %v", err)
	}
}

func TestInvalidJobs(t *testing.T) {
	sc := scenario(t, 1, 10)
	good := strategies(sc.Laps())
	bad := []Job{
		{Scenario: nil, Strategies: good, Sims: 10},
		{Scenario: sc, Strategies: nil, Sims: 10},
		{Scenario: sc, Strategies: good, Sims: 0},
		{Scenario: sc, Strategies: good, Sims: MaxSims + 1},
		{Scenario: sc, Strategies: append(append(good, good...), good...), Sims: 10},
		{Scenario: sc, Strategies: []race.Strategy{{Start: model.Medium}}, Sims: 10},
	}
	for i, j := range bad {
		if _, err := Run(context.Background(), j, nil); !errors.Is(err, ErrInvalidJob) {
			t.Fatalf("job %d: expected ErrInvalidJob, got %v", i, err)
		}
	}
}

func BenchmarkMonteCarlo(b *testing.B) {
	sc := scenario(b, 1, 20)
	job := Job{Scenario: sc, Strategies: strategies(sc.Laps())[:1], Sims: 2000}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Run(context.Background(), job, nil)
	}
	b.ReportMetric(float64(b.N*2000)/b.Elapsed().Seconds(), "races/s")
}
