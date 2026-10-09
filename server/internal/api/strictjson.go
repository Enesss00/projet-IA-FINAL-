package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// MaxJSONDepth bounds the nesting of any inbound message. The deepest
// legitimate message (sim.start → strategies → stops → stop) has depth 5.
const MaxJSONDepth = 8

// errStrict is a French, user-facing decoding error.
type errStrict struct{ msg string }

func (e *errStrict) Error() string { return e.msg }

func strictErr(format string, a ...any) error { return &errStrict{msg: fmt.Sprintf(format, a...)} }

// checkShape walks the raw JSON once, iteratively: it rejects nesting deeper
// than MaxJSONDepth and duplicated keys in any object (a proxy reading the
// first occurrence and the server the last would disagree).
func checkShape(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var stack []frame
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return translate(err)
		}
		if len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.obj && top.expect {
				if d, ok := tok.(json.Delim); ok && d == '}' {
					stack = stack[:len(stack)-1]
					markValueDone(stack)
					continue
				}
				k, _ := tok.(string)
				if _, dup := top.keys[k]; dup {
					return strictErr("clé « %s » en double", trunc(k, 32))
				}
				top.keys[k] = struct{}{}
				top.expect = false
				continue
			}
		}
		switch tok {
		case json.Delim('{'):
			if len(stack) >= MaxJSONDepth {
				return strictErr("JSON trop imbriqué (profondeur maximale %d)", MaxJSONDepth)
			}
			stack = append(stack, frame{obj: true, keys: map[string]struct{}{}, expect: true})
		case json.Delim('['):
			if len(stack) >= MaxJSONDepth {
				return strictErr("JSON trop imbriqué (profondeur maximale %d)", MaxJSONDepth)
			}
			stack = append(stack, frame{})
		case json.Delim(']'), json.Delim('}'):
			stack = stack[:len(stack)-1]
			markValueDone(stack)
		default:
			markValueDone(stack)
		}
	}
}

type frame struct {
	obj    bool
	keys   map[string]struct{}
	expect bool // in an object: the next string token is a key
}

func markValueDone(stack []frame) {
	if n := len(stack); n > 0 && stack[n-1].obj {
		stack[n-1].expect = true
	}
}

// checkKeys verifies that every object key matches a json tag of the target
// type exactly (encoding/json alone accepts "SEED" or "ſeed" for "seed").
func checkKeys(b []byte, t reflect.Type) error {
	var v any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return translate(err)
	}
	return walkKeys(v, t, "")
}

var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

func walkKeys(v any, t reflect.Type, path string) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == rawMessageType {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return nil // type mismatch: reported by the real decoder
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			fields[name] = f.Type
		}
		for k, sub := range m {
			ft, ok := fields[k]
			if !ok {
				return strictErr("champ inconnu « %s »", trunc(path+k, 48))
			}
			if err := walkKeys(sub, ft, path+k+"."); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		a, ok := v.([]any)
		if !ok {
			return nil
		}
		for i, x := range a {
			if err := walkKeys(x, t.Elem(), fmt.Sprintf("%s[%d].", strings.TrimSuffix(path, "."), i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// translate turns encoding/json errors into French messages. Raw Go
// messages never reach the user.
func translate(err error) error {
	var es *errStrict
	if errors.As(err, &es) {
		return es
	}
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return strictErr("JSON mal formé (octet %d)", se.Offset)
	}
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		f := te.Field
		if f == "" {
			f = "message"
		}
		return strictErr("type invalide pour « %s » : %s attendu", trunc(f, 48), kindFR(te.Type))
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return strictErr("JSON incomplet")
	}
	s := err.Error()
	if strings.HasPrefix(s, "json: unknown field ") {
		return strictErr("champ inconnu %s", trunc(strings.TrimPrefix(s, "json: unknown field "), 48))
	}
	return strictErr("JSON invalide")
}

func kindFR(t reflect.Type) string {
	if t == nil {
		return "une autre valeur"
	}
	if t == reflect.TypeOf(Num("")) {
		return "un nombre"
	}
	switch t.Kind() {
	case reflect.String:
		return "un texte"
	case reflect.Int, reflect.Int64, reflect.Int32, reflect.Float64, reflect.Uint64:
		return "un nombre"
	case reflect.Bool:
		return "un booléen"
	case reflect.Slice, reflect.Array:
		return "une liste"
	case reflect.Struct, reflect.Map:
		return "un objet"
	default:
		return "une autre valeur"
	}
}

var plainInt = regexp.MustCompile(`^-?[0-9]{1,18}$`)

// exactInt parses a JSON number literal that denotes an integer exactly:
// "12", "1.0e1", "2e1" are 10/20; "10.5" and "10.0000000000000001" are not.
// Literals with huge exponents or many digits are refused before any
// arbitrary-precision work (no CPU amplification).
func exactInt(s string) (int64, bool) {
	if plainInt.MatchString(s) {
		v, err := strconv.ParseInt(s, 10, 64)
		return v, err == nil
	}
	if len(s) > 40 {
		return 0, false
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		e, err := strconv.Atoi(strings.TrimPrefix(s[i+1:], "+"))
		if err != nil || e > 20 || e < -40 {
			return 0, false
		}
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || !r.IsInt() || !r.Num().IsInt64() {
		return 0, false
	}
	return r.Num().Int64(), true
}
