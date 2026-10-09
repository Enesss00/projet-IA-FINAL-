// Package api exposes the engine over WebSocket.
//
// Every inbound byte is untrusted: messages are size-limited, decoded
// strictly (unknown fields, trailing data and wrong types are rejected),
// validated against explicit bounds, and answered with a clear error. No
// input can make the server panic; every goroutine recovers.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"unicode"
	"unicode/utf8"

	"pitwall/internal/model"
	"pitwall/internal/race"
)

// ProtocolVersion is the version of the JSON protocol (docs/PROTOCOL.md).
const ProtocolVersion = 1

// Limits of the protocol.
const (
	MaxMessageBytes = 64 << 10
	MaxIDLen        = 32
	MaxSeedLen      = 32
	MaxSessionLen   = 64
	MinSpeed        = 1.0
	MaxSpeed        = 240.0
	DefaultCars     = 10
)

// Envelope wraps every message, in both directions.
type Envelope struct {
	V    int             `json:"v"`
	Type string          `json:"type"`
	ID   string          `json:"id,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Error codes.
const (
	CodeBadJSON      = "bad_json"
	CodeBadVersion   = "bad_version"
	CodeUnknownType  = "unknown_type"
	CodeInvalid      = "invalid"
	CodeRateLimited  = "rate_limited"
	CodeBusy         = "busy"
	CodeNotFound     = "not_found"
	CodeInternal     = "internal"
	CodeNoRace       = "no_race"
	CodeSimCancelled = "cancelled"
)

// ProtoError is a client-facing error.
type ProtoError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

func (e *ProtoError) Error() string { return e.Code + ": " + e.Message }

func perr(code, field, format string, a ...any) *ProtoError {
	return &ProtoError{Code: code, Field: field, Message: fmt.Sprintf(format, a...)}
}

// DecodeEnvelope parses and validates the outer envelope.
func DecodeEnvelope(b []byte) (Envelope, *ProtoError) {
	var env Envelope
	if len(b) > MaxMessageBytes {
		return env, perr(CodeBadJSON, "", "message trop gros (%d octets, %d max)", len(b), MaxMessageBytes)
	}
	if !utf8.Valid(b) {
		return env, perr(CodeBadJSON, "", "message non UTF-8")
	}
	if err := strictUnmarshal(b, &env); err != nil {
		return Envelope{}, perr(CodeBadJSON, "", "JSON invalide : %s", cleanErr(err))
	}
	if len(env.ID) > MaxIDLen || !printableASCII(env.ID) {
		env.ID = "" // never echo an invalid id back
		return env, perr(CodeBadJSON, "id", "id invalide (%d caractères ASCII imprimables max)", MaxIDLen)
	}
	if env.V != ProtocolVersion {
		return env, perr(CodeBadVersion, "v", "version de protocole %d non supportée (attendu %d)", env.V, ProtocolVersion)
	}
	if env.Type == "" || len(env.Type) > 32 {
		return env, perr(CodeBadJSON, "type", "type de message manquant ou trop long")
	}
	return env, nil
}

// strictUnmarshal decodes exactly one JSON value, rejecting unknown fields
// and trailing data.
//
// Beyond encoding/json it also refuses: nesting deeper than MaxJSONDepth,
// duplicated keys, and keys that only match a field case-insensitively.
// Every error it returns is in French.
func strictUnmarshal(b []byte, v any) error {
	if err := checkShape(b); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return translate(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return strictErr("données après la valeur JSON")
	}
	return checkKeys(b, reflect.TypeOf(v))
}

// decodeData decodes the payload of a message into T. A missing payload
// decodes as the zero value (so that `{}` and absent are equivalent).
func decodeData[T any](raw json.RawMessage) (T, *ProtoError) {
	var v T
	if len(raw) == 0 || string(raw) == "null" {
		return v, nil
	}
	if err := strictUnmarshal(raw, &v); err != nil {
		return v, perr(CodeInvalid, "data", "contenu invalide : %s", cleanErr(err))
	}
	return v, nil
}

func cleanErr(err error) string {
	s := translate(err).Error()
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// ---- inbound payloads ----

// EmptyMsg is the payload of ping and sim.cancel: an empty object.
type EmptyMsg struct{}

// HelloMsg opens or resumes a session.
type HelloMsg struct {
	Session string `json:"session"`
	Client  string `json:"client"`
}

// ScenarioMsg asks for the scenario of a seed.
type ScenarioMsg struct {
	Seed string `json:"seed"`
	Cars *Num   `json:"cars"`
}

// StopDTO is the wire form of a pit stop.
type StopDTO struct {
	Lap      Num    `json:"lap"`
	Compound string `json:"compound"`
}

// StrategyDTO is the wire form of a strategy.
type StrategyDTO struct {
	Name  string    `json:"name"`
	Start string    `json:"start"`
	Stops []StopDTO `json:"stops"`
}

// SimStartMsg starts a Monte Carlo comparison.
type SimStartMsg struct {
	Seed       string        `json:"seed"`
	Cars       *Num          `json:"cars"`
	Strategies []StrategyDTO `json:"strategies"`
	Sims       Num           `json:"sims"`
	Pace       string        `json:"pace"` // "live" (default) or "fast"
}

// RaceStartMsg starts a live race.
type RaceStartMsg struct {
	Seed     string      `json:"seed"`
	Cars     *Num        `json:"cars"`
	Strategy StrategyDTO `json:"strategy"`
	Speed    *Num        `json:"speed"`
}

// RaceControlMsg controls the live race.
type RaceControlMsg struct {
	Action string `json:"action"` // pause | resume | speed | stop
	Speed  *Num   `json:"speed"`
}

// ---- validation helpers ----

// ParseSeed validates a seed code: 1-32 characters among [A-Za-z0-9_-].
func ParseSeed(s string) (string, *ProtoError) {
	if s == "" {
		return "", perr(CodeInvalid, "seed", "seed manquante")
	}
	if len(s) > MaxSeedLen {
		return "", perr(CodeInvalid, "seed", "seed trop longue (%d caractères max)", MaxSeedLen)
	}
	for _, r := range s {
		if !(r == '-' || r == '_' || (r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)))) {
			return "", perr(CodeInvalid, "seed", "seed invalide : lettres, chiffres, '-' et '_' uniquement")
		}
	}
	return s, nil
}

// parseInt parses a JSON number that must be an integer in [lo, hi].
func parseInt(n Num, field string, lo, hi int) (int, *ProtoError) {
	s := n.String()
	if s == "" {
		return 0, perr(CodeInvalid, field, "%s manquant", field)
	}
	v, ok := exactInt(s)
	if !ok {
		return 0, perr(CodeInvalid, field, "%s doit être un entier dans [%d, %d] (reçu %s)", field, lo, hi, trunc(s, 24))
	}
	if v < int64(lo) || v > int64(hi) {
		return 0, perr(CodeInvalid, field, "%s = %d hors bornes [%d, %d]", field, v, lo, hi)
	}
	return int(v), nil
}

// parseFloat parses a finite JSON number in [lo, hi].
func parseFloat(n Num, field string, lo, hi float64) (float64, *ProtoError) {
	f, err := strconv.ParseFloat(n.String(), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, perr(CodeInvalid, field, "%s doit être un nombre fini", field)
	}
	if f < lo || f > hi {
		return 0, perr(CodeInvalid, field, "%s = %g hors bornes [%g, %g]", field, f, lo, hi)
	}
	return f, nil
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// parseCars returns the grid size (default DefaultCars).
func parseCars(n *Num) (int, *ProtoError) {
	if n == nil {
		return DefaultCars, nil
	}
	return parseInt(*n, "cars", race.MinCars, race.MaxCars)
}

// ToStrategy converts and fully validates a wire strategy for a race of the
// given number of laps.
func (d StrategyDTO) ToStrategy(laps int, field string) (race.Strategy, *ProtoError) {
	var s race.Strategy
	s.Name = d.Name
	c, ok := model.ParseCode(d.Start)
	if !ok {
		return s, perr(CodeInvalid, field+".start", "gomme de départ inconnue %q : S, M ou H attendu", trunc(d.Start, 16))
	}
	s.Start = c
	if len(d.Stops) > race.MaxStops {
		return s, perr(CodeInvalid, field+".stops", "%d arrêts demandés, %d au maximum", len(d.Stops), race.MaxStops)
	}
	for i, st := range d.Stops {
		f := fmt.Sprintf("%s.stops[%d]", field, i)
		lap, pe := parseInt(st.Lap, f+".lap", math.MinInt32, math.MaxInt32)
		if pe != nil {
			return s, pe
		}
		comp, ok := model.ParseCode(st.Compound)
		if !ok {
			return s, perr(CodeInvalid, f+".compound", "gomme inconnue %q : S, M ou H attendu", trunc(st.Compound, 16))
		}
		s.Stops = append(s.Stops, race.Stop{Lap: lap, Compound: comp})
	}
	if err := s.Validate(laps); err != nil {
		var ve *race.ValidationError
		if errors.As(err, &ve) {
			return s, perr(CodeInvalid, field+"."+ve.Field, "%s", ve.Msg)
		}
		return s, perr(CodeInvalid, field, "%s", err.Error())
	}
	return s, nil
}

func itoaNum(i int) Num { return Num(strconv.Itoa(i)) }

// Num is a JSON number kept verbatim (so that 1e999 or 12.5 can be reported
// precisely). Unlike json.Number it refuses quoted strings: "10" is not 10.
type Num string

// String returns the literal.
func (n Num) String() string { return string(n) }

// MarshalJSON writes the literal.
func (n Num) MarshalJSON() ([]byte, error) {
	if n == "" {
		return []byte("0"), nil
	}
	return []byte(n), nil
}

// UnmarshalJSON accepts only a bare JSON number.
func (n *Num) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || !(b[0] == '-' || (b[0] >= '0' && b[0] <= '9')) {
		return errors.New("nombre attendu")
	}
	if !json.Valid(b) {
		return errors.New("nombre invalide")
	}
	*n = Num(b)
	return nil
}
