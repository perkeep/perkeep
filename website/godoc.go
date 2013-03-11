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
	"runtime"
	"strings"
	"text/template"
	"time"
)

const domainName = "camlistore.org"

var docRx = regexp.MustCompile(`^/((?:pkg|cmd)/([\w/]+?)(\.go)??)/?$`)

var tabwidth = 4

type docServer struct {
	pattern string // url pattern; e.g. "/pkg/"
	fsRoot  string // file system root to which the pattern is mapped
}

var (
	cmdHandler = docServer{"/cmd/", "/cmd"}
	pkgHandler = docServer{"/pkg/", "/pkg"}
)

// DirEntry describes a directory entry. The Depth and Height values
// are useful for presenting an entry in an indented fashion.
//
type DirEntry struct {
	Depth    int    // >= 0
	Height   int    // = DirList.MaxHeight - Depth, > 0
	Path     string // directory path; includes Name, relative to DirList root
	Name     string // directory name
	HasPkg   bool   // true if the directory contains at least one package
	Synopsis string // package documentation, if any
}

type DirList struct {
	MaxHeight int // directory tree height, > 0
	List      []DirEntry
}

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
	"pkgLink":     pkgLinkFunc,
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

func pkgLinkFunc(path string) string {
	relpath := path[1:]
	// because of the irregular mapping under goroot
	// we need to correct certain relative paths
	relpath = strings.TrimLeft(relpath, "pkg/")
	return pkgHandler.pattern[1:] + relpath // remove trailing '/' for relative URL
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

var packageHTML = readGodocTemplate("package.html")

func readGodocTemplate(name string) *template.Template {
	path := runtime.GOROOT() + "/lib/godoc/" + name

	// use underlying file system fs to read the template file
	// (cannot use template ParseFile functions directly)
	data, err := ioutil.ReadFile(path)
	if err != nil {
		log.Fatal("readTemplate: ", err)
	}
	// be explicit with errors (for app engine use)
	t, err := template.New(name).Funcs(godocFmap).Parse(string(data))
	if err != nil {
		log.Fatal("readTemplate: ", err)
	}
	return t
}

func getPageInfo(pkgName, diskPath string) (pi PageInfo, err error) {
	bpkg, err := build.ImportDir(diskPath, 0)
	if err != nil {
		return
	}
	inSet := make(map[string]bool)
	for _, name := range bpkg.GoFiles {
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
	pi.IsPkg = !strings.Contains(pkgName, domainName+"/cmd/")
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
	if m == nil {
		http.NotFound(w, r)
		return
	}
	suffix := m[1]
	diskPath := filepath.Join(*root, "..", suffix)

	switch pathpkg.Ext(suffix) {
	case ".go":
		serveTextFile(w, r, diskPath, suffix, "Source file")
		return
	}

	pkgName := domainName + "/" + suffix
	pi, err := getPageInfo(pkgName, diskPath)
	if err != nil {
		log.Print(err)
		return
	}

	title := pathpkg.Base(diskPath) + " (" + pkgName + ")"
	servePage(w, title, "", applyTextTemplate(packageHTML, "packageHTML", pi))
}
