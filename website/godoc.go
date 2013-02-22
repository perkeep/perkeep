/*
Copyright 2013 The Camlistore Authors.

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
	"fmt"
	"go/build"
	"go/doc"
	"go/parser"
	"go/token"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
)

var docRx = regexp.MustCompile(`^/((?:pkg|cmd)/([\w/]+?))/?$`)

type godocHandler struct{}

func (godocHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m := docRx.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	suffix := m[1]
	pkgName := "camlistore.org/" + suffix
	diskPath := filepath.Join(*root, "..", suffix)
	bpkg, err := build.ImportDir(diskPath, 0)
	if err != nil {
		log.Print(err)
		return
	}
	inSet := make(map[string]bool)
	for _, name := range bpkg.GoFiles {
		inSet[filepath.Base(name)] = true
	}

	fset := token.NewFileSet()
	filter := func(fi os.FileInfo) bool {
		return inSet[fi.Name()]
	}
	aPkgMap, err := parser.ParseDir(fset, diskPath, filter, 0)
	if err != nil {
		log.Print(err)
		return
	}
	aPkg := aPkgMap[path.Base(suffix)]
	if aPkg == nil {
		for _, v := range aPkgMap {
			aPkg = v
			break
		}
		if aPkg == nil {
			log.Printf("no apkg found?")
			http.NotFound(w, r)
			return
		}
	}

	docpkg := doc.New(aPkg, pkgName, 0)
	fmt.Fprintf(w, "%#v", docpkg)
}
