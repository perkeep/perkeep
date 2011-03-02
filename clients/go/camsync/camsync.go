/*
Copyright 2011 Google Inc.

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
	"camli/blobref"
	"camli/client"
	"flag"
	"fmt"
//	"io"
	"log"
	"os"
)

// Things that can be uploaded.  (at most one of these)
var flagLoop = flag.Bool("loop", false, "sync in a loop once done")
var flagVerbose = flag.Bool("verbose", false, "be verbose")

var flagSrcHost = flag.String("srchost", "", "Source host")
var flagSrcPass = flag.String("srcpass", "", "Source password")
var flagDestHost = flag.String("desthost", "", "Destination host")
var flagDestPass = flag.String("destpass", "", "Destination password")

func usage(err string) {
	if err != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
	}
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	flag.Parse()

	if *flagSrcHost == "" {
		usage("No srchost specified.")
	}
	if *flagDestHost == "" {
		usage("No desthost specified.")
	}

	sc := client.New(*flagSrcHost, *flagSrcPass)
	dc := client.New(*flagDestHost, *flagDestPass)

	var logger *log.Logger = nil
	if *flagVerbose {
		logger = log.New(os.Stderr, "", 0)
	}
	sc.SetLogger(logger)
	dc.SetLogger(logger)

	ch := make(chan *blobref.SizedBlobRef, 100)
	enumErrCh := make(chan os.Error)
	go func() {
		enumErrCh <- sc.EnumerateBlobs(ch)
	}()

	for sb := range ch {
		fmt.Printf("Got blob: %s\n", sb)
	}

	if err := <-enumErrCh; err != nil {
		log.Fatalf("Enumerate error from source: %v", err)
	}
}
