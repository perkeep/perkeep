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
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	txttemplate "text/template"
	"time"
)

const defaultAddr = ":31798" // default webserver address

var h1TitlePattern = regexp.MustCompile(`<h1>([^<]+)</h1>`)

var (
	httpAddr        = flag.String("http", defaultAddr, "HTTP service address (e.g., '"+defaultAddr+"')")
	httpsAddr       = flag.String("https", "", "HTTPS service address")
	root            = flag.String("root", "", "Website root (parent of 'static', 'content', and 'tmpl")
	logDir          = flag.String("logdir", "", "Directory to write log files to (one per hour), or empty to not log.")
	logStdout       = flag.Bool("logstdout", true, "Write to stdout?")
	tlsCertFile     = flag.String("tlscert", "", "TLS cert file")
	tlsKeyFile      = flag.String("tlskey", "", "TLS private key file")
	buildbotBackend = flag.String("buildbot_backend", "", "Build bot status backend URL")
	buildbotHost    = flag.String("buildbot_host", "", "Hostname to map to the buildbot_backend. If an HTTP request with this hostname is received, it proxies to buildbot_backend.")
	alsoRun         = flag.String("also_run", "", "Optional path to run as a child process. (Used to run camlistore.org's ./scripts/run-blob-server)")

	pageHtml, errorHtml *template.Template
	packageHTML         *txttemplate.Template
)

var fmap = template.FuncMap{
	"":        textFmt,
	"html":    htmlFmt,
	"htmlesc": htmlEscFmt,
}

// Template formatter for "" (default) format.
func textFmt(w io.Writer, format string, x ...interface{}) string {
	writeAny(w, false, x[0])
	return ""
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

func servePage(w http.ResponseWriter, title, subtitle string, content []byte) {
	// insert an "install command" if it applies
	if strings.Contains(title, cmdPattern) && subtitle != cmdPattern {
		toInsert := `
		<h3>Installation</h3>
		<pre>go get camlistore.org/cmd/` + subtitle + `</pre>
		<h3>Overview</h3><p>`
		content = bytes.Replace(content, []byte("<p>"), []byte(toInsert), 1)
	}
	d := struct {
		Title    string
		Subtitle string
		Content  template.HTML
	}{
		title,
		subtitle,
		template.HTML(content),
	}

	if err := pageHtml.Execute(w, &d); err != nil {
		log.Printf("godocHTML.Execute: %s", err)
	}
}

func readTemplate(name string) *template.Template {
	fileName := filepath.Join(*root, "tmpl", name)
	data, err := ioutil.ReadFile(fileName)
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
	pageHtml = readTemplate("page.html")
	errorHtml = readTemplate("error.html")
	// TODO(mpl): see about not using text template anymore?
	packageHTML = readTextTemplate("package.html")
}

func serveError(w http.ResponseWriter, r *http.Request, relpath string, err error) {
	contents := applyTemplate(errorHtml, "errorHtml", err) // err may contain an absolute path!
	w.WriteHeader(http.StatusNotFound)
	servePage(w, "File "+relpath, "", contents)
}

const gerritURLPrefix = "https://camlistore.googlesource.com/camlistore/+/"

var commitHash = regexp.MustCompile(`^p=camlistore.git;a=commit;h=([0-9a-f]+)$`)

// empty return value means don't redirect.
func redirectPath(u *url.URL) string {
	// Example:
	// /code/?p=camlistore.git;a=commit;h=b0d2a8f0e5f27bbfc025a96ec3c7896b42d198ed
	if strings.HasPrefix(u.Path, "/code/") {
		m := commitHash.FindStringSubmatch(u.RawQuery)
		if len(m) == 2 {
			return gerritURLPrefix + m[1]
		}
	}

	if strings.HasPrefix(u.Path, "/gw/") {
		path := strings.TrimPrefix(u.Path, "/gw/")
		if strings.HasPrefix(path, "doc") || strings.HasPrefix(path, "clients") {
			return gerritURLPrefix + "master/" + path
		}
		// Assume it's a commit
		return gerritURLPrefix + path
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

	relPath := req.URL.Path[1:] // serveFile URL paths start with '/'
	if strings.Contains(relPath, "..") {
		return
	}

	absPath := filepath.Join(*root, "content", relPath)
	fi, err := os.Lstat(absPath)
	if err != nil {
		log.Print(err)
		serveError(rw, req, relPath, err)
		return
	}
	if fi.IsDir() {
		relPath += "/index.html"
		absPath = filepath.Join(*root, "content", relPath)
		fi, err = os.Lstat(absPath)
		if err != nil {
			log.Print(err)
			serveError(rw, req, relPath, err)
			return
		}
	}

	if !fi.IsDir() {
		if checkLastModified(rw, req, fi.ModTime()) {
			return
		}
		serveFile(rw, req, relPath, absPath)
	}
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

func serveFile(rw http.ResponseWriter, req *http.Request, relPath, absPath string) {
	data, err := ioutil.ReadFile(absPath)
	if err != nil {
		serveError(rw, req, absPath, err)
		return
	}

	title := ""
	if m := h1TitlePattern.FindSubmatch(data); len(m) > 1 {
		title = string(m[1])
	}

	servePage(rw, title, "", data)
}

func isBot(r *http.Request) bool {
	agent := r.Header.Get("User-Agent")
	return strings.Contains(agent, "Baidu") || strings.Contains(agent, "bingbot") ||
		strings.Contains(agent, "Ezooms") || strings.Contains(agent, "Googlebot")
}

type noWwwHandler struct {
	Handler http.Handler
}

func (h *noWwwHandler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	// Some bots (especially Baidu) don't seem to respect robots.txt and swamp gitweb.cgi,
	// so explicitly protect it from bots.
	if ru := r.URL.RequestURI(); strings.Contains(ru, "/code/") && strings.Contains(ru, "?") && isBot(r) {
		http.Error(rw, "bye", http.StatusUnauthorized)
		log.Printf("bot denied")
		return
	}

	host := strings.ToLower(r.Host)
	if host == "www.camlistore.org" {
		http.Redirect(rw, r, "http://camlistore.org"+r.URL.RequestURI(), http.StatusFound)
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

func main() {
	flag.Parse()

	if *root == "" {
		var err error
		*root, err = os.Getwd()
		if err != nil {
			log.Fatalf("Failed to getwd: %v", err)
		}
	}
	readTemplates()

	mux := http.DefaultServeMux
	mux.Handle("/favicon.ico", http.FileServer(http.Dir(filepath.Join(*root, "static"))))
	mux.Handle("/robots.txt", http.FileServer(http.Dir(filepath.Join(*root, "static"))))
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(*root, "static")))))
	mux.Handle("/talks/", http.StripPrefix("/talks/", http.FileServer(http.Dir(filepath.Join(*root, "talks")))))
	mux.Handle(pkgPattern, godocHandler{})
	mux.Handle(cmdPattern, godocHandler{})

	mux.HandleFunc("/r/", gerritRedirect)
	mux.HandleFunc("/debugz/ip", ipHandler)

	mux.HandleFunc("/", mainHandler)

	if *buildbotHost != "" && *buildbotBackend != "" {
		buildbotUrl, err := url.Parse(*buildbotBackend)
		if err != nil {
			log.Fatalf("Failed to parse %v as a URL: %v", *buildbotBackend, err)
		}
		buildbotHandler := httputil.NewSingleHostReverseProxy(buildbotUrl)
		bbhpattern := strings.TrimRight(*buildbotHost, "/") + "/"
		mux.Handle(bbhpattern, buildbotHandler)
	}

	var handler http.Handler = &noWwwHandler{Handler: mux}
	if *logDir != "" || *logStdout {
		handler = NewLoggingHandler(handler, *logDir, *logStdout)
	}

	errc := make(chan error)
	startEmailCommitLoop(errc)

	if *alsoRun != "" {
		runAsChild(*alsoRun)
	}

	httpServer := &http.Server{
		Addr:         *httpAddr,
		Handler:      handler,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 30 * time.Minute,
	}
	go func() {
		errc <- httpServer.ListenAndServe()
	}()

	if *httpsAddr != "" {
		log.Printf("Starting TLS server on %s", *httpsAddr)
		httpsServer := new(http.Server)
		*httpsServer = *httpServer
		httpsServer.Addr = *httpsAddr
		go func() {
			errc <- httpsServer.ListenAndServeTLS(*tlsCertFile, *tlsKeyFile)
		}()
	}

	log.Fatalf("Serve error: %v", <-errc)
}

var issueNum = regexp.MustCompile(`^/(?:issue(?:s)?|bugs)(/\d*)?$`)

// issueRedirect returns whether the request should be redirected to the
// issues tracker, and the url for that redirection if yes, the empty
// string otherwise.
func issueRedirect(urlPath string) (string, bool) {
	m := issueNum.FindStringSubmatch(urlPath)
	if m == nil {
		return "", false
	}
	issueNumber := strings.TrimPrefix(m[1], "/")
	suffix := "list"
	if issueNumber != "" {
		suffix = "detail?id=" + issueNumber
	}
	return "https://code.google.com/p/camlistore/issues/" + suffix, true
}

func gerritRedirect(w http.ResponseWriter, r *http.Request) {
	dest := "https://camlistore-review.googlesource.com/"
	if len(r.URL.Path) > len("/r/") {
		dest += r.URL.Path[1:]
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// Not sure what's making these broken URLs like:
//
//   http://localhost:8080/code/?p=camlistore.git%3Bf=doc/json-signing/json-signing.txt%3Bhb=master
//
// ... but something is.  Maybe Buzz?  For now just re-write them
// . Doesn't seem to be a bug in the CGI implementation, though, which
// is what I'd originally suspected.
/*
func (fu *fixUpGitwebUrls) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	oldUrl := req.URL.String()
	newUrl := strings.Replace(oldUrl, "%3B", ";", -1)
	if newUrl == oldUrl {
		fu.handler.ServeHTTP(rw, req)
		return
	}
	http.Redirect(rw, req, newUrl, http.StatusFound)
}
*/

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
