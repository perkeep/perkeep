package astrewrite

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"io/ioutil"
	"testing"
)

func TestSimplify(t *testing.T) {
	simplifyAndCompareStmts(t, "-a()", "_1 := a(); -_1")
	simplifyAndCompareStmts(t, "a() + b()", "_1 := a(); _2 := b(); _1 + _2")
	simplifyAndCompareStmts(t, "f(g(), h())", "_1 := g(); _2 := h(); f(_1, _2)")
	simplifyAndCompareStmts(t, "f().x", "_1 := f(); _1.x")
	simplifyAndCompareStmts(t, "f()()", "_1 := f(); _1()")
	simplifyAndCompareStmts(t, "x.f()", "x.f()")
	simplifyAndCompareStmts(t, "f()[g()]", "_1 := f(); _2 := g(); _1[_2]")
	simplifyAndCompareStmts(t, "f()[g():h()]", "_1 := f(); _2 := g(); _3 := h(); _1[_2:_3]")
	simplifyAndCompareStmts(t, "f()[g():h():i()]", "_1 := f(); _2 := g(); _3 := h(); _4 := i(); _1[_2:_3:_4]")
	simplifyAndCompareStmts(t, "*f()", "_1 := f(); *_1")
	simplifyAndCompareStmts(t, "f().(t)", "_1 := f(); _1.(t)")
	simplifyAndCompareStmts(t, "func() { -a() }", "func() { _1 := a(); -_1 }")
	simplifyAndCompareStmts(t, "T{a(), b()}", "_1 := a(); _2 := b(); T{_1, _2}")
	simplifyAndCompareStmts(t, "T{A: a(), B: b()}", "_1 := a(); _2 := b(); T{A: _1, B: _2}")
	simplifyAndCompareStmts(t, "func() { a()() }", "func() { _1 := a(); _1() }")

	simplifyAndCompareStmts(t, "a() && b", "_1 := a(); _1 && b")
	simplifyAndCompareStmts(t, "a && b()", "_1 := a; if _1 { _1 = b() }; _1")
	simplifyAndCompareStmts(t, "a() && b()", "_1 := a(); if _1 { _1 = b() }; _1")

	simplifyAndCompareStmts(t, "a() || b", "_1 := a(); _1 || b")
	simplifyAndCompareStmts(t, "a || b()", "_1 := a; if !_1 { _1 = b() }; _1")
	simplifyAndCompareStmts(t, "a() || b()", "_1 := a(); if !_1 { _1 = b() }; _1")

	simplifyAndCompareStmts(t, "a && (b || c())", "_1 := a; if(_1) { _2 := b; if(!_2) { _2 = c() }; _1 = (_2) }; _1")

	simplifyAndCompareStmts(t, "a := b()()", "_1 := b(); a := _1()")
	simplifyAndCompareStmts(t, "a().f = b", "_1 := a(); _1.f = b")
	simplifyAndCompareStmts(t, "var a int = b()", "_1 := b(); var a int = _1")

	simplifyAndCompareStmts(t, "if a() { b }", "_1 := a(); if _1 { b }")
	simplifyAndCompareStmts(t, "if a := b(); a { c }", "{ a := b(); if a { c } }")
	simplifyAndCompareStmts(t, "if a { b()() }", "if a { _1 := b(); _1() }")
	simplifyAndCompareStmts(t, "if a { b } else { c()() }", "if a { b } else { _1 := c(); _1() }")
	simplifyAndCompareStmts(t, "if a { b } else if c { d()() }", "if a { b } else if c { _1 := d(); _1() }")
	simplifyAndCompareStmts(t, "if a { b } else if c() { d }", "if a { b } else { _1 := c(); if _1 { d } }")
	simplifyAndCompareStmts(t, "if a { b } else if c := d(); c { e }", "if a { b } else { c := d(); if c { e } }")

	simplifyAndCompareStmts(t, "l: switch a { case b, c: d()() }", "l: switch { default: _1 := a; if _1 == (b) || _1 == (c) { _2 := d(); _2() } }")
	simplifyAndCompareStmts(t, "switch a() { case b: c }", "switch { default: _1 := a(); if _1 == (b) { c } }")
	simplifyAndCompareStmts(t, "switch x := a(); x { case b, c: d }", "switch { default: x := a(); _1 := x; if _1 == (b) || _1 == (c) { d } }")
	simplifyAndCompareStmts(t, "switch a() { case b: c; default: e; case c: d }", "switch { default: _1 := a(); if _1 == (b) { c } else if _1 == (c) { d } else { e } }")
	simplifyAndCompareStmts(t, "switch a { case b(): c }", "switch { default: _1 := a; _2 := b(); if _1 == (_2) { c } }")
	simplifyAndCompareStmts(t, "switch a { default: d; fallthrough; case b: c }", "switch { default: _1 := a; if _1 == (b) { c } else { d; c } }")
	simplifyAndCompareStmts(t, "switch a := 0; a {}", "switch { default: a := 0; _ = a }")
	simplifyAndCompareStmts(t, "switch a := 0; a { default: }", "switch { default: a := 0; _ = a }")

	simplifyAndCompareStmts(t, "switch a().(type) { case b, c: d }", "_1 := a(); switch _1.(type) { case b, c: d }")
	simplifyAndCompareStmts(t, "switch x := a(); x.(type) { case b: c }", "{ x := a(); switch x.(type) { case b: c } }")
	simplifyAndCompareStmts(t, "switch a := b().(type) { case c: d }", "_1 := b(); switch a := _1.(type) { case c: d }")
	simplifyAndCompareStmts(t, "switch a.(type) { case b, c: d()() }", "switch a.(type) { case b, c: _1 := d(); _1() }")

	simplifyAndCompareStmts(t, "for a { b()() }", "for a { _1 := b(); _1() }")
	// simplifyAndCompareStmts(t, "for a() { b() }", "for { _1 := a(); if !_1 { break }; b() }")

	simplifyAndCompareStmts(t, "select { case <-a: b()(); default: c()() }", "select { case <-a: _1 := b(); _1(); default: _2 := c(); _2() }")
	simplifyAndCompareStmts(t, "select { case <-a(): b; case <-c(): d }", "_1 := a(); _2 := c(); select { case <-_1: b; case <-_2: d }")
	simplifyAndCompareStmts(t, "var d int; select { case a().f, a().g = <-b(): c; case d = <-e(): f }", "var d int; _5 := b(); _6 := e(); select { case _1, _3 := <-_5: _2 := a(); _2.f = _1; _4 := a(); _4.g = _3; c; case d = <-_6: f }")
	simplifyAndCompareStmts(t, "select { case a() <- b(): c; case d() <- e(): f }", "_1 := a(); _2 := b(); _3 := d(); _4 := e(); select { case _1 <- _2: c; case _3 <- _4: f }")

	simplifyAndCompareStmts(t, "a().f++", "_1 := a(); _1.f++")
	simplifyAndCompareStmts(t, "return a()", "_1 := a(); return _1")
	simplifyAndCompareStmts(t, "go a()()", "_1 := a(); go _1()")
	simplifyAndCompareStmts(t, "defer a()()", "_1 := a(); defer _1()")
	simplifyAndCompareStmts(t, "a() <- b", "_1 := a(); _1 <- b")
	simplifyAndCompareStmts(t, "a <- b()", "_1 := b(); a <- _1")

	for _, name := range []string{"var", "tuple", "range"} {
		fset := token.NewFileSet()
		inFile, err := parser.ParseFile(fset, fmt.Sprintf("testdata/%s.go", name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}

		typesInfo := &types.Info{
			Types:  make(map[ast.Expr]types.TypeAndValue),
			Defs:   make(map[*ast.Ident]types.Object),
			Uses:   make(map[*ast.Ident]types.Object),
			Scopes: make(map[ast.Node]*types.Scope),
		}
		config := &types.Config{
			Importer: importer.Default(),
		}
		if _, err := config.Check("main", fset, []*ast.File{inFile}, typesInfo); err != nil {
			t.Fatal(err)
		}

		outFile := Simplify(inFile, typesInfo, true)
		got := fprint(t, fset, outFile)
		expected, err := ioutil.ReadFile(fmt.Sprintf("testdata/%s.expected.go", name))
		if err != nil {
			t.Fatal(err)
		}
		if got != string(expected) {
			t.Errorf("expected:\n%s\n--- got:\n%s\n", string(expected), got)
		}
	}
}

func simplifyAndCompareStmts(t *testing.T, in, out string) {
	inFile := "package main; func main() { " + in + " }"
	outFile := "package main; func main() { " + out + " }"
	simplifyAndCompare(t, inFile, outFile)
	simplifyAndCompare(t, outFile, outFile)
}

func simplifyAndCompare(t *testing.T, in, out string) {
	fset := token.NewFileSet()

	expected := fprint(t, fset, parse(t, fset, out))

	inFile := parse(t, fset, in)
	typesInfo := &types.Info{
		Types:  make(map[ast.Expr]types.TypeAndValue),
		Defs:   make(map[*ast.Ident]types.Object),
		Uses:   make(map[*ast.Ident]types.Object),
		Scopes: make(map[ast.Node]*types.Scope),
	}
	outFile := Simplify(inFile, typesInfo, true)
	got := fprint(t, fset, outFile)

	if got != expected {
		t.Errorf("\n--- input:\n%s\n--- expected output:\n%s\n--- got:\n%s\n", in, expected, got)
	}
}

func parse(t *testing.T, fset *token.FileSet, body string) *ast.File {
	file, err := parser.ParseFile(fset, "", body, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func fprint(t *testing.T, fset *token.FileSet, file *ast.File) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, file); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestContainsCall(t *testing.T) {
	testContainsCall(t, "a", false)
	testContainsCall(t, "a()", true)
	testContainsCall(t, "T{a, b}", false)
	testContainsCall(t, "T{a, b()}", true)
	testContainsCall(t, "T{a: a, b: b()}", true)
	testContainsCall(t, "(a())", true)
	testContainsCall(t, "a().f", true)
	testContainsCall(t, "a()[b]", true)
	testContainsCall(t, "a[b()]", true)
	testContainsCall(t, "a()[:]", true)
	testContainsCall(t, "a[b():]", true)
	testContainsCall(t, "a[:b()]", true)
	testContainsCall(t, "a[:b:c()]", true)
	testContainsCall(t, "a().(T)", true)
	testContainsCall(t, "*a()", true)
	testContainsCall(t, "-a()", true)
	testContainsCall(t, "&a()", true)
	testContainsCall(t, "&a()", true)
	testContainsCall(t, "a() + b", true)
	testContainsCall(t, "a + b()", true)
}

func testContainsCall(t *testing.T, in string, expected bool) {
	x, err := parser.ParseExpr(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := ContainsCall(x); got != expected {
		t.Errorf("ContainsCall(%s): expected %t, got %t", in, expected, got)
	}
}
