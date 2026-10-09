package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func testServer(t *testing.T, mut func(*Config)) (*Server, *httptest.Server) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.MinEmitInterval = 0
	cfg.RateBurst = 1000
	cfg.RatePerSec = 1000
	if mut != nil {
		mut(&cfg)
	}
	s := New(cfg)
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(func() { hs.Close(); s.Close() })
	return s, hs
}

type wsc struct {
	t *testing.T
	c *websocket.Conn
}

func dial(t *testing.T, hs *httptest.Server) *wsc {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, //nolint:bodyclose // coder/websocket: resp.Body never needs closing
		"ws"+strings.TrimPrefix(hs.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadLimit(16 << 20)
	t.Cleanup(func() { _ = c.CloseNow() })
	return &wsc{t, c}
}

func (w *wsc) sendRaw(s string) {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.c.Write(ctx, websocket.MessageText, []byte(s)); err != nil {
		w.t.Fatal(err)
	}
}

func (w *wsc) send(typ string, data any) {
	w.t.Helper()
	b, _ := json.Marshal(map[string]any{"v": 1, "type": typ, "id": "t1", "data": data})
	w.sendRaw(string(b))
}

// next reads messages until one of the wanted type arrives.
func (w *wsc) next(typ string) Envelope {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for {
		_, b, err := w.c.Read(ctx)
		if err != nil {
			w.t.Fatalf("waiting for %s: %v", typ, err)
		}
		var env Envelope
		if err := json.Unmarshal(b, &env); err != nil {
			w.t.Fatalf("server sent invalid JSON: %v", err)
		}
		if env.Type == typ {
			return env
		}
		if env.Type == "error" && typ != "error" {
			w.t.Fatalf("unexpected error while waiting for %s: %s", typ, env.Data)
		}
	}
}

func (w *wsc) expectError(code string) ProtoError {
	w.t.Helper()
	env := w.next("error")
	var pe ProtoError
	_ = json.Unmarshal(env.Data, &pe)
	if pe.Code != code {
		w.t.Fatalf("want error %s, got %+v", code, pe)
	}
	if pe.Message == "" {
		w.t.Fatal("error without message")
	}
	return pe
}

var goodStrats = []map[string]any{
	{"name": "A", "start": "M", "stops": []map[string]any{{"lap": 25, "compound": "H"}}},
	{"name": "B", "start": "S", "stops": []map[string]any{{"lap": 14, "compound": "M"}, {"lap": 33, "compound": "H"}}},
}

func TestHelloScenarioSim(t *testing.T) {
	_, hs := testServer(t, nil)
	w := dial(t, hs)
	w.send("hello", map[string]any{})
	var wel struct {
		Session string `json:"session"`
	}
	_ = json.Unmarshal(w.next("welcome").Data, &wel)
	if len(wel.Session) != 32 {
		t.Fatalf("session id %q", wel.Session)
	}
	w.send("scenario.get", map[string]any{"seed": "NIGHT-42"})
	var sv ScenarioView
	if err := json.Unmarshal(w.next("scenario").Data, &sv); err != nil {
		t.Fatal(err)
	}
	if sv.Cars != DefaultCars || sv.Laps < 30 || len(sv.Tyres) != 3 || len(sv.Track.Points) == 0 {
		t.Fatalf("bad scenario %+v", sv.Laps)
	}
	w.send("sim.start", map[string]any{"seed": "NIGHT-42", "strategies": goodStrats, "sims": 600, "pace": "fast"})
	w.next("sim.started")
	var done SimProgressView
	if err := json.Unmarshal(w.next("sim.done").Data, &done); err != nil {
		t.Fatal(err)
	}
	if done.Done != 600 || done.Truncated || len(done.Stats) != 2 || done.Reason != "" {
		t.Fatalf("bad done %+v", done)
	}
}

func TestSimSameResultForEveryone(t *testing.T) {
	_, hs := testServer(t, func(c *Config) { c.Workers = 1 })
	_, hs2 := testServer(t, func(c *Config) { c.Workers = 8 })
	get := func(h *httptest.Server) string {
		w := dial(t, h)
		w.send("sim.start", map[string]any{"seed": "SHARE-1", "strategies": goodStrats, "sims": 500, "pace": "fast"})
		var v SimProgressView
		_ = json.Unmarshal(w.next("sim.done").Data, &v)
		b, _ := json.Marshal(v.Progress)
		return string(b)
	}
	if get(hs) != get(hs2) {
		t.Fatal("the same seed must give the same result on every server configuration")
	}
}

func TestInvalidInputsNeverKillTheConnection(t *testing.T) {
	_, hs := testServer(t, nil)
	w := dial(t, hs)
	cases := []struct {
		raw  string
		code string
	}{
		{`not json`, CodeBadJSON},
		{`{}`, CodeBadVersion},
		{`{"v":2,"type":"hello"}`, CodeBadVersion},
		{`{"v":1}`, CodeBadJSON},
		{`{"v":1,"type":"hello","extra":1}`, CodeBadJSON},
		{`{"v":1,"type":"hello"} {"v":1}`, CodeBadJSON},
		{`{"v":1,"type":"nope"}`, CodeUnknownType},
		{`{"v":1,"type":"hello","id":"` + strings.Repeat("x", 40) + `"}`, CodeBadJSON},
		{`{"v":1,"type":"scenario.get","data":{"seed":""}}`, CodeInvalid},
		{`{"v":1,"type":"scenario.get","data":{"seed":"../../etc"}}`, CodeInvalid},
		{`{"v":1,"type":"scenario.get","data":{"seed":"` + strings.Repeat("9", 40) + `"}}`, CodeInvalid},
		{`{"v":1,"type":"scenario.get","data":{"seed":"x","cars":0}}`, CodeInvalid},
		{`{"v":1,"type":"scenario.get","data":{"seed":"x","cars":21}}`, CodeInvalid},
		{`{"v":1,"type":"scenario.get","data":{"seed":"x","cars":1e400}}`, CodeInvalid},
		{`{"v":1,"type":"scenario.get","data":{"seed":"x","cars":"10"}}`, CodeInvalid},
		{`{"v":1,"type":"scenario.get","data":[1,2]}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[],"sims":100}}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"M","stops":[{"lap":10,"compound":"H"}]}],"sims":0}}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"M","stops":[{"lap":10,"compound":"H"}]}],"sims":-5}}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"M","stops":[{"lap":10,"compound":"H"}]}],"sims":99999999999}}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"M","stops":[{"lap":10,"compound":"H"}]}],"sims":12.5}}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"M","stops":[{"lap":10,"compound":"H"}]}],"sims":100,"pace":"warp"}}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"X","stops":[]}],"sims":100}}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"M","stops":[]}],"sims":100}}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"M","stops":[{"lap":0,"compound":"H"}]}],"sims":100}}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"M","stops":[{"lap":500,"compound":"H"}]}],"sims":100}}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"M","stops":[{"lap":-3,"compound":"H"}]}],"sims":100}}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"M","stops":[{"lap":10.5,"compound":"H"}]}],"sims":100}}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"M","stops":[{"lap":10,"compound":"Z"}]}],"sims":100}}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"M","stops":[{"lap":20,"compound":"H"},{"lap":10,"compound":"S"}]}],"sims":100}}`, CodeInvalid},
		{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"M","stops":[{"lap":10,"compound":"H"},{"lap":10,"compound":"S"}]}],"sims":100}}`, CodeInvalid},
		{`{"v":1,"type":"race.control","data":{"action":"pause"}}`, CodeNoRace},
		{`{"v":1,"type":"race.start","data":{"seed":"x","strategy":{"start":"M","stops":[{"lap":10,"compound":"H"}]},"speed":0}}`, CodeInvalid},
		{`{"v":1,"type":"race.start","data":{"seed":"x","strategy":{"start":"M","stops":[{"lap":10,"compound":"H"}]},"speed":1e308}}`, CodeInvalid},
		{`{"v":1,"type":"race.start","data":{"seed":"x","strategy":{"start":"M","stops":[{"lap":10,"compound":"H"}]},"speed":-1}}`, CodeInvalid},
	}
	// 500 stops
	var stops []string
	for i := 1; i <= 500; i++ {
		stops = append(stops, fmt.Sprintf(`{"lap":%d,"compound":"H"}`, i))
	}
	cases = append(cases, struct{ raw, code string }{`{"v":1,"type":"sim.start","data":{"seed":"x","strategies":[{"start":"M","stops":[` + strings.Join(stops, ",") + `]}],"sims":100}}`, CodeInvalid})
	for _, c := range cases {
		w.sendRaw(c.raw)
		t.Log(c.raw[:min(len(c.raw), 90)])
		pe := w.expectError(c.code)
		if strings.Contains(pe.Message, "panic") {
			t.Fatalf("%s: %s", c.raw, pe.Message)
		}
	}
	// still alive
	w.send("ping", nil)
	w.next("pong")
}

func TestGiantMessageClosesConnection(t *testing.T) {
	_, hs := testServer(t, nil)
	w := dial(t, hs)
	big := `{"v":1,"type":"hello","data":{"client":"` + strings.Repeat("a", MaxMessageBytes+10) + `"}}`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = w.c.Write(ctx, websocket.MessageText, []byte(big))
	_, _, err := w.c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
		t.Fatalf("want StatusMessageTooBig, got %v", err)
	}
	// the server is still healthy
	w2 := dial(t, hs)
	w2.send("ping", nil)
	w2.next("pong")
}

func TestRateLimit(t *testing.T) {
	_, hs := testServer(t, func(c *Config) { c.RateBurst = 5; c.RatePerSec = 1 })
	w := dial(t, hs)
	for i := 0; i < 30; i++ {
		w.send("ping", nil)
	}
	w.expectError(CodeRateLimited)
}

func TestOneSimPerSessionAndCancel(t *testing.T) {
	_, hs := testServer(t, func(c *Config) { c.Workers = 1 })
	w := dial(t, hs)
	w.send("sim.start", map[string]any{"seed": "A", "strategies": goodStrats, "sims": 50000, "pace": "fast"})
	w.next("sim.started")
	// a second request replaces the first
	w.send("sim.start", map[string]any{"seed": "A", "strategies": goodStrats[:1], "sims": 200, "pace": "fast"})
	var first SimProgressView
	_ = json.Unmarshal(w.next("sim.done").Data, &first)
	if !first.Truncated || first.Reason == "" {
		t.Fatalf("first run must be cancelled, got %+v", first.Done)
	}
	var second SimProgressView
	_ = json.Unmarshal(w.next("sim.done").Data, &second)
	if second.Truncated || second.Done != 200 {
		t.Fatalf("second run must complete: %+v", second.Done)
	}
	w.send("sim.start", map[string]any{"seed": "A", "strategies": goodStrats, "sims": 50000, "pace": "fast"})
	w.next("sim.started")
	w.send("sim.cancel", nil)
	w.next("sim.cancelled")
}

func TestBusyWhenJobsExhausted(t *testing.T) {
	_, hs := testServer(t, func(c *Config) { c.MaxJobs = 1; c.Workers = 1 })
	a, b := dial(t, hs), dial(t, hs)
	a.send("sim.start", map[string]any{"seed": "A", "strategies": goodStrats, "sims": 50000, "pace": "fast"})
	a.next("sim.started")
	b.send("sim.start", map[string]any{"seed": "A", "strategies": goodStrats, "sims": 100, "pace": "fast"})
	b.expectError(CodeBusy)
}

func TestClientLeavingCancelsJob(t *testing.T) {
	s, hs := testServer(t, func(c *Config) { c.MaxJobs = 1; c.Workers = 1 })
	a := dial(t, hs)
	a.send("sim.start", map[string]any{"seed": "A", "strategies": goodStrats, "sims": 50000, "pace": "fast"})
	a.next("sim.started")
	_ = a.c.CloseNow()
	deadline := time.Now().Add(5 * time.Second)
	for len(s.jobs) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("job slot not released after the client left")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLiveRaceAndResume(t *testing.T) {
	_, hs := testServer(t, nil)
	w := dial(t, hs)
	w.send("hello", nil)
	var wel struct{ Session string }
	_ = json.Unmarshal(w.next("welcome").Data, &wel)
	w.send("race.start", map[string]any{"seed": "LIVE", "strategy": goodStrats[0], "speed": 240})
	w.next("race.started")
	var lv LapView
	_ = json.Unmarshal(w.next("race.lap").Data, &lv)
	if lv.Lap != 1 || len(lv.Cars) != DefaultCars {
		t.Fatalf("bad first lap %+v", lv.Lap)
	}
	_ = json.Unmarshal(w.next("race.lap").Data, &lv)
	_ = json.Unmarshal(w.next("race.lap").Data, &lv)
	if lv.Lap != 3 {
		t.Fatalf("laps out of order: %d", lv.Lap)
	}
	// reconnect with the same session: the race comes back
	_ = w.c.CloseNow()
	w2 := dial(t, hs)
	w2.send("hello", map[string]any{"session": wel.Session})
	var wel2 struct {
		Session string
		Resumed bool
	}
	_ = json.Unmarshal(w2.next("welcome").Data, &wel2)
	if !wel2.Resumed || wel2.Session != wel.Session {
		t.Fatalf("session not resumed: %+v", wel2)
	}
	var sync RaceSyncView
	_ = json.Unmarshal(w2.next("race.sync").Data, &sync)
	if len(sync.Laps) < 3 || sync.Seed != "LIVE" {
		t.Fatalf("bad sync: %d laps", len(sync.Laps))
	}
	for i, l := range sync.Laps {
		if l.Lap != i+1 {
			t.Fatal("sync laps not contiguous")
		}
	}
	w2.send("race.control", map[string]any{"action": "speed", "speed": 120})
	w2.next("race.state")
	w2.send("race.control", map[string]any{"action": "fly"})
	w2.expectError(CodeInvalid)
	w2.send("race.control", map[string]any{"action": "stop"})
}

func TestUnknownSessionGetsNewOne(t *testing.T) {
	_, hs := testServer(t, nil)
	w := dial(t, hs)
	w.send("hello", map[string]any{"session": "deadbeef"})
	var wel struct {
		Session string
		Resumed bool
	}
	_ = json.Unmarshal(w.next("welcome").Data, &wel)
	if wel.Resumed || wel.Session == "deadbeef" {
		t.Fatal("unknown session must not be resumed")
	}
}

func TestHealthAndStatic(t *testing.T) {
	_, hs := testServer(t, nil)
	resp, err := hs.Client().Get(hs.URL + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz: %v", err)
	}
	_ = resp.Body.Close()
}
