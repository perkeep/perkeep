/*
Copyright 2021 The Perkeep Authors.

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

// The Static App application serves static web resources stored in a Perkeep camliRoot directory.
package main // import "perkeep.org/app/staticapp"

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"perkeep.org/pkg/app"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/localdisk"
	"perkeep.org/pkg/buildinfo"
	"perkeep.org/pkg/cacher"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/webserver"
	"runtime"
	"strings"
	"time"
)

var (
	flagVersion = flag.Bool("version", false, "show version")
)

type config struct {
	StaticRoot string `json:"staticRoot"`
	CacheRoot  string `json:"cacheRoot,omitempty"`  // Root path for the caching blobserver. No caching if empty.
	SearchRoot string `json:"searchRoot,omitempty"` // Root path for initializing app search with a master query. No search if empty.
}

func appConfig() *config {
	configURL := os.Getenv("CAMLI_APP_CONFIG_URL")
	if configURL == "" {
		log.Fatalf("Static App application needs a CAMLI_APP_CONFIG_URL env var")
	}
	cl, err := app.Client()
	if err != nil {
		log.Fatalf("could not get a client to fetch extra config: %v", err)
	}
	conf := &config{}
	if err := cl.GetJSON(context.TODO(), configURL, conf); err != nil {
		log.Fatalf("could not get app config at %v: %v", configURL, err)
	}
	return conf
}

type client interface {
	search.QueryDescriber
	GetJSON(ctx context.Context, url string, data interface{}) error
	blob.Fetcher
}

type staticAppHandler struct {
	rootDirRef string
	cl         client
	fetcher    blob.Fetcher
}

func (sa *staticAppHandler) treeConstraint(filePath string) *search.FileConstraint {
	filePath = strings.TrimPrefix(filePath, "/")
	tree := strings.Split(filePath, "/")

	fileName, tree := tree[len(tree)-1], tree[:len(tree)-1]
	fc := &search.FileConstraint{FileName: &search.StringConstraint{Equals: fileName}}

	if len(tree) != 0 {
		dirName, parts := tree[len(tree)-1], tree[:len(tree)-1]
		fc.ParentDir = &search.DirConstraint{FileName: &search.StringConstraint{Equals: dirName}}

		var pn string
		var ancestor = fc.ParentDir
		last := len(parts) - 1
		for pi := range parts {
			pn = parts[last-pi]
			ancestor.ParentDir = &search.DirConstraint{FileName: &search.StringConstraint{Equals: pn}}
			ancestor = ancestor.ParentDir
		}
		ancestor.ParentDir = &search.DirConstraint{BlobRefPrefix: sa.rootDirRef}
	} else {
		fc.ParentDir = &search.DirConstraint{BlobRefPrefix: sa.rootDirRef}
	}

	return fc
}

func (sa *staticAppHandler) initStaticRoot(rootName string) (string, error) {
	result, err := sa.cl.Query(context.TODO(), &search.SearchQuery{
		Limit: 1,
		Constraint: &search.Constraint{
			Logical: &search.LogicalConstraint{
				A: &search.Constraint{
					Permanode: &search.PermanodeConstraint{
						Attr:       "camliRoot",
						Value:      rootName,
						SkipHidden: true,
					},
				},
				B: &search.Constraint{
					Permanode: &search.PermanodeConstraint{
						Attr: "camliContent",
						ValueInSet: &search.Constraint{
							CamliType: "directory",
						},
						SkipHidden: true,
					},
				},
				Op: "and",
			},
		},
		Describe: &search.DescribeRequest{},
	})
	if err != nil {
		return "", err
	}
	if len(result.Blobs) == 0 || !result.Blobs[0].Blob.Valid() {
		return "", os.ErrNotExist
	}
	found := result.Blobs[0].Blob.String()
	root := result.Describe.Meta[found]
	return root.Permanode.Attr.Get("camliContent"), nil
}

func (sa *staticAppHandler) getFile(filePath string) (*search.DescribedBlob, error) {
	fileTree := sa.treeConstraint(filePath)
	result, err := sa.cl.Query(context.TODO(), &search.SearchQuery{
		Limit:      1,
		Constraint: &search.Constraint{File: fileTree},
		Describe:   &search.DescribeRequest{},
	})
	if err != nil {
		return &search.DescribedBlob{}, err
	}
	if len(result.Blobs) == 0 || !result.Blobs[0].Blob.Valid() {
		return &search.DescribedBlob{}, os.ErrNotExist
	}
	found := result.Blobs[0].Blob.String()
	return result.Describe.Meta[found], nil
}

func (sa *staticAppHandler) setMasterQuery(searchRoot string) error {
	masterQueryURL := os.Getenv("CAMLI_APP_MASTERQUERY_URL")
	if masterQueryURL == "" {
		return fmt.Errorf("missing CAMLI_APP_MASTERQUERY_URL env var")
	}
	query := &search.SearchQuery{
		Sort:  search.CreatedDesc,
		Limit: -1,
		Constraint: &search.Constraint{
			Permanode: &search.PermanodeConstraint{
				Relation: &search.RelationConstraint{
					Relation: "parent",
					Any: &search.Constraint{
						Permanode: &search.PermanodeConstraint{
							Attr:  "camliRoot",
							Value: searchRoot,
						},
					},
				},
			},
		},
		Describe: &search.DescribeRequest{
			Depth: 1,
			Rules: []*search.DescribeRule{
				{Attrs: []string{"camliContent", "camliContentImage", "camliMember", "camliPath:*"}},
			},
		},
	}
	data, err := json.Marshal(query)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", masterQueryURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	am, err := app.Auth()
	if err != nil {
		return err
	}
	am.AddAuthHeader(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if string(body) != "OK" {
		return fmt.Errorf("error setting master query on app handler: %v", string(body))
	}
	return nil
}

func (sa *staticAppHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqPath := app.PathPrefix(r)
	filePath := strings.TrimPrefix(r.URL.Path, reqPath)
	if filePath == "" {
		filePath = "index.html"
	}

	file, err := sa.getFile(filePath)
	if err != nil {
		log.Printf("file blob not found with path %s - %s", filePath, err)
		http.NotFound(w, r)
	} else {
		fr, err := schema.NewFileReader(r.Context(), sa.fetcher, file.BlobRef)
		if err != nil {
			log.Printf("%s", err)
			http.Error(w, fmt.Sprintf("server error while fetching %s at %s", filePath, file.BlobRef), 500)
		} else {
			http.ServeContent(w, r, file.File.FileName, time.Now(), fr)
		}
	}
}

func newStaticAppHandler(conf *config) *staticAppHandler {
	cl, err := app.Client()
	if err != nil {
		log.Fatalf("could not get a client for the staticapp app handler %v", err)
	}
	var fetcher blob.Fetcher
	if conf.CacheRoot != "" {
		cache, err := localdisk.New(conf.CacheRoot)
		if err != nil {
			log.Fatalf("Could not create localdisk fetcher: %v", err)
		}
		fetcher = cacher.NewCachingFetcher(cache, cl)
	} else {
		fetcher = cl
	}
	sa := &staticAppHandler{
		cl:      cl,
		fetcher: fetcher,
	}
	staticRoot, err := sa.initStaticRoot(conf.StaticRoot)
	if err != nil {
		log.Fatalf("failed to initialize static content directory: %v", err)
	}
	sa.rootDirRef = staticRoot
	if conf.SearchRoot != "" {
		err = sa.setMasterQuery(conf.SearchRoot)
		if err != nil {
			log.Fatalf("failed to set master query %v", err)
		}
	}
	return sa
}

func main() {
	flag.Parse()

	if *flagVersion {
		fmt.Fprintf(os.Stderr, "staticapp version: %s\nGo version: %s (%s/%s)\n",
			buildinfo.Summary(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	}

	log.Printf("Starting staticapp version %s; Go %s (%s/%s)", buildinfo.Summary(), runtime.Version(),
		runtime.GOOS, runtime.GOARCH)
	listenAddr, err := app.ListenAddress()
	if err != nil {
		log.Fatalf("Listen address: %v", err)
	}
	conf := appConfig()
	sa := newStaticAppHandler(conf)
	ws := webserver.New()
	ws.Handle("/", sa)
	if err := ws.Listen(listenAddr); err != nil {
		log.Fatalf("Listen: %v", err)
	}

	ws.Serve()
}
