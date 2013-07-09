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

// This is a hacked-up version of godoc.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"
)

const (
	domainName       = "camlistore.org"
	pkgPattern       = "/pkg/"
	cmdPattern       = "/cmd/"
	fileembedPattern = "fileembed.go"
)

var docRx = regexp.MustCompile(`^/((?:pkg|cmd)/([\w/]+?)(\.go)??)/?$`)

var tabwidth = 4

type PageInfo struct {
	Dirname string // directory containing the package
	Err     error  // error or nil

	// package info
	FSet     *token.FileSet // nil if no package documentation
	PDoc     *doc.Package   // nil if no package documentation
	Examples []*doc.Example // nil if no example code
	PAst     *ast.File      // nil if no AST with package exports
	IsPkg    bool           // true for pkg, false for cmd

	// directory info
	Dirs    *DirList  // nil if no directory information
	DirTime time.Time // directory time stamp
	DirFlat bool      // if set, show directory in a flat (non-indented) manner
	PList   []string  // list of package names found
}

// godocFmap describes the template functions installed with all godoc templates.
// Convention: template function names ending in "_html" or "_url" produce
//             HTML- or URL-escaped strings; all other function results may
//             require explicit escaping in the template.
var godocFmap = template.FuncMap{
	// various helpers
	"filename": filenameFunc,
	"repeat":   strings.Repeat,

	// accss to FileInfos (directory listings)
	"fileInfoName": fileInfoNameFunc,
	"fileInfoTime": fileInfoTimeFunc,

	// access to search result information
	//"infoKind_html":    infoKind_htmlFunc,
	//"infoLine":         infoLineFunc,
	//"infoSnippet_html": infoSnippet_htmlFunc,

	// formatting of AST nodes
	"node":         nodeFunc,
	"node_html":    node_htmlFunc,
	"comment_html": comment_htmlFunc,
	//"comment_text": comment_textFunc,

	// support for URL attributes
	"srcLink":     srcLinkFunc,
	"posLink_url": posLink_urlFunc,

	// formatting of Examples
	"example_html":   example_htmlFunc,
	"example_name":   example_nameFunc,
	"example_suffix": example_suffixFunc,
}

func example_htmlFunc(funcName string, examples []*doc.Example, fset *token.FileSet) string {
	return ""
}

func example_nameFunc(s string) string {
	return ""
}

func example_suffixFunc(name string) string {
	return ""
}

func filenameFunc(path string) string {
	_, localname := pathpkg.Split(path)
	return localname
}

func fileInfoNameFunc(fi os.FileInfo) string {
	name := fi.Name()
	if fi.IsDir() {
		name += "/"
	}
	return name
}

func fileInfoTimeFunc(fi os.FileInfo) string {
	if t := fi.ModTime(); t.Unix() != 0 {
		return t.Local().String()
	}
	return "" // don't return epoch if time is obviously not set
}

// Write an AST node to w.
func writeNode(w io.Writer, fset *token.FileSet, x interface{}) {
	// convert trailing tabs into spaces using a tconv filter
	// to ensure a good outcome in most browsers (there may still
	// be tabs in comments and strings, but converting those into
	// the right number of spaces is much harder)
	//
	// TODO(gri) rethink printer flags - perhaps tconv can be eliminated
	//           with an another printer mode (which is more efficiently
	//           implemented in the printer than here with another layer)
	mode := printer.TabIndent | printer.UseSpaces
	err := (&printer.Config{Mode: mode, Tabwidth: tabwidth}).Fprint(&tconv{output: w}, fset, x)
	if err != nil {
		log.Print(err)
	}
}

func nodeFunc(node interface{}, fset *token.FileSet) string {
	var buf bytes.Buffer
	writeNode(&buf, fset, node)
	return buf.String()
}

func node_htmlFunc(node interface{}, fset *token.FileSet) string {
	var buf1 bytes.Buffer
	writeNode(&buf1, fset, node)
	var buf2 bytes.Buffer
	FormatText(&buf2, buf1.Bytes(), -1, true, "", nil)
	return buf2.String()
}

func comment_htmlFunc(comment string) string {
	var buf bytes.Buffer
	// TODO(gri) Provide list of words (e.g. function parameters)
	//           to be emphasized by ToHTML.
	doc.ToHTML(&buf, comment, nil) // does html-escaping
	return buf.String()
}

func posLink_urlFunc(node ast.Node, fset *token.FileSet) string {
	var relpath string
	var line int
	var low, high int // selection

	if p := node.Pos(); p.IsValid() {
		pos := fset.Position(p)
		idx := strings.LastIndex(pos.Filename, domainName)
		if idx == -1 {
			log.Fatalf("No \"%s\" in path to file %s", domainName, pos.Filename)
		}
		relpath = pathpkg.Clean(pos.Filename[idx+len(domainName):])
		line = pos.Line
		low = pos.Offset
	}
	if p := node.End(); p.IsValid() {
		high = fset.Position(p).Offset
	}

	var buf bytes.Buffer
	template.HTMLEscape(&buf, []byte(relpath))
	// selection ranges are of form "s=low:high"
	if low < high {
		fmt.Fprintf(&buf, "?s=%d:%d", low, high) // no need for URL escaping
		// if we have a selection, position the page
		// such that the selection is a bit below the top
		line -= 10
		if line < 1 {
			line = 1
		}
	}
	// line id's in html-printed source are of the
	// form "L%d" where %d stands for the line number
	if line > 0 {
		fmt.Fprintf(&buf, "#L%d", line) // no need for URL escaping
	}

	return buf.String()
}

func srcLinkFunc(s string) string {
	idx := strings.LastIndex(s, domainName)
	if idx == -1 {
		log.Fatalf("No \"%s\" in path to file %s", domainName, s)
	}
	return pathpkg.Clean(s[idx+len(domainName):])
}

func (pi *PageInfo) populateDirs(diskPath string, depth int) {
	var dir *Directory
	dir = newDirectory(diskPath, depth)
	pi.Dirs = dir.listing(true)
	pi.DirTime = time.Now()
}

func getPageInfo(pkgName, diskPath string) (pi PageInfo, err error) {
	if pkgName == pathpkg.Join(domainName, pkgPattern) ||
		pkgName == pathpkg.Join(domainName, cmdPattern) {
		pi.Dirname = diskPath
		pi.populateDirs(diskPath, -1)
		return
	}
	bpkg, err := build.ImportDir(diskPath, 0)
	if err != nil {
		if _, ok := err.(*build.NoGoError); ok {
			pi.populateDirs(diskPath, -1)
			return pi, nil
		}
		return
	}
	inSet := make(map[string]bool)
	for _, name := range bpkg.GoFiles {
		if name == fileembedPattern {
			continue
		}
		inSet[filepath.Base(name)] = true
	}

	pi.FSet = token.NewFileSet()
	filter := func(fi os.FileInfo) bool {
		return inSet[fi.Name()]
	}
	aPkgMap, err := parser.ParseDir(pi.FSet, diskPath, filter, parser.ParseComments)
	if err != nil {
		return
	}
	aPkg := aPkgMap[pathpkg.Base(pkgName)]
	if aPkg == nil {
		for _, v := range aPkgMap {
			aPkg = v
			break
		}
		if aPkg == nil {
			err = errors.New("no apkg found?")
			return
		}
	}

	pi.Dirname = diskPath
	pi.PDoc = doc.New(aPkg, pkgName, 0)
	pi.IsPkg = strings.Contains(pkgName, domainName+pkgPattern)

	// get directory information
	pi.populateDirs(diskPath, -1)
	return
}

const (
	indenting = iota
	collecting
)

// A tconv is an io.Writer filter for converting leading tabs into spaces.
type tconv struct {
	output io.Writer
	state  int // indenting or collecting
	indent int // valid if state == indenting
}

var spaces = []byte("                                ") // 32 spaces seems like a good number

func (p *tconv) writeIndent() (err error) {
	i := p.indent
	for i >= len(spaces) {
		i -= len(spaces)
		if _, err = p.output.Write(spaces); err != nil {
			return
		}
	}
	// i < len(spaces)
	if i > 0 {
		_, err = p.output.Write(spaces[0:i])
	}
	return
}

func (p *tconv) Write(data []byte) (n int, err error) {
	if len(data) == 0 {
		return
	}
	pos := 0 // valid if p.state == collecting
	var b byte
	for n, b = range data {
		switch p.state {
		case indenting:
			switch b {
			case '\t':
				p.indent += tabwidth
			case '\n':
				p.indent = 0
				if _, err = p.output.Write(data[n : n+1]); err != nil {
					return
				}
			case ' ':
				p.indent++
			default:
				p.state = collecting
				pos = n
				if err = p.writeIndent(); err != nil {
					return
				}
			}
		case collecting:
			if b == '\n' {
				p.state = indenting
				p.indent = 0
				if _, err = p.output.Write(data[pos : n+1]); err != nil {
					return
				}
			}
		}
	}
	n = len(data)
	if pos < n && p.state == collecting {
		_, err = p.output.Write(data[pos:])
	}
	return
}

func readTextTemplate(name string) *template.Template {
	fileName := filepath.Join(*root, "tmpl", name)
	data, err := ioutil.ReadFile(fileName)
	if err != nil {
		log.Fatalf("ReadFile %s: %v", fileName, err)
	}
	t, err := template.New(name).Funcs(godocFmap).Parse(string(data))
	if err != nil {
		log.Fatalf("%s: %v", fileName, err)
	}
	return t
}

func applyTextTemplate(t *template.Template, name string, data interface{}) []byte {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		log.Printf("%s.Execute: %s", name, err)
	}
	return buf.Bytes()
}

func serveTextFile(w http.ResponseWriter, r *http.Request, abspath, relpath, title string) {
	src, err := ioutil.ReadFile(abspath)
	if err != nil {
		log.Printf("ReadFile: %s", err)
		serveError(w, r, relpath, err)
		return
	}

	var buf bytes.Buffer
	buf.WriteString("<pre>")
	FormatText(&buf, src, 1, pathpkg.Ext(abspath) == ".go", r.FormValue("h"), rangeSelection(r.FormValue("s")))
	buf.WriteString("</pre>")

	servePage(w, title, "", buf.Bytes())
}

type godocHandler struct{}

func (godocHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m := docRx.FindStringSubmatch(r.URL.Path)
	suffix := ""
	if m == nil {
		if r.URL.Path != pkgPattern && r.URL.Path != cmdPattern {
			http.NotFound(w, r)
			return
		}
		suffix = r.URL.Path
	} else {
		suffix = m[1]
	}
	diskPath := filepath.Join(*root, "..", suffix)

	switch pathpkg.Ext(suffix) {
	case ".go":
		serveTextFile(w, r, diskPath, suffix, "Source file")
		return
	}

	pkgName := pathpkg.Join(domainName, suffix)
	pi, err := getPageInfo(pkgName, diskPath)
	if err != nil {
		log.Print(err)
		return
	}

	subtitle := pathpkg.Base(diskPath)
	title := subtitle + " (" + pkgName + ")"
	servePage(w, title, subtitle, applyTextTemplate(packageHTML, "packageHTML", pi))
}
