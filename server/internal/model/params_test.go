package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every exported constant of this package documents "unit, meaning [lo, hi]",
// its value lies inside the bounds, and docs/MODELS.md mentions it.
func TestParametersHaveUnitsAndBounds(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "model.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("../../../docs/MODELS.md")
	if err != nil {
		t.Fatal("docs/MODELS.md is required: ", err)
	}
	re := regexp.MustCompile(`^\s*([^,]+),.*\[(-?[0-9.]+),\s*(-?[0-9.]+)\]\s*$`)
	n := 0
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, sp := range gd.Specs {
			vs := sp.(*ast.ValueSpec)
			for i, name := range vs.Names {
				// numeric literal constants only (the Compound enum uses iota)
				if !name.IsExported() || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || (lit.Kind != token.INT && lit.Kind != token.FLOAT) {
					continue
				}
				n++
				if vs.Comment == nil {
					t.Errorf("%s: missing `// unit, meaning [lo, hi]` comment", name)
					continue
				}
				m := re.FindStringSubmatch(strings.TrimSpace(vs.Comment.Text()))
				if m == nil {
					t.Errorf("%s: comment %q must read `unit, meaning [lo, hi]`", name, vs.Comment.Text())
					continue
				}
				lo, _ := strconv.ParseFloat(m[2], 64)
				hi, _ := strconv.ParseFloat(m[3], 64)
				v, err := strconv.ParseFloat(lit.Value, 64)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if v < lo || v > hi {
					t.Errorf("%s = %v outside its documented bounds [%v, %v]", name, v, lo, hi)
				}
				if !strings.Contains(string(doc), name.Name) {
					t.Errorf("%s is not documented in docs/MODELS.md", name)
				}
			}
		}
	}
	if n < 15 {
		t.Fatalf("only %d parameters found: parser out of sync", n)
	}
}

// The tyre table is within the documented bounds of TyreSpec.
func TestTyreTableBounds(t *testing.T) {
	for c, ty := range Tyres {
		ok := ty.PaceS >= -1.5 && ty.PaceS <= 1.5 && ty.WearPerLap >= 0.005 && ty.WearPerLap <= 0.1 &&
			ty.DegS >= 0 && ty.DegS <= 4 && ty.Cliff >= 0.4 && ty.Cliff <= 1 && ty.CliffS >= 0 && ty.CliffS <= 3 &&
			ty.WarmupS >= 0 && ty.WarmupS <= 2
		if !ok {
			t.Errorf("compound %v outside TyreSpec bounds: %+v", Compound(c), ty)
		}
	}
}
