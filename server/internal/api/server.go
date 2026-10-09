package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"pitwall/internal/race"
	"pitwall/internal/rng"
)

// Config tunes the server.
type Config struct {
	MaxConns       int           // concurrent WebSocket connections
	MaxJobs        int           // concurrent Monte Carlo jobs, server-wide
	JobTimeout     time.Duration // time budget of one Monte Carlo job
	SessionTTL     time.Duration // how long a detached session survives
	Workers        int           // workers per Monte Carlo job (0 = GOMAXPROCS)
	RatePerSec     float64       // sustained messages per second per connection
	RateBurst      float64       // burst size
	OriginPatterns []string      // extra allowed origins (dev server)
	Static         fs.FS         // built frontend (optional)
	Logger         *slog.Logger
	// MinEmitInterval spaces "live" Monte Carlo progress messages so that the
	// convergence is visible; "fast" pace ignores it.
	MinEmitInterval time.Duration
}

// DefaultConfig returns production defaults.
func DefaultConfig() Config {
	return Config{
		MaxConns: 256, MaxJobs: 4, JobTimeout: 25 * time.Second, SessionTTL: 2 * time.Minute,
		RatePerSec: 15, RateBurst: 40, MinEmitInterval: 70 * time.Millisecond,
		Logger: slog.Default(),
	}
}

// Server is the HTTP + WebSocket server.
type Server struct {
	cfg      Config
	jobs     chan struct{}
	conns    atomic.Int64
	mu       sync.Mutex
	sessions map[string]*Session
	scen     *scenarioCache
	stop     chan struct{}
	stopOnce sync.Once
}

// New creates a server.
func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxJobs <= 0 {
		cfg.MaxJobs = 1
	}
	s := &Server{cfg: cfg, jobs: make(chan struct{}, cfg.MaxJobs), sessions: map[string]*Session{}, scen: newScenarioCache(64), stop: make(chan struct{})}
	go s.janitor()
	return s
}

// Close stops background goroutines and every session.
func (s *Server) Close() {
	s.stopOnce.Do(func() {
		close(s.stop)
		s.mu.Lock()
		for id, ss := range s.sessions {
			ss.shutdown()
			delete(s.sessions, id)
		}
		s.mu.Unlock()
	})
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"ok":true,"protocol":%d,"conns":%d}`, ProtocolVersion, s.conns.Load())
	})
	if s.cfg.Static != nil {
		mux.Handle("/", spaHandler(s.cfg.Static))
	}
	return s.recoverMW(securityHeaders(mux))
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	})
}

func (s *Server) recoverMW(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.cfg.Logger.Error("http panic", "err", v, "stack", string(debug.Stack()))
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		h.ServeHTTP(w, r)
	})
}

// spaHandler serves static files and falls back to index.html for unknown
// paths (client-side routing, ?seed=… links).
func spaHandler(fsys fs.FS) http.Handler {
	files := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(fsys, p); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			files.ServeHTTP(w, r2)
			return
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if n := s.conns.Add(1); n > int64(s.cfg.MaxConns) {
		s.conns.Add(-1)
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	defer s.conns.Add(-1)
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.cfg.OriginPatterns})
	if err != nil {
		return // Accept already wrote the HTTP error
	}
	c.SetReadLimit(MaxMessageBytes)
	cl := &client{srv: s, conn: c, out: make(chan outMsg, 512), tokens: s.cfg.RateBurst, last: time.Now()}
	ctx, cancel := context.WithCancel(r.Context())
	cl.cancel = cancel
	defer cancel()
	cl.serve(ctx)
}

// ---- sessions ----

// Session survives short disconnections: a reconnecting client that presents
// its session id gets its live race back (race.sync). Monte Carlo jobs are
// cancelled when the client leaves.
type Session struct {
	id         string
	mu         sync.Mutex
	cl         *client
	detachedAt time.Time
	simCancel  context.CancelFunc
	simDone    chan struct{}
	simRun     int
	live       *liveRace
}

func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Server) attach(id string, cl *client) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss, ok := s.sessions[id]; ok && id != "" {
		ss.mu.Lock()
		old := ss.cl
		ss.cl = cl
		ss.mu.Unlock()
		if old != nil && old != cl {
			old.cancel() // one connection per session
		}
		return ss, true
	}
	if len(s.sessions) >= s.cfg.MaxConns*2 {
		s.evictOldestLocked()
	}
	ss := &Session{id: newSessionID(), cl: cl}
	s.sessions[ss.id] = ss
	return ss, false
}

func (s *Server) evictOldestLocked() {
	var oldest *Session
	for _, ss := range s.sessions {
		ss.mu.Lock()
		det, at := ss.cl == nil, ss.detachedAt
		ss.mu.Unlock()
		if det && (oldest == nil || at.Before(oldest.detachedAt)) {
			oldest = ss
		}
	}
	if oldest != nil {
		oldest.shutdown()
		delete(s.sessions, oldest.id)
	}
}

func (ss *Session) detach(cl *client) {
	ss.mu.Lock()
	if ss.cl == cl {
		ss.cl = nil
		ss.detachedAt = time.Now()
	}
	cancel := ss.simCancel
	ss.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	ss.mu.Lock()
	lr := ss.live
	ss.mu.Unlock()
	if lr != nil {
		lr.control(ctrlMsg{action: "pause", auto: true})
	}
}

func (ss *Session) shutdown() {
	ss.mu.Lock()
	cancel := ss.simCancel
	lr := ss.live
	ss.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if lr != nil {
		lr.stop()
	}
}

// send delivers a message to the session's current client, if any.
func (ss *Session) send(typ, id string, data any, critical bool) {
	ss.mu.Lock()
	cl := ss.cl
	ss.mu.Unlock()
	if cl != nil {
		cl.send(typ, id, data, critical)
	}
}

func (s *Server) janitor() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.mu.Lock()
			for id, ss := range s.sessions {
				ss.mu.Lock()
				expired := ss.cl == nil && time.Since(ss.detachedAt) > s.cfg.SessionTTL
				ss.mu.Unlock()
				if expired {
					ss.shutdown()
					delete(s.sessions, id)
				}
			}
			s.mu.Unlock()
		}
	}
}

// ---- scenario cache ----

type scenarioCache struct {
	mu    sync.Mutex
	max   int
	order []string
	m     map[string]*race.Scenario
}

func newScenarioCache(n int) *scenarioCache {
	return &scenarioCache{max: n, m: map[string]*race.Scenario{}}
}

func (c *scenarioCache) get(seed string, cars int) (*race.Scenario, error) {
	key := fmt.Sprintf("%s/%d", seed, cars)
	c.mu.Lock()
	if sc, ok := c.m[key]; ok {
		c.mu.Unlock()
		return sc, nil
	}
	c.mu.Unlock()
	sc, err := race.NewScenario(rng.SeedFromString(seed), race.Options{Cars: cars})
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[key]; !ok {
		if len(c.order) >= c.max {
			delete(c.m, c.order[0])
			c.order = c.order[1:]
		}
		c.order = append(c.order, key)
		c.m[key] = sc
	}
	return c.m[key], nil
}

// ---- client connection ----

type outMsg struct {
	b        []byte
	critical bool
}

type client struct {
	srv     *Server
	conn    *websocket.Conn
	out     chan outMsg
	cancel  context.CancelFunc
	session *Session
	// token bucket
	tokens  float64
	last    time.Time
	strikes int
	dropped atomic.Int64
}

func (cl *client) serve(ctx context.Context) {
	defer func() {
		if v := recover(); v != nil {
			cl.srv.cfg.Logger.Error("connection panic", "err", v, "stack", string(debug.Stack()))
		}
		if cl.session != nil {
			cl.session.detach(cl)
		}
		_ = cl.conn.CloseNow()
	}()
	go cl.writer(ctx)
	for {
		typ, b, err := cl.conn.Read(ctx)
		if err != nil {
			return // closed, too big (StatusMessageTooBig), or cancelled
		}
		if typ != websocket.MessageText {
			cl.sendErr("", perr(CodeBadJSON, "", "messages binaires non supportés"))
			continue
		}
		if !cl.allow() {
			cl.strikes++
			if cl.strikes > 200 {
				_ = cl.conn.Close(websocket.StatusPolicyViolation, "rate limit")
				return
			}
			if cl.strikes%20 == 1 {
				cl.sendErr("", perr(CodeRateLimited, "", "trop de messages, ralentissez"))
			}
			continue
		}
		cl.handle(ctx, b)
	}
}

func (cl *client) allow() bool {
	now := time.Now()
	cl.tokens += now.Sub(cl.last).Seconds() * cl.srv.cfg.RatePerSec
	cl.last = now
	if cl.tokens > cl.srv.cfg.RateBurst {
		cl.tokens = cl.srv.cfg.RateBurst
	}
	if cl.tokens < 1 {
		return false
	}
	cl.tokens--
	return true
}

func (cl *client) writer(ctx context.Context) {
	defer func() {
		if v := recover(); v != nil {
			cl.srv.cfg.Logger.Error("writer panic", "err", v)
			cl.cancel()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-cl.out:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := cl.conn.Write(wctx, websocket.MessageText, m.b)
			cancel()
			if err != nil {
				cl.cancel()
				return
			}
		}
	}
}

// send queues a message. Non-critical messages (intermediate progress) are
// dropped when the client is slow; if a critical message cannot be queued,
// the connection is closed (the client will reconnect and resync).
func (cl *client) send(typ, id string, data any, critical bool) {
	raw, err := json.Marshal(data)
	if err != nil {
		cl.srv.cfg.Logger.Error("marshal", "type", typ, "err", err)
		typ, raw = "error", mustJSON(ProtoError{Code: CodeInternal, Message: "erreur interne d'encodage"})
	}
	b, _ := json.Marshal(Envelope{V: ProtocolVersion, Type: typ, ID: id, Data: raw})
	select {
	case cl.out <- outMsg{b: b, critical: critical}:
	default:
		cl.dropped.Add(1)
		if critical {
			cl.cancel()
		}
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func (cl *client) sendErr(id string, e *ProtoError) { cl.send("error", id, e, true) }

func (cl *client) handle(ctx context.Context, b []byte) {
	env, pe := DecodeEnvelope(b)
	if pe != nil {
		cl.sendErr(env.ID, pe)
		return
	}
	if cl.session == nil && env.Type != "hello" && env.Type != "ping" {
		// implicit hello: the protocol stays usable from a bare client
		cl.hello(HelloMsg{})
	}
	switch env.Type {
	case "hello":
		m, pe := decodeData[HelloMsg](env.Data)
		if pe != nil {
			cl.sendErr(env.ID, pe)
			return
		}
		if len(m.Session) > MaxSessionLen {
			cl.sendErr(env.ID, perr(CodeInvalid, "session", "session trop longue"))
			return
		}
		cl.hello(m)
	case "ping":
		cl.send("pong", env.ID, map[string]any{"t": time.Now().UnixMilli()}, false)
	case "scenario.get":
		cl.scenario(env)
	case "sim.start":
		cl.simStart(ctx, env)
	case "sim.cancel":
		cl.session.cancelSim()
		cl.send("sim.cancelled", env.ID, struct{}{}, true)
	case "race.start":
		cl.raceStart(env)
	case "race.control":
		cl.raceControl(env)
	default:
		cl.sendErr(env.ID, perr(CodeUnknownType, "type", "type de message inconnu %q", trunc(env.Type, 32)))
	}
}

func (cl *client) hello(m HelloMsg) {
	if cl.session != nil && (m.Session == "" || m.Session == cl.session.id) {
		cl.send("welcome", "", map[string]any{"session": cl.session.id, "protocol": ProtocolVersion, "resumed": true, "limits": limitsView()}, true)
		return
	}
	if cl.session != nil {
		cl.session.detach(cl)
	}
	ss, resumed := cl.srv.attach(m.Session, cl)
	cl.session = ss
	cl.send("welcome", "", map[string]any{"session": ss.id, "protocol": ProtocolVersion, "resumed": resumed, "limits": limitsView()}, true)
	ss.mu.Lock()
	lr := ss.live
	ss.mu.Unlock()
	if resumed && lr != nil {
		cl.send("race.sync", "", lr.syncView(), true)
	}
}

func (cl *client) scenario(env Envelope) {
	m, pe := decodeData[ScenarioMsg](env.Data)
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
	sc, err := cl.srv.scen.get(seed, cars)
	if err != nil {
		cl.sendErr(env.ID, perr(CodeInvalid, "seed", "%s", err.Error()))
		return
	}
	cl.send("scenario", env.ID, newScenarioView(seed, sc), true)
}
