package montecarlo

// Red-team attack tests on the Monte Carlo runner.

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"pitwall/internal/model"
	"pitwall/internal/race"
)

// Determinism with odd sim counts (not multiples of BatchSize) and odd
// worker counts: progress stream and final result byte-identical.
func TestRedteamDeterminismOddSimsAndWorkers(t *testing.T) {
	sc := scenario(t, 77, 20)
	strats := strategies(sc.Laps())
	for _, sims := range []int{1, 24, 26, 1001} {
		var ref string
		for _, w := range []int{1, 3, 7, 16} {
			ps, f := collect(t, Job{Scenario: sc, Strategies: strats, Sims: sims, Workers: w})
			b, err := json.Marshal(struct {
				P []Progress
				F Progress
			}{ps, f})
			if err != nil {
				t.Fatal(err)
			}
			if ref == "" {
				ref = string(b)
			} else if ref != string(b) {
				t.Fatalf("sims=%d workers=%d: output differs from workers=1", sims, w)
			}
			if f.Done != sims || !f.Final || f.Truncated {
				t.Fatalf("sims=%d: final %+v", sims, f.Done)
			}
		}
	}
}

func checkProgress(t *testing.T, p Progress, cars, ns int) {
	t.Helper()
	if _, err := json.Marshal(p); err != nil {
		t.Fatalf("progress not JSON-safe: %v", err)
	}
	if p.Done < 0 || p.Done > p.Total || p.Races != p.Done*ns || len(p.Stats) != ns {
		t.Fatalf("incoherent progress: done=%d total=%d races=%d stats=%d", p.Done, p.Total, p.Races, len(p.Stats))
	}
	for _, s := range p.Stats {
		sum := 0
		for _, h := range s.Hist {
			sum += h
		}
		if len(s.Hist) != cars+1 || sum != p.Done || s.N != p.Done || len(s.Ahead) != ns {
			t.Fatalf("incoherent stats: hist=%v done=%d n=%d", s.Hist, p.Done, s.N)
		}
		for _, v := range []float64{s.Win.P, s.Win.Lo, s.Win.Hi, s.MeanPos.P, s.MeanPos.Lo, s.MeanPos.Hi, s.MedPos, s.P95Pos, s.CVaRPos, s.TimeMed.P, s.TimeMed.Lo, s.TimeMed.Hi, s.TimeP5, s.TimeP95} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				t.Fatalf("bad number %v in %+v", v, s)
			}
		}
		// exact containment is checked in stats (TestRedteamWilsonContainsEstimate)
		if s.Win.Lo > s.Win.P+1e-12 || s.Win.P > s.Win.Hi+1e-12 || s.Win.Hi > 1 {
			t.Fatalf("bad Wilson interval %+v", s.Win)
		}
		if p.Done > 0 && (s.BestPos < 1 || s.WorstPos > cars+1 || s.BestPos > s.WorstPos) {
			t.Fatalf("bad best/worst %d/%d", s.BestPos, s.WorstPos)
		}
	}
}

// Cancellation at many different moments: the result and every emitted
// progress stay JSON-safe and coherent; the stream is monotone.
func TestRedteamCancelAtRandomMoments(t *testing.T) {
	for _, cars := range []int{1, 20} {
		sc := scenario(t, 5, cars)
		strats := append(strategies(sc.Laps()),
			race.Strategy{Name: "5", Start: model.Soft, Stops: []race.Stop{{Lap: 1, Compound: model.Medium}, {Lap: 2, Compound: model.Hard}, {Lap: 3, Compound: model.Soft}, {Lap: 4, Compound: model.Medium}, {Lap: sc.Laps() - 1, Compound: model.Hard}}},
			race.Strategy{Name: "late", Start: model.Hard, Stops: []race.Stop{{Lap: sc.Laps() - 1, Compound: model.Soft}}})
		for i := 0; i < 30; i++ {
			ctx, cancel := context.WithCancel(context.Background())
			delay := time.Duration(i*i*37) * time.Microsecond
			go func() { time.Sleep(delay); cancel() }()
			var ps []Progress
			f, err := Run(ctx, Job{Scenario: sc, Strategies: strats, Sims: 20000, Workers: 1 + i%5}, func(p Progress) { ps = append(ps, p) })
			cancel()
			if err == nil && !(f.Done == f.Total && !f.Truncated) {
				t.Fatalf("nil error but incomplete result")
			}
			checkProgress(t, f, cars, len(strats))
			if !f.Final {
				t.Fatal("returned progress not final")
			}
			prev := -1
			for k, p := range ps {
				checkProgress(t, p, cars, len(strats))
				if p.Done < prev {
					t.Fatalf("progress went backwards: %d after %d", p.Done, prev)
				}
				if p.Final && k != len(ps)-1 {
					t.Fatal("progress emitted after the final one")
				}
				prev = p.Done
			}
			if len(ps) == 0 || !ps[len(ps)-1].Final {
				t.Fatal("no final progress emitted")
			}
		}
	}
}

// The maximal job (50 000 sims × 4 strategies) must be cancellable quickly.
func TestRedteamMaxJobCancelsFast(t *testing.T) {
	sc := scenario(t, 9, 20)
	s := strategies(sc.Laps())
	strats := []race.Strategy{s[0], s[1], s[0], s[1]}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	f, err := Run(ctx, Job{Scenario: sc, Strategies: strats, Sims: MaxSims, Workers: 2}, nil)
	if err == nil {
		t.Skip("machine too fast to observe cancellation")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("cancellation took %v", d)
	}
	checkProgress(t, f, 20, 4)
	if !f.Truncated {
		t.Fatal("cancelled job not flagged truncated")
	}
}

// Invalid jobs are refused, never panic.
func TestRedteamInvalidJobsNoPanic(t *testing.T) {
	sc := scenario(t, 1, 10)
	good := strategies(sc.Laps())
	jobs := []Job{
		{},
		{Scenario: sc},
		{Scenario: sc, Strategies: good, Sims: 0},
		{Scenario: sc, Strategies: good, Sims: -1},
		{Scenario: sc, Strategies: good, Sims: MaxSims + 1},
		{Scenario: sc, Strategies: []race.Strategy{good[0], good[1], good[0], good[1], good[0]}, Sims: 10},
		{Scenario: sc, Strategies: []race.Strategy{{Start: model.Compound(9)}}, Sims: 10},
		{Scenario: sc, Strategies: []race.Strategy{{Start: model.Medium, Stops: []race.Stop{{Lap: 999, Compound: model.Hard}}}}, Sims: 10},
	}
	for i, j := range jobs {
		if _, err := Run(context.Background(), j, nil); err == nil {
			t.Errorf("job %d accepted", i)
		}
	}
	// absurd worker counts are clamped
	for _, w := range []int{-1000, 0, 1 << 30} {
		if _, err := Run(context.Background(), Job{Scenario: sc, Strategies: good, Sims: 30, Workers: w}, nil); err != nil {
			t.Errorf("workers=%d: %v", w, err)
		}
	}
}
