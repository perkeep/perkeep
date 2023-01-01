/*
Copyright 2017 The Perkeep Authors.

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
	"context"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	uistatic "perkeep.org/app/scanningcabinet/ui"
	"perkeep.org/internal/httputil"
	"perkeep.org/internal/magic"
	"perkeep.org/pkg/app"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/constants"
	"perkeep.org/pkg/search"
	camliserver "perkeep.org/pkg/server"

	"go4.org/syncutil"
)

const (
	maxScan = 50 // number of scans fetched/displayed. arbitrary.
	maxDue  = 30 // number of due documents fetched

	scanNodeType     = "scanningcabinet:scan"
	documentNodeType = "scanningcabinet:doc"
)

var (
	rootTemplate = template.Must(template.New("root").Parse(rootHTML))
	docTemplate  = template.Must(template.New("doc").Parse(docHTML))

	resourcePattern *regexp.Regexp = regexp.MustCompile(`^/resource/(` + blob.Pattern + `)$`)
)

// config is used to unmarshal the application configuration JSON
// that we get from Perkeep when we request it at $CAMLI_APP_CONFIG_URL.
type extraConfig struct {
	Auth       string `json:"auth,omitempty"`       // userpass:username:password
	HTTPSCert  string `json:"httpsCert,omitempty"`  // path to the HTTPS certificate file.
	HTTPSKey   string `json:"httpsKey,omitempty"`   // path to the HTTPS key file.
	SourceRoot string `json:"sourceRoot,omitempty"` // Path to the app's resources dir, such as html and css files.
}

func appConfig() (*extraConfig, error) {
	configURL := os.Getenv("CAMLI_APP_CONFIG_URL")
	if configURL == "" {
		logf("CAMLI_APP_CONFIG_URL not defined, the app will run without any auth")
		return nil, nil
	}
	cl, err := app.Client()
	if err != nil {
		return nil, fmt.Errorf("could not get a client to fetch extra config: %v", err)
	}
	conf := &extraConfig{}
	if err := cl.GetJSON(context.Background(), configURL, conf); err != nil {
		return nil, fmt.Errorf("could not get app extra config at %v: %v", configURL, err)
	}
	return conf, nil
}

type handler struct {
	httpsCert string
	httpsKey  string
	am        auth.AuthMode
	mux       *http.ServeMux
	sh        search.QueryDescriber
	// TODO(mpl): later we should have an uploader interface instead. implemented by *client.Client like sh, but they wouldn't have to be the same in theory. right now they actually are.
	cl *client.Client

	signer blob.Ref
	server string

	uiFiles fs.FS
}

func newHandler() (*handler, error) {
	cl, err := app.Client()
	if err != nil {
		return nil, fmt.Errorf("could not initialize a client: %v", err)
	}
	h := &handler{
		sh:      cl,
		cl:      cl,
		uiFiles: uistatic.Files,
	}

	config, err := appConfig()
	if err != nil {
		return nil, err
	}

	// Serve files from source root when running devcam
	if config.SourceRoot != "" {
		logf("Using UI resources (HTML, JS, CSS) from disk, under %v", config.SourceRoot)
		h.uiFiles = os.DirFS(config.SourceRoot)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleRoot)
	mux.HandleFunc("/ui/", h.handleUiFile)
	mux.HandleFunc("/uploadurl", h.handleUploadURL)
	mux.HandleFunc("/upload", h.handleUpload)
	mux.HandleFunc("/resource/", h.handleResource)
	mux.HandleFunc("/makedoc", h.handleMakedoc)
	mux.HandleFunc("/doc/", h.handleDoc)
	mux.HandleFunc("/changedoc", h.handleChangedoc)
	mux.HandleFunc("/robots.txt", handleRobots)
	h.mux = mux

	if err := h.disco(); err != nil {
		return nil, err
	}

	if config != nil {
		h.httpsCert = config.HTTPSCert
		h.httpsKey = config.HTTPSKey
	}
	var authConfig string
	if config == nil || config.Auth == "" {
		authConfig = "none"
	} else {
		authConfig = config.Auth
	}
	am, err := auth.FromConfig(authConfig)
	if err != nil {
		return nil, err
	}
	h.am = am
	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if auth.AllowedWithAuth(h.am, r, auth.OpAll) {
		h.serveHTTP(w, r)
		return
	}
	if us, ok := h.am.(auth.UnauthorizedSender); ok {
		if us.SendUnauthorized(w, r) {
			return
		}
	}
	w.Header().Set("WWW-Authenticate", "Basic realm=scanning cabinet")
	w.WriteHeader(http.StatusUnauthorized)
	fmt.Fprintf(w, "<html><body><h1>Unauthorized</h1>")
}

func (h *handler) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if h.mux == nil {
		http.Error(w, "handler not properly initialized", http.StatusInternalServerError)
		return
	}
	r.URL.Path = strings.Replace(r.URL.Path, app.PathPrefix(r), "/", 1)
	h.mux.ServeHTTP(w, r)
}

type rootData struct {
	BaseURL      string
	Tags         separatedString
	Media        []MediaObjectVM
	SearchedDocs []DocumentVM
	UntaggedDocs []DocumentVM
	UpcomingDocs []DocumentVM
	TopMessage   template.HTML
	ErrorMessage string
	AllTags      map[string]int
}

func (h *handler) disco() error {
	var err error
	server := os.Getenv("CAMLI_API_HOST")
	if server == "" {
		server, err = h.cl.BlobRoot()
		if err != nil {
			return fmt.Errorf("CAMLI_API_HOST var not set, and client could not discover server blob root: %v", err)
		}
	}
	h.server = server

	// TODO(mpl): setup our own signer if we got our own key and stuff.
	signer, err := h.cl.ServerPublicKeyBlobRef()
	if err != nil {
		return fmt.Errorf("client has no signing capability and server can't sign for us either: %v", err)
	}
	h.signer = signer
	return nil
}

func (h *handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	topMessage := ""
	if saved_doc := r.FormValue("saved_doc"); saved_doc != "" {
		topMessage = fmt.Sprintf("Saved <a href='doc/%s'>doc %s</a>", saved_doc, saved_doc)
	}
	errorMessage := r.FormValue("error_message")

	limit := maxScan
	if limitparam := r.FormValue("limit"); limitparam != "" {
		newlimit, err := strconv.Atoi(limitparam)
		if err == nil {
			limit = newlimit
		}
	}

	var (
		movm         []MediaObjectVM
		searchedDocs []DocumentVM
	)
	tags := newSeparatedString(r.FormValue("tags"))
	docs, err := h.fetchDocuments(limit, searchOpts{tags: tags})
	if err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	if len(tags) != 0 {
		searchedDocs = MakeDocumentViewModels(docs)
		// We've just done a search, in which case we don't show the scans,
		// so no need to look for them.
	} else {
		// fetch media objects
		mediaObjects, err := h.fetchScans(limit)
		if err != nil {
			httputil.ServeError(w, r, err)
			return
		}
		movm = MakeMediaObjectViewModels(mediaObjects)
	}
	allTags, err := h.fetchTags()
	if err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	// fetch upcoming documents
	upcoming, err := h.fetchDocuments(maxDue, searchOpts{due: true})
	if err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	// fetch untagged documents
	untagged, err := h.fetchDocuments(limit, searchOpts{untagged: true})
	if err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	d := rootData{
		BaseURL:      baseURL(r),
		Tags:         tags,
		Media:        movm,
		SearchedDocs: searchedDocs,
		UntaggedDocs: MakeDocumentViewModels(untagged),
		UpcomingDocs: MakeDocumentViewModels(upcoming),
		TopMessage:   template.HTML(topMessage),
		ErrorMessage: errorMessage,
		AllTags:      allTags,
	}
	if err := rootTemplate.Execute(w, d); err != nil {
		logf("root template error: %v", err)
		httputil.ServeError(w, r, err)
		return
	}
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s%s", scheme, r.Host, app.PathPrefix(r))
}

func (h *handler) handleUploadURL(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "%supload", baseURL(r))
	return
}

func (h *handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != "POST" {
		http.Error(w, "not a POST", http.StatusMethodNotAllowed)
		return
	}

	mr, err := r.MultipartReader()
	if err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	var br blob.Ref
	var fileName string
	var creation time.Time
	cr := countingReader{}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			httputil.ServeError(w, r, err)
			return
		}
		name := part.FileName()
		if name == "" {
			// Let the user provide a creation time (for migrating Brad's data)
			if part.FormName() != "creation" {
				logf("form field %q provided in upload, but ignored", part.FormName())
				continue
			}
			creationParam, err := ioutil.ReadAll(part)
			if err != nil {
				httputil.ServeError(w, r, fmt.Errorf("could not read provided creation time: %v", err))
				return
			}
			creation, err = time.Parse(time.RFC3339, string(creationParam))
			if err != nil {
				httputil.ServeError(w, r, fmt.Errorf("could not parse provided creation time %q: %v", creationParam, err))
				return
			}

			continue
		}
		fileName = path.Base(name)
		cr.r = part
		br, err = h.cl.UploadFile(ctx, fileName, &cr, nil)
		if err != nil {
			httputil.ServeError(w, r, fmt.Errorf("could not write %v to blobserver: %v", fileName, err))
			return
		}
	}

	creationProvided := true
	if creation.IsZero() {
		creationProvided = false
		creation = time.Now()
	}

	scan, err := h.fetchScanByContent(br)
	if err == nil {
		if creationProvided {
			newScan := scan
			newScan.creation = creation
			if err := h.updateScan(ctx, scan.permanode, &newScan, &scan); err != nil {
				httputil.ServeError(w, r, fmt.Errorf("could not update scan: %v", err))
				return
			}
		}
		w.Write([]byte(scan.permanode.String()))
		return
	}
	if !os.IsNotExist(err) {
		httputil.ServeError(w, r, fmt.Errorf("could not check if scan with %v already exists: %v", fileName, err))
		return
	}

	scanRef, err := h.createScan(ctx, mediaObject{
		contentRef: br,
		creation:   creation,
	})

	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("could not create scan object for %v: %v", fileName, err))
		return
	}
	io.WriteString(w, scanRef.String())
}

type countingReader struct {
	hdr []byte
	n   int
	r   io.Reader
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if c.n < 1024 {
		c.hdr = append(c.hdr, p...)
	}
	c.n += n
	return n, err
}

func (c *countingReader) Mime() string {
	return magic.MIMEType(c.hdr)
}

func (h *handler) handleResource(w http.ResponseWriter, r *http.Request) {
	m := resourcePattern.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.Error(w, "invalid resource URL", http.StatusBadRequest)
		return
	}
	scanRef, ok := blob.Parse(m[1])
	if !ok {
		http.Error(w, fmt.Sprintf("invalid resource blobref: %q", m[1]), http.StatusBadRequest)
		return
	}

	mediaObject, err := h.fetchScan(scanRef)
	if err != nil {
		if err == os.ErrNotExist {
			http.Error(w, fmt.Sprintf("%v not found", scanRef), http.StatusNotFound)
			return
		}
		httputil.ServeError(w, r, fmt.Errorf("resource %v not found: %v", scanRef, err))
		return
	}

	// TODO(mpl): cache and thumbmeta
	ih := &camliserver.ImageHandler{
		Fetcher:   h.cl,
		MaxWidth:  search.MaxImageSize,
		MaxHeight: search.MaxImageSize,
		Square:    false,
		// TODO(mpl): make the image pkg default to the below when ResizeSem is nil
		ResizeSem: syncutil.NewSem(constants.DefaultMaxResizeMem),
	}

	if resizeParam := r.FormValue("resize"); resizeParam != "" {
		resized, err := strconv.Atoi(resizeParam)
		if err != nil {
			httputil.ServeError(w, r, fmt.Errorf("bogus resize param %q: %v", resizeParam, err))
			return
		}
		ih.MaxWidth = resized
		ih.MaxHeight = resized
	}
	ih.ServeHTTP(w, r, mediaObject.contentRef)
	return
}

func (h *handler) handleMakedoc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != "POST" {
		http.Error(w, "not a POST", http.StatusMethodNotAllowed)
		return
	}

	// gather the media_ids from the form into scanKeys
	if r.Form == nil {
		r.ParseMultipartForm(1)
	}
	refs := r.Form["blobref"]
	var pages []blob.Ref
	for _, ref := range refs {
		br, ok := blob.Parse(ref)
		if !ok {
			httputil.ServeError(w, r, fmt.Errorf("invalid page blobRef %q", ref))
			return
		}
		pages = append(pages, br)
	}

	var doc *document
	var err error
	doc, err = h.fetchDocumentByPages(pages)
	if err != nil {
		if !os.IsNotExist(err) {
			httputil.ServeError(w, r, fmt.Errorf("could not check if document already existed: %v", err))
			return
		}
		newDoc := document{
			pages:    pages,
			creation: time.Now(),
		}
		docRef, err := h.persistDocAndPages(ctx, newDoc)
		if err != nil {
			httputil.ServeError(w, r, fmt.Errorf("could not create new document: %v", err))
			return
		}
		newDoc.permanode = docRef
		doc = &newDoc

	}

	noUI := r.Form["noui"]
	if len(noUI) == 1 && noUI[0] == "1" {
		// For when we just want to get the doc's blobRef as a response.
		// Initially for migrating Brad's data, but can't hurt anyway.
		w.Write([]byte(doc.permanode.String()))
		return
	}
	http.Redirect(w, r, fmt.Sprintf("%s%s?size=1200", baseURL(r), doc.displayURL()), http.StatusFound)
}

func (h *handler) handleDoc(w http.ResponseWriter, r *http.Request) {
	urlFields := strings.Split(r.URL.Path, "/")
	if len(urlFields) < 3 {
		http.Error(w, "no document blobref", http.StatusBadRequest)
		return
	}

	docRef, ok := blob.Parse(urlFields[2])
	if !ok {
		http.Error(w, fmt.Sprintf("invalid document blobref: %q", urlFields[2]), http.StatusBadRequest)
		return
	}
	document, err := h.fetchDocument(docRef)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("%s?error_message=DocRef+%s+not+found", baseURL(r), docRef), http.StatusFound)
		return
	}

	var pages []mediaObject
	for _, v := range document.pages {
		// TODO(mpl): group fetch ?
		page, err := h.fetchScan(v)
		if err != nil {
			httputil.ServeError(w, r, fmt.Errorf("could not fetch page %v for document %v: %v", v, document.permanode, err))
			return
		}
		pages = append(pages, page)
	}
	var size int
	size = 1200
	if sizeParam := r.FormValue("size"); sizeParam != "" {
		sizeint, err := strconv.Atoi(sizeParam)
		if err != nil {
			httputil.ServeError(w, r, fmt.Errorf("invalid size param %q: %v", sizeParam, err))
			return
		}
		size = sizeint
	}
	show_single_list := size > 600

	allTags, err := h.fetchTags()
	if err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	d := struct {
		BaseURL        string
		Pages          []MediaObjectVM
		Doc            DocumentVM
		ShowSingleList bool
		Size           int
		AllTags        map[string]int
	}{
		BaseURL:        baseURL(r),
		Pages:          MakeMediaObjectViewModels(pages),
		Doc:            document.MakeViewModel(),
		ShowSingleList: show_single_list,
		Size:           size,
		AllTags:        allTags,
	}
	if err := docTemplate.Execute(w, d); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("could not serve doc template: %v", err))
		return
	}
}

func (h *handler) handleChangedoc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != "POST" {
		http.Error(w, "not a POST", http.StatusMethodNotAllowed)
		return
	}

	docRef, ok := blob.Parse(r.FormValue("docref"))
	if !ok {
		httputil.ServeError(w, r, fmt.Errorf("invalid document blobRef %q", r.FormValue("docref")))
		return
	}

	mode := r.FormValue("mode")
	if mode == "break" {
		if err := h.breakAndDeleteDoc(ctx, docRef); err != nil {
			httputil.ServeError(w, r, fmt.Errorf("could not delete document %v: %v", docRef, err))
			return
		}
		fmt.Fprintf(w, "<html><body>[&lt;&lt; <a href='%s'>Back</a>] Doc %s deleted and images broken out as un-annotated.</body></html>", baseURL(r), docRef)
		return
	}
	if mode == "delete" {
		if err := h.deleteDocAndImages(ctx, docRef); err != nil {
			httputil.ServeError(w, r, fmt.Errorf("could not do full delete of %v: %v", docRef, err))
			return
		}
		fmt.Fprintf(w, "<html><body>[&lt;&lt; <a href='%s'>Back</a>] Doc %s and its images deleted.</body></html>", baseURL(r), docRef)
		return
	}

	document := &document{}
	document.physicalLocation = r.FormValue("physical_location")
	document.title = r.FormValue("title")
	document.tags = newSeparatedString(r.FormValue("tags"))

	docDate, err := dateOrZero(r.FormValue("date"), dateformatYyyyMmDd)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("could not assign new date to document: %v", err))
		return
	}
	document.docDate = docDate

	duedate, err := dateOrZero(r.FormValue("due_date"), dateformatYyyyMmDd)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("could not assign new due date to document: %v", err))
		return
	}
	document.dueDate = duedate

	if err := h.updateDocument(ctx, docRef, document); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("could not update document %v: %v", docRef, err))
		return
	}

	http.Redirect(w, r, fmt.Sprintf("%s?saved_doc=%s", baseURL(r), docRef), http.StatusFound)
}

func handleRobots(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
}

func (h *handler) handleUiFile(w http.ResponseWriter, r *http.Request) {
	file := strings.TrimPrefix(r.URL.Path, "/ui/")

	root := h.uiFiles

	f, err := root.Open(file)
	if err != nil {
		http.NotFound(w, r)
		logf("Failed to open file %v from embedded resources: %v", file, err)
		return
	}
	defer f.Close()
	var modTime time.Time
	if fi, err := f.Stat(); err == nil {
		modTime = fi.ModTime()
	}
	if strings.HasSuffix(file, ".css") {
		w.Header().Set("Content-Type", "text/css")
	} else if strings.HasSuffix(file, ".js") {
		w.Header().Set("Content-Type", "application/javascript")
	}
	http.ServeContent(w, r, file, modTime, f.(io.ReadSeeker))
}
