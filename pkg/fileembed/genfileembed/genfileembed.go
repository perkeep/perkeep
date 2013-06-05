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
	"compress/zlib"
	"flag"
	"fmt"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxUncompressed = 50 << 10 // 50KB
	// Threshold ratio for compression.
	// Files which don't compress at least as well are kept uncompressed.
	zRatio = 0.5
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage: genfileembed [flags] [<dir>]\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	var processAll bool
	flag.BoolVar(&processAll, "all", false, "process all files (if false, only process modified files)")
	var fileEmbedPkgPath string
	flag.StringVar(&fileEmbedPkgPath, "fileembed-package", "camlistore.org/pkg/fileembed", "the Go package name for fileembed. If you have vendored fileembed (e.g. with goven), you can use this flag to ensure that generated code imports the vendored package.")
	flag.Usage = usage
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
		flag.Usage()
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
		if err == nil && !efi.ModTime().Before(fi.ModTime()) && !processAll {
			continue
		}
		log.Printf("Updating %s (package %s)", filepath.Join(dir, embedName), pkgName)

		zb, fileSize := compressFile(fileName)
		ratio := float64(len(zb)) / float64(fileSize)
		var bs []byte
		byteStreamType := ""
		if fileSize < maxUncompressed || ratio > zRatio {
			byteStreamType = "fileembed.String"
			bs, err = ioutil.ReadFile(fileName)
			if err != nil {
				log.Fatal(err)
			}
		} else {
			byteStreamType = "fileembed.ZlibCompressed"
			bs = zb
		}
		qb := quote(bs) // quoted bytes

		var b bytes.Buffer
		fmt.Fprintf(&b, "// THIS FILE IS AUTO-GENERATED FROM %s\n", fileName)
		fmt.Fprintf(&b, "// DO NOT EDIT.\n\n")
		fmt.Fprintf(&b, "package %s\n\n", pkgName)
		fmt.Fprintf(&b, "import \"time\"\n\n")
		fmt.Fprintf(&b, "import \""+fileEmbedPkgPath+"\"\n\n")
		fmt.Fprintf(&b, "func init() {\n\tFiles.Add(%q, %d, %s(%s), time.Unix(0, %d));\n}\n",
			fileName, fileSize, byteStreamType, qb, fi.ModTime().UnixNano())

		// gofmt it
		fset := token.NewFileSet()
		ast, err := parser.ParseFile(fset, "", b.Bytes(), parser.ParseComments)
		if err != nil {
			log.Fatal(err)
		}

		var clean bytes.Buffer
		config := &printer.Config{
			Mode:     printer.TabIndent | printer.UseSpaces,
			Tabwidth: 8,
		}
		err = config.Fprint(&clean, fset, ast)
		if err != nil {
			log.Fatal(err)
		}

		if err := ioutil.WriteFile(embedName, clean.Bytes(), 0644); err != nil {
			log.Fatal(err)
		}
	}
}

func compressFile(fileName string) ([]byte, int64) {
	var zb bytes.Buffer
	f, err := os.Open(fileName)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	w := zlib.NewWriter(&zb)
	n, err := io.Copy(w, f)
	if err != nil {
		log.Fatal(err)
	}
	w.Close()
	return zb.Bytes(), n
}

func quote(bs []byte) []byte {
	var qb bytes.Buffer
	qb.WriteByte('"')
	run := 0
	for _, b := range bs {
		if b == '\n' {
			qb.WriteString(`\n`)
		}
		if b == '\n' || run > 80 {
			qb.WriteString("\" +\n\t\"")
			run = 0
		}
		if b == '\n' {
			continue
		}
		run++
		if b == '\\' {
			qb.WriteString(`\\`)
			continue
		}
		if b == '"' {
			qb.WriteString(`\"`)
			continue
		}
		if (b >= 32 && b <= 126) || b == '\t' {
			qb.WriteByte(b)
			continue
		}
		fmt.Fprintf(&qb, "\\x%02x", b)
	}
	qb.WriteByte('"')
	return qb.Bytes()
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
