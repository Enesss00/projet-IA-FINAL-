package api

// Red-team attack tests on the WebSocket protocol (docs/PROTOCOL.md).
// Every test asserts the CORRECT behaviour; a failing test is a finding.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"pitwall/internal/race"
	"pitwall/internal/rng"
)

// ---- helpers ----

// rtRecv reads one frame with a timeout. ok=false on timeout/close.
func rtRecv(w *wsc, d time.Duration) (Envelope, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_, b, err := w.c.Read(ctx)
	if err != nil {
		return Envelope{}, false
	}
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		w.t.Fatalf("server sent invalid JSON: %v", err)
	}
	return env, true
}

// rtFirstNonWelcome returns the first message that is not a welcome
// (implicit hello).
func rtFirstNonWelcome(w *wsc) Envelope {
	w.t.Helper()
	for {
		env, ok := rtRecv(w, 10*time.Second)
		if !ok {
			w.t.Fatal("no answer from the server")
		}
		if env.Type != "welcome" {
			return env
		}
	}
}

// rtExpectRejected sends raw and asserts an error with one of the codes;
// the connection must stay usable.
func rtExpectRejected(t *testing.T, hs *httptest.Server, raw string, codes ...string) ProtoError {
	t.Helper()
	w := dial(t, hs)
	w.sendRaw(raw)
	env := rtFirstNonWelcome(w)
	if env.Type != "error" {
		t.Fatalf("input must be rejected, server answered %q: %.200s", env.Type, env.Data)
	}
	var pe ProtoError
	_ = json.Unmarshal(env.Data, &pe)
	okc := false
	for _, c := range codes {
		okc = okc || pe.Code == c
	}
	if !okc {
		t.Fatalf("want one of %v, got %+v", codes, pe)
	}
	if pe.Message == "" {
		t.Fatal("error without message")
	}
	w.send("ping", nil)
	w.next("pong")
	return pe
}

// englishLeaks lists fragments of Go / English error messages that must
// never reach the (French) user.
var englishLeaks = []string{
	"unknown field", "cannot unmarshal", "invalid character", "unexpected end",
	"unknown tyre compound", "expected S, M or H", "exceeded max depth", "Go value",
	"Go struct field", "looking for beginning", "after top-level value", "of type",
}

func rtLeak(msg string) string {
	for _, l := range englishLeaks {
		if strings.Contains(msg, l) {
			return l
		}
	}
	return ""
}

const rtStrat = `{"start":"M","stops":[{"lap":10,"compound":"H"}]}`

func rtSim(data string) string { return `{"v":1,"type":"sim.start","id":"r","data":` + data + `}` }

// ---- strictness ----

// encoding/json matches keys case-insensitively (and with Unicode simple
// folding: "ſ" folds to "s"); DisallowUnknownFields does not catch that.
// The protocol promises strict decoding.
func TestRedteamStrictKeysCaseFolding(t *testing.T) {
	_, hs := testServer(t, nil)
	cases := map[string]string{
		"upper envelope keys": `{"V":1,"TYPE":"ping"}`,
		"mixed case type":     `{"v":1,"Type":"scenario.get","data":{"seed":"X"}}`,
		"upper data key":      `{"v":1,"type":"scenario.get","data":{"SEED":"X"}}`,
		"long s folding":      `{"v":1,"type":"scenario.get","data":{"ſeed":"X"}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) { rtExpectRejected(t, hs, raw, CodeBadJSON, CodeInvalid, CodeNoRace) })
	}
}

// Duplicated keys are ambiguous (a proxy reading the first one and the
// server reading the last one disagree). Strict decoding must refuse them.
func TestRedteamDuplicateKeysRejected(t *testing.T) {
	_, hs := testServer(t, nil)
	cases := map[string]string{
		"dup type":     `{"v":1,"type":"race.start","type":"ping"}`,
		"dup v":        `{"v":2,"v":1,"type":"ping"}`,
		"dup seed":     `{"v":1,"type":"scenario.get","data":{"seed":"../../etc","seed":"OK"}}`,
		"dup sims":     rtSim(`{"seed":"X","strategies":[` + rtStrat + `],"sims":99999999,"sims":1}`),
		"dup compound": rtSim(`{"seed":"X","strategies":[{"start":"M","stops":[{"lap":10,"compound":"Z","compound":"H"}]}],"sims":1}`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) { rtExpectRejected(t, hs, raw, CodeBadJSON, CodeInvalid) })
	}
}

// "ping" and "sim.cancel" never decode their payload: unknown fields and
// wrong types are silently accepted, contrary to invariant 2.
func TestRedteamPayloadOfPingAndCancelIsValidated(t *testing.T) {
	_, hs := testServer(t, nil)
	cases := map[string]string{
		"ping unknown field":   `{"v":1,"type":"ping","data":{"evil":1}}`,
		"ping number data":     `{"v":1,"type":"ping","data":5}`,
		"ping array data":      `{"v":1,"type":"ping","data":[1,2,3]}`,
		"cancel unknown field": `{"v":1,"type":"sim.cancel","data":{"run":"all"}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) { rtExpectRejected(t, hs, raw, CodeInvalid, CodeBadJSON) })
	}
}

// Error messages must be French (invariant 3); the raw Go / English
// messages of encoding/json and model.ParseCompound leak through.
func TestRedteamErrorMessagesAreFrench(t *testing.T) {
	_, hs := testServer(t, nil)
	cases := map[string]string{
		"syntax":              `{"v":1,"type":`,
		"bad char":            `{"v":1,"type":"ping"}x`,
		"envelope unknown":    `{"v":1,"type":"ping","extra":true}`,
		"envelope wrong type": `{"v":"1","type":"ping"}`,
		"data unknown field":  `{"v":1,"type":"scenario.get","data":{"seed":"X","color":"red"}}`,
		"data wrong type":     `{"v":1,"type":"scenario.get","data":{"seed":42}}`,
		"strategies type":     rtSim(`{"seed":"X","strategies":"M-10-H","sims":1}`),
		"unknown compound":    rtSim(`{"seed":"X","strategies":[{"start":"Q","stops":[]}],"sims":1}`),
		"unknown stop comp":   rtSim(`{"seed":"X","strategies":[{"start":"M","stops":[{"lap":3,"compound":"ultra"}]}],"sims":1}`),
		"deep nesting":        `{"v":1,"type":"ping","x":` + strings.Repeat("[", 10001) + strings.Repeat("]", 10001) + `}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			pe := rtExpectRejected(t, hs, raw, CodeBadJSON, CodeInvalid, CodeBadVersion)
			if l := rtLeak(pe.Message); l != "" {
				t.Fatalf("message not in French (leaks %q): %s", l, pe.Message)
			}
		})
	}
}

// When the id itself is invalid, the error must not echo it back verbatim
// (it is up to 64 KiB of arbitrary text / control characters).
func TestRedteamErrorDoesNotEchoInvalidID(t *testing.T) {
	_, hs := testServer(t, nil)
	for name, id := range map[string]string{
		"long":    strings.Repeat("A", 5000),
		"control": "a\u0000b\u001b[31m",
		"unicode": "été",
	} {
		t.Run(name, func(t *testing.T) {
			w := dial(t, hs)
			b, _ := json.Marshal(map[string]any{"v": 1, "type": "ping", "id": id})
			w.sendRaw(string(b))
			env := w.next("error")
			if len(env.ID) > MaxIDLen || !printableASCII(env.ID) {
				t.Fatalf("server echoed an invalid id (%d bytes): %.40q", len(env.ID), env.ID)
			}
		})
	}
}

// ---- numbers ----

func TestRedteamNumberEdgeCases(t *testing.T) {
	_, hs := testServer(t, nil)
	scen := func(cars string) string {
		return `{"v":1,"type":"scenario.get","data":{"seed":"NUM","cars":` + cars + `}}`
	}
	rejected := map[string]string{
		"cars -0":                  scen("-0"),
		"cars 1e-400":              scen("1e-400"),
		"cars 1e999":               scen("1e999"),
		"cars -1e999":              scen("-1e999"),
		"cars huge exponent":       scen("1e2147483648"),
		"cars 5000 digits":         scen("1" + strings.Repeat("0", 5000)),
		"cars neg 5000 digits":     scen("-1" + strings.Repeat("0", 5000)),
		"cars not integer":         scen("10.5"),
		"cars 10.0000000000000001": scen("10.0000000000000001"), // not an integer literal; rounds to 10
		"cars 1e1 tiny frac":       scen("1.00000000000000001e1"),
		"cars true":                scen("true"),
		"cars object":              scen("{}"),
		"cars null string":         scen(`"null"`),
		"sims int64 overflow":      rtSim(`{"seed":"X","strategies":[` + rtStrat + `],"sims":9223372036854775808}`),
		"sims -int64":              rtSim(`{"seed":"X","strategies":[` + rtStrat + `],"sims":-9223372036854775809}`),
		"lap 10.0000000000000001":  rtSim(`{"seed":"X","strategies":[{"start":"M","stops":[{"lap":10.0000000000000001,"compound":"H"}]}],"sims":1}`),
		"lap 2^31":                 rtSim(`{"seed":"X","strategies":[{"start":"M","stops":[{"lap":2147483648,"compound":"H"}]}],"sims":1}`),
		"lap -2^31-1":              rtSim(`{"seed":"X","strategies":[{"start":"M","stops":[{"lap":-2147483649,"compound":"H"}]}],"sims":1}`),
		"speed 1e-400":             `{"v":1,"type":"race.start","data":{"seed":"X","strategy":` + rtStrat + `,"speed":1e-400}}`,
		"speed -0":                 `{"v":1,"type":"race.start","data":{"seed":"X","strategy":` + rtStrat + `,"speed":-0}}`,
		"speed 240.1":              `{"v":1,"type":"race.start","data":{"seed":"X","strategy":` + rtStrat + `,"speed":240.1}}`,
		"sims null":                rtSim(`{"seed":"X","strategies":[` + rtStrat + `],"sims":null}`),
		"stops null lap":           rtSim(`{"seed":"X","strategies":[{"start":"M","stops":[{"lap":null,"compound":"H"}]}],"sims":1}`),
	}
	for name, raw := range rejected {
		t.Run(name, func(t *testing.T) {
			pe := rtExpectRejected(t, hs, raw, CodeInvalid, CodeBadJSON)
			if len(pe.Message) > 400 {
				t.Fatalf("error message not bounded: %d bytes", len(pe.Message))
			}
		})
	}
	// boundaries that must be accepted
	for _, cars := range []string{"1", "20", "1.0e1", "2e1"} {
		t.Run("accept cars "+cars, func(t *testing.T) {
			w := dial(t, hs)
			w.sendRaw(scen(cars))
			var sv ScenarioView
			if err := json.Unmarshal(w.next("scenario").Data, &sv); err != nil {
				t.Fatal(err)
			}
			if len(sv.Drivers) != sv.Cars || len(sv.Grid) != sv.Cars {
				t.Fatalf("incoherent scenario: cars=%d drivers=%d grid=%d", sv.Cars, len(sv.Drivers), len(sv.Grid))
			}
		})
	}
}

// ---- seeds ----

func TestRedteamSeeds(t *testing.T) {
	_, hs := testServer(t, nil)
	accepted := []string{"0", "00", "18446744073709551615", "18446744073709551616", "9999999999999999999",
		strings.Repeat("9", 32), "-", "_", "--__--", strings.Repeat("z", 32)}
	for _, seed := range accepted {
		t.Run("accept "+seed, func(t *testing.T) {
			w := dial(t, hs)
			w.send("scenario.get", map[string]any{"seed": seed, "cars": 20})
			a := w.next("scenario").Data
			w.send("scenario.get", map[string]any{"seed": seed, "cars": 20})
			b := w.next("scenario").Data
			if string(a) != string(b) {
				t.Fatal("same seed, different scenario")
			}
			if strings.Contains(string(a), "null") {
				t.Fatal("null in scenario JSON")
			}
		})
	}
	rejected := []string{"é", "Ａ", "a b", "a\u0000", "٣", "a/b", "a.b", strings.Repeat("a", 33), "\u200b", "a\nb", "🚀"}
	for _, seed := range rejected {
		t.Run(fmt.Sprintf("reject %q", seed), func(t *testing.T) {
			b, _ := json.Marshal(map[string]any{"v": 1, "type": "scenario.get", "data": map[string]any{"seed": seed}})
			pe := rtExpectRejected(t, hs, string(b), CodeInvalid)
			if pe.Field != "seed" {
				t.Fatalf("field = %q", pe.Field)
			}
		})
	}
}

// ---- strategy names ----

func TestRedteamStrategyNames(t *testing.T) {
	_, hs := testServer(t, nil)
	mk := func(name string) string {
		b, _ := json.Marshal(map[string]any{"v": 1, "type": "sim.start", "data": map[string]any{
			"seed": "NAME", "sims": 1, "pace": "fast",
			"strategies": []map[string]any{{"name": name, "start": "M", "stops": []map[string]any{{"lap": 10, "compound": "H"}}}}}})
		return string(b)
	}
	for name, n := range map[string]string{
		"10KB":       strings.Repeat("x", 10_000),
		"25 runes":   strings.Repeat("é", 25),
		"control":    "A\u0007B",
		"nul":        "A\u0000",
		"bidi":       "A\u202eB",
		"zero width": "A\u200bB",
		"ansi":       "\u001b[2J",
	} {
		t.Run("reject "+name, func(t *testing.T) {
			pe := rtExpectRejected(t, hs, mk(n), CodeInvalid)
			if pe.Field != "strategies[0].name" {
				t.Fatalf("field = %q (%s)", pe.Field, pe.Message)
			}
		})
	}
	t.Run("accept 24 emoji", func(t *testing.T) {
		name := strings.Repeat("🏁", 24)
		w := dial(t, hs)
		w.sendRaw(mk(name))
		var done SimProgressView
		if err := json.Unmarshal(w.next("sim.done").Data, &done); err != nil {
			t.Fatal(err)
		}
		if len(done.Stats) != 1 || done.Stats[0].Name != name {
			t.Fatalf("name not preserved: %+v", done.Stats)
		}
	})
}

// ---- wrong types / nesting / binary / proto keys ----

func TestRedteamWrongTypesEverywhere(t *testing.T) {
	_, hs := testServer(t, nil)
	raws := []string{
		`{"v":1,"type":"hello","data":{"session":1}}`,
		`{"v":1,"type":"hello","data":{"client":{}}}`,
		`{"v":1,"type":"hello","data":"x"}`,
		`{"v":1,"type":"hello","data":{"session":"` + strings.Repeat("a", 65) + `"}}`,
		`{"v":1,"type":1}`,
		`{"v":1,"type":"ping","id":5}`,
		`{"v":1.5,"type":"ping"}`,
		`{"v":true,"type":"ping"}`,
		`{"v":null,"type":"ping"}`,
		`{"v":1,"type":null}`,
		`{"v":1,"type":""}`,
		`{"v":1,"type":"` + strings.Repeat("t", 33) + `"}`,
		`[{"v":1,"type":"ping"}]`,
		`"ping"`, `1`, `null`, `true`, ``, ` `, "\ufeff" + `{"v":1,"type":"ping"}`,
		rtSim(`{"seed":"X","strategies":{"0":` + rtStrat + `},"sims":1}`),
		rtSim(`{"seed":"X","strategies":[1],"sims":1}`),
		rtSim(`{"seed":"X","strategies":[null],"sims":1}`),
		rtSim(`{"seed":"X","strategies":null,"sims":1}`),
		rtSim(`{"seed":"X","strategies":[{"start":"M","stops":{"lap":3}}],"sims":1}`),
		rtSim(`{"seed":"X","strategies":[{"start":"M","stops":[null]}],"sims":1}`),
		rtSim(`{"seed":"X","strategies":[{"start":1,"stops":[]}],"sims":1}`),
		rtSim(`{"seed":"X","strategies":[{"start":"M","name":5,"stops":[]}],"sims":1}`),
		rtSim(`{"seed":"X","strategies":[` + rtStrat + `],"sims":1,"pace":1}`),
		rtSim(`{"seed":"X","strategies":[` + rtStrat + `],"sims":1,"pace":"FAST"}`),
		rtSim(`{"seed":["X"],"strategies":[` + rtStrat + `],"sims":1}`),
		rtSim(`{"seed":null,"strategies":[` + rtStrat + `],"sims":1}`),
		rtSim(`{"seed":"X","strategies":[` + strings.Repeat(rtStrat+",", 4) + rtStrat + `],"sims":1}`),
		`{"v":1,"type":"race.start","data":{"seed":"X","strategy":null}}`,
		`{"v":1,"type":"race.start","data":{"seed":"X","strategy":[` + rtStrat + `]}}`,
		`{"v":1,"type":"race.start","data":{"seed":"X","strategy":` + rtStrat + `,"speed":"30"}}`,
		`{"v":1,"type":"race.control","data":{"action":1}}`,
		`{"v":1,"type":"race.control","data":{"action":null}}`,
	}
	for i, raw := range raws {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			rtExpectRejected(t, hs, raw, CodeBadJSON, CodeInvalid, CodeBadVersion, CodeUnknownType, CodeNoRace)
		})
	}
}

func TestRedteamDeepNesting(t *testing.T) {
	_, hs := testServer(t, nil)
	deepArr := strings.Repeat("[", 30000) + strings.Repeat("]", 30000)
	deepObj := strings.Repeat(`{"a":`, 10000) + "1" + strings.Repeat("}", 10000)
	cases := map[string]string{
		"data arrays 10k":    `{"v":1,"type":"scenario.get","data":` + strings.Repeat("[", 10000) + strings.Repeat("]", 10000) + `}`,
		"data arrays 30k":    `{"v":1,"type":"scenario.get","data":` + deepArr + `}`,
		"data objects 10k":   `{"v":1,"type":"scenario.get","data":` + deepObj + `}`,
		"in seed 10k":        `{"v":1,"type":"scenario.get","data":{"seed":` + strings.Repeat("[", 10000) + strings.Repeat("]", 10000) + `}}`,
		"envelope field 30k": `{"v":1,"type":"ping","x":` + deepArr + `}`,
		"unbalanced 60k":     `{"v":1,"type":"ping","data":` + strings.Repeat("[", 60000) + `}`,
		"ping data deep":     `{"v":1,"type":"ping","data":` + deepArr + `}`,
	}
	for name, raw := range cases {
		if len(raw) > MaxMessageBytes {
			t.Fatalf("%s: test message too big", name)
		}
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			rtExpectRejected(t, hs, raw, CodeBadJSON, CodeInvalid)
			if d := time.Since(start); d > 3*time.Second {
				t.Fatalf("too slow: %v", d)
			}
		})
	}
}

func TestRedteamBinaryFrameAndProtoKeys(t *testing.T) {
	_, hs := testServer(t, nil)
	t.Run("binary", func(t *testing.T) {
		w := dial(t, hs)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for i := 0; i < 5; i++ {
			if err := w.c.Write(ctx, websocket.MessageBinary, []byte(`{"v":1,"type":"ping"}`)); err != nil {
				t.Fatal(err)
			}
			w.expectError(CodeBadJSON)
		}
		w.send("ping", nil)
		w.next("pong")
	})
	for name, raw := range map[string]string{
		"proto envelope":    `{"v":1,"type":"ping","__proto__":{"admin":true}}`,
		"constructor":       `{"v":1,"type":"ping","constructor":{"prototype":{}}}`,
		"proto data":        `{"v":1,"type":"scenario.get","data":{"seed":"X","__proto__":{"cars":99}}}`,
		"proto type":        `{"v":1,"type":"__proto__"}`,
		"hasOwnProperty":    `{"v":1,"type":"hasOwnProperty"}`,
		"proto strategy":    rtSim(`{"seed":"X","strategies":[{"start":"M","stops":[],"__proto__":{"stops":[{"lap":3,"compound":"H"}]}}],"sims":1}`),
		"type with control": `{"v":1,"type":"ping\u0000"}`,
	} {
		t.Run(name, func(t *testing.T) { rtExpectRejected(t, hs, raw, CodeBadJSON, CodeInvalid, CodeUnknownType) })
	}
}

// Unicode case folding in model.ParseCompound: strings.ToUpper("ſ") == "S".
// The protocol says start / compound are "S"|"M"|"H".
func TestRedteamCompoundStrictness(t *testing.T) {
	_, hs := testServer(t, nil)
	for name, comp := range map[string]string{
		"long s":    "ſ",
		"long soft": "ſoft",
		"nbsp M":    " M ",
		"tab S":     "\tS\n",
	} {
		t.Run(name, func(t *testing.T) {
			b, _ := json.Marshal(map[string]any{"v": 1, "type": "sim.start", "data": map[string]any{
				"seed": "X", "sims": 1, "pace": "fast",
				"strategies": []map[string]any{{"start": comp, "stops": []map[string]any{{"lap": 10, "compound": "H"}}}}}})
			rtExpectRejected(t, hs, string(b), CodeInvalid)
		})
	}
}

// ---- jobs and slots ----

// Many sim.start in a row on one session: each replaces the previous one,
// the session must never be told "busy" by its own previous job, and every
// slot must be released at the end.
func TestRedteamManySimStartsReleaseSlots(t *testing.T) {
	s, hs := testServer(t, func(c *Config) { c.MaxJobs = 1; c.Workers = 1 })
	w := dial(t, hs)
	const n = 15
	for i := 0; i < n; i++ {
		w.sendRaw(rtSim(`{"seed":"BURST","strategies":[` + rtStrat + `],"sims":50000,"pace":"fast"}`))
	}
	started, done := 0, 0
	deadline := time.Now().Add(30 * time.Second)
	for done < n && time.Now().Before(deadline) {
		env, ok := rtRecv(w, 20*time.Second)
		if !ok {
			break
		}
		switch env.Type {
		case "sim.started":
			started++
		case "sim.done":
			done++
		case "error":
			t.Fatalf("after %d starts: %s", started, env.Data)
		}
		if started == n && done == n-1 {
			w.send("sim.cancel", nil)
		}
	}
	if started != n || done != n {
		t.Fatalf("started %d, done %d, want %d", started, n, n)
	}
	waitSlots(t, s)
	// another session can still compute
	w2 := dial(t, hs)
	w2.sendRaw(rtSim(`{"seed":"BURST","strategies":[` + rtStrat + `],"sims":30,"pace":"fast"}`))
	w2.next("sim.done")
}

func waitSlots(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for len(s.jobs) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d job slot(s) never released", len(s.jobs))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A storm of clients that start a long live-paced job and vanish.
func TestRedteamDisconnectStormReleasesSlots(t *testing.T) {
	s, hs := testServer(t, func(c *Config) { c.MaxJobs = 4; c.Workers = 1; c.MinEmitInterval = 300 * time.Millisecond })
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http")+"/ws", nil) //nolint:bodyclose // coder/websocket
			if err != nil {
				return
			}
			msg := rtSim(`{"seed":"STORM","strategies":[` + rtStrat + `],"sims":50000}`)
			_ = c.Write(ctx, websocket.MessageText, []byte(msg))
			if i%2 == 0 {
				_, _, _ = c.Read(ctx) // wait for the first answer on half of them
			}
			_ = c.CloseNow()
		}(i)
	}
	wg.Wait()
	waitSlots(t, s)
	w := dial(t, hs)
	w.sendRaw(rtSim(`{"seed":"STORM","strategies":[` + rtStrat + `],"sims":30,"pace":"fast"}`))
	w.next("sim.done")
}

func TestRedteamConnectionLimitReleased(t *testing.T) {
	_, hs := testServer(t, func(c *Config) { c.MaxConns = 3 })
	var ws []*wsc
	for i := 0; i < 3; i++ {
		w := dial(t, hs)
		w.send("ping", nil)
		w.next("pong")
		ws = append(ws, w)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http")+"/ws", nil) //nolint:bodyclose // coder/websocket
	if err == nil {
		t.Fatal("4th connection must be refused")
	}
	if resp != nil && resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d", resp.StatusCode)
	}
	_ = ws[0].c.CloseNow()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http")+"/ws", nil) //nolint:bodyclose // coder/websocket
		if err == nil {
			_ = c.CloseNow()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connection slot not released")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Many concurrent clients doing real work (run with -race).
func TestRedteamConcurrentClients(t *testing.T) {
	_, hs := testServer(t, func(c *Config) { c.MaxJobs = 64; c.Workers = 2 })
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http")+"/ws", nil) //nolint:bodyclose // coder/websocket
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = c.CloseNow() }()
			c.SetReadLimit(16 << 20)
			cars := 1 + i%20
			msgs := []string{
				`{"v":1,"type":"hello","data":{}}`,
				fmt.Sprintf(`{"v":1,"type":"scenario.get","data":{"seed":"C%d","cars":%d}}`, i%5, cars),
				rtSim(fmt.Sprintf(`{"seed":"C%d","cars":%d,"strategies":[`+rtStrat+`],"sims":60,"pace":"fast"}`, i%5, cars)),
			}
			for _, m := range msgs {
				if err := c.Write(ctx, websocket.MessageText, []byte(m)); err != nil {
					errs <- err
					return
				}
			}
			for {
				_, b, err := c.Read(ctx)
				if err != nil {
					errs <- fmt.Errorf("client %d: %w", i, err)
					return
				}
				var env Envelope
				_ = json.Unmarshal(b, &env)
				if env.Type == "error" {
					errs <- fmt.Errorf("client %d: %s", i, env.Data)
					return
				}
				if env.Type == "sim.done" {
					var v SimProgressView
					_ = json.Unmarshal(env.Data, &v)
					sum := 0
					for _, h := range v.Stats[0].Hist {
						sum += h
					}
					if len(v.Stats[0].Hist) != cars+1 || sum != v.Done || v.Done != 60 {
						errs <- fmt.Errorf("client %d: hist %v done %d", i, v.Stats[0].Hist, v.Done)
					}
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// ---- live race ----

// race.control "stop" must be answered with race.state{status:"stopped"}
// (docs/PROTOCOL.md lists "stopped"; nothing ever sends it).
func TestRedteamRaceStopIsAcknowledged(t *testing.T) {
	_, hs := testServer(t, nil)
	w := dial(t, hs)
	w.send("race.start", map[string]any{"seed": "STOP", "strategy": goodStrats[0], "speed": 1})
	w.next("race.started")
	w.next("race.state") // initial running state
	w.send("race.control", map[string]any{"action": "stop"})
	for {
		env, ok := rtRecv(w, 2*time.Second)
		if !ok {
			t.Fatal("no race.state after stop")
		}
		if env.Type == "race.state" {
			var st RaceStateView
			_ = json.Unmarshal(env.Data, &st)
			if st.Status != "stopped" {
				t.Fatalf("status %q after stop, want \"stopped\"", st.Status)
			}
			return
		}
	}
}

// A second race.start replaces the running race: once race.started of the
// new race is received, no lap of the old race may arrive (messages carry no
// race id, the client cannot tell them apart).
func TestRedteamRaceStartReplacesCleanly(t *testing.T) {
	_, hs := testServer(t, nil)
	w := dial(t, hs)
	for it := 0; it < 25; it++ {
		w.sendRaw(`{"v":1,"type":"race.start","id":"old","data":{"seed":"OLD","cars":20,"strategy":` + rtStrat + `,"speed":240}}`)
		// let the old race produce laps
		for n := 0; n < 4; {
			env, ok := rtRecv(w, 5*time.Second)
			if !ok {
				t.Fatal("old race silent")
			}
			if env.Type == "race.lap" {
				n++
			}
		}
		w.sendRaw(`{"v":1,"type":"race.start","id":"new","data":{"seed":"NEW","cars":3,"strategy":` + rtStrat + `,"speed":1}}`)
		for {
			env, ok := rtRecv(w, 5*time.Second)
			if !ok {
				t.Fatal("no race.started")
			}
			if env.Type == "race.started" && env.ID == "new" {
				break
			}
		}
		// a ping round-trip: everything the server queued before the pong
		// must belong to the new race
		w.send("ping", nil)
		want := 1
		for {
			env, ok := rtRecv(w, 5*time.Second)
			if !ok {
				t.Fatal("no pong")
			}
			if env.Type == "pong" {
				break
			}
			if env.Type == "race.lap" {
				var lv LapView
				_ = json.Unmarshal(env.Data, &lv)
				if len(lv.Cars) != 3 || lv.Lap != want {
					t.Fatalf("iteration %d: lap %d with %d cars received after the new race started (want lap %d, 3 cars)", it, lv.Lap, len(lv.Cars), want)
				}
				want++
			}
		}
	}
}

// Hijack / takeover: a second connection presents the session id while the
// first one is alive. The old connection is closed, and its departure must
// not auto-pause the race (nor cancel the search) of the new owner.
func TestRedteamSessionTakeoverDoesNotPauseNewOwner(t *testing.T) {
	_, hs := testServer(t, nil)
	a := dial(t, hs)
	a.send("hello", nil)
	var wel struct{ Session string }
	_ = json.Unmarshal(a.next("welcome").Data, &wel)
	a.send("race.start", map[string]any{"seed": "TAKE", "strategy": goodStrats[0], "speed": 2})
	a.next("race.started")

	b := dial(t, hs)
	b.send("hello", map[string]any{"session": wel.Session})
	b.next("race.sync")
	// the old connection must be closed by the server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := a.c.Read(ctx); err != nil {
			break
		}
	}
	time.Sleep(200 * time.Millisecond)
	b.send("race.control", map[string]any{"action": "speed", "speed": 3})
	for {
		env, ok := rtRecv(b, 3*time.Second)
		if !ok {
			t.Fatal("no race.state")
		}
		if env.Type != "race.state" {
			continue
		}
		var st RaceStateView
		_ = json.Unmarshal(env.Data, &st)
		if st.Speed != 3 {
			continue
		}
		if st.Status != "running" || st.AutoPaused {
			t.Fatalf("new owner's race was paused by the old connection leaving: %+v", st)
		}
		return
	}
}

// Same, for a Monte Carlo search started by the new owner right after the
// takeover: the old connection leaving must not cancel it.
func TestRedteamSessionTakeoverDoesNotCancelNewSearch(t *testing.T) {
	_, hs := testServer(t, func(c *Config) { c.Workers = 1 })
	a := dial(t, hs)
	a.send("hello", nil)
	var wel struct{ Session string }
	_ = json.Unmarshal(a.next("welcome").Data, &wel)
	for i := 0; i < 10; i++ {
		b := dial(t, hs)
		// hello and sim.start back to back: the job starts while the old
		// connection is being torn down.
		hb, _ := json.Marshal(map[string]any{"v": 1, "type": "hello", "data": map[string]any{"session": wel.Session}})
		b.sendRaw(string(hb))
		b.sendRaw(rtSim(`{"seed":"TAKE","strategies":[` + rtStrat + `],"sims":3000,"pace":"fast"}`))
		var done SimProgressView
		_ = json.Unmarshal(b.next("sim.done").Data, &done)
		if done.Truncated {
			t.Fatalf("iteration %d: search of the new owner cancelled (%s)", i, done.Reason)
		}
		a = b
	}
	_ = a.c.CloseNow()
}

// race.control flood: every accepted control must be answered (race.state)
// or refused (error) — never silently dropped.
func TestRedteamRaceControlFloodAnswered(t *testing.T) {
	_, hs := testServer(t, nil)
	w := dial(t, hs)
	w.send("race.start", map[string]any{"seed": "FLOOD", "strategy": goodStrats[0], "speed": 1})
	w.next("race.started")
	w.next("race.state")
	const n = 150
	for i := 0; i < n; i++ {
		w.send("race.control", map[string]any{"action": "speed", "speed": 1 + i%100})
	}
	got := 0
	for got < n {
		env, ok := rtRecv(w, 3*time.Second)
		if !ok {
			break
		}
		if env.Type == "race.state" || env.Type == "error" {
			got++
		}
	}
	if got < n {
		t.Fatalf("%d race.control sent, only %d answered (the rest were silently dropped)", n, got)
	}
	w.send("ping", nil)
	w.next("pong")
}

// sim with 1 and 20 cars end to end.
func TestRedteamSimGridExtremes(t *testing.T) {
	_, hs := testServer(t, nil)
	for _, cars := range []int{1, 2, 3, 20} {
		w := dial(t, hs)
		w.send("sim.start", map[string]any{"seed": "GRID", "cars": cars, "strategies": goodStrats, "sims": 77, "pace": "fast"})
		var done SimProgressView
		if err := json.Unmarshal(w.next("sim.done").Data, &done); err != nil {
			t.Fatal(err)
		}
		if done.Done != 77 || len(done.Stats) != 2 {
			t.Fatalf("cars=%d: %+v", cars, done)
		}
		for _, st := range done.Stats {
			sum := 0
			for _, h := range st.Hist {
				sum += h
			}
			if len(st.Hist) != cars+1 || sum != 77 || st.BestPos < 1 || st.WorstPos > cars+1 {
				t.Fatalf("cars=%d: bad stats %+v", cars, st)
			}
		}
	}
}

// ---- views are always JSON-safe ----

func TestRedteamViewsJSONSafeEveryLap(t *testing.T) {
	seeds := []string{"0", "1", "18446744073709551615", "18446744073709551616", "9999999999999999999", "-", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"}
	for i := 0; i < 25; i++ {
		seeds = append(seeds, fmt.Sprintf("R%d", i*7919))
	}
	for _, seed := range seeds {
		for cars := 1; cars <= 20; cars += 3 {
			sc, err := race.NewScenario(rng.SeedFromString(seed), race.Options{Cars: cars})
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(newScenarioView(seed, sc))
			if err != nil || strings.Contains(string(b), "null") {
				t.Fatalf("seed %s cars %d: scenario JSON: %v", seed, cars, err)
			}
			st := sc.NewState(rng.New(sc.Seed).Derive(rng.LabelLive), sc.Rivals[sc.Player])
			for !st.Done() {
				var log race.Log
				sc.Step(&st, &log)
				lv := newLapView(sc, &st, log.Events)
				b, err := json.Marshal(lv)
				if err != nil || strings.Contains(string(b), "null") {
					t.Fatalf("seed %s cars %d lap %d: lap JSON: %v", seed, cars, st.Lap, err)
				}
				if len(lv.Cars) != cars {
					t.Fatalf("lap view has %d cars", len(lv.Cars))
				}
				for p, c := range lv.Cars {
					if c.Pos != p+1 || c.Time < 0 || c.LapTime < 0 || math.IsNaN(c.Wear) || c.Wear < 0 {
						t.Fatalf("seed %s lap %d: bad car line %+v", seed, st.Lap, c)
					}
					if !c.Out && p > 0 && c.Gap < lv.Cars[p-1].Gap && !lv.Cars[p-1].Out {
						t.Fatalf("gap not monotone")
					}
				}
			}
			sv := (&liveRace{sc: sc, st: st}).stateLocked()
			if _, err := json.Marshal(sv); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// FuzzRedteamParseIntExact: parseInt may only accept literals whose exact
// decimal value is an integer in bounds ("10.0000000000000001" is not 10).
func FuzzRedteamParseIntExact(f *testing.F) {
	for _, s := range []string{"10", "-0", "1e1", "10.5", "10.0000000000000001", "1e-400", "1e999", "50000", "4.99999999999999999e4"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !json.Valid([]byte(s)) {
			return
		}
		var n Num
		if err := n.UnmarshalJSON([]byte(s)); err != nil {
			return
		}
		v, pe := parseInt(n, "x", 1, 50000)
		if pe != nil {
			if pe.Message == "" || pe.Code != CodeInvalid {
				t.Fatalf("bad error %+v", pe)
			}
			return
		}
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			t.Fatalf("accepted %q which is not a number", s)
		}
		if !r.IsInt() || r.Num().Int64() != int64(v) {
			t.Fatalf("accepted %q as %d but its exact value is %s", s, v, r.RatString())
		}
	})
}
