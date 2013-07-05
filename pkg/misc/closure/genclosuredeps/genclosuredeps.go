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

// The genclosuredeps command, similarly to the closure depswriter.py tool,
// outputs to os.Stdout for each .js file, which namespaces
// it provides, and the namespaces it requires, hence helping
// the closure library to resolve dependencies between those files.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"camlistore.org/pkg/misc/closure"
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: genclosuredeps <dir>\n")
	os.Exit(1)
}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) != 1 {
		usage()
	}
	b, err := closure.GenDeps(http.Dir(args[0]))
	if err != nil {
		log.Fatal(err)
	}
	io.Copy(os.Stdout, bytes.NewReader(b))
}
