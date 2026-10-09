package api

import (
	"sync"
	"time"

	"pitwall/internal/race"
	"pitwall/internal/rng"
)

// liveRace runs one race realisation in real time for a session.
//
// The server keeps a virtual playhead (race seconds) that advances at
// `speed` race-seconds per real second, and sends lap j as soon as the
// playhead reaches the end of lap j-2 by the leader: the client always holds
// one lap of look-ahead, so its animation never starves.
type liveRace struct {
	sess  *Session
	sc    *race.Scenario
	seed  string
	strat race.Strategy

	mu         sync.Mutex
	st         race.State
	laps       []LapView
	ends       []float64 // leader end time of each sent lap
	speed      float64
	paused     bool
	autoPaused bool
	finished   bool
	playhead   float64

	ctrl     chan ctrlMsg
	quit     chan struct{}
	quitOnce sync.Once
}

type ctrlMsg struct {
	action string
	speed  float64
	auto   bool
}

// RaceStateView is the race.state payload.
type RaceStateView struct {
	Status     string  `json:"status"` // running | paused | finished | stopped
	Speed      float64 `json:"speed"`
	Lap        int     `json:"lap"`
	Laps       int     `json:"laps"`
	Playhead   float64 `json:"playhead"`
	AutoPaused bool    `json:"autoPaused"`
}

// RaceSyncView resynchronises a reconnecting client.
type RaceSyncView struct {
	Seed     string        `json:"seed"`
	Cars     int           `json:"cars"`
	Strategy StrategyDTO   `json:"strategy"`
	State    RaceStateView `json:"state"`
	Laps     []LapView     `json:"laps"`
}

func newLiveRace(sess *Session, sc *race.Scenario, seed string, strat race.Strategy, speed float64) *liveRace {
	lr := &liveRace{sess: sess, sc: sc, seed: seed, strat: strat, speed: speed, ctrl: make(chan ctrlMsg, 16), quit: make(chan struct{})}
	lr.st = sc.NewState(rng.New(sc.Seed).Derive(rng.LabelLive), strat)
	return lr
}

func (lr *liveRace) stop() { lr.quitOnce.Do(func() { close(lr.quit) }) }

func (lr *liveRace) control(m ctrlMsg) {
	select {
	case lr.ctrl <- m:
	default:
	}
}

func (lr *liveRace) stateLocked() RaceStateView {
	status := "running"
	switch {
	case lr.finished:
		status = "finished"
	case lr.paused:
		status = "paused"
	}
	return RaceStateView{Status: status, Speed: lr.speed, Lap: lr.st.Lap, Laps: lr.st.Laps, Playhead: round(lr.playhead, 3), AutoPaused: lr.autoPaused}
}

func (lr *liveRace) syncView() RaceSyncView {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	return RaceSyncView{Seed: lr.seed, Cars: lr.sc.N(), Strategy: StrategyToDTO(lr.strat), State: lr.stateLocked(), Laps: append([]LapView(nil), lr.laps...)}
}

// step computes and sends the next lap. Caller holds lr.mu.
func (lr *liveRace) stepLocked() {
	var log race.Log
	lr.sc.Step(&lr.st, &log)
	lv := newLapView(lr.sc, &lr.st, log.Events)
	lr.laps = append(lr.laps, lv)
	lr.ends = append(lr.ends, lr.st.Time[lr.st.Order[0]])
	lr.sess.send("race.lap", "", lv, true)
}

func (lr *liveRace) run() {
	defer func() {
		if v := recover(); v != nil {
			lr.sess.send("error", "", perr(CodeInternal, "", "erreur interne de la course"), true)
		}
	}()
	lr.mu.Lock()
	lr.stepLocked()
	if !lr.st.Done() {
		lr.stepLocked()
	}
	lr.sess.send("race.state", "", lr.stateLocked(), true)
	lr.mu.Unlock()

	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	last := time.Now()
	for {
		select {
		case <-lr.quit:
			return
		case m := <-lr.ctrl:
			lr.mu.Lock()
			switch m.action {
			case "pause":
				lr.paused = true
				lr.autoPaused = m.auto
			case "resume":
				lr.paused, lr.autoPaused = false, false
			case "speed":
				lr.speed = m.speed
			case "stop":
				lr.mu.Unlock()
				return
			}
			last = time.Now()
			st := lr.stateLocked()
			lr.mu.Unlock()
			lr.sess.send("race.state", "", st, true)
		case now := <-tick.C:
			lr.mu.Lock()
			dt := now.Sub(last).Seconds()
			last = now
			if !lr.paused && !lr.finished {
				lr.playhead += dt * lr.speed
				for !lr.st.Done() && len(lr.ends) >= 2 && lr.playhead >= lr.ends[len(lr.ends)-2] {
					lr.stepLocked()
				}
				if lr.st.Done() && lr.playhead >= lr.ends[len(lr.ends)-1] {
					lr.finished = true
					lr.sess.send("race.state", "", lr.stateLocked(), true)
				}
			}
			lr.mu.Unlock()
		}
	}
}

func (cl *client) raceStart(env Envelope) {
	m, pe := decodeData[RaceStartMsg](env.Data)
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
	speed := 30.0
	if m.Speed != nil {
		if speed, pe = parseFloat(*m.Speed, "speed", MinSpeed, MaxSpeed); pe != nil {
			cl.sendErr(env.ID, pe)
			return
		}
	}
	sc, err := cl.srv.scen.get(seed, cars)
	if err != nil {
		cl.sendErr(env.ID, perr(CodeInvalid, "seed", "%s", err.Error()))
		return
	}
	strat, pe := m.Strategy.ToStrategy(sc.Laps(), "strategy")
	if pe != nil {
		cl.sendErr(env.ID, pe)
		return
	}
	ss := cl.session
	lr := newLiveRace(ss, sc, seed, strat, speed)
	ss.mu.Lock()
	old := ss.live
	ss.live = lr
	ss.mu.Unlock()
	if old != nil {
		old.stop()
	}
	cl.send("race.started", env.ID, map[string]any{"seed": seed, "cars": cars, "laps": sc.Laps(), "speed": speed, "strategy": StrategyToDTO(strat)}, true)
	go lr.run()
}

func (cl *client) raceControl(env Envelope) {
	m, pe := decodeData[RaceControlMsg](env.Data)
	if pe != nil {
		cl.sendErr(env.ID, pe)
		return
	}
	ss := cl.session
	ss.mu.Lock()
	lr := ss.live
	ss.mu.Unlock()
	if lr == nil {
		cl.sendErr(env.ID, perr(CodeNoRace, "", "aucune course en cours"))
		return
	}
	switch m.Action {
	case "pause", "resume", "stop":
		if m.Speed != nil {
			cl.sendErr(env.ID, perr(CodeInvalid, "speed", "speed n'est accepté qu'avec action=\"speed\""))
			return
		}
		lr.control(ctrlMsg{action: m.Action})
		if m.Action == "stop" {
			ss.mu.Lock()
			if ss.live == lr {
				ss.live = nil
			}
			ss.mu.Unlock()
		}
	case "speed":
		if m.Speed == nil {
			cl.sendErr(env.ID, perr(CodeInvalid, "speed", "speed manquant"))
			return
		}
		v, pe := parseFloat(*m.Speed, "speed", MinSpeed, MaxSpeed)
		if pe != nil {
			cl.sendErr(env.ID, pe)
			return
		}
		lr.control(ctrlMsg{action: "speed", speed: v})
	default:
		cl.sendErr(env.ID, perr(CodeInvalid, "action", "action inconnue %q (pause, resume, speed, stop)", trunc(m.Action, 16)))
	}
}
