package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"pitwall/internal/montecarlo"
	"pitwall/internal/race"
)

// SimProgressView is the payload of sim.progress / sim.done.
type SimProgressView struct {
	Run int `json:"run"`
	montecarlo.Progress
	ElapsedMs int64  `json:"elapsedMs"`
	Reason    string `json:"reason,omitempty"`
}

func (ss *Session) cancelSim() {
	ss.mu.Lock()
	cancel, done := ss.simCancel, ss.simDone
	ss.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
}

func (cl *client) simStart(ctx context.Context, env Envelope) {
	m, pe := decodeData[SimStartMsg](env.Data)
	if pe != nil {
		cl.sendErr(env.ID, pe)
		return
	}
	seed, pe := ParseSeed(m.Seed)
	if pe != nil {
		cl.sendErr(env.ID, pe)
		return
	}
	cars, pe := parseCars(m.Cars)
	if pe != nil {
		cl.sendErr(env.ID, pe)
		return
	}
	if len(m.Strategies) == 0 || len(m.Strategies) > montecarlo.MaxStrategies {
		cl.sendErr(env.ID, perr(CodeInvalid, "strategies", "entre 1 et %d stratégies à comparer (reçu %d)", montecarlo.MaxStrategies, len(m.Strategies)))
		return
	}
	sims, pe := parseInt(m.Sims, "sims", montecarlo.MinSims, montecarlo.MaxSims)
	if pe != nil {
		cl.sendErr(env.ID, pe)
		return
	}
	live := true
	switch m.Pace {
	case "", "live":
	case "fast":
		live = false
	default:
		cl.sendErr(env.ID, perr(CodeInvalid, "pace", "pace doit valoir \"live\" ou \"fast\""))
		return
	}
	sc, err := cl.srv.scen.get(seed, cars)
	if err != nil {
		cl.sendErr(env.ID, perr(CodeInvalid, "seed", "%s", err.Error()))
		return
	}
	strats := make([]race.Strategy, 0, len(m.Strategies))
	for i, d := range m.Strategies {
		s, pe := d.ToStrategy(sc.Laps(), fmt.Sprintf("strategies[%d]", i))
		if pe != nil {
			cl.sendErr(env.ID, pe)
			return
		}
		if s.Name == "" {
			s.Name = string(rune('A' + i))
		}
		strats = append(strats, s)
	}

	ss := cl.session
	ss.cancelSim() // one search per session: a new request replaces the old one
	select {
	case cl.srv.jobs <- struct{}{}:
	default:
		cl.sendErr(env.ID, perr(CodeBusy, "", "serveur occupé : trop de simulations en cours, réessayez dans un instant"))
		return
	}
	jctx, cancel := context.WithTimeout(ctx, cl.srv.cfg.JobTimeout)
	done := make(chan struct{})
	ss.mu.Lock()
	ss.simRun++
	run := ss.simRun
	ss.simCancel, ss.simDone = cancel, done
	ss.mu.Unlock()
	cl.send("sim.started", env.ID, map[string]any{"run": run, "sims": sims, "strategies": len(strats)}, true)

	go func() {
		defer func() {
			if v := recover(); v != nil {
				cl.srv.cfg.Logger.Error("sim panic", "err", v)
				ss.send("error", env.ID, perr(CodeInternal, "", "erreur interne du simulateur"), true)
			}
			cancel()
			<-cl.srv.jobs
			ss.mu.Lock()
			if ss.simRun == run {
				ss.simCancel, ss.simDone = nil, nil
			}
			ss.mu.Unlock()
			close(done)
		}()
		start := time.Now()
		var lastEmit time.Time
		emit := func(p montecarlo.Progress) {
			if p.Final {
				return // sent below with the outcome
			}
			if live {
				if wait := cl.srv.cfg.MinEmitInterval - time.Since(lastEmit); wait > 0 {
					select {
					case <-time.After(wait):
					case <-jctx.Done():
						return
					}
				}
			}
			lastEmit = time.Now()
			ss.send("sim.progress", env.ID, SimProgressView{Run: run, Progress: p, ElapsedMs: time.Since(start).Milliseconds()}, false)
		}
		final, err := montecarlo.Run(jctx, montecarlo.Job{Scenario: sc, Strategies: strats, Sims: sims, Workers: cl.srv.cfg.Workers}, emit)
		v := SimProgressView{Run: run, Progress: final, ElapsedMs: time.Since(start).Milliseconds()}
		switch {
		case err == nil:
		case errors.Is(err, context.DeadlineExceeded):
			v.Reason = "budget de temps atteint : résultat partiel"
		case errors.Is(err, context.Canceled):
			v.Reason = "annulé"
		default:
			v.Reason = "erreur : " + err.Error()
		}
		ss.send("sim.done", env.ID, v, true)
	}()
}
