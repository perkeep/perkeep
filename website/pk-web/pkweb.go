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
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
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

	"perkeep.org/pkg/buildinfo"
	"perkeep.org/pkg/types/camtypes"

	"github.com/russross/blackfriday"
	"golang.org/x/crypto/acme/autocert"
)

const (
	defaultAddr = ":31798"                      // default webserver address
	prodBucket  = "camlistore-website-resource" // where we store misc resources for the production website
	prodDomain  = "perkeep.org"
)

var h1TitlePattern = regexp.MustCompile(`<h1[^>]*>([^<]+)</h1>`)

var (
	httpAddr     = flag.String("http", defaultAddr, "HTTP address. If using Let's Encrypt, this server needs to be able to answer the http-01 challenge on port 80.")
	httpsAddr    = flag.String("https", "", "HTTPS address")
	root         = flag.String("root", "", "Website root (parent of 'static', 'content', and 'tmpl)")
	logDir       = flag.String("logdir", "", "Directory to write log files to (one per hour), or empty to not log.")
	logStdout    = flag.Bool("logstdout", true, "Whether to log to stdout")
	tlsCertFile  = flag.String("tlscert", "", "TLS cert file")
	tlsKeyFile   = flag.String("tlskey", "", "TLS private key file")
	alsoRun      = flag.String("also_run", "", "[optiona] Path to run as a child process. (Used to run perkeep.org's ./scripts/run-blob-server)")
	flagVersion  = flag.Bool("version", false, "show version")
	adminEmail   = flag.String("email", "", "Address that Let's Encrypt will notify about problems with issued certificates")
	shortLogFile = flag.String("gitlog-file", "", "If non-empty, the path to the `git log | git shortlog -sen output` to use. If empty, it's run as needed.")
)

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
func htmlFmt(w io.Writer, format string, x ...any) string {
	writeAny(w, true, x[0])
	return ""
}

// Template formatter for "htmlesc" format.
func htmlEscFmt(w io.Writer, format string, x ...any) string {
	var buf bytes.Buffer
	writeAny(&buf, false, x[0])
	template.HTMLEscape(w, buf.Bytes())
	return ""
}

// Write anything to w; optionally html-escaped.
func writeAny(w io.Writer, html bool, x any) {
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

func applyTemplate(t *template.Template, name string, data any) []byte {
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
	fileName := filepath.Join(*root, "tmpl", name)
	data, err := os.ReadFile(fileName)
	if err != nil {
		log.Fatalf("ReadFile %s: %v", fileName, err)
	}
	t, err := template.New(name).Funcs(fmap).Parse(string(data))
	if err != nil {
		log.Fatalf("%s: %v", fileName, err)
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

	if after, ok := strings.CutPrefix(u.Path, "/gw/"); ok {
		path := after
		if commitHash.MatchString(path) {
			// Assume it's a commit
			return viewCommitPrefix + path
		}
		return viewFilePrefix + "master/" + path
	}

	if after, ok := strings.CutPrefix(u.Path, "/docs/"); ok {
		return "/doc/" + after
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

	findAndServeFile(rw, req, filepath.Join(*root, "content"))
}

func docHandler(rw http.ResponseWriter, req *http.Request) {
	if target := redirectPath(req.URL); target != "" {
		http.Redirect(rw, req, target, http.StatusFound)
		return
	}

	findAndServeFile(rw, req, filepath.Dir(*root))
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
func findAndServeFile(rw http.ResponseWriter, req *http.Request, root string) {
	relPath := strings.TrimSuffix(req.URL.Path[1:], "/") // serveFile URL paths start with '/'
	if strings.Contains(relPath, "..") {
		return
	}

	var (
		absPath string
		fi      os.FileInfo
		err     error
	)

	for _, ext := range append([]string{""}, fileExtensions...) {
		absPath = filepath.Join(root, relPath+ext)
		fi, err = os.Lstat(absPath)
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
			childAbsPath := filepath.Join(root, relPath, index)
			childFi, err := os.Lstat(childAbsPath)
			if err != nil {
				if os.IsNotExist(err) {
					// didn't find this file, try the next
					continue
				}
				log.Print(err)
				serveError(rw, req, relPath, err)
				return
			}
			fi = childFi
			absPath = childAbsPath
			break
		}
	}

	if fi.IsDir() {
		log.Printf("Error serving website content: %q is a directory", absPath)
		serveError(rw, req, relPath, fmt.Errorf("error: %q is a directory", absPath))
		return
	}

	if checkLastModified(rw, req, fi.ModTime()) {
		return
	}
	serveFile(rw, req, absPath)
}

// serveFile serves a file from disk, converting any markdown to HTML.
func serveFile(w http.ResponseWriter, r *http.Request, absPath string) {
	if !strings.HasSuffix(absPath, ".html") && !strings.HasSuffix(absPath, ".md") {
		http.ServeFile(w, r, absPath)
		return
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		serveError(w, r, absPath, err)
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

// runDemoBlobServerContainer runs the demo blobserver as name in a docker
// container. It is not run in daemon mode, so it never returns if successful.
func runDemoBlobServerContainer(name string) error {
	// removeContainer(name)
	// cmd := exec.Command("docker", "run",
	// 	"--rm",
	// 	"--name="+name,
	// 	"-e", "CAMLI_ROOT="+prodSrcDir+"/website/blobserver-example/root",
	// 	"-e", "CAMLI_PASSWORD="+randHex(20),
	// 	"-v", pkSrcDir()+":"+prodSrcDir,
	// 	"--net=host",
	// 	"--workdir="+prodSrcDir,
	// 	"camlistore/demoblobserver",
	// 	"camlistored",
	// 	"--openbrowser=false",
	// 	"--listen=:3179",
	// 	"--configfile="+prodSrcDir+"/website/blobserver-example/example-blobserver-config.json")
	// stderr := &writerutil.PrefixSuffixSaver{N: 32 << 10}
	// cmd.Stderr = stderr
	// if err := cmd.Run(); err != nil {
	// 	return fmt.Errorf("failed to run demo blob server: %v, stderr: %v", err, string(stderr.Bytes()))
	// }
	return nil
}

func runDemoBlobserverLoop() {
	if runtime.GOOS != "linux" {
		return
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return
	}
	for {
		if err := runDemoBlobServerContainer("demoblob3179"); err != nil {
			log.Printf("%v", err)
		}
		if !inProd {
			// Do not bother retrying if we're most likely just testing on localhost
			return
		}
		time.Sleep(10 * time.Second)
	}
}

func main() {
	flag.Parse()
	if *flagVersion {
		fmt.Fprintf(os.Stderr, "pk-web version: %s\nGo version: %s (%s/%s)\n",
			buildinfo.Summary(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	}

	if *root == "" {
		var err error
		*root, err = os.Getwd()
		if err != nil {
			log.Fatalf("Failed to getwd: %v", err)
		}
	}
	// ensure root is always a cleaned absolute path
	var err error
	*root, err = filepath.Abs(*root)
	if err != nil {
		log.Fatalf("Failed to get absolute path of root: %v", err)
	}
	// calculate domain name we are serving packages for based on the directory we are serving from
	domainName = filepath.Base(filepath.Dir(*root))

	readTemplates()
	go runDemoBlobserverLoop()

	mux := http.DefaultServeMux
	mux.Handle("/favicon.ico", http.FileServer(http.Dir(filepath.Join(*root, "static"))))
	mux.Handle("/robots.txt", http.FileServer(http.Dir(filepath.Join(*root, "static"))))
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(*root, "static")))))
	mux.Handle("/talks/", http.StripPrefix("/talks/", http.FileServer(http.Dir(filepath.Join(*root, "talks")))))
	mux.HandleFunc(errPattern, errHandler)

	// Google Webmaster Tools ownership proof:
	const webmasterToolsFile = "googlec74a9a91c9cfcd8c.html"
	mux.HandleFunc("/"+webmasterToolsFile, func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, webmasterToolsFile, time.Time{}, strings.NewReader("google-site-verification: googlec74a9a91c9cfcd8c.html"))
	})

	mux.HandleFunc("/r/", gerritRedirect)
	mux.HandleFunc("/dl/", releaseRedirect)
	mux.HandleFunc("/debug/ip", ipHandler)
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
		http.Error(w, "GCE launcher no longer supported", 500)
	})

	var handler http.Handler = &redirectRootHandler{Handler: mux}
	if *logDir != "" || *logStdout {
		handler = NewLoggingHandler(handler, NewApacheLogger(*logDir, *logStdout))
	}

	if *alsoRun != "" {
		runAsChild(*alsoRun)
	}

	httpServer := &http.Server{
		Addr:         *httpAddr,
		Handler:      handler,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 30 * time.Minute,
	}

	httpsErr := make(chan error)
	go func() {
		httpsErr <- serve(httpServer, func(err error) {
			log.Fatalf("Error serving HTTP: %v", err)
		})
	}()

	log.Fatalf("Error serving HTTPS: %v", <-httpsErr)
}

// serve starts listening and serving for HTTP, and for HTTPS if it applies.
// onHTTPError, if non-nil, is called if there's a problem serving the HTTP
// (typically port 80) server. Any error from the HTTPS server is returned.
func serve(httpServer *http.Server, onHTTPError func(error)) error {
	if *httpsAddr == "" {
		log.Printf("Listening for HTTP on %v", *httpAddr)
		onHTTPError(httpServer.ListenAndServe())
		return nil
	}
	log.Printf("Starting TLS server on %s", *httpsAddr)
	httpsServer := &http.Server{
		Addr:              *httpsAddr,
		Handler:           httpServer.Handler,
		TLSConfig:         httpServer.TLSConfig,
		ReadTimeout:       httpServer.ReadTimeout,
		ReadHeaderTimeout: httpServer.ReadHeaderTimeout,
		WriteTimeout:      httpServer.WriteTimeout,
		IdleTimeout:       httpServer.IdleTimeout,
		MaxHeaderBytes:    httpServer.MaxHeaderBytes,
		TLSNextProto:      httpServer.TLSNextProto,
		ConnState:         httpServer.ConnState,
		ErrorLog:          httpServer.ErrorLog,
		BaseContext:       httpServer.BaseContext,
		ConnContext:       httpServer.ConnContext,
	}
	cacheDir := autocert.DirCache("letsencrypt.cache")
	var hostPolicy autocert.HostPolicy
	if !inProd {
		if *tlsCertFile != "" && *tlsKeyFile != "" {
			go func() {
				log.Printf("Listening for HTTP on %v", *httpAddr)
				onHTTPError(httpServer.ListenAndServe())
			}()
			return httpsServer.ListenAndServeTLS(*tlsCertFile, *tlsKeyFile)
		}
		// Otherwise use Let's Encrypt, i.e. same use case as in prod
		if strings.HasPrefix(*httpsAddr, ":") {
			return errors.New("for Let's Encrypt, -https needs to start with a host name")
		}
		host, _, err := net.SplitHostPort(*httpsAddr)
		if err != nil {
			return err
		}
		hostPolicy = autocert.HostWhitelist(host)
	} else {
		hostPolicy = autocert.HostWhitelist(prodDomain, "www."+prodDomain,
			"www.camlistore.org", "camlistore.org")
		cacheDir = autocert.DirCache("/var/le/letsencrypt.cache")
	}
	m := autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: hostPolicy,
		Cache:      cacheDir,
	}
	go func() {
		log.Printf("Listening for HTTP on %v", *httpAddr)
		onHTTPError(http.ListenAndServe(*httpAddr, m.HTTPHandler(httpServer.Handler)))
	}()
	if *adminEmail != "" {
		m.Email = *adminEmail
	}
	httpsServer.TLSConfig = m.TLSConfig()
	log.Printf("Listening for HTTPS on %v", *httpsAddr)
	ln, err := net.Listen("tcp", *httpsAddr)
	if err != nil {
		return err
	}
	return httpsServer.Serve(tls.NewListener(tcpKeepAliveListener{ln.(*net.TCPListener)}, httpsServer.TLSConfig))
}

type tcpKeepAliveListener struct {
	*net.TCPListener
}

func (ln tcpKeepAliveListener) Accept() (c net.Conn, err error) {
	tc, err := ln.AcceptTCP()
	if err != nil {
		return
	}
	tc.SetKeepAlive(true)
	tc.SetKeepAlivePeriod(3 * time.Minute)
	return tc, nil
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

func ipHandler(w http.ResponseWriter, r *http.Request) {
	out, _ := exec.Command("ip", "-f", "inet", "addr", "show", "dev", "eth0").Output()
	str := string(out)
	pos := strings.Index(str, "inet ")
	if pos == -1 {
		return
	}
	str = str[pos+5:]
	pos = strings.Index(str, "/")
	if pos == -1 {
		return
	}
	str = str[:pos]
	w.Write([]byte(str))
}

var startTime = time.Now()

func uptimeHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%v", time.Since(startTime))
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
