package api

import (
	"encoding/json"
	"testing"
)

// FuzzDecode drives arbitrary bytes through the whole decoding and
// validation path of every message type. It must never panic, and any
// accepted strategy must be valid.
func FuzzDecode(f *testing.F) {
	seeds := []string{
		`{"v":1,"type":"hello","data":{"session":"x"}}`,
		`{"v":1,"type":"sim.start","id":"a","data":{"seed":"S","strategies":[{"name":"A","start":"M","stops":[{"lap":20,"compound":"H"}]}],"sims":100}}`,
		`{"v":1,"type":"race.start","data":{"seed":"S","strategy":{"start":"S","stops":[{"lap":1e3,"compound":"H"}]},"speed":30}}`,
		`{"v":1,"type":"race.control","data":{"action":"speed","speed":-0}}`,
		`{"v":1,"type":"scenario.get","data":{"seed":"9999999999999999999999","cars":20}}`,
		`{"v":1,"type":"x","data":null}`, `[]`, `{"v":1e999}`, "\xff\xfe",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		env, pe := DecodeEnvelope(b)
		if pe != nil {
			if pe.Message == "" {
				t.Fatal("error without message")
			}
			return
		}
		switch env.Type {
		case "sim.start":
			m, pe := decodeData[SimStartMsg](env.Data)
			if pe != nil {
				return
			}
			_, _ = ParseSeed(m.Seed)
			_, _ = parseCars(m.Cars)
			_, _ = parseInt(m.Sims, "sims", 1, 50000)
			for _, s := range m.Strategies {
				st, pe := s.ToStrategy(50, "s")
				if pe == nil {
					if err := st.Validate(50); err != nil {
						t.Fatalf("accepted invalid strategy: %v", err)
					}
				}
			}
		case "race.start":
			m, pe := decodeData[RaceStartMsg](env.Data)
			if pe != nil {
				return
			}
			if m.Speed != nil {
				v, pe := parseFloat(*m.Speed, "speed", MinSpeed, MaxSpeed)
				if pe == nil && (v < MinSpeed || v > MaxSpeed) {
					t.Fatal("speed out of bounds accepted")
				}
			}
			_, _ = m.Strategy.ToStrategy(40, "strategy")
		case "race.control":
			_, _ = decodeData[RaceControlMsg](env.Data)
		case "scenario.get":
			m, pe := decodeData[ScenarioMsg](env.Data)
			if pe == nil {
				_, _ = parseCars(m.Cars)
				_, _ = ParseSeed(m.Seed)
			}
		case "hello":
			_, _ = decodeData[HelloMsg](env.Data)
		}
		// error payloads must always encode
		if _, err := json.Marshal(perr(CodeInvalid, "f", "%s", string(b))); err != nil {
			t.Fatal(err)
		}
	})
}
