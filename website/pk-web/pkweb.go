/*
Copyright 2011 The Perkeep Authors

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

package main // import "perkeep.org/website/pk-web"

import (
	"bytes"
	"crypto/rand"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	txttemplate "text/template"
	"time"

	"github.com/russross/blackfriday"
	"perkeep.org/doc"
	"perkeep.org/pkg/buildinfo"
	"perkeep.org/pkg/types/camtypes"
	"perkeep.org/website"
)

const (
	prodBucket = "camlistore-website-resource" // where we store misc resources for the production website
	prodDomain = "perkeep.org"
)

var h1TitlePattern = regexp.MustCompile(`<h1[^>]*>([^<]+)</h1>`)

var (
	httpAddr    = flag.String("http", defaultAddr(), "HTTP address.")
	root        = flag.String("root", "", "Website root (parent of 'static', 'content', and 'tmpl)")
	flagVersion = flag.Bool("version", false, "show version")
)

func defaultAddr() string {
	if inKnative() {
		return ":" + os.Getenv("PORT")
	}
	return ":31798"
}

func inKnative() bool {
	// https://cloud.google.com/run/docs/reference/container-contract#env-vars
	if os.Getenv("K_REVISION") != "" && os.Getenv("K_CONFIGURATION") != "" &&
		os.Getenv("K_SERVICE") != "" && os.Getenv("PORT") != "" {
		return true
	}
	return false
}

var (
	inProd bool

	pageHTML, errorHTML, camliErrorHTML *template.Template
	packageHTML                         *txttemplate.Template

	buildbotBackend, buildbotHost string

	// file extensions checked in order to satisfy file requests
	fileExtensions = []string{".md", ".html"}

	// files used to satisfy directory requests
	indexFiles = []string{"index.html", "README.md"}
)

var fmap = template.FuncMap{
	//	"":        textFmt,  // Used to work in Go 1.5
	"html":    htmlFmt,
	"htmlesc": htmlEscFmt,
}

// Template formatter for "html" format.
func htmlFmt(w io.Writer, format string, x ...interface{}) string {
	writeAny(w, true, x[0])
	return ""
}

// Template formatter for "htmlesc" format.
func htmlEscFmt(w io.Writer, format string, x ...interface{}) string {
	var buf bytes.Buffer
	writeAny(&buf, false, x[0])
	template.HTMLEscape(w, buf.Bytes())
	return ""
}

// Write anything to w; optionally html-escaped.
func writeAny(w io.Writer, html bool, x interface{}) {
	switch v := x.(type) {
	case []byte:
		writeText(w, v, html)
	case string:
		writeText(w, []byte(v), html)
	default:
		if html {
			var buf bytes.Buffer
			fmt.Fprint(&buf, x)
			writeText(w, buf.Bytes(), true)
		} else {
			fmt.Fprint(w, x)
		}
	}
}

// Write text to w; optionally html-escaped.
func writeText(w io.Writer, text []byte, html bool) {
	if html {
		template.HTMLEscape(w, text)
		return
	}
	w.Write(text)
}

func applyTemplate(t *template.Template, name string, data interface{}) []byte {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		log.Printf("%s.Execute: %s", name, err)
	}
	return buf.Bytes()
}

// goGetDomain returns one of the two domains that we serve for the "go-import"
// meta header
func goGetDomain(host string) string {
	if host == "camlistore.org" {
		return host
	}
	return "perkeep.org"
}

type pageParams struct {
	title    string // required
	subtitle string // used by pkg doc
	content  []byte // required
}

// pageTmplData is the template data passed to page.html.
type pageTmplData struct {
	Title    string
	Subtitle string
	Content  template.HTML

	// For the "go-import" meta header:
	GoImportDomain   string
	GoImportUpstream string
}

func servePage(w http.ResponseWriter, r *http.Request, params pageParams) {
	title, subtitle, content := params.title, params.subtitle, params.content
	// insert an "install command" if it applies
	if strings.Contains(title, cmdPattern) && subtitle != cmdPattern {
		toInsert := `
		<h3>Installation</h3>
		<pre>go get ` + prodDomain + `/cmd/` + subtitle + `</pre>
		<h3>Overview</h3><p>`
		content = bytes.Replace(content, []byte("<p>"), []byte(toInsert), 1)
	}
	domain := goGetDomain(r.Host) // camlistore.org or perkeep.org (anti-www redirects already happened)
	upstream := "https://github.com/perkeep/perkeep"
	if domain == "camlistore.org" {
		upstream = "https://github.com/camlistore/old-cam-snapshot"
	}
	if err := pageHTML.ExecuteTemplate(w, "page", &pageTmplData{
		Title:            title,
		Subtitle:         subtitle,
		Content:          template.HTML(content),
		GoImportDomain:   domain,
		GoImportUpstream: upstream,
	}); err != nil {
		log.Printf("godocHTML.Execute: %s", err)
	}
}

func readTemplate(name string) *template.Template {
	data, err := fs.ReadFile(website.Root, filepath.Join("tmpl", name))
	if err != nil {
		log.Fatalf("ReadFile tmpl/%s: %v", name, err)
	}
	t, err := template.New(name).Funcs(fmap).Parse(string(data))
	if err != nil {
		log.Fatalf("tmpl/%s: %v", name, err)
	}
	return t
}

func readTemplates() {
	pageHTML = readTemplate("page.html")
	errorHTML = readTemplate("error.html")
	camliErrorHTML = readTemplate("camlierror.html")
	// TODO(mpl): see about not using text template anymore?
	packageHTML = readTextTemplate("package.html")
}

func serveError(w http.ResponseWriter, r *http.Request, relpath string, err error) {
	contents := applyTemplate(errorHTML, "errorHTML", err) // err may contain an absolute path!
	w.WriteHeader(http.StatusNotFound)
	servePage(w, r, pageParams{
		title:   "File " + relpath,
		content: contents,
	})
}

const (
	viewCommitPrefix = "https://github.com/perkeep/perkeep/commit/"
	viewFilePrefix   = "https://github.com/perkeep/perkeep/blob/"
)

var commitHash = regexp.MustCompile(`^(?i)[0-9a-f]+$`)
var gitwebCommit = regexp.MustCompile(`^p=camlistore.git;a=commit;h=([0-9a-f]+)$`)

// empty return value means don't redirect.
func redirectPath(u *url.URL) string {
	// Redirect old gitweb URLs to gerrit. Example:
	// /code/?p=camlistore.git;a=commit;h=b0d2a8f0e5f27bbfc025a96ec3c7896b42d198ed
	if strings.HasPrefix(u.Path, "/code/") {
		m := gitwebCommit.FindStringSubmatch(u.RawQuery)
		if len(m) == 2 {
			return viewCommitPrefix + m[1]
		}
	}

	if strings.HasPrefix(u.Path, "/gw/") {
		path := strings.TrimPrefix(u.Path, "/gw/")
		if commitHash.MatchString(path) {
			// Assume it's a commit
			return viewCommitPrefix + path
		}
		return viewFilePrefix + "master/" + path
	}

	if strings.HasPrefix(u.Path, "/docs/") {
		return "/doc/" + strings.TrimPrefix(u.Path, "/docs/")
	}

	// strip directory index files
	for _, x := range indexFiles {
		if strings.HasSuffix(u.Path, "/"+x) {
			return strings.TrimSuffix(u.Path, x)
		}
	}

	// strip common file extensions
	for _, x := range fileExtensions {
		if strings.HasSuffix(u.Path, x) {
			return strings.TrimSuffix(u.Path, x)
		}
	}

	return ""
}

func mainHandler(rw http.ResponseWriter, req *http.Request) {
	if target := redirectPath(req.URL); target != "" {
		http.Redirect(rw, req, target, http.StatusFound)
		return
	}

	if dest, ok := issueRedirect(req.URL.Path); ok {
		http.Redirect(rw, req, dest, http.StatusFound)
		return
	}

	// try to serve godoc if requested path exists
	if req.URL.Path != "/" {
		if err := serveGodoc(rw, req); err == nil {
			return
		}
	}

	// TODO(bradfitz): move this/these to globals
	contentFS, err := fs.Sub(website.Root, "content")
	if err != nil {
		http.Error(rw, "bad content root", 500)
		return
	}
	findAndServeFile(rw, req, contentFS)
}

func docHandler(rw http.ResponseWriter, req *http.Request) {
	if target := redirectPath(req.URL); target != "" {
		http.Redirect(rw, req, target, http.StatusFound)
		return
	}

	findAndServeFile(rw, req, doc.Root)
}

// modtime is the modification time of the resource to be served, or IsZero().
// return value is whether this request is now complete.
func checkLastModified(w http.ResponseWriter, r *http.Request, modtime time.Time) bool {
	if modtime.IsZero() {
		return false
	}

	// The Date-Modified header truncates sub-second precision, so
	// use mtime < t+1s instead of mtime <= t to check for unmodified.
	if t, err := time.Parse(http.TimeFormat, r.Header.Get("If-Modified-Since")); err == nil && modtime.Before(t.Add(1*time.Second)) {
		h := w.Header()
		delete(h, "Content-Type")
		delete(h, "Content-Length")
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	w.Header().Set("Last-Modified", modtime.UTC().Format(http.TimeFormat))
	return false
}

// findAndServeFile finds the file in root to satisfy req.  This method will
// map URLs to exact filename matches, falling back to files ending in ".md" or
// ".html".  For example, a request for "/foo" may be served by a file named
// foo, foo.md, or foo.html.  Requests that map to directories may be served by
// an index.html or README.md file in that directory.
func findAndServeFile(rw http.ResponseWriter, req *http.Request, baseFS fs.FS) {
	relPath := strings.TrimSuffix(req.URL.Path[1:], "/") // serveFile URL paths start with '/'
	if strings.Contains(relPath, "..") {
		return
	}
	relPath = strings.TrimPrefix(relPath, "doc/")
	if relPath == "doc" {
		relPath = ""
	}

	var (
		fsPath string
		fi     os.FileInfo
		err    error
	)

	for _, ext := range append([]string{""}, fileExtensions...) {
		fsPath = relPath + ext
		if fsPath == "" {
			fsPath = "."
		}
		fi, err = fs.Stat(baseFS, fsPath)
		if err == nil || !os.IsNotExist(err) {
			break
		}
	}
	if err != nil {
		log.Print(err)
		serveError(rw, req, relPath, err)
		return
	}

	// If it's a directory without a trailing slash, redirect to
	// the URL with a trailing slash so relative links within that
	// directory work.
	if fi.IsDir() && !strings.HasSuffix(req.URL.Path, "/") {
		http.Redirect(rw, req, req.URL.Path+"/", http.StatusFound)
		return
	}
	// If it's a file with a trailing slash, redirect to the URL
	// without a trailing slash.
	if !fi.IsDir() && strings.HasSuffix(req.URL.Path, "/") {
		http.Redirect(rw, req, "/"+relPath, http.StatusFound)
		return
	}

	// if directory request, try to find an index file
	if fi.IsDir() {
		for _, index := range indexFiles {
			fileRel := strings.TrimPrefix(fsPath+"/"+index, "./")
			childFi, err := fs.Stat(baseFS, fileRel)
			if os.IsNotExist(err) {
				// didn't find this file, try the next
				continue
			}
			if err != nil {
				log.Print(err)
				serveError(rw, req, relPath, err)
				return
			}
			fi = childFi
			fsPath = fileRel
			break
		}
	}

	if fi.IsDir() {
		log.Printf("Error serving website content: %q is a directory", fsPath)
		serveError(rw, req, relPath, fmt.Errorf("error: %q is a directory", fsPath))
		return
	}

	if checkLastModified(rw, req, fi.ModTime()) {
		return
	}
	serveFile(rw, req, baseFS, fsPath)
}

// serveFile serves a file from disk, converting any markdown to HTML.
func serveFile(w http.ResponseWriter, r *http.Request, baseFS fs.FS, path string) {
	f, err := baseFS.Open(path)
	var fi fs.FileInfo
	if err == nil {
		defer f.Close()
		fi, err = f.Stat()
	}
	var data []byte
	if err == nil {
		data, err = io.ReadAll(f)
	}
	if err != nil {
		serveError(w, r, path, err)
		return
	}

	if !strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, ".md") {
		http.ServeContent(w, r, path, fi.ModTime(), bytes.NewReader(data))
		return
	}

	// AutoHeadingIDs is the only extension missing
	data = blackfriday.Run(data, blackfriday.WithExtensions(blackfriday.CommonExtensions|blackfriday.AutoHeadingIDs))

	title := ""
	if m := h1TitlePattern.FindSubmatch(data); len(m) > 1 {
		title = string(m[1])
	}

	servePage(w, r, pageParams{
		title:   title,
		content: data,
	})
}

// redirectRootHandler redirects users to strip off "www." prefixes
// and redirects http to https.
type redirectRootHandler struct {
	Handler http.Handler
}

func (h *redirectRootHandler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if goget := r.FormValue("go-get"); goget == "1" {
		// do not redirect on a go get request, because we want to be able to serve the
		// "go-import" meta for camlistore.org, and not just for perkeep.org
		h.Handler.ServeHTTP(rw, r)
		return
	}
	host := strings.ToLower(r.Host)
	if host == "www.camlistore.org" || host == "camlistore.org" ||
		host == "www."+prodDomain || (inProd && r.TLS == nil) {
		http.Redirect(rw, r, "https://"+prodDomain+r.URL.RequestURI(), http.StatusFound)
		return
	}
	h.Handler.ServeHTTP(rw, r)
}

// runAsChild runs res as a child process and
// does not wait for it to finish.
func runAsChild(res string) {
	cmdName, err := exec.LookPath(res)
	if err != nil {
		log.Fatalf("Could not find %v in $PATH: %v", res, err)
	}
	cmd := exec.Command(cmdName)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	log.Printf("Running %v", res)
	if err := cmd.Start(); err != nil {
		log.Fatalf("Program %v failed to start: %v", res, err)
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Fatalf("Program %s did not end successfully: %v", res, err)
		}
	}()
}

/*
var launchConfig = &cloudlaunch.Config{
	Name:         "camweb",
	BinaryBucket: prodBucket,
	GCEProjectID: "camlistore-website",
	Scopes: []string{
		storageapi.DevstorageFullControlScope,
		compute.ComputeScope,
		logging.WriteScope,
		datastore.ScopeDatastore,
		cloudresourcemanager.CloudPlatformScope,
	},
}
*/

func randHex(n int) string {
	buf := make([]byte, n/2+1)
	rand.Read(buf)
	return fmt.Sprintf("%x", buf)[:n]
}

// runDemoBlobServerContainer runs the demo blobserver as name in a docker
// container. It is not run in daemon mode, so it never returns if successful.
func runDemoBlobServerContainer() error {
	cmd := exec.Command("/bin/perkeepd",
		"--openbrowser=false",
		"--listen=:3179",
		"--configfile="+*root+"/blobserver-example/example-blobserver-config.json",
	)
	cmd.Env = append(os.Environ(),
		"CAMLI_ROOT="+*root+"/blobserver-example/root",
		"CAMLI_PASSWORD="+randHex(20),
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run demo blob server: %v", err)
	}
	return nil
}

func runDemoBlobserverLoop() {
	for {
		if err := runDemoBlobServerContainer(); err != nil {
			log.Printf("%v", err)
		}
		time.Sleep(10 * time.Second)
	}
}

var inContainer = os.Getenv("IN_PKWEB_CONTAINER") == "1"

func main() {
	flag.Parse()
	if *flagVersion {
		fmt.Fprintf(os.Stderr, "pk-web version: %s\nGo version: %s (%s/%s)\n",
			buildinfo.Summary(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	}
	if *root == "" {
		if inContainer {
			*root = "/perkeep/website"
		} else {
			var err error
			*root, err = os.Getwd()
			if err != nil {
				log.Fatalf("Failed to getwd: %v", err)
			}
		}
	}
	// ensure root is always a cleaned absolute path
	var err error
	*root, err = filepath.Abs(*root)
	if err != nil {
		log.Fatalf("Failed to get absolute path of root: %v", err)
	}

	readTemplates()
	if inContainer {
		go runDemoBlobserverLoop()
	}

	mux := http.DefaultServeMux
	staticFS, err := fs.Sub(website.Root, "static")
	if err != nil {
		log.Fatal(err)
	}
	talksFS, err := fs.Sub(website.Root, "talks")
	if err != nil {
		log.Fatal(err)
	}

	mux.Handle("/favicon.ico", http.FileServer(http.FS(staticFS)))
	mux.Handle("/robots.txt", http.FileServer(http.FS(staticFS)))
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.Handle("/talks/", http.StripPrefix("/talks/", http.FileServer(http.FS(talksFS))))
	mux.HandleFunc(errPattern, errHandler)

	// Google Webmaster Tools ownership proof:
	const webmasterToolsFile = "googlec74a9a91c9cfcd8c.html"
	mux.HandleFunc("/"+webmasterToolsFile, func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, webmasterToolsFile, time.Time{}, strings.NewReader("google-site-verification: googlec74a9a91c9cfcd8c.html"))
	})

	mux.HandleFunc("/r/", gerritRedirect)
	mux.HandleFunc("/dl/", releaseRedirect)
	mux.HandleFunc("/debug/uptime", uptimeHandler)
	mux.Handle("/doc/contributing", redirTo("/code#contributing"))
	mux.Handle("/lists", redirTo("/community"))

	mux.HandleFunc("/contributors", contribHandler())
	mux.HandleFunc("/doc/", docHandler)
	mux.HandleFunc("/", mainHandler)

	if buildbotHost != "" && buildbotBackend != "" {
		if _, err := url.Parse(buildbotBackend); err != nil {
			log.Fatalf("Failed to parse %v as a URL: %v", buildbotBackend, err)
		}
		bbhpattern := strings.TrimRight(buildbotHost, "/") + "/"
		mux.HandleFunc(bbhpattern, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, buildbotBackend, http.StatusFound)
		})
	}

	mux.HandleFunc("/launch/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "The Perkeep Cloud Launcher is no longer available.", 404)
	})

	var handler http.Handler = &redirectRootHandler{Handler: mux}

	httpServer := &http.Server{
		Addr:         *httpAddr,
		Handler:      handler,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 30 * time.Minute,
	}

	log.Fatal(httpServer.ListenAndServe())
}

var issueNum = regexp.MustCompile(`^/(?:issue|bug)s?(/\d*)?$`)

// issueRedirect returns whether the request should be redirected to the
// issues tracker, and the url for that redirection if yes, the empty
// string otherwise.
func issueRedirect(urlPath string) (string, bool) {
	m := issueNum.FindStringSubmatch(urlPath)
	if m == nil {
		return "", false
	}
	issueNumber := strings.TrimPrefix(m[1], "/")
	suffix := ""
	if issueNumber != "" {
		suffix = "/" + issueNumber
	}
	return "https://github.com/perkeep/perkeep/issues" + suffix, true
}

// gerritRedirect redirects /r/ to the old Gerrit reviews, and
// /r/NNNN to that particular old Gerrit review.
func gerritRedirect(w http.ResponseWriter, r *http.Request) {
	dest := "https://perkeep-review.googlesource.com"
	if len(r.URL.Path) > len("/r/") {
		dest += r.URL.Path
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// things in the camlistore-release bucket
var legacyDownloadBucket = map[string]bool{
	"0.10":       true,
	"0.9":        true,
	"android":    true,
	"djpeg":      true,
	"docker":     true,
	"monthly":    true,
	"README.txt": true,
}

func releaseRedirect(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/dl" || r.URL.Path == "/dl/" {
		http.Redirect(w, r, "https://"+prodDomain+"/download/", http.StatusFound)
		return
	}
	prefix := strings.TrimPrefix(r.URL.Path, "/dl/")
	firstDir := strings.Split(prefix, "/")[0]
	var dest string
	if legacyDownloadBucket[firstDir] {
		dest = "https://storage.googleapis.com/camlistore-release/" + prefix
	} else {
		dest = "https://storage.googleapis.com/perkeep-release/" + prefix
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

func redirTo(dest string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest, http.StatusFound)
	})
}

var startTime = time.Now()

func uptimeHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%v", time.Since(startTime).Round(time.Second/10))
}

const (
	errPattern  = "/err/"
	toHyperlink = `<a href="$1$2">$1$2</a>`
)

var camliURLPattern = regexp.MustCompile(`(https?://` + prodDomain + `)([a-zA-Z0-9\-\_/]+)?`)

func errHandler(w http.ResponseWriter, r *http.Request) {
	errString := strings.TrimPrefix(r.URL.Path, errPattern)

	defer func() {
		if x := recover(); x != nil {
			http.Error(w, fmt.Sprintf("unknown error: %v", errString), http.StatusNotFound)
		}
	}()
	err := camtypes.Err(errString)
	data := struct {
		Code        string
		Description template.HTML
	}{
		Code:        errString,
		Description: template.HTML(camliURLPattern.ReplaceAllString(err.Error(), toHyperlink)),
	}
	contents := applyTemplate(camliErrorHTML, "camliErrorHTML", data)
	w.WriteHeader(http.StatusFound)
	servePage(w, r, pageParams{
		title:   errString,
		content: contents,
	})
}
