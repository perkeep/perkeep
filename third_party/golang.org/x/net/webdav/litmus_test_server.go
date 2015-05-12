// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

/*
This program is a server for the WebDAV 'litmus' compliance test at
http://www.webdav.org/neon/litmus/
To run the test:

go run litmus_test_server.go

and separately, from the downloaded litmus-xxx directory:

make URL=http://localhost:9999/ check
*/
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"

	"camlistore.org/third_party/golang.org/x/net/webdav"
)

var port = flag.Int("port", 9999, "server port")

func main() {
	flag.Parse()
	log.SetFlags(0)
	fs := webdav.NewMemFS()
	ls := webdav.NewMemLS()
	http.Handle("/", &webdav.Handler{
		FileSystem: fs,
		LockSystem: ls,
		PropSystem: webdav.NewMemPS(fs, ls, webdav.ReadWrite),
		Logger: func(r *http.Request, err error) {
			litmus := r.Header.Get("X-Litmus")
			if len(litmus) > 19 {
				litmus = litmus[:16] + "..."
			}

			switch r.Method {
			case "COPY", "MOVE":
				dst := ""
				if u, err := url.Parse(r.Header.Get("Destination")); err == nil {
					dst = u.Path
				}
				o := r.Header.Get("Overwrite")
				log.Printf("%-20s%-10s%-30s%-30so=%-2s%v", litmus, r.Method, r.URL.Path, dst, o, err)
			default:
				log.Printf("%-20s%-10s%-30s%v", litmus, r.Method, r.URL.Path, err)
			}
		},
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Serving %v", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
