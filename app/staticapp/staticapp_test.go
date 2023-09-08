package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"perkeep.org/internal/testhooks"
	"perkeep.org/pkg/blob"
	camliClient "perkeep.org/pkg/client"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/search"
	"testing"
	"time"
)

func init() {
	testhooks.SetUseSHA1(true)
}

type fakeClient struct {
	*camliClient.Client
	sh *search.Handler
}

func (fc *fakeClient) Query(ctx context.Context, req *search.SearchQuery) (*search.SearchResult, error) {
	return fc.sh.Query(ctx, req)
}

func TestStaticApp_TreeConstraint(t *testing.T) {
	h := &staticAppHandler{
		rootDirRef: "rootDirRef",
		cl:         &camliClient.Client{},
	}
	result := h.treeConstraint("path/to/file/index.html")
	resultJSON, err := json.Marshal(result)
	if err != nil {
		log.Fatalf(err.Error())
	}
	expected := []byte(`{
	  "fileName": {
		"equals": "index.html"
	  },
	  "wholeRef": null,
	  "parentDir": {
		"fileName": {
		  "equals": "file"
		},
		"parentDir": {
		  "fileName": {
			"equals": "to"
		  },
		  "parentDir": {
			"fileName": {
			  "equals": "path"
			},
			"parentDir": {
			  "blobRefPrefix": "rootDirRef"
			}
		  }
		}
	  }
	}`)
	buffer := new(bytes.Buffer)
	if err := json.Compact(buffer, expected); err != nil {
		t.Fatal(err)
	}
	if bytes.Compare(resultJSON, buffer.Bytes()) != 0 {
		t.Errorf("Unexpected file constraint query, got %s", string(resultJSON))
	}
}

type staticAppReqTest struct {
	path   string // input
	status int    // expected
}

var staticAppReqTests []staticAppReqTest

func TestStaticApp_ServeHTTP(t *testing.T) {
	idx := index.NewMemoryIndex()
	idxd := indextest.NewIndexDeps(idx)
	ownerRef := indextest.PubKey
	owner := index.NewOwner(indextest.KeyID, ownerRef.BlobRef())
	sh := search.NewHandler(idxd.Index, owner)

	staticRoot := idxd.NewPlannedPermanode("staticRoot")
	indexHTML, _ := idxd.UploadFile("index.html", "<!DOCTYPE html><html><head></head><body></body></html>", time.Time{})
	staticDir := idxd.UploadDir("test-app", []blob.Ref{indexHTML}, time.Time{})
	idxd.SetAttribute(staticRoot, "camliRoot", "test-app")
	idxd.SetAttribute(staticRoot, "camliContent", staticDir.String())

	corpus, err := idxd.Index.KeepInMemory()
	if err != nil {
		t.Fatalf("error slurping index to memory: %v", err)
	}
	sh.SetCorpus(corpus)

	cl, err := camliClient.New(camliClient.OptionServer("http://perkeep-test.com"))
	if err != nil {
		t.Fatal(err)
	}
	fcl := &fakeClient{cl, sh}
	sa := &staticAppHandler{
		rootDirRef: staticDir.String(),
		cl:         fcl,
		fetcher:    idxd.BlobSource,
	}

	staticAppReqTests = []staticAppReqTest{
		{
			path:   "/index.html",
			status: http.StatusOK,
		},
		{
			path:   "/does_not_exist.html",
			status: http.StatusNotFound,
		},
	}

	for i, rt := range staticAppReqTests {
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", fmt.Sprintf("http://test.com%s", rt.path), nil)
		sa.ServeHTTP(w, r)

		if w.Code != rt.status {
			t.Errorf("test #%d, got status %q, want %q", i, rt.status, w.Code)
		}
	}
}
