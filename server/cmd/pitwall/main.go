// Command pitwall is the PIT WALL server and toolbox.
//
//	pitwall serve   [-addr :8080] [-web dir] [-origins host,...]
//	pitwall sim     -seed S [-cars 10] [-sims 5000] [-workers 0] [-json] STRATEGY...
//	pitwall verify  [-seeds 5] [-sims 2000] [-workers 16]
//	pitwall bench   [-dur 3s] [-min 0]
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"pitwall/internal/api"
	"pitwall/internal/montecarlo"
	"pitwall/internal/race"
	"pitwall/internal/rng"
	"pitwall/internal/webui"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "sim":
		err = sim(os.Args[2:], os.Stdout)
	case "verify":
		err = verify(os.Args[2:], os.Stdout)
	case "bench":
		err = bench(os.Args[2:], os.Stdout)
	case "-h", "--help", "help":
		usage(os.Stdout)
		return
	default:
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pitwall:", err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `PIT WALL — race strategy command centre

usage:
  pitwall serve   [-addr :8080] [-web DIR] [-origins localhost:5173]
  pitwall sim     -seed SEED [-cars 10] [-sims 5000] [-workers 0] [-json] STRATEGY...
                  STRATEGY = GOMME[-TOUR-GOMME]...   e.g. M-23-H  S-14-M-35-H
  pitwall verify  [-seeds 5] [-sims 2000] [-workers 16]
  pitwall bench   [-dur 3s] [-min RACES_PER_SEC]
`)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", envOr("PITWALL_ADDR", ":"+envOr("PORT", "8080")), "listen address")
	web := fs.String("web", "", "serve the frontend from this directory instead of the embedded build")
	origins := fs.String("origins", envOr("PITWALL_ORIGINS", "localhost:5173,127.0.0.1:5173"), "extra allowed WebSocket origins")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg := api.DefaultConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	if *origins != "" {
		cfg.OriginPatterns = strings.Split(*origins, ",")
	}
	if *web != "" {
		cfg.Static = os.DirFS(*web)
	} else {
		cfg.Static = webui.FS()
	}
	srv := api.New(cfg)
	defer srv.Close()
	hs := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	cfg.Logger.Info("PIT WALL listening", "addr", *addr, "frontend", cfg.Static != nil)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return hs.Shutdown(sctx)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func sim(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("sim", flag.ContinueOnError)
	seed := fs.String("seed", "1", "seed code")
	cars := fs.Int("cars", 10, "grid size")
	sims := fs.Int("sims", 5000, "simulations per strategy")
	workers := fs.Int("workers", 0, "workers (0 = all cores)")
	asJSON := fs.Bool("json", false, "print the full JSON result")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, pe := api.ParseSeed(*seed); pe != nil {
		return pe
	}
	sc, err := race.NewScenario(rng.SeedFromString(*seed), race.Options{Cars: *cars})
	if err != nil {
		return err
	}
	var strats []race.Strategy
	in := fs.Args()
	if len(in) == 0 {
		strats = append(strats, sc.Rivals[sc.Player])
	}
	for _, a := range in {
		s, err := race.ParseStrategy(a)
		if err != nil {
			return err
		}
		if err := s.Validate(sc.Laps()); err != nil {
			return fmt.Errorf("%s: %w", a, err)
		}
		strats = append(strats, s)
	}
	t0 := time.Now()
	res, err := montecarlo.Run(context.Background(), montecarlo.Job{Scenario: sc, Strategies: strats, Sims: *sims, Workers: *workers}, nil)
	if err != nil {
		return err
	}
	el := time.Since(t0)
	if *asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	d := sc.Drivers[sc.Player]
	fmt.Fprintf(w, "%s — %s · %d laps · %.0f m · seed %s\n", sc.Track.Name, sc.Track.Country, sc.Laps(), sc.Track.LengthM, *seed)
	fmt.Fprintf(w, "player: #%d %s (%s), grid P%d\n\n", d.Number, d.Name, d.Team, gridPos(sc)+1)
	fmt.Fprintf(w, "%-16s %7s %7s %7s %7s %6s %6s %10s\n", "strategy", "win", "podium", "points", "E[pos]", "P95", "CVaR", "median t")
	for _, s := range res.Stats {
		fmt.Fprintf(w, "%-16s %6.1f%% %6.1f%% %6.1f%% %7.2f %6.1f %6.2f %10.2f\n", s.Plan, 100*s.Win.P, 100*s.Podium.P, 100*s.Points.P, s.MeanPos.P, s.P95Pos, s.CVaRPos, s.TimeMed.P)
	}
	fmt.Fprintf(w, "\n%d races in %v (%.0f races/s)\n", res.Races, el.Round(time.Millisecond), float64(res.Races)/el.Seconds())
	return nil
}

func gridPos(sc *race.Scenario) int {
	for p, c := range sc.Grid {
		if c == sc.Player {
			return p
		}
	}
	return 0
}

// verify is the determinism self-check run by CI and /determinism-check:
// the same job with 1 worker and with N workers must produce byte-identical
// JSON, for several seeds and grid sizes.
func verify(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	seeds := fs.Int("seeds", 5, "number of seeds")
	sims := fs.Int("sims", 2000, "simulations per strategy")
	workers := fs.Int("workers", 16, "parallel workers to compare with 1")
	if err := fs.Parse(args); err != nil {
		return err
	}
	failed := 0
	for i := 0; i < *seeds; i++ {
		for _, cars := range []int{10, 20} {
			seed := fmt.Sprintf("VERIFY-%d", i)
			sc, err := race.NewScenario(rng.SeedFromString(seed), race.Options{Cars: cars})
			if err != nil {
				return err
			}
			laps := sc.Laps()
			strats := []race.Strategy{sc.Rivals[sc.Player], {Name: "2S", Start: 0, Stops: []race.Stop{{Lap: laps / 3, Compound: 1}, {Lap: 2 * laps / 3, Compound: 2}}}}
			h := func(nw int) (string, error) {
				hs := sha256.New()
				res, err := montecarlo.Run(context.Background(), montecarlo.Job{Scenario: sc, Strategies: strats, Sims: *sims, Workers: nw}, func(p montecarlo.Progress) {
					b, _ := json.Marshal(p)
					hs.Write(b)
				})
				if err != nil {
					return "", err
				}
				b, _ := json.Marshal(res)
				hs.Write(b)
				return hex.EncodeToString(hs.Sum(nil))[:16], nil
			}
			a, err := h(1)
			if err != nil {
				return err
			}
			b, err := h(*workers)
			if err != nil {
				return err
			}
			status := "OK  "
			if a != b {
				status = "FAIL"
				failed++
			}
			fmt.Fprintf(w, "%s seed=%-9s cars=%2d sims=%d  1 worker %s  %d workers %s\n", status, seed, cars, *sims, a, *workers, b)
		}
	}
	if failed > 0 {
		return fmt.Errorf("determinism check failed for %d case(s)", failed)
	}
	fmt.Fprintln(w, "determinism: PASS")
	return nil
}

// bench reports races per second, single-core and all cores, for the
// reference workload (20 cars, 50 laps).
func bench(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	dur := fs.Duration("dur", 3*time.Second, "duration of each measurement")
	minRate := fs.Float64("min", 0, "fail if single-core races/s is below this")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sc, err := race.NewScenario(rng.SeedFromString("BENCH"), race.Options{Cars: 20})
	if err != nil {
		return err
	}
	sc = sc.WithLaps(50)
	strat := sc.Rivals[sc.Player]
	root := rng.New(1)
	single := func() float64 {
		n := 0
		t0 := time.Now()
		for time.Since(t0) < *dur {
			for k := 0; k < 200; k++ {
				st := sc.NewState(root.Derive(uint64(n)), strat)
				sc.Run(&st, nil)
				n++
			}
		}
		return float64(n) / time.Since(t0).Seconds()
	}
	r1 := single()
	fmt.Fprintf(w, "single core : %8.0f races/s  (20 cars × 50 laps, %.1f µs/race)\n", r1, 1e6/r1)
	t0 := time.Now()
	total := 0
	for time.Since(t0) < *dur {
		res, err := montecarlo.Run(context.Background(), montecarlo.Job{Scenario: sc, Strategies: []race.Strategy{strat}, Sims: 20000}, nil)
		if err != nil {
			return err
		}
		total += res.Races
	}
	fmt.Fprintf(w, "all cores   : %8.0f races/s  (%d cores, Monte Carlo engine)\n", float64(total)/time.Since(t0).Seconds(), runtime.GOMAXPROCS(0))
	if *minRate > 0 && r1 < *minRate {
		return fmt.Errorf("performance regression: %.0f races/s < %.0f", r1, *minRate)
	}
	return nil
}
