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

// Package closure provides tools to help with the use of the
// closure library.
//
// See https://code.google.com/p/closure-library/
package closure

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// GenDeps returns the namespace dependencies between the closure javascript files in root. It does not descend in directories.
// Each of the files listed in the output is prepended with the path "../../", which is assumed to be the location where these files can be found, relative to Closure's base.js.
//
// The format for each relevant javascript file is:
// goog.addDependency("filepath", ["namespace provided"], ["required namespace 1", "required namespace 2", ...]);
func GenDeps(root http.FileSystem) ([]byte, error) {
	// In the typical configuration, Closure is served at 'closure/goog/...''
	return GenDepsWithPath("../../", root)
}

// GenDepsWithPath is like GenDeps, but you can specify a path where the files are to be found at runtime relative to Closure's base.js.
func GenDepsWithPath(pathPrefix string, root http.FileSystem) ([]byte, error) {
	d, err := root.Open("/")
	if err != nil {
		return nil, fmt.Errorf("Failed to open root of %v: %v", root, err)
	}
	fi, err := d.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("root of %v is not a dir", root)
	}
	ent, err := d.Readdir(-1)
	if err != nil {
		return nil, fmt.Errorf("Could not read dir entries of root: %v", err)
	}
	var buf bytes.Buffer
	for _, info := range ent {
		name := info.Name()
		if !strings.HasSuffix(name, ".js") {
			continue
		}
		if strings.HasPrefix(name, ".#") {
			// Emacs noise.
			continue
		}
		f, err := root.Open(name)
		if err != nil {
			return nil, fmt.Errorf("Could not open %v: %v", name, err)
		}
		prov, req, err := parseProvidesRequires(info, name, f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("Could not parse deps for %v: %v", name, err)
		}
		if len(prov) > 0 {
			fmt.Fprintf(&buf, "goog.addDependency(%q, %v, %v);\n", pathPrefix+name, jsList(prov), jsList(req))
		}
	}
	return buf.Bytes(), nil
}

var provReqRx = regexp.MustCompile(`^goog\.(provide|require)\(['"]([\w\.]+)['"]\)`)

type depCacheItem struct {
	modTime            time.Time
	provides, requires []string
}

var (
	depCacheMu sync.Mutex
	depCache   = map[string]depCacheItem{}
)

func parseProvidesRequires(fi os.FileInfo, path string, f io.Reader) (provides, requires []string, err error) {
	mt := fi.ModTime()
	depCacheMu.Lock()
	defer depCacheMu.Unlock()
	if ci := depCache[path]; ci.modTime.Equal(mt) {
		return ci.provides, ci.requires, nil
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		l := scanner.Text()
		if !strings.HasPrefix(l, "goog.") {
			continue
		}
		m := provReqRx.FindStringSubmatch(l)
		if m != nil {
			if m[1] == "provide" {
				provides = append(provides, m[2])
			} else {
				requires = append(requires, m[2])
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	depCache[path] = depCacheItem{provides: provides, requires: requires, modTime: mt}
	return provides, requires, nil
}

// jsList prints a list of strings as JavaScript list.
type jsList []string

func (s jsList) String() string {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, v := range s {
		if i > 0 {
			buf.WriteString(", ")
		}
		fmt.Fprintf(&buf, "%q", v)
	}
	buf.WriteByte(']')
	return buf.String()
}

// Example of a match:
// goog.addDependency('asserts/asserts.js', ['goog.asserts', 'goog.asserts.AssertionError'], ['goog.debug.Error', 'goog.string']);
// So with m := depsRx.FindStringSubmatch,
// the provider: m[1] == "asserts/asserts.js"
// the provided namespaces: m[2] == "'goog.asserts', 'goog.asserts.AssertionError'"
// the required namespaces: m[5] == "'goog.debug.Error', 'goog.string'"
var depsRx = regexp.MustCompile(`^goog.addDependency\(['"]([^/]+[a-zA-Z0-9\-\_/\.]*\.js)['"], \[((['"][\w\.]+['"])+(, ['"][\w\.]+['"])*)\], \[((['"][\w\.]+['"])+(, ['"][\w\.]+['"])*)?\]\);`)

// ParseDeps reads closure namespace dependency lines and
// returns a map giving the js file provider for each namespace,
// and a map giving the namespace dependencies for each namespace.
func ParseDeps(r io.Reader) (providedBy map[string]string, requires map[string][]string, err error) {
	providedBy = make(map[string]string)
	requires = make(map[string][]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		l := scanner.Text()
		if strings.HasPrefix(l, "//") {
			continue
		}
		if l == "" {
			continue
		}
		m := depsRx.FindStringSubmatch(l)
		if m == nil {
			return nil, nil, fmt.Errorf("Invalid line in deps: %q", l)
		}
		jsfile := m[1]
		provides := strings.Split(m[2], ", ")
		var required []string
		if m[5] != "" {
			required = strings.Split(
				strings.Replace(strings.Replace(m[5], "'", "", -1), `"`, "", -1), ", ")
		}
		for _, v := range provides {
			namespace := strings.Trim(v, `'"`)
			if otherjs, ok := providedBy[namespace]; ok {
				return nil, nil, fmt.Errorf("Name %v is provided by both %v and %v", namespace, jsfile, otherjs)
			}
			providedBy[namespace] = jsfile
			if _, ok := requires[namespace]; ok {
				return nil, nil, fmt.Errorf("Name %v has two sets of dependencies", namespace)
			}
			if required != nil {
				requires[namespace] = required
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return providedBy, requires, nil
}

// DeepParseDeps reads closure namespace dependency lines and
// returns a map giving all the required js files for each namespace.
func DeepParseDeps(r io.Reader) (map[string][]string, error) {
	providedBy, requires, err := ParseDeps(r)
	if err != nil {
		return nil, err
	}
	filesDeps := make(map[string][]string)
	var deeperDeps func(namespace string) []string
	deeperDeps = func(namespace string) []string {
		if jsdeps, ok := filesDeps[namespace]; ok {
			return jsdeps
		}
		jsfiles := []string{providedBy[namespace]}
		for _, dep := range requires[namespace] {
			jsfiles = append(jsfiles, deeperDeps(dep)...)
		}
		return jsfiles
	}
	for namespace, _ := range providedBy {
		filesDeps[namespace] = deeperDeps(namespace)
	}
	return filesDeps, nil
}
