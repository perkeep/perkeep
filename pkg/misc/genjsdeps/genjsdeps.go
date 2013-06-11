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

// The genjsdeps command, similarly to the closure depswriter.py tool,
// outputs to os.Stdout for each .js file, which namespaces
// it provides, and the namespaces it requires, hence allowing
// the closure library to resolve dependencies between those files.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// TODO(mpl): make a library and a separate command which uses
// that library.
// http://camlistore.org/issue/142

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: genjsdeps <dir>\n")
	os.Exit(1)
}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) != 1 {
		usage()
	}
	dir := path.Clean(args[0])
	fi, err := os.Stat(dir)
	if err != nil {
		log.Fatal(err)
	}
	if !fi.IsDir() {
		log.Fatalf("%v not a dir", dir)
	}
	var buf bytes.Buffer
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !strings.HasSuffix(path, ".js") {
			return nil
		}
		suffix := path[len(dir)+1:]
		prov, req, err := parseProvidesRequires(info, path)
		if err != nil {
			return err
		}
		if len(prov) > 0 {
			fmt.Fprintf(&buf, "goog.addDependency(%q, %v, %v);\n", "../../"+suffix, jsList(prov), jsList(req))
		}
		return nil
	})
	if err != nil {
		log.Fatalf("Error walking %d generating deps.js: %v", dir, err)
	}
	io.Copy(os.Stdout, &buf)
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

func parseProvidesRequires(fi os.FileInfo, path string) (provides, requires []string, err error) {
	mt := fi.ModTime()
	depCacheMu.Lock()
	defer depCacheMu.Unlock()
	if ci := depCache[path]; ci.modTime.Equal(mt) {
		return ci.provides, ci.requires, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
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
