/*
Copyright 2012 Google Inc.

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
	"bytes"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	flag.Parse()
	dir := "."
	switch flag.NArg() {
	case 0:
	case 1:
		dir = flag.Arg(0)
		if err := os.Chdir(dir); err != nil {
			log.Fatalf("chdir(%q) = %v", dir, err)
		}
	default:
		fmt.Fprintf(os.Stderr, "usage: genfileembed [<dir>]\n")
		os.Exit(2)
	}

	pkgName, filePattern, err := parseFileEmbed()
	if err != nil {
		log.Fatalf("Error parsing %s/fileembed.go: %v", dir, err)
	}
	for _, fileName := range matchingFiles(filePattern) {
		embedName := "zembed_" + fileName + ".go"
		fi, err := os.Stat(fileName)
		if err != nil {
			log.Fatal(err)
		}
		efi, err := os.Stat(embedName)
		if err == nil && !efi.ModTime().Before(fi.ModTime()) {
			continue
		}
		log.Printf("Updating %s (package %s)", filepath.Join(dir, embedName), pkgName)
		bs, err := ioutil.ReadFile(fileName)
		if err != nil {
			log.Fatal(err)
		}
		var b bytes.Buffer
		fmt.Fprintf(&b, "// THIS FILE IS AUTO-GENERATED FROM %s\n", fileName)
		fmt.Fprintf(&b, "// DO NOT EDIT.\n")
		fmt.Fprintf(&b, "package %s\n", pkgName)
		fmt.Fprintf(&b, "func init() {\n\tFiles.Add(%q, %q);\n}\n", fileName, bs)
		if err := ioutil.WriteFile(embedName, b.Bytes(), 0644); err != nil {
			log.Fatal(err)
		}
	}
}

func matchingFiles(p *regexp.Regexp) []string {
	var f []string
	d, err := os.Open(".")
	if err != nil {
		log.Fatal(err)
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		log.Fatal(err)
	}
	for _, n := range names {
		if strings.HasPrefix(n, "zembed_") {
			continue
		}
		if p.MatchString(n) {
			f = append(f, n)
		}
	}
	return f
}

func parseFileEmbed() (pkgName string, filePattern *regexp.Regexp, err error) {
	fe, err := os.Open("fileembed.go")
	if err != nil {
		return
	}
	defer fe.Close()

	fs := token.NewFileSet()
	astf, err := parser.ParseFile(fs, "fileembed.go", fe, parser.PackageClauseOnly|parser.ParseComments)
	if err != nil {
		return
	}
	pkgName = astf.Name.Name

	if astf.Doc == nil {
		err = fmt.Errorf("no package comment before the %q line", "package "+pkgName)
		return
	}

	pkgComment := astf.Doc.Text()
	findPattern := regexp.MustCompile(`(?m)^#fileembed\s+pattern\s+(\S+)\s*$`)
	m := findPattern.FindStringSubmatch(pkgComment)
	if m == nil {
		err = fmt.Errorf("package comment lacks line of form: #fileembed pattern <pattern>")
		return
	}
	pattern := m[1]
	filePattern, err = regexp.Compile(pattern)
	if err != nil {
		err = fmt.Errorf("bad regexp %q: %v", pattern, err)
		return
	}
	return
}
