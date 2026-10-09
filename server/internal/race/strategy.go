package race

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"pitwall/internal/model"
)

// MaxStops is the maximal number of pit stops in a strategy.
const MaxStops = 5

// Stop is a planned pit stop: the car enters the pit lane at the end of lap
// Lap (1-based) and leaves on Compound.
type Stop struct {
	Lap      int            `json:"lap"`
	Compound model.Compound `json:"compound"`
}

// Strategy is a full race plan.
type Strategy struct {
	Name  string         `json:"name"`
	Start model.Compound `json:"start"`
	Stops []Stop         `json:"stops"`
}

// Plan is the fixed-size, copyable form of a Strategy used inside a race
// State (so that snapshots are plain value copies).
type Plan struct {
	N     uint8
	Stops [MaxStops]Stop
}

// ToPlan converts a validated strategy.
func (s Strategy) ToPlan() Plan {
	var p Plan
	for i, st := range s.Stops {
		if i >= MaxStops {
			break
		}
		p.Stops[i] = st
		p.N++
	}
	return p
}

// String renders a compact form, e.g. "M-23-H" or "S-14-M-35-H".
func (s Strategy) String() string {
	var b strings.Builder
	b.WriteString(s.Start.String())
	for _, st := range s.Stops {
		fmt.Fprintf(&b, "-%d-%s", st.Lap, st.Compound)
	}
	return b.String()
}

// ValidationError is a user-facing validation failure.
type ValidationError struct {
	Field string
	Msg   string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Msg }

func verr(field, format string, a ...any) error {
	return &ValidationError{Field: field, Msg: fmt.Sprintf(format, a...)}
}

// ErrInvalid is wrapped by every validation failure.
var ErrInvalid = errors.New("invalid strategy")

// Validate checks a strategy against a race of the given number of laps.
// It never panics and returns a clear, user-facing message.
func (s Strategy) Validate(laps int) error {
	if laps < 1 {
		return verr("laps", "la course doit compter au moins un tour")
	}
	if !utf8.ValidString(s.Name) {
		return verr("name", "nom non UTF-8")
	}
	if utf8.RuneCountInString(s.Name) > 24 {
		return verr("name", "nom trop long (24 caractères max)")
	}
	for _, r := range s.Name {
		if !unicode.IsPrint(r) {
			return verr("name", "caractère non imprimable dans le nom")
		}
	}
	if s.Start >= model.NumCompounds {
		return verr("start", "gomme de départ inconnue")
	}
	if len(s.Stops) > MaxStops {
		return verr("stops", "%d arrêts demandés, %d au maximum", len(s.Stops), MaxStops)
	}
	used := 1 << s.Start
	prev := 0
	for i, st := range s.Stops {
		f := fmt.Sprintf("stops[%d]", i)
		if st.Compound >= model.NumCompounds {
			return verr(f, "gomme inconnue")
		}
		if st.Lap < 1 {
			return verr(f, "arrêt au tour %d : le premier arrêt possible est à la fin du tour 1", st.Lap)
		}
		if st.Lap >= laps {
			return verr(f, "arrêt au tour %d : la course s'arrête au tour %d, aucun arrêt possible au dernier tour ou après", st.Lap, laps)
		}
		if st.Lap <= prev {
			return verr(f, "les arrêts doivent être dans l'ordre et à des tours distincts (tour %d après le tour %d)", st.Lap, prev)
		}
		prev = st.Lap
		used |= 1 << st.Compound
	}
	if popcount(used) < 2 {
		return verr("stops", "règlement : au moins deux gommes sèches différentes doivent être utilisées en course")
	}
	return nil
}

func popcount(x int) int {
	n := 0
	for ; x != 0; x &= x - 1 {
		n++
	}
	return n
}

// StintLengths returns the length (laps) of each stint.
func (s Strategy) StintLengths(laps int) []int {
	out := make([]int, 0, len(s.Stops)+1)
	prev := 0
	for _, st := range s.Stops {
		out = append(out, st.Lap-prev)
		prev = st.Lap
	}
	return append(out, laps-prev)
}

// Compounds returns the compound of each stint.
func (s Strategy) Compounds() []model.Compound {
	out := []model.Compound{s.Start}
	for _, st := range s.Stops {
		out = append(out, st.Compound)
	}
	return out
}

// ParseStrategy parses the compact form "M-23-H" / "S-14-M-35-H" (start
// compound, then lap/compound pairs). The result still has to be validated
// against a race distance.
func ParseStrategy(s string) (Strategy, error) {
	parts := strings.Split(strings.TrimSpace(s), "-")
	if len(parts) == 0 || len(parts)%2 == 0 || len(parts) > 2*MaxStops+1 {
		return Strategy{}, verr("strategy", "format attendu : GOMME[-TOUR-GOMME]… (ex. M-23-H)")
	}
	start, err := model.ParseCompound(parts[0])
	if err != nil {
		return Strategy{}, verr("start", "%v", err)
	}
	st := Strategy{Start: start}
	for i := 1; i < len(parts); i += 2 {
		lap := 0
		if len(parts[i]) == 0 || len(parts[i]) > 4 {
			return Strategy{}, verr("stops", "tour invalide %q", parts[i])
		}
		for _, c := range parts[i] {
			if c < '0' || c > '9' {
				return Strategy{}, verr("stops", "tour invalide %q", parts[i])
			}
			lap = lap*10 + int(c-'0')
		}
		c, err := model.ParseCompound(parts[i+1])
		if err != nil {
			return Strategy{}, verr("stops", "%v", err)
		}
		st.Stops = append(st.Stops, Stop{Lap: lap, Compound: c})
	}
	// the canonical notation is the name: never the raw input, which may
	// contain blanks or control characters
	st.Name = st.String()
	if len(st.Name) > 24 {
		st.Name = st.Name[:24]
	}
	return st, nil
}
