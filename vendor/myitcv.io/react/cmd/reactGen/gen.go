// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package main

import (
	"bytes"
	"go/ast"
	"go/build"
	"go/parser"
	"go/printer"
	"go/token"
	"os/exec"
	"path"
	"strings"

	"myitcv.io/gogenerate"
)

const (
	reactPkg      = "myitcv.io/react"
	compDefName   = "ComponentDef"
	compDefSuffix = "Def"

	stateTypeSuffix     = "State"
	propsTypeSuffix     = "Props"
	propsTypeTmplPrefix = "_"

	getInitialState           = "GetInitialState"
	componentWillReceiveProps = "ComponentWillReceiveProps"
	equals                    = "Equals"
)

type typeFile struct {
	ts   *ast.TypeSpec
	file *ast.File
}

type funcFile struct {
	fn   *ast.FuncDecl
	file *ast.File
}

var fset = token.NewFileSet()

func astNodeString(i interface{}) string {
	b := bytes.NewBuffer(nil)
	err := printer.Fprint(b, fset, i)
	if err != nil {
		fatalf("failed to astNodeString %v: %v", i, err)
	}

	return b.String()
}

func goFmtBuf(b *bytes.Buffer) (*bytes.Buffer, error) {
	out := bytes.NewBuffer(nil)
	cmd := exec.Command("gofmt", "-s")
	cmd.Stdin = b
	cmd.Stdout = out

	err := cmd.Run()

	return out, err
}

type gen struct {
	pkg        string
	pkgImpPath string

	isReactCore bool

	propsTmpls    map[string]typeFile
	components    map[string]typeFile
	types         map[string]typeFile
	pointMeths    map[string][]funcFile
	nonPointMeths map[string][]funcFile
}

func dogen(dir, license string) {

	bpkg, err := build.ImportDir(dir, 0)
	if err != nil {
		fatalf("unable to import pkg in dir %v: %v", dir, err)
	}

	isReactCore := bpkg.ImportPath == reactPkg

	pkgs, err := parser.ParseDir(fset, dir, nil, parser.ParseComments)
	if err != nil {
		fatalf("unable to parse %v: %v", dir, err)
	}

	// we intentionally walk all packages, i.e. the package in the current directory
	// and any x-test package that may also be present
	for pn, pkg := range pkgs {
		g := &gen{
			pkg:        pn,
			pkgImpPath: bpkg.ImportPath,

			isReactCore: isReactCore,

			propsTmpls:    make(map[string]typeFile),
			components:    make(map[string]typeFile),
			types:         make(map[string]typeFile),
			pointMeths:    make(map[string][]funcFile),
			nonPointMeths: make(map[string][]funcFile),
		}

		for fn, file := range pkg.Files {

			if gogenerate.FileGeneratedBy(fn, "reactGen") {
				continue
			}

			foundImp := false
			impName := ""

			for _, i := range file.Imports {
				p := strings.Trim(i.Path.Value, "\"")

				if p == reactPkg {
					foundImp = true

					if i.Name != nil {
						impName = i.Name.Name
					} else {
						impName = path.Base(reactPkg)
					}

					break
				}
			}

			if !foundImp && !isReactCore {
				continue
			}

			for _, d := range file.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil {
						continue
					}

					f := d.Recv.List[0]

					switch v := f.Type.(type) {
					case *ast.StarExpr:
						id, ok := v.X.(*ast.Ident)
						if !ok {
							continue
						}
						g.pointMeths[id.Name] = append(g.pointMeths[id.Name], funcFile{d, file})
					case *ast.Ident:
						g.nonPointMeths[v.Name] = append(g.nonPointMeths[v.Name], funcFile{d, file})
					}

				case *ast.GenDecl:
					if d.Tok != token.TYPE {
						continue
					}

					for _, ts := range d.Specs {
						ts := ts.(*ast.TypeSpec)

						st, ok := ts.Type.(*ast.StructType)
						if !ok || st.Fields == nil {
							continue
						}

						if n := ts.Name.Name; strings.HasPrefix(n, propsTypeTmplPrefix) &&
							strings.HasSuffix(n, propsTypeSuffix) {

							if ts.Doc == nil {
								ts.Doc = d.Doc
							}

							g.propsTmpls[n] = typeFile{
								ts:   ts,
								file: file,
							}

							continue
						}

						foundAnon := false

						for _, f := range st.Fields.List {
							if f.Names != nil {
								// it must be anonymous
								continue
							}

							se, ok := f.Type.(*ast.SelectorExpr)
							if !ok {
								continue
							}

							if se.Sel.Name != compDefName {
								continue
							}

							id, ok := se.X.(*ast.Ident)
							if !ok {
								continue
							}

							if id.Name != impName {
								continue
							}

							foundAnon = true
						}

						if foundAnon && strings.HasSuffix(ts.Name.Name, compDefSuffix) {
							g.components[ts.Name.Name] = typeFile{ts, file}
						} else {
							g.types[ts.Name.Name] = typeFile{ts, file}
						}
					}
				}
			}
		}

		// at this point we have the components and their methods
		for cd := range g.components {
			g.genComp(cd)
		}

		for pt, t := range g.propsTmpls {
			g.genProps(pt, t)
		}
	}
}
