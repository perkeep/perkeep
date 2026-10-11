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
	"bufio"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	uistatic "perkeep.org/app/pdfcabinet/ui"
	"perkeep.org/internal/httputil"
	"perkeep.org/internal/magic"
	"perkeep.org/pkg/app"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
)

const (
	maxPdf = 50 // number of pdfs fetched/displayed. arbitrary.
	maxDue = 30 // number of due documents fetched

	documentNodeType = "pdfcabinet:doc"
)

var (
	rootTemplate = template.Must(template.New("root").Parse(rootHTML))
	docTemplate  = template.Must(template.New("doc").Parse(docHTML))
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
	if config != nil && config.SourceRoot != "" {
		logf("Using UI resources (HTML, JS, CSS) from disk, under %v", config.SourceRoot)
		h.uiFiles = os.DirFS(config.SourceRoot)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleRoot)
	mux.HandleFunc("/ui/", h.handleUiFile)
	mux.HandleFunc("/uploadurl", h.handleUploadURL)
	mux.HandleFunc("/upload", h.handleUpload)
	mux.HandleFunc("/doc/", h.handleDoc)
	mux.HandleFunc("/pdf/", h.handlePDF)
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
	w.Header().Set("WWW-Authenticate", "Basic realm=pdf cabinet")
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
	if savedDoc, ok := blob.Parse(r.FormValue("saved_doc")); ok {
		topMessage = fmt.Sprintf("Saved <a href='doc/%s'>doc %s</a>", savedDoc, savedDoc)
	}
	errorMessage := r.FormValue("error_message")

	limit := maxPdf
	if limitparam := r.FormValue("limit"); limitparam != "" {
		newlimit, err := strconv.Atoi(limitparam)
		if err == nil {
			limit = newlimit
		}
	}

	var searchedDocs []DocumentVM
	tags := newSeparatedString(r.FormValue("tags"))
	docs, err := h.fetchDocuments(limit, searchOpts{tags: tags})
	if err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	if len(tags) != 0 {
		searchedDocs = MakeDocumentViewModels(docs)
		// We've just done a search, in which case we don't show the pdfs,
		// so no need to look for them.
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
			continue
		}
		fileName := path.Base(name)
		bufr := bufio.NewReader(part)
		hdr, _ := bufr.Peek(1024)
		if magic.MIMEType(hdr) != "application/pdf" {
			http.Error(w, fmt.Sprintf("%v is not a PDF", fileName), http.StatusBadRequest)
			return
		}
		br, err := h.cl.UploadFile(ctx, fileName, bufr, nil)
		if err != nil {
			httputil.ServeError(w, r, fmt.Errorf("could not write %v to blobserver: %v", fileName, err))
			return
		}

		pdf, err := h.fetchDocumentByPDF(br)
		if err == nil {
			w.Write([]byte(pdf.permanode.String()))
			return
		}
		if !os.IsNotExist(err) {
			httputil.ServeError(w, r, fmt.Errorf("could not check if pdf with %v already exists: %v", fileName, err))
			return
		}

		docRef, err := h.createDocument(ctx, document{
			pdf:      br,
			creation: time.Now(),
			title:    strings.TrimSuffix(fileName, path.Ext(fileName)),
		})
		if err != nil {
			httputil.ServeError(w, r, fmt.Errorf("could not create document for %v: %v", fileName, err))
			return
		}
		io.WriteString(w, docRef.String())
		return
	}
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

	allTags, err := h.fetchTags()
	if err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	d := struct {
		BaseURL string
		Doc     DocumentVM
		AllTags map[string]int
	}{
		BaseURL: baseURL(r),
		Doc:     document.MakeViewModel(),
		AllTags: allTags,
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

	if r.FormValue("mode") == "delete" {
		if err := h.deleteNode(ctx, docRef); err != nil {
			httputil.ServeError(w, r, fmt.Errorf("could not delete document %v: %v", docRef, err))
			return
		}
		fmt.Fprintf(w, "<html><body>[&lt;&lt; <a href='%s'>Back</a>] Doc %s deleted.</body></html>", baseURL(r), docRef)
		return
	}

	document := &document{}
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

// handlePDF serves the contents of the PDF file of a document, as
// /pdf/<file blobref>.
func (h *handler) handlePDF(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimPrefix(r.URL.Path, "/pdf/")
	pdfRef, ok := blob.Parse(ref)
	if !ok {
		http.Error(w, fmt.Sprintf("invalid pdf blobref: %q", ref), http.StatusBadRequest)
		return
	}
	// Only serve files that are the content of a document.
	if _, err := h.fetchDocumentByPDF(pdfRef); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		httputil.ServeError(w, r, err)
		return
	}
	fr, err := schema.NewFileReader(r.Context(), h.cl, pdfRef)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("could not read pdf %v: %v", pdfRef, err))
		return
	}
	defer fr.Close()
	w.Header().Set("Content-Type", "application/pdf")
	http.ServeContent(w, r, fr.FileName(), fr.ModTime(), fr)
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
