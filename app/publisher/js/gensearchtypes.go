//go:build ignore
// +build ignore

/*
Copyright 2016 The Perkeep Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/parser"
	"go/token"
	"log"
	"os"
)

func main() {
	flagOut := flag.String("out", "zsearch.go", "output file name ('-' is stdout)")
	flag.Parse()

	out := os.Stdout
	if *flagOut != "-" {
		var err error
		if out, err = os.Create(*flagOut); err != nil {
			log.Fatal(err)
		}
		defer func() {
			if err := out.Close(); err != nil {
				log.Fatal(err)
			}
		}()
	}

	fmt.Fprintln(out, `// +build js

// generated by gensearchtypes.go; DO NOT EDIT

/*
Copyright 2016 The Perkeep Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"net/url"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/types/camtypes"
)

// Duplicating the search pkg types in here - since we only use them for json
// decoding - , instead of importing them through the search package, which would
// bring in more dependencies, and hence a larger js file.
// To give an idea, the generated publisher.js is ~3.5MB, whereas if we instead import
// camlistore.org/pkg/search to use its types instead of the ones below, we grow to
// ~5.7MB.`)

	wantTypes := []string{
		"SearchResult",
		"SearchResultBlob",
		"DescribeResponse",
		"MetaMap",
		"DescribedBlob",
		"DescribedPermanode",
	}

	m := make(map[string]*ast.GenDecl, len(wantTypes))
	for _, n := range wantTypes {
		m[n] = nil
	}

	fileSet, pkg, err := loadPkg("perkeep.org/pkg/search")
	if err != nil {
		log.Fatal(err)
	}

	merged := ast.MergePackageFiles(pkg, 0)

	for _, decl := range merged.Decls {
		g, ok := decl.(*ast.GenDecl)
		if !ok || g.Tok != token.TYPE || len(g.Specs) != 1 {
			// skip non-type declarations and
			// combined type declarations in the form:
			//  type (X T1; Y T2...)
			continue
		}

		t := g.Specs[0].(*ast.TypeSpec)
		if _, ok := m[t.Name.Name]; ok {
			m[t.Name.Name] = g
		}
	}

	for _, n := range wantTypes {
		g := m[n]
		if g == nil {
			log.Fatalf("type %v not found", n)
		}

		// strip DescribeRequest from DescribeBlob because it would pull a lot more of search pkg in
		t := g.Specs[0].(*ast.TypeSpec)
		if t.Name.Name == "DescribedBlob" {
			fl := t.Type.(*ast.StructType).Fields
			var nfl []*ast.Field
			for _, f := range fl.List {
				if isStarExprName(f.Type, "DescribeRequest") {
					continue
				}
				nfl = append(nfl, f)
			}
			fl.List = nfl
		}

		fmt.Fprintln(out)
		format.Node(out, fileSet, g)
		fmt.Fprintln(out)
	}
}

func isStarExprName(x ast.Expr, name string) bool {
	star, ok := x.(*ast.StarExpr)
	if !ok {
		return false
	}

	id, ok := star.X.(*ast.Ident)
	if !ok {
		return false
	}

	return id.Name == name
}

func loadPkg(importPath string) (*token.FileSet, *ast.Package, error) {
	buildPkg, err := build.Import(importPath, ".", 0)
	if err != nil {
		return nil, nil, err
	}

	fileSet := token.NewFileSet()
	pkgs, err := parser.ParseDir(fileSet, buildPkg.Dir, nil, parser.ParseComments)
	if err != nil {
		return nil, nil, err
	}

	pkg := pkgs[buildPkg.Name]
	if pkg == nil {
		return nil, nil, fmt.Errorf("package %v not found in %v", buildPkg.Name, importPath)
	}

	ast.PackageExports(pkg)
	return fileSet, pkg, nil
}
