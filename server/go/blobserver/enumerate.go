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
	"fmt"
	"http"
	"os"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var validPartitionName = regexp.MustCompile(`^[a-z0-9_]*$`)

const maxEnumerate = 100000

type blobInfo struct {
	*blobref.BlobRef
	*os.FileInfo
	os.Error
}

type readBlobRequest struct {
	ch     chan *blobInfo
	after  string
	remain *uint         // limit countdown
	dirRoot string

	// Not used on initial request, only on recursion
	blobPrefix, pathInto string
}

func readBlobs(opts readBlobRequest) {
	dirFullPath := opts.dirRoot + "/" + opts.pathInto
	dir, err := os.Open(dirFullPath, os.O_RDONLY, 0)
	if err != nil {
		log.Println("Error opening directory: ", err)
		opts.ch <- &blobInfo{Error: err}
		return
	}
	defer dir.Close()
	names, err := dir.Readdirnames(32768)
	if err != nil {
		log.Println("Error reading dirnames: ", err)
		opts.ch <- &blobInfo{Error: err}
		return
	}
	sort.SortStrings(names)
	for _, name := range names {
		if *opts.remain == 0 {
			opts.ch <- &blobInfo{Error: os.ENOSPC}
			return
		}

		fullPath := dirFullPath + "/" + name
		fi, err := os.Stat(fullPath)
		if err != nil {
			bi := &blobInfo{Error: err}
			opts.ch <- bi
			return
		}

		if fi.IsDirectory() {
			var newBlobPrefix string
			if opts.blobPrefix == "" {
				newBlobPrefix = name + "-"
			} else {
				newBlobPrefix = opts.blobPrefix + name
			}
			if len(opts.after) > 0 {
				compareLen := len(newBlobPrefix)
				if len(opts.after) < compareLen {
					compareLen = len(opts.after)
				}
				if newBlobPrefix[0:compareLen] < opts.after[0:compareLen] {
					continue
				}
			}
			ropts := opts
			ropts.blobPrefix = newBlobPrefix
			ropts.pathInto = opts.pathInto+"/"+name
			readBlobs(ropts)
			continue
		}

		if fi.IsRegular() && strings.HasSuffix(name, ".dat") {
			blobName := name[0 : len(name)-4]
			if blobName <= opts.after {
				continue
			}
			blobRef := blobref.Parse(blobName)
			if blobRef != nil {
				bi := &blobInfo{BlobRef: blobRef, FileInfo: fi}
				opts.ch <- bi
				(*opts.remain)--
			}
			continue
		}
	}

	if opts.pathInto == "" {
		opts.ch <- nil
	}
}

func handleEnumerateBlobs(conn http.ResponseWriter, req *http.Request) {

	limit, err := strconv.Atoui(req.FormValue("limit"))
	if err != nil || limit > maxEnumerate {
		limit = maxEnumerate
	}

	partition := req.FormValue("partition")
	if len(partition) > 50 || !validPartitionName.MatchString(partition) {
		conn.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(conn, "Invalid partition.")
		return
	}

	ch := make(chan *blobInfo, 100)
	conn.SetHeader("Content-Type", "text/javascript; charset=utf-8")
	fmt.Fprintf(conn, "{\n  \"blobs\": [\n")

	dirRoot := *flagStorageRoot
	if dirRoot != "" {
		dirRoot += "/partition/" + partition + "/"
	}
	go readBlobs(readBlobRequest{
	   ch: ch,
	   dirRoot: dirRoot,
	   after: req.FormValue("after"),
	   remain: &limit,
	})

	after := ""
	needsComma := false
	for bi := range ch {
		if bi == nil {
			after = ""
			break
		}
		if bi.Error != nil {
			break
		}
		blobName := bi.BlobRef.String()
		if needsComma {
			fmt.Fprintf(conn, ",\n")
		}
		fmt.Fprintf(conn, "    {\"blobRef\": \"%s\", \"size\": %d}",
			blobName, bi.FileInfo.Size)
		after = blobName
		needsComma = true
	}
	fmt.Fprintf(conn, "\n  ]")
	if after != "" {
		fmt.Fprintf(conn, ",\n  \"after\": \"%s\"", after)
	}
	fmt.Fprintf(conn, "\n}\n")
}
