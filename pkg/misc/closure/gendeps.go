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

// GenDeps returns the namespace dependencies between the
// closure javascript files in root. It does not descend
// in directories.
// The format for each relevant javascript file is:
// goog.addDependency("filepath", ["namespace provided"], ["required namespace 1", "required namespace 2", ...]);
func GenDeps(root http.FileSystem) ([]byte, error) {
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
			fmt.Fprintf(&buf, "goog.addDependency(%q, %v, %v);\n", "../../"+name, jsList(prov), jsList(req))
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

	br := bufio.NewReader(f)
	for {
		l, err := br.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
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
