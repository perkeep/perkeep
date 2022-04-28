/*
Copyright 2014 The Perkeep Authors

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

// The publisher command is a server application to publish items from a
// Perkeep server. See also https://camlistore.org/doc/publishing
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/internal/magic"
	"perkeep.org/internal/netutil"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/app"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/localdisk"
	"perkeep.org/pkg/buildinfo"
	"perkeep.org/pkg/cacher"
	"perkeep.org/pkg/constants"
	"perkeep.org/pkg/publish"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/server"
	"perkeep.org/pkg/sorted"
	_ "perkeep.org/pkg/sorted/kvfile"
	"perkeep.org/pkg/types/camtypes"
	"perkeep.org/pkg/webserver"

	"go4.org/syncutil"
	"golang.org/x/crypto/acme/autocert"
)

var (
	flagVersion = flag.Bool("version", false, "show version")
)

var (
	logger = log.New(os.Stderr, "PUBLISHER: ", log.LstdFlags)
	logf   = logger.Printf
)

// config is used to unmarshal the application configuration JSON
// that we get from Perkeep when we request it at $CAMLI_APP_CONFIG_URL.
type config struct {
	HTTPSCert      string `json:"httpsCert,omitempty"`      // Path to the HTTPS certificate file.
	HTTPSKey       string `json:"httpsKey,omitempty"`       // Path to the HTTPS key file.
	CertManager    bool   `json:"certManager,omitempty"`    // Use Perkeep's Let's Encrypt cache to get a certificate.
	RootName       string `json:"camliRoot"`                // Publish root name (i.e. value of the camliRoot attribute on the root permanode).
	MaxResizeBytes int64  `json:"maxResizeBytes,omitempty"` // See constants.DefaultMaxResizeMem
	SourceRoot     string `json:"sourceRoot,omitempty"`     // Path to the app's resources dir, such as html and css files.
	GoTemplate     string `json:"goTemplate"`               // Go html template to render the publication.
	CacheRoot      string `json:"cacheRoot,omitempty"`      // Root path for the caching blobserver. No caching if empty.
}

// appConfig keeps on trying to fetch the extra config from the app handler. If
// it doesn't succeed after an hour has passed, the program exits.
func appConfig() (*config, error) {
	configURL := os.Getenv("CAMLI_APP_CONFIG_URL")
	if configURL == "" {
		return nil, fmt.Errorf("Publisher application needs a CAMLI_APP_CONFIG_URL env var")
	}
	cl, err := app.Client()
	if err != nil {
		return nil, fmt.Errorf("could not get a client to fetch extra config: %v", err)
	}
	conf := &config{}
	pause := time.Second
	giveupTime := time.Now().Add(time.Hour)
	for {
		err := cl.GetJSON(context.TODO(), configURL, conf)
		if err == nil {
			break
		}
		if time.Now().After(giveupTime) {
			logger.Fatalf("Giving up on starting: could not get app config at %v: %v", configURL, err)
		}
		logger.Printf("could not get app config at %v: %v. Will retry in a while.", configURL, err)
		time.Sleep(pause)
		pause *= 2
	}
	return conf, nil
}

// setMasterQuery registers with the app handler a master query that includes
// topNode and all its descendants as the search response.
func (ph *publishHandler) setMasterQuery(topNode blob.Ref) error {
	query := &search.SearchQuery{
		Sort:  search.CreatedDesc,
		Limit: -1,
		Constraint: &search.Constraint{
			Permanode: &search.PermanodeConstraint{
				Relation: &search.RelationConstraint{
					Relation: "parent",
					Any: &search.Constraint{
						BlobRefPrefix: topNode.String(),
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
	// TODO(mpl): we should use app.Client instead, but a *client.Client
	// Post method doesn't let us get the body.
	req, err := http.NewRequest("POST", ph.masterQueryURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if err := addAuth(req); err != nil {
		return err
	}
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

func (ph *publishHandler) refreshMasterQuery() error {
	// TODO(mpl): we should use app.Client instead, but a *client.Client
	// Post method doesn't let us get the body.
	req, err := http.NewRequest("POST", ph.masterQueryURL+"?refresh=1", nil)
	if err != nil {
		return err
	}
	if err := addAuth(req); err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		// request suppression. let's not consider it an error.
		return nil
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if string(body) != "OK" {
		return fmt.Errorf("error refreshing master query: %v", string(body))
	}
	return nil
}

func addAuth(r *http.Request) error {
	am, err := app.Auth()
	if err != nil {
		return err
	}
	am.AddAuthHeader(r)
	return nil
}

func setupTLS(ws *webserver.Server, conf *config) error {
	if conf.HTTPSCert != "" && conf.HTTPSKey != "" {
		ws.SetTLS(webserver.TLSSetup{
			CertFile: conf.HTTPSCert,
			KeyFile:  conf.HTTPSKey,
		})
		return nil
	}
	if !conf.CertManager {
		return nil
	}

	// As all requests to the publisher are proxied through Perkeep's app
	// handler, it makes sense to assume that both Perkeep and the publisher
	// are behind the same domain name. Therefore, it follows that
	// perkeepd's autocert is the one actually getting a cert (and answering
	// the challenge) for the both of them. Plus, if they run on the same host
	// (default setup), they can't both listen on 443 to answer the TLS-SNI
	// challenge.
	// TODO(mpl): however, perkeepd and publisher could be running on
	// different hosts, in which case we need to find a way for perkeepd to
	// share its autocert cache with publisher. But I think that can wait a
	// later CL.
	hostname := os.Getenv("CAMLI_API_HOST")
	hostname = strings.TrimPrefix(hostname, "https://")
	hostname = strings.SplitN(hostname, "/", 2)[0]
	hostname = strings.SplitN(hostname, ":", 2)[0]
	if !netutil.IsFQDN(hostname) {
		return fmt.Errorf("cannot ask Let's Encrypt for a cert because %v is not a fully qualified domain name", hostname)
	}
	logger.Print("TLS enabled, with Let's Encrypt")

	// TODO(mpl): we only want publisher to use the same cache as
	// perkeepd, and we don't actually need an autocert.Manager.
	// So we could just instantiate an autocert.DirCache, and generate
	// from there a *tls.Certificate, but it looks like it would mean
	// extracting quite a bit of code from the autocert pkg to do it properly.
	// Instead, I've opted for using an autocert.Manager (even though it's
	// never going to reply to any challenge), but with NOOP for Put and
	// Delete, just to be on the safe side. It's simple enough, but there's
	// still a catch: there's no ServerName in the clientHello we get, so we
	// reinject one so we can simply use the autocert.Manager.GetCertificate
	// method as the way to get the certificate from the cache. Is that OK?
	m := autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(hostname),
		Cache:      roCertCacheDir(osutil.DefaultLetsEncryptCache()),
	}
	ws.SetTLS(webserver.TLSSetup{
		CertManager: func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if clientHello.ServerName == "" {
				clientHello.ServerName = hostname
			}
			return m.GetCertificate(clientHello)
		},
	})
	return nil
}

func main() {
	flag.Parse()

	if *flagVersion {
		fmt.Fprintf(os.Stderr, "publisher version: %s\nGo version: %s (%s/%s)\n",
			buildinfo.Summary(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	}

	logf("Starting publisher version %s; Go %s (%s/%s)", buildinfo.Summary(), runtime.Version(),
		runtime.GOOS, runtime.GOARCH)

	listenAddr, err := app.ListenAddress()
	if err != nil {
		logger.Fatalf("Listen address: %v", err)
	}
	conf, err := appConfig()
	if err != nil {
		logger.Fatalf("no app config: %v", err)
	}
	ph := newPublishHandler(conf)
	masterQueryURL := os.Getenv("CAMLI_APP_MASTERQUERY_URL")
	if masterQueryURL == "" {
		logger.Fatalf("Publisher application needs a CAMLI_APP_MASTERQUERY_URL env var")
	}
	ph.masterQueryURL = masterQueryURL
	if err := ph.initRootNode(); err != nil {
		logf("%v", err)
	} else {
		if err := ph.setMasterQuery(ph.rootNode); err != nil {
			logf("%v", err)
		}
	}
	ws := webserver.New()
	ws.Logger = logger
	if err := setupTLS(ws, conf); err != nil {
		logger.Fatalf("could not setup TLS: %v", err)
	}
	ws.Handle("/", ph)
	if err := ws.Listen(listenAddr); err != nil {
		logger.Fatalf("Listen: %v", err)
	}
	ws.Serve()
}

func newPublishHandler(conf *config) *publishHandler {
	cl, err := app.Client()
	if err != nil {
		logger.Fatalf("could not get a client for the publish handler %v", err)
	}
	if conf.RootName == "" {
		logger.Fatal("camliRoot not found in the app configuration")
	}
	maxResizeBytes := conf.MaxResizeBytes
	if maxResizeBytes == 0 {
		maxResizeBytes = constants.DefaultMaxResizeMem
	}
	var CSSFiles, JSDeps []string
	var staticFiles fs.FS = Files
	if conf.SourceRoot != "" {
		staticFiles = os.DirFS(conf.SourceRoot)
		// TODO(mpl): Can I readdir by listing with "/" on Files, even with DirFallBack?
		// Apparently not, but retry later.
		dir, err := os.Open(conf.SourceRoot)
		if err != nil {
			logger.Fatal(err)
		}
		defer dir.Close()
		names, err := dir.Readdirnames(-1)
		if err != nil {
			logger.Fatal(err)
		}
		for _, v := range names {
			if strings.HasSuffix(v, ".css") {
				CSSFiles = append(CSSFiles, v)
				continue
			}
			// TODO(mpl): document or fix (use a map?) the ordering
			// problem: i.e. jquery.js must be sourced before
			// publisher.js. For now, just cheat by sorting the
			// slice.
			if strings.HasSuffix(v, ".js") {
				JSDeps = append(JSDeps, v)
			}
		}
		sort.Strings(JSDeps)
	} else {
		fis, err := Files.ReadDir(".")
		if err != nil {
			logger.Fatal(err)
		}
		for _, v := range fis {
			name := v.Name()
			if strings.HasSuffix(name, ".css") {
				CSSFiles = append(CSSFiles, name)
				continue
			}
			if strings.HasSuffix(name, ".js") {
				JSDeps = append(JSDeps, name)
			}
		}
		sort.Strings(JSDeps)
	}
	// TODO(mpl): add all htmls found in Files to the template if none specified?
	if conf.GoTemplate == "" {
		logger.Fatal("a go template is required in the app configuration")
	}
	goTemplate, err := goTemplate(staticFiles, conf.GoTemplate)
	if err != nil {
		logger.Fatal(err)
	}

	var cache blobserver.Storage
	var thumbMeta *server.ThumbMeta
	if conf.CacheRoot != "" {
		cache, err = localdisk.New(conf.CacheRoot)
		if err != nil {
			logger.Fatalf("Could not create localdisk cache: %v", err)
		}
		thumbsCacheDir := filepath.Join(os.TempDir(), "camli-publisher-cache")
		if err := os.MkdirAll(thumbsCacheDir, 0700); err != nil {
			logger.Fatalf("Could not create cache dir %s for %v publisher: %v", thumbsCacheDir, conf.RootName, err)
		}
		kv, err := sorted.NewKeyValue(map[string]interface{}{
			"type": "kv",
			"file": filepath.Join(thumbsCacheDir, conf.RootName+"-thumbnails.kv"),
		})
		if err != nil {
			logger.Fatalf("Could not create kv for %v's thumbs cache: %v", conf.RootName, err)
		}
		thumbMeta = server.NewThumbMeta(kv)
	}

	return &publishHandler{
		rootName:       conf.RootName,
		cl:             cl,
		resizeSem:      syncutil.NewSem(maxResizeBytes),
		staticFiles:    staticFiles,
		goTemplate:     goTemplate,
		CSSFiles:       CSSFiles,
		JSDeps:         JSDeps,
		describedCache: make(map[string]*search.DescribedBlob),
		cache:          cache,
		thumbMeta:      thumbMeta,
	}
}

func goTemplate(files fs.FS, templateFile string) (*template.Template, error) {
	f, err := files.Open(templateFile)
	if err != nil {
		return nil, fmt.Errorf("Could not open template %v: %v", templateFile, err)
	}
	defer f.Close()
	templateBytes, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("Could not read template %v: %v", templateFile, err)
	}
	return template.Must(template.New("subject").Parse(string(templateBytes))), nil
}

// We're using this interface in a publishHandler, instead of directly
// a *client.Client, so we can use a fake client in tests.
type client interface {
	search.QueryDescriber
	GetJSON(ctx context.Context, url string, data interface{}) error
	Post(ctx context.Context, url string, bodyType string, body io.Reader) error
	blob.Fetcher
}

type publishHandler struct {
	rootName string // Publish root name (i.e. value of the camliRoot attribute on the root permanode).

	rootNodeMu sync.Mutex
	rootNode   blob.Ref // Root permanode, origin of all camliPaths for this publish handler.

	masterQueryURL  string // not guarded by mutex below, because set on startup.
	masterQueryMu   sync.Mutex
	masterQueryDone bool // master query has been registered with the app handler

	cl client // Used for searching, and remote storage.

	staticFiles fs.FS              // For static resources.
	goTemplate  *template.Template // For publishing/rendering.
	CSSFiles    []string
	JSDeps      []string
	resizeSem   *syncutil.Sem // Limit peak RAM used by concurrent image thumbnail calls.

	describedCacheMu sync.RWMutex
	describedCache   map[string]*search.DescribedBlob // So that each item in a gallery does not actually require a describe round-trip.

	cache     blobserver.Storage // For caching images and files, or nil.
	thumbMeta *server.ThumbMeta  // For keeping track of cached images, or nil.
}

func (ph *publishHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ph.rootNodeMu.Lock()
	if !ph.rootNode.Valid() {
		// we want to retry doing this every time because the rootNode could have been created
		// (by e.g. the owner) since last time.
		err := ph.initRootNode()
		if err != nil {
			httputil.ServeError(w, r, fmt.Errorf("No publish root node: %v", err))
			ph.rootNodeMu.Unlock()
			return
		}
	}
	ph.rootNodeMu.Unlock()
	ph.masterQueryMu.Lock()
	if !ph.masterQueryDone {
		if err := ph.setMasterQuery(ph.rootNode); err != nil {
			httputil.ServeError(w, r, fmt.Errorf("master query not set: %v", err))
			ph.masterQueryMu.Unlock()
			return
		}
		ph.masterQueryDone = true
	}
	ph.masterQueryMu.Unlock()

	preq, err := ph.NewRequest(w, r)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Could not create publish request: %v", err))
		return
	}
	preq.serveHTTP()
}

func (ph *publishHandler) initRootNode() error {
	var getRootNode = func() (blob.Ref, error) {
		result, err := ph.camliRootQuery()
		if err != nil {
			return blob.Ref{}, fmt.Errorf("could not find permanode for root %q of publish handler: %v", ph.rootName, err)
		}
		if len(result.Blobs) == 0 || !result.Blobs[0].Blob.Valid() {
			return blob.Ref{}, fmt.Errorf("could not find permanode for root %q of publish handler: %v", ph.rootName, os.ErrNotExist)
		}
		return result.Blobs[0].Blob, nil
	}
	node, err := getRootNode()
	if err != nil {
		return err
	}
	ph.rootNode = node
	return nil
}

func (ph *publishHandler) camliRootQuery() (*search.SearchResult, error) {
	// TODO(mpl): I've voluntarily omitted the owner because it's not clear to
	// me that we actually care about that. Same for signer in lookupPathTarget.
	return ph.cl.Query(context.TODO(), &search.SearchQuery{
		Limit: 1,
		Constraint: &search.Constraint{
			Permanode: &search.PermanodeConstraint{
				Attr:  "camliRoot",
				Value: ph.rootName,
			},
		},
	})
}

func (ph *publishHandler) lookupPathTarget(root blob.Ref, suffix string) (blob.Ref, error) {
	if suffix == "" {
		return root, nil
	}
	// TODO: verify it's optimized: http://perkeep.org/issue/405
	result, err := ph.cl.Query(context.TODO(), &search.SearchQuery{
		Limit: 1,
		Constraint: &search.Constraint{
			Permanode: &search.PermanodeConstraint{
				SkipHidden: true,
				Relation: &search.RelationConstraint{
					Relation: "parent",
					EdgeType: "camliPath:" + suffix,
					Any: &search.Constraint{
						BlobRefPrefix: root.String(),
					},
				},
			},
		},
	})
	if err != nil {
		return blob.Ref{}, err
	}
	if len(result.Blobs) == 0 || !result.Blobs[0].Blob.Valid() {
		return blob.Ref{}, os.ErrNotExist
	}
	return result.Blobs[0].Blob, nil
}

// Given a blobref and a few hex characters of the digest of the next hop, return the complete
// blobref of the prefix, if that's a valid next hop.
func (ph *publishHandler) resolvePrefixHop(parent blob.Ref, prefix string) (child blob.Ref, err error) {
	// TODO: this is a linear scan right now. this should be
	// optimized to use a new database table of members so this is
	// a quick lookup.  in the meantime it should be in memcached
	// at least.
	if len(prefix) < 8 {
		return blob.Ref{}, fmt.Errorf("Member prefix %q too small", prefix)
	}
	des, err := ph.describe(parent)
	if err != nil {
		return blob.Ref{}, fmt.Errorf("Failed to describe member %q in parent %q", prefix, parent)
	}
	if des.Permanode != nil {
		cr, ok := des.ContentRef()
		if ok && strings.HasPrefix(cr.Digest(), prefix) {
			return cr, nil
		}
		for _, member := range des.Members() {
			if strings.HasPrefix(member.BlobRef.Digest(), prefix) {
				return member.BlobRef, nil
			}
		}
		crdes, err := ph.describe(cr)
		if err != nil {
			return blob.Ref{}, fmt.Errorf("Failed to describe content %q of parent %q", cr, parent)
		}
		if crdes.Dir != nil {
			return ph.resolvePrefixHop(cr, prefix)
		}
	} else if des.Dir != nil {
		for _, child := range des.DirChildren {
			if strings.HasPrefix(child.Digest(), prefix) {
				return child, nil
			}
		}
	}
	return blob.Ref{}, fmt.Errorf("Member prefix %q not found in %q", prefix, parent)
}

func (ph *publishHandler) describe(br blob.Ref) (*search.DescribedBlob, error) {
	ph.describedCacheMu.RLock()
	if des, ok := ph.describedCache[br.String()]; ok {
		ph.describedCacheMu.RUnlock()
		return des, nil
	}
	ph.describedCacheMu.RUnlock()
	ctx := context.TODO()
	res, err := ph.cl.Describe(ctx, &search.DescribeRequest{
		BlobRef: br,
		Depth:   1,
	})
	if err != nil {
		return nil, fmt.Errorf("Could not describe %v: %v", br, err)
	}
	// TODO(mpl): check why Describe is not giving us an error when br is invalid.
	if res == nil || res.Meta == nil || res.Meta[br.String()] == nil {
		return nil, fmt.Errorf("Could not describe %v", br)
	}
	return res.Meta[br.String()], nil
}

func (ph *publishHandler) deepDescribe(br blob.Ref) (*search.DescribeResponse, error) {
	res, err := ph.cl.Query(context.TODO(), &search.SearchQuery{
		Constraint: &search.Constraint{
			BlobRefPrefix: br.String(),
			CamliType:     "permanode",
		},
		Describe: &search.DescribeRequest{
			Depth: 1,
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliContent", "camliContentImage", "camliMember", "camliPath:*"},
				},
			},
		},
		Limit: -1,
	})
	if err != nil {
		return nil, fmt.Errorf("Could not deep describe %v: %v", br, err)
	}
	if res == nil || res.Describe == nil {
		return nil, fmt.Errorf("no describe result for %v", br)
	}
	return res.Describe, nil
}

// publishRequest is the state around a single HTTP request to the
// publish handler
type publishRequest struct {
	ph                   *publishHandler
	rw                   http.ResponseWriter
	req                  *http.Request
	base, suffix, subres string
	rootpn               blob.Ref
	subject              blob.Ref
	inSubjectChain       map[string]bool // blobref -> true
	subjectBasePath      string
	publishedRoot        blob.Ref // on camliRoot, camliPath:somePath = publishedRoot
}

func (ph *publishHandler) NewRequest(w http.ResponseWriter, r *http.Request) (*publishRequest, error) {
	// splits a path request into its suffix and subresource parts.
	// e.g. /blog/foo/camli/res/file/xxx -> ("foo", "file/xxx")
	base := app.PathPrefix(r)
	suffix, res := strings.TrimPrefix(r.URL.Path, base), ""
	if strings.HasPrefix(suffix, "-/") {
		suffix, res = "", suffix[2:]
	} else if s := strings.SplitN(suffix, "/-/", 2); len(s) == 2 {
		suffix, res = s[0], s[1]
	}

	return &publishRequest{
		ph:              ph,
		rw:              w,
		req:             r,
		suffix:          strings.TrimSuffix(suffix, "/"),
		base:            base,
		subres:          res,
		rootpn:          ph.rootNode,
		inSubjectChain:  make(map[string]bool),
		subjectBasePath: "",
	}, nil
}

func (pr *publishRequest) serveHTTP() {
	if !pr.rootpn.Valid() {
		http.NotFound(pr.rw, pr.req)
		return
	}

	if pr.suffix == "" {
		// Do not show everything at the root.
		http.Error(pr.rw, "403 forbidden", http.StatusForbidden)
		return
	}

	if pr.Debug() {
		pr.rw.Header().Set("Content-Type", "text/html")
		pr.pf("I am publish handler at base %q, serving root %q (permanode=%s), suffix %q, subreq %q<hr>",
			pr.base, pr.ph.rootName, pr.rootpn, html.EscapeString(pr.suffix), html.EscapeString(pr.subres))
	}

	if err := pr.findSubject(); err != nil {
		if err == os.ErrNotExist {
			http.NotFound(pr.rw, pr.req)
			return
		}
		logf("Error looking up %s/%q: %v", pr.rootpn, pr.suffix, err)
		pr.rw.WriteHeader(500)
		return
	}

	if pr.Debug() {
		pr.pf("<p><b>Subject:</b> <a href='/ui/?p=%s'>%s</a></p>", pr.subject, pr.subject)
		return
	}

	switch pr.subresourceType() {
	case "":
		// This should not happen too often as now that we have the
		// javascript code we're not hitting that code path often anymore
		// in a typical navigation. And the app handler is doing
		// request suppression anyway.
		// If needed, we can always do it in a narrower case later.
		if err := pr.ph.refreshMasterQuery(); err != nil {
			logf("Error refreshing master query: %v", err)
			pr.rw.WriteHeader(500)
			return
		}
		pr.serveSubjectTemplate()
	case "b":
		// TODO: download a raw blob
	case "f": // file download
		pr.serveSubresFileDownload()
	case "i": // image, scaled
		pr.serveSubresImage()
	case "s": // static
		pr.req.URL.Path = pr.subres[len("/=s"):]
		if len(pr.req.URL.Path) <= 1 {
			http.Error(pr.rw, "Illegal URL.", http.StatusNotFound)
			return
		}
		file := pr.req.URL.Path[1:]
		server.ServeStaticFile(pr.rw, pr.req, pr.ph.staticFiles, file)
	case "z":
		pr.serveZip()
	default:
		pr.rw.WriteHeader(400)
		pr.pf("<p>Invalid or unsupported resource request.</p>")
	}
}

func (pr *publishRequest) Debug() bool {
	return pr.req.FormValue("debug") == "1"
}

var memberRE = regexp.MustCompile(`^/?h([0-9a-f]+)`)

func (pr *publishRequest) findSubject() error {
	if strings.HasPrefix(pr.suffix, "=s/") {
		pr.subres = "/" + pr.suffix
		return nil
	}

	subject, err := pr.ph.lookupPathTarget(pr.rootpn, pr.suffix)
	if err != nil {
		return err
	}
	pr.publishedRoot = subject
	if strings.HasPrefix(pr.subres, "=z/") {
		// this happens when we are at the root of the published path,
		// e.g /base/suffix/-/=z/foo.zip
		// so we need to reset subres as fullpath so that it is detected
		// properly when switching on pr.subresourceType()
		pr.subres = "/" + pr.subres
		// since we return early, we set the subject because that is
		// what is going to be used as a root node by the zip handler.
		pr.subject = subject
		return nil
	}

	pr.inSubjectChain[subject.String()] = true
	pr.subjectBasePath = pr.base + pr.suffix

	// Chase /h<xxxxx> hops in suffix.
	for {
		m := memberRE.FindStringSubmatch(pr.subres)
		if m == nil {
			break
		}
		match, memberPrefix := m[0], m[1]

		if err != nil {
			return fmt.Errorf("Error looking up potential member %q in describe of subject %q: %v",
				memberPrefix, subject, err)
		}

		subject, err = pr.ph.resolvePrefixHop(subject, memberPrefix)
		if err != nil {
			return err
		}
		pr.inSubjectChain[subject.String()] = true
		pr.subres = pr.subres[len(match):]
		pr.subjectBasePath = addPathComponent(pr.subjectBasePath, match)
	}

	pr.subject = subject
	return nil
}

func (pr *publishRequest) subresourceType() string {
	if len(pr.subres) >= 3 && strings.HasPrefix(pr.subres, "/=") {
		return pr.subres[2:3]
	}
	return ""
}

func (pr *publishRequest) pf(format string, args ...interface{}) {
	fmt.Fprintf(pr.rw, format, args...)
}

func addPathComponent(base, addition string) string {
	if !strings.HasPrefix(addition, "/") {
		addition = "/" + addition
	}
	if strings.Contains(base, "/-/") {
		return base + addition
	}
	return base + "/-" + addition
}

// var hopRE = regexp.MustCompile(fmt.Sprintf("^/%s([0-9a-f]{%d})", digestPrefix, digestLen))

func getFileInfo(item blob.Ref, peers map[string]*search.DescribedBlob) (path []blob.Ref, fi *camtypes.FileInfo, ok bool) {
	described := peers[item.String()]
	if described == nil ||
		described.Permanode == nil ||
		described.Permanode.Attr == nil {
		return
	}
	contentRef := described.Permanode.Attr.Get("camliContent")
	if contentRef == "" {
		return
	}
	if cdes := peers[contentRef]; cdes != nil && cdes.File != nil {
		return []blob.Ref{described.BlobRef, cdes.BlobRef}, cdes.File, true
	}
	return
}

// serveSubjectTemplate creates the funcs to generate the PageHeader, PageFile,
// and pageMembers that can be used by the subject template, and serves the template.
func (pr *publishRequest) serveSubjectTemplate() {
	res, err := pr.ph.deepDescribe(pr.subject)
	if err != nil {
		httputil.ServeError(pr.rw, pr.req, err)
		return
	}
	pr.ph.cacheDescribed(res.Meta)

	subdes := res.Meta[pr.subject.String()]
	if subdes.CamliType == schema.TypeFile {
		pr.serveFileDownload(subdes)
		return
	}

	headerFunc := func() *publish.PageHeader {
		return pr.subjectHeader(res.Meta)
	}
	fileFunc := func() *publish.PageFile {
		file, err := pr.subjectFile(res.Meta)
		if err != nil {
			logf("%v", err)
			return nil
		}
		return file
	}
	membersFunc := func() *publish.PageMembers {
		members, err := pr.subjectMembers(res.Meta)
		if err != nil {
			logf("%v", err)
			return nil
		}
		return members
	}
	page := &publish.SubjectPage{
		Header:  headerFunc,
		File:    fileFunc,
		Members: membersFunc,
	}

	err = pr.ph.goTemplate.Execute(pr.rw, page)
	if err != nil {
		logf("Error serving subject template: %v", err)
		http.Error(pr.rw, "Error serving template", http.StatusInternalServerError)
		return
	}
}

const cacheSize = 1000

func (ph *publishHandler) cacheDescribed(described map[string]*search.DescribedBlob) {
	ph.describedCacheMu.Lock()
	defer ph.describedCacheMu.Unlock()
	if len(ph.describedCache) > cacheSize {
		ph.describedCache = described
		return
	}
	for k, v := range described {
		ph.describedCache[k] = v
	}
}

func (pr *publishRequest) serveFileDownload(des *search.DescribedBlob) {
	fileref, fileinfo, ok := pr.fileSchemaRefFromBlob(des)
	if !ok {
		logf("Didn't get file schema from described blob %q", des.BlobRef)
		return
	}
	mimeType := ""
	if fileinfo != nil {
		if fileinfo.IsImage() || fileinfo.IsText() {
			mimeType = fileinfo.MIMEType
			if mimeType == "" {
				mimeType = magic.MIMETypeByExtension(filepath.Ext(fileinfo.FileName))
			}
		}
	}
	dh := &server.DownloadHandler{
		Fetcher:   cacher.NewCachingFetcher(pr.ph.cache, pr.ph.cl),
		ForceMIME: mimeType,
	}
	dh.ServeFile(pr.rw, pr.req, fileref)
}

// Given a described blob, optionally follows a camliContent and
// returns the file's schema blobref and its fileinfo (if found).
func (pr *publishRequest) fileSchemaRefFromBlob(des *search.DescribedBlob) (fileref blob.Ref, fileinfo *camtypes.FileInfo, ok bool) {
	if des == nil {
		http.NotFound(pr.rw, pr.req)
		return
	}
	if des.Permanode != nil {
		// TODO: get "forceMime" attr out of the permanode? or
		// fileName content-disposition?
		if cref := des.Permanode.Attr.Get("camliContent"); cref != "" {
			cbr, ok2 := blob.Parse(cref)
			if !ok2 {
				http.Error(pr.rw, "bogus camliContent", 500)
				return
			}
			des = des.PeerBlob(cbr)
			if des == nil {
				http.Error(pr.rw, "camliContent not a peer in describe", 500)
				return
			}
		}
	}
	if des.CamliType == "file" {
		return des.BlobRef, des.File, true
	}
	http.Error(pr.rw, "failed to find fileSchemaRefFromBlob", 404)
	return
}

// subjectHeader returns the PageHeader corresponding to the described subject.
func (pr *publishRequest) subjectHeader(described map[string]*search.DescribedBlob) *publish.PageHeader {
	subdes := described[pr.subject.String()]
	header := &publish.PageHeader{
		Title:           html.EscapeString(getTitle(subdes.BlobRef, described)),
		CSSFiles:        pr.cssFiles(),
		JSDeps:          pr.jsDeps(),
		Subject:         pr.subject,
		Host:            pr.req.Host,
		SubjectBasePath: pr.subjectBasePath,
		PathPrefix:      pr.base,
		PublishedRoot:   pr.publishedRoot,
	}
	return header
}

func (pr *publishRequest) cssFiles() []string {
	files := []string{}
	for _, filename := range pr.ph.CSSFiles {
		files = append(files, pr.staticPath(filename))
	}
	return files
}

func (pr *publishRequest) jsDeps() []string {
	files := []string{}
	for _, filename := range pr.ph.JSDeps {
		files = append(files, pr.staticPath(filename))
	}
	return files
}

func (pr *publishRequest) staticPath(fileName string) string {
	return pr.base + "=s/" + fileName
}

func getTitle(item blob.Ref, peers map[string]*search.DescribedBlob) string {
	described := peers[item.String()]
	if described == nil {
		return ""
	}
	if described.Permanode != nil {
		if t := described.Permanode.Attr.Get("title"); t != "" {
			return t
		}
		if contentRef := described.Permanode.Attr.Get("camliContent"); contentRef != "" {
			if cdes := peers[contentRef]; cdes != nil {
				return getTitle(cdes.BlobRef, peers)
			}
		}
	}
	if described.File != nil {
		return described.File.FileName
	}
	if described.Dir != nil {
		return described.Dir.FileName
	}
	return ""
}

// subjectFile returns the relevant PageFile if the described subject is a file permanode.
func (pr *publishRequest) subjectFile(described map[string]*search.DescribedBlob) (*publish.PageFile, error) {
	subdes := described[pr.subject.String()]
	contentRef, ok := subdes.ContentRef()
	if !ok {
		return nil, nil
	}
	fileDes, err := pr.ph.describe(contentRef)
	if err != nil {
		return nil, err
	}
	if fileDes.File == nil {
		// most likely a dir
		return nil, nil
	}

	path := []blob.Ref{pr.subject, contentRef}
	downloadURL := pr.SubresFileURL(path, fileDes.File.FileName)
	thumbnailURL := ""
	if fileDes.File.IsImage() {
		thumbnailURL = pr.SubresThumbnailURL(path, fileDes.File.FileName, 600)
	}
	fileName := html.EscapeString(fileDes.File.FileName)
	return &publish.PageFile{
		FileName:     fileName,
		Size:         fileDes.File.Size,
		MIMEType:     fileDes.File.MIMEType,
		IsImage:      fileDes.File.IsImage(),
		DownloadURL:  downloadURL,
		ThumbnailURL: thumbnailURL,
		DomID:        contentRef.DomID(),
		Nav: func() *publish.Nav {
			return nil
		},
	}, nil
}

func (pr *publishRequest) SubresFileURL(path []blob.Ref, fileName string) string {
	return pr.SubresThumbnailURL(path, fileName, -1)
}

func (pr *publishRequest) SubresThumbnailURL(path []blob.Ref, fileName string, maxDimen int) string {
	var buf bytes.Buffer
	resType := "i"
	if maxDimen == -1 {
		resType = "f"
	}
	fmt.Fprintf(&buf, "%s", pr.subjectBasePath)
	if !strings.Contains(pr.subjectBasePath, "/-/") {
		buf.Write([]byte("/-"))
	}
	for _, br := range path {
		if pr.inSubjectChain[br.String()] {
			continue
		}
		fmt.Fprintf(&buf, "/h%s", br.DigestPrefix(10))
	}
	fmt.Fprintf(&buf, "/=%s", resType)
	fmt.Fprintf(&buf, "/%s", url.QueryEscape(fileName))
	if maxDimen != -1 {
		fmt.Fprintf(&buf, "?mw=%d&mh=%d", maxDimen, maxDimen)
	}
	return buf.String()
}

// subjectMembers returns the relevant PageMembers if the described subject is a permanode with members.
func (pr *publishRequest) subjectMembers(resMap map[string]*search.DescribedBlob) (*publish.PageMembers, error) {
	subdes := resMap[pr.subject.String()]
	res, err := pr.ph.describeMembers(pr.subject)
	if err != nil {
		return nil, err
	}
	members := []*search.DescribedBlob{}
	for _, v := range res.Blobs {
		members = append(members, res.Describe.Meta[v.Blob.String()])
	}
	if len(members) == 0 {
		return nil, nil
	}

	zipName := ""
	if title := getTitle(subdes.BlobRef, resMap); title == "" {
		zipName = "download.zip"
	} else {
		zipName = title + ".zip"
	}
	subjectPath := pr.subjectBasePath
	if !strings.Contains(subjectPath, "/-/") {
		subjectPath += "/-"
	}

	return &publish.PageMembers{
		SubjectPath: subjectPath,
		ZipName:     zipName,
		Members:     members,
		Description: func(member *search.DescribedBlob) string {
			des := member.Description()
			if des != "" {
				des = " - " + des
			}
			return des
		},
		Title: func(member *search.DescribedBlob) string {
			memberTitle := getTitle(member.BlobRef, resMap)
			if memberTitle == "" {
				memberTitle = member.BlobRef.DigestPrefix(10)
			}
			return html.EscapeString(memberTitle)
		},
		Path: func(member *search.DescribedBlob) string {
			return pr.memberPath(member.BlobRef)
		},
		DomID: func(member *search.DescribedBlob) string {
			return member.DomID()
		},
		FileInfo: func(member *search.DescribedBlob) *publish.MemberFileInfo {
			if path, fileInfo, ok := getFileInfo(member.BlobRef, resMap); ok {
				info := &publish.MemberFileInfo{
					FileName:  fileInfo.FileName,
					FileDomID: path[len(path)-1].DomID(),
					FilePath:  html.EscapeString(pr.SubresFileURL(path, fileInfo.FileName)),
				}
				if fileInfo.IsImage() {
					info.FileThumbnailURL = pr.SubresThumbnailURL(path, fileInfo.FileName, 200)
				}
				return info
			}
			return nil
		},
	}, nil
}

func (ph *publishHandler) describeMembers(br blob.Ref) (*search.SearchResult, error) {
	res, err := ph.cl.Query(context.TODO(), &search.SearchQuery{
		Constraint: &search.Constraint{
			Permanode: &search.PermanodeConstraint{
				Relation: &search.RelationConstraint{
					Relation: "parent",
					Any: &search.Constraint{
						BlobRefPrefix: br.String(),
					},
				},
			},
			CamliType: "permanode",
		},
		Describe: &search.DescribeRequest{
			Depth: 1,
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliContent", "camliContentImage"},
				},
			},
		},
		Limit: -1,
	})
	if err != nil {
		return nil, fmt.Errorf("Could not describe members of %v: %v", br, err)
	}
	return res, nil
}

func (pr *publishRequest) memberPath(member blob.Ref) string {
	return addPathComponent(pr.subjectBasePath, "/h"+member.DigestPrefix(10))
}

func (pr *publishRequest) serveSubresFileDownload() {
	des, err := pr.ph.describe(pr.subject)
	if err != nil {
		logf("error describing subject %q: %v", pr.subject, err)
		return
	}
	pr.serveFileDownload(des)
}

func (pr *publishRequest) serveSubresImage() {
	params := pr.req.URL.Query()
	mw, _ := strconv.Atoi(params.Get("mw"))
	mh, _ := strconv.Atoi(params.Get("mh"))
	des, err := pr.ph.describe(pr.subject)
	if err != nil {
		logf("error describing subject %q: %v", pr.subject, err)
		return
	}
	pr.serveScaledImage(des, mw, mh, params.Get("square") == "1")
}

func (pr *publishRequest) serveScaledImage(des *search.DescribedBlob, maxWidth, maxHeight int, square bool) {
	fileref, _, ok := pr.fileSchemaRefFromBlob(des)
	if !ok {
		logf("scaled image fail; failed to get file schema from des %q", des.BlobRef)
		return
	}
	ih := &server.ImageHandler{
		Fetcher:   pr.ph.cl,
		Cache:     pr.ph.cache,
		MaxWidth:  maxWidth,
		MaxHeight: maxHeight,
		Square:    square,
		ThumbMeta: pr.ph.thumbMeta,
		ResizeSem: pr.ph.resizeSem,
	}
	ih.ServeHTTP(pr.rw, pr.req, fileref)
}

// serveZip streams a zip archive of all the files "under"
// pr.subject. That is, all the files pointed by file permanodes,
// which are directly members of pr.subject or recursively down
// directory permanodes and permanodes members.
func (pr *publishRequest) serveZip() {
	filename := ""
	if len(pr.subres) > len("/=z/") {
		filename = pr.subres[4:]
	}
	zh := &zipHandler{
		fetcher:  pr.ph.cl,
		cl:       pr.ph.cl,
		root:     pr.subject,
		filename: filename,
	}
	zh.ServeHTTP(pr.rw, pr.req)
}

// roCertCacheDir is a read-only implementation of autocert.Cache.
type roCertCacheDir string

func (d roCertCacheDir) Get(ctx context.Context, key string) ([]byte, error) {
	return autocert.DirCache(d).Get(ctx, key)
}

func (d roCertCacheDir) Put(ctx context.Context, key string, data []byte) error {
	return nil
}

func (d roCertCacheDir) Delete(ctx context.Context, key string) error {
	return nil
}
