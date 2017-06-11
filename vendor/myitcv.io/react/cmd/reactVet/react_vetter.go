package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path"
	"sort"
	"strings"
)

const (
	jsxImportPath = "myitcv.io/react/jsx"

	jsxHTML     = "HTML"
	jsxMarkdown = "Markdown"
	jsxHTMLElem = "HTMLElem"
)

type reactVetter struct {
	wd   string
	bpkg *build.Package
	pkgs map[string]*ast.Package

	info *types.Info

	errlist []vetErr
}

func (r *reactVetter) errorf(node ast.Node, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	r.errlist = append(r.errlist, vetErr{
		pos: fset.Position(node.Pos()),
		msg: msg,
	})
}

var fset = token.NewFileSet()

func newReactVetter(bpkg *build.Package, wd string) *reactVetter {
	pkgs, err := parser.ParseDir(fset, bpkg.Dir, nil, parser.ParseComments)
	if err != nil {
		fatalf("could not parse package directory for %v", bpkg.Name)
	}

	return &reactVetter{
		pkgs: pkgs,
		bpkg: bpkg,
		wd:   wd,
	}
}

func (r *reactVetter) vetPackages() {
	pns := make([]string, 0, len(r.pkgs))

	for n := range r.pkgs {
		pns = append(pns, n)
	}

	sort.Strings(pns)

	for _, n := range pns {
		pkg := r.pkgs[n]

		files := make([]*ast.File, 0, len(pkg.Files))
		var jsxFiles []*jsxWalker

		for _, f := range pkg.Files {
			files = append(files, f)

			for _, i := range f.Imports {
				if strings.Trim(i.Path.Value, "\"") == jsxImportPath {
					name := path.Base(jsxImportPath)
					if i.Name != nil && i.Name.Name != "_" {
						name = i.Name.Name
					}
					jsxFiles = append(jsxFiles, &jsxWalker{
						reactVetter: r,
						f:           f,
						name:        name,
					})
				}
			}
		}

		// TODO this logic will certainly need to be relaxed if we add more vet rules
		if len(jsxFiles) == 0 {
			continue
		}

		conf := types.Config{
			Importer: importer.Default(),
		}
		info := &types.Info{
			Defs:  make(map[*ast.Ident]types.Object),
			Types: make(map[ast.Expr]types.TypeAndValue),
			Uses:  make(map[*ast.Ident]types.Object),
		}
		_, err := conf.Check(r.bpkg.ImportPath, fset, files, info)
		if err != nil {
			fatalf("type checking failed, %v", err)
		}

		r.info = info

		for _, j := range jsxFiles {
			ast.Walk(j, j.f)
		}
	}
}

type jsxWalker struct {
	*reactVetter
	f    *ast.File
	name string
}

func (j *jsxWalker) Visit(n ast.Node) ast.Visitor {
	switch n := n.(type) {
	case *ast.CallExpr:
		se, ok := n.Fun.(*ast.SelectorExpr)
		if !ok {
			break
		}

		i, ok := se.X.(*ast.Ident)
		if !ok {
			break
		}

		if i.Name != j.name {
			break
		}

		switch se.Sel.Name {
		case jsxHTML, jsxHTMLElem, jsxMarkdown:
		default:
			fatalf("unknown jsx package method %v", se.Sel.Name)
		}

		if v := len(n.Args); v != 1 {
			fatalf("expected 1 arg; got %v", v)
		}

		a := n.Args[0]

		tv, ok := j.info.Types[a]
		if !ok || tv.Type != types.Typ[types.String] || tv.Value == nil {
			j.errorf(a, "argument must be a constant string")
		}

	}
	return j
}
