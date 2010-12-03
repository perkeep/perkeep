package main

import (
	"camli/blobref"
	"fmt"
	"http"
	"os"
	"log"
	"sort"
	"strconv"
	"strings"
)

const maxEnumerate = 100000

type blobInfo struct {
	blobref.BlobRef
	*os.FileInfo
	os.Error
}

func readBlobs(ch chan *blobInfo, blobPrefix, diskRoot, after string, remain *uint) {
	dirFullPath := *flagStorageRoot + "/" + diskRoot
	dir, err := os.Open(dirFullPath, os.O_RDONLY, 0)
	if err != nil {
		log.Println("Error opening directory: ", err)
		ch <- &blobInfo{Error: err}
		return
	}
	defer dir.Close()
	names, err := dir.Readdirnames(32768)
	if err != nil {
		log.Println("Error reading dirnames: ", err)
		ch <- &blobInfo{Error: err}
		return
	}
	sort.SortStrings(names)
	for _, name := range names {
		if *remain == 0 {
			ch <- &blobInfo{Error: os.ENOSPC}
			return
		}

		fullPath := dirFullPath + "/" + name
		fi, err := os.Stat(fullPath)
		if err != nil {
			bi := &blobInfo{Error: err}
			ch <- bi
			return
		}

		if fi.IsDirectory() {
			var newBlobPrefix string
			if blobPrefix == "" {
				newBlobPrefix = name + "-"
			} else {
				newBlobPrefix = blobPrefix + name
			}
			if len(after) > 0 {
				compareLen := len(newBlobPrefix)
				if len(after) < compareLen {
					compareLen = len(after)
				}
				if newBlobPrefix[0:compareLen] < after[0:compareLen] {
					continue
				}
			}
			readBlobs(ch, newBlobPrefix, diskRoot + "/" + name, after, remain)
			continue
		}

		if fi.IsRegular() && strings.HasSuffix(name, ".dat") {
			blobName := name[0:len(name)-4]
			if blobName <= after {
				continue
			}
			blobRef := blobref.Parse(blobName)
			if blobRef != nil {
				bi := &blobInfo{BlobRef: blobRef, FileInfo: fi}
				ch <- bi
				(*remain)--
			}
			continue
		}
	}

	if diskRoot == "" {
		ch <- nil
	}
}

func handleEnumerateBlobs(conn http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	ch := make(chan *blobInfo, 100)

	limit, err := strconv.Atoui(req.FormValue("limit"))
	if err != nil || limit > maxEnumerate {
		limit = maxEnumerate
	}

	conn.SetHeader("Content-Type", "text/javascript; charset=utf-8")
	fmt.Fprintf(conn, "{\n  \"blobs\": [\n")

	var after string
	go readBlobs(ch, "", "", req.FormValue("after"), &limit);
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
		if (needsComma) {
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

