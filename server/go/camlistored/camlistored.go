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
	"http"
	"json"
	"log"
	"path/filepath"
	"strings"
	"os"

	"camli/auth"
	"camli/blobref"
	"camli/blobserver"
	"camli/blobserver/handlers"
	"camli/errorutil"
	"camli/httputil"
	"camli/jsonconfig"
	"camli/osutil"
	"camli/search"
	"camli/webserver"

	// Storage options:
	_ "camli/blobserver/localdisk"
	_ "camli/blobserver/s3"
	_ "camli/mysqlindexer" // indexer, but uses storage interface
)

var flagConfigFile = flag.String("configfile", "serverconfig",
	"Config file to use, relative to camli config dir root, or blank to not use config files.")

const camliPrefix = "/camli/"

var ErrCamliPath = os.NewError("Invalid Camlistore request path")

func parseCamliPath(path string) (action string, err os.Error) {
	camIdx := strings.Index(path, camliPrefix)
	if camIdx == -1 {
		return "", ErrCamliPath
	}
	action = path[camIdx+len(camliPrefix):]
	return
}

func unsupportedHandler(conn http.ResponseWriter, req *http.Request) {
	httputil.BadRequestError(conn, "Unsupported camlistore path or method.")
}

// where prefix is like "/" or "/s3/" for e.g. "/camli/" or "/s3/camli/*"
func makeCamliHandler(prefix, baseURL string, storage blobserver.Storage) http.Handler {
	return http.HandlerFunc(func(conn http.ResponseWriter, req *http.Request) {
		action, err := parseCamliPath(req.URL.Path[len(prefix)-1:])
		if err != nil {
			log.Printf("Invalid request for method %q, path %q",
				req.Method, req.URL.Path)
			unsupportedHandler(conn, req)
			return
		}
		// TODO: actually deal with partitions here
		part := &partitionConfig{"", true, true, false, nil, baseURL + prefix[:len(prefix)-1]}
		handleCamliUsingStorage(conn, req, action, part, storage)
	})
}

func handleCamliUsingStorage(conn http.ResponseWriter, req *http.Request, action string, partition blobserver.Partition, storage blobserver.Storage) {
	handler := unsupportedHandler
	switch req.Method {
	case "GET":
		switch action {
		case "enumerate-blobs":
			handler = auth.RequireAuth(handlers.CreateEnumerateHandler(storage))
		case "stat":
			handler = auth.RequireAuth(handlers.CreateStatHandler(storage))
		default:
			handler = handlers.CreateGetHandler(storage)
		}
	case "POST":
		switch action {
		case "stat":
			handler = auth.RequireAuth(handlers.CreateStatHandler(storage))
		case "upload":
			handler = auth.RequireAuth(handlers.CreateUploadHandler(storage))
		case "remove":
			handler = auth.RequireAuth(handlers.CreateRemoveHandler(storage))
		}
	case "PUT": // no longer part of spec
		handler = auth.RequireAuth(handlers.CreateNonStandardPutHandler(storage))
	}
	handler(conn, req)
}

func exitFailure(pattern string, args ...interface{}) {
	if !strings.HasSuffix(pattern, "\n") {
		pattern = pattern + "\n"
	}
	fmt.Fprintf(os.Stderr, pattern, args...)
	os.Exit(1)
}

func main() {
	flag.Parse()

	configPath := *flagConfigFile
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(osutil.CamliConfigDir(), configPath)
	}
	f, err := os.Open(configPath)
	if err != nil {
		exitFailure("error opening %s: %v", configPath, err)
	}
	defer f.Close()
	dj := json.NewDecoder(f)
	rootjson := make(map[string]interface{})
	if err = dj.Decode(&rootjson); err != nil {
		extra := ""
		if serr, ok := err.(*json.SyntaxError); ok {
			if _, serr := f.Seek(0, os.SEEK_SET); serr != nil {
				log.Fatalf("seek error: %v", serr)
			}
			line, col, highlight := errorutil.HighlightBytePosition(f, serr.Offset)
			extra = fmt.Sprintf(":\nError at line %d, column %d (file offset %d):\n%s",
				line, col, serr.Offset, highlight)
		}
		exitFailure("error parsing JSON object in config file %s%s\n%v",
			osutil.UserServerConfigPath(), extra, err)
	}
	if err := jsonconfig.EvaluateExpressions(rootjson); err != nil {
		exitFailure("error expanding JSON config expressions in %s: %v", configPath, err)
	}

	ws := webserver.New()
	baseURL := ws.BaseURL()

	// Root configuration
	config := jsonconfig.Obj(rootjson)

	{
		cert, key := config.OptionalString("TLSCertFile", ""), config.OptionalString("TLSKeyFile", "")
		if (cert != "") != (key != "") {
			exitFailure("TLSCertFile and TLSKeyFile must both be either present or absent")
		}
		if cert != "" {
			ws.SetTLS(cert, key)
		}
	}

	auth.AccessPassword = config.OptionalString("password", "")
	if url := config.OptionalString("baseURL", ""); url != "" {
		baseURL = url
	}
	prefixes := config.RequiredObject("prefixes")
	if err := config.Validate(); err != nil {
		exitFailure("configuration error in root object's keys in %s: %v", configPath, err)
	}
	createdHandlers := make(map[string]interface{})

	for prefix, vei := range prefixes {
		if !strings.HasPrefix(prefix, "/") {
			exitFailure("prefix %q doesn't start with /", prefix)
		}
		if !strings.HasSuffix(prefix, "/") {
			exitFailure("prefix %q doesn't end with /", prefix)
		}
		pconf, ok := vei.(map[string]interface{})
		if !ok {
			exitFailure("prefix %q value isn't an object", prefix)
		}
		handlerType, ok := pconf["handler"].(string)
		if !ok {
			exitFailure("in prefix %q, expected the \"handler\" parameter to be a string, got %T",
				prefix, pconf["handler"])
		}
		handlerArgs, ok := pconf["handlerArgs"].(map[string]interface{})
		if !ok {
			if _, present := pconf["handlerArgs"]; !present {
				handlerArgs = make(jsonconfig.Obj)
			} else {
				exitFailure("in prefix %q, expected the \"handlerArgs\" to be a JSON object",
					prefix)
			}
		}
		installHandler := func(creator func(conf jsonconfig.Obj) (h http.Handler, err os.Error)) {
			h, err := creator(jsonconfig.Obj(handlerArgs))
			if err != nil {
				exitFailure("error instantiating handler for prefix %s: %v",
					prefix, err)
			}
			createdHandlers[prefix] = h
			ws.Handle(prefix, &httputil.PrefixHandler{prefix, h})
		}
		switch {
		case handlerType == "search":
			// Skip it this round. Get it in second pass
			// to ensure the search's dependent indexer
			// has been created.
		case handlerType == "sync":
			// Skip it this round. Get it in second pass
			// to ensure the dependent blob servers have
			// been created.
		case handlerType == "root":
			installHandler(createRootHandler)
		case handlerType == "ui":
			installHandler(createUIHandler)
		case handlerType == "jsonsign":
			installHandler(createJSONSignHandler)
		default:
			// Assume a storage interface
			pstorage, err := blobserver.CreateStorage(handlerType, jsonconfig.Obj(handlerArgs))
			if err != nil {
				exitFailure("error instantiating storage for prefix %q, type %q: %v",
					prefix, handlerType, err)
			}
			createdHandlers[prefix] = pstorage
			ws.Handle(prefix+"camli/", makeCamliHandler(prefix, baseURL, pstorage))
		}
	}

	// Another pass for search handler(s)
	for prefix, vei := range prefixes {
		if _, alreadyCreated := createdHandlers[prefix]; alreadyCreated {
			continue
		}
		pconf := vei.(map[string]interface{})
		handlerType := pconf["handler"].(string)
		config := jsonconfig.Obj(pconf["handlerArgs"].(map[string]interface{}))
		checkConfig := func() {
			if err := config.Validate(); err != nil {
				exitFailure("configuration error in \"handlerArgs\" for prefix %s: %v", err)
			}
		}
		switch handlerType {
		case "search":
			indexPrefix := config.RequiredString("index") // TODO: add optional help tips here?
			ownerBlobStr := config.RequiredString("owner")
			checkConfig()
			indexer, ok := createdHandlers[indexPrefix].(search.Index)
			if !ok {
				exitFailure("prefix %q references invalid indexer %q", prefix, indexPrefix)
			}
			ownerBlobRef := blobref.Parse(ownerBlobStr)
			if ownerBlobRef == nil {
				exitFailure("prefix %q references has malformed blobref %q; expecting e.g. sha1-xxxxxxxxxxxx",
					prefix, ownerBlobStr)
			}
			h := auth.RequireAuth(search.CreateHandler(indexer, ownerBlobRef))
			ws.HandleFunc(prefix+"camli/", h)
		case "sync":
			from := config.RequiredString("from")
			to := config.RequiredString("to")
			checkConfig()
			getBlobServer := func(bsPrefix string) blobserver.Storage {
				h, ok := createdHandlers[bsPrefix]
				if !ok {
					exitFailure("sync prefix %q references non-existent %q blob storage prefix",
						prefix, bsPrefix)
				}
				bs, ok := h.(blobserver.Storage)
				if !ok {
                                        exitFailure("sync prefix %q references %q, of type %T, but expected a blob server",
                                                prefix, bsPrefix, h)
                                }
				return bs
			}
			fromBs, toBs := getBlobServer(from), getBlobServer(to)
			h, err := createSyncHandler(from, to, fromBs, toBs)
			if err != nil {
				exitFailure(err.String())
			}
			ws.Handle(prefix, h)
		default:
			panic("unexpected handlerType: " + handlerType)
		}

	}

	ws.Serve()
}
