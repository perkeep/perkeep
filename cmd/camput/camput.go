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
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"camlistore.org/pkg/blobserver/dir"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/httputil"

	"go4.org/syncutil"
)

const buffered = 16 // arbitrary

var (
	flagProxyLocal = false
	flagHTTP       = flag.Bool("verbose_http", false, "show HTTP request summaries")
	flagHaveCache  = true
	flagBlobDir    = flag.String("blobdir", "", "If non-empty, the local directory to put blobs, instead of sending them over the network. If the string \"discard\", no blobs are written or sent over the network anywhere.")
)

var (
	uploaderOnce sync.Once
	uploader     *Uploader // initialized by getUploader
)

var debugFlagOnce sync.Once

func registerDebugFlags() {
	flag.BoolVar(&flagProxyLocal, "proxy_local", false, "If true, the HTTP_PROXY environment is also used for localhost requests. This can be helpful during debugging.")
	flag.BoolVar(&flagHaveCache, "havecache", true, "Use the 'have cache', a cache keeping track of what blobs the remote server should already have from previous uploads.")
}

func init() {
	if debug, _ := strconv.ParseBool(os.Getenv("CAMLI_DEBUG")); debug {
		debugFlagOnce.Do(registerDebugFlags)
	}
	cmdmain.ExtraFlagRegistration = client.AddFlags
	cmdmain.PreExit = func() {
		if up := uploader; up != nil {
			up.Close()
			stats := up.Stats()
			if *cmdmain.FlagVerbose {
				log.Printf("Client stats: %s", stats.String())
				if up.stats != nil {
					log.Printf("  #HTTP reqs: %d", up.stats.Requests())
					h1, h2 := up.stats.ProtoVersions()
					log.Printf("   responses: %d (h1), %d (h2)\n", h1, h2)
				}
			}
		}

		// So multiple cmd/camput TestFoo funcs run, each with
		// an fresh (and not previously closed) Uploader:
		uploader = nil
		uploaderOnce = sync.Once{}
	}
}

func getUploader() *Uploader {
	uploaderOnce.Do(initUploader)
	return uploader
}

func initUploader() {
	up := newUploader()
	if flagHaveCache && *flagBlobDir == "" {
		gen, err := up.StorageGeneration()
		if err != nil {
			log.Printf("WARNING: not using local server inventory cache; failed to retrieve server's storage generation: %v", err)
		} else {
			up.haveCache = NewKvHaveCache(gen)
			up.Client.SetHaveCache(up.haveCache)
		}
	}
	uploader = up
}

func handleResult(what string, pr *client.PutResult, err error) error {
	if err != nil {
		cmdmain.Errorf("Error putting %s: %s\n", what, err)
		cmdmain.ExitWithFailure = true
		return err
	}
	fmt.Fprintln(cmdmain.Stdout, pr.BlobRef.String())
	return nil
}

func getenvEitherCase(k string) string {
	if v := os.Getenv(strings.ToUpper(k)); v != "" {
		return v
	}
	return os.Getenv(strings.ToLower(k))
}

// proxyFromEnvironment is similar to http.ProxyFromEnvironment but it skips
// $NO_PROXY blacklist so it proxies every requests, including localhost
// requests.
func proxyFromEnvironment(req *http.Request) (*url.URL, error) {
	proxy := getenvEitherCase("HTTP_PROXY")
	if proxy == "" {
		return nil, nil
	}
	proxyURL, err := url.Parse(proxy)
	if err != nil || proxyURL.Scheme == "" {
		if u, err := url.Parse("http://" + proxy); err == nil {
			proxyURL = u
			err = nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("invalid proxy address %q: %v", proxy, err)
	}
	return proxyURL, nil
}

func newUploader() *Uploader {
	var cc *client.Client
	var httpStats *httputil.StatsTransport
	if d := *flagBlobDir; d != "" {
		ss, err := dir.New(d)
		if err != nil && d == "discard" {
			ss = discardStorage{}
			err = nil
		}
		if err != nil {
			log.Fatalf("Error using dir %s as storage: %v", d, err)
		}
		cc = client.NewStorageClient(ss)
	} else {
		var proxy func(*http.Request) (*url.URL, error)
		if flagProxyLocal {
			proxy = proxyFromEnvironment
		}
		cc = client.NewOrFail(client.OptionTransportConfig(&client.TransportConfig{
			Proxy:   proxy,
			Verbose: *flagHTTP,
		}))
		httpStats = cc.HTTPStats()
	}
	if *cmdmain.FlagVerbose {
		cc.SetLogger(log.New(cmdmain.Stderr, "", log.LstdFlags))
	} else {
		cc.SetLogger(nil)
	}

	pwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("os.Getwd: %v", err)
	}

	return &Uploader{
		Client: cc,
		stats:  httpStats,
		pwd:    pwd,
		fdGate: syncutil.NewGate(100), // gate things that waste fds, assuming a low system limit
	}
}

func main() {
	cmdmain.Main()
}
