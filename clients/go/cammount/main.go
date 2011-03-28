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
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"

	"camli/blobref"
	"camli/blobserver/localdisk" // used for the blob cache
	"camli/client"
	"camli/third_party/github.com/hanwen/go-fuse/fuse"
)

func PrintMap(m map[string]float64)  {
	keys := make([]string, len(m))
	for k, _ := range m {
		keys = append(keys, k)
	}

	sort.SortStrings(keys)
	for _, k := range keys {
		if m[k] > 0 {
			fmt.Println(k, m[k])
		}
	}
}

func main() {
	// Scans the arg list and sets up flags
	debug := flag.Bool("debug", false, "print debugging messages.")
	threaded := flag.Bool("threaded", true, "switch off threading; print debugging messages.")
	flag.Parse()

	errorf := func(msg string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, msg, args...)
		os.Exit(2)
	}

	if flag.NArg() < 2 {
		errorf("usage: cammount <blobref> <mountpoint>\n")
	}

	root := blobref.Parse(flag.Arg(0))
	if root == nil {
		errorf("Error parsing root blobref: %q\n", root)
	}
	client := client.NewOrFail() // automatic from flags

	cacheDir, err := ioutil.TempDir("", "camlicache")
	if err != nil {
		errorf("Error creating temp cache directory: %v\n", err)
	}
	defer os.RemoveAll(cacheDir)
	diskcache, err := localdisk.New(cacheDir)
	if err != nil {
		errorf("Error setting up local disk cache: %v", err)
	}
	fetcher := NewCachingFetcher(diskcache, client)

	fs := NewCamliFileSystem(fetcher, root)
	timing := fuse.NewTimingPathFilesystem(fs)

	conn := fuse.NewPathFileSystemConnector(timing)
	rawTiming := fuse.NewTimingRawFilesystem(conn)

	state := fuse.NewMountState(rawTiming)
	state.Debug = *debug

	mountPoint := flag.Arg(1)
	err = state.Mount(mountPoint)
	if err != nil {
		fmt.Printf("MountFuse fail: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Mounted %s on %s (threaded=%v, debug=%v)\n", root.String(), mountPoint, *threaded, *debug)
	state.Loop(*threaded)
	fmt.Println("Finished", state.Stats())

	counts := state.OperationCounts()
	fmt.Println("Counts: ", counts)

	latency := state.Latencies()
	fmt.Println("MountState latency (ms):")
	PrintMap(latency)

	latency = timing.Latencies()
	fmt.Println("Path ops (ms):", latency)

	latency = rawTiming.Latencies()
	fmt.Println("Raw FS (ms):", latency)
}
