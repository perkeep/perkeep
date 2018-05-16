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
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
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

	"perkeep.org/internal/netutil"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/deploy/gce"
	"perkeep.org/pkg/types/camtypes"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/datastore"
	"cloud.google.com/go/logging"
	"cloud.google.com/go/storage"
	"github.com/mailgun/mailgun-go"
	"github.com/russross/blackfriday"
	"go4.org/cloud/cloudlaunch"
	"go4.org/writerutil"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/oauth2/google"
	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v1"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
	storageapi "google.golang.org/api/storage/v1"
)

const (
	defaultAddr = ":31798"                      // default webserver address
	prodBucket  = "camlistore-website-resource" // where we store misc resources for the production website
	prodDomain  = "perkeep.org"
)

var h1TitlePattern = regexp.MustCompile(`<h1[^>]*>([^<]+)</h1>`)

var (
	httpAddr    = flag.String("http", defaultAddr, "HTTP address. If using Let's Encrypt, this server needs to be able to answer the http-01 challenge on port 80.")
	httpsAddr   = flag.String("https", "", "HTTPS address")
	root        = flag.String("root", "", "Website root (parent of 'static', 'content', and 'tmpl)")
	logDir      = flag.String("logdir", "", "Directory to write log files to (one per hour), or empty to not log.")
	logStdout   = flag.Bool("logstdout", true, "Whether to log to stdout")
	tlsCertFile = flag.String("tlscert", "", "TLS cert file")
	tlsKeyFile  = flag.String("tlskey", "", "TLS private key file")
	alsoRun     = flag.String("also_run", "", "[optiona] Path to run as a child process. (Used to run perkeep.org's ./scripts/run-blob-server)")
	devMode     = flag.Bool("dev", false, "in dev mode")
	flagStaging = flag.Bool("staging", false, "Deploy to a test GCE instance. Requires -cloudlaunch=true")

	gceProjectID = flag.String("gce_project_id", "", "GCE project ID; required if not running on GCE and gce_log_name is specified.")
	gceLogName   = flag.String("gce_log_name", "", "GCE Cloud Logging log name; if non-empty, logs go to Cloud Logging instead of Apache-style local disk log files")
	gceJWTFile   = flag.String("gce_jwt_file", "", "If non-empty, a filename to the GCE Service Account's JWT (JSON) config file.")
	gitContainer = flag.Bool("git_container", false, "Use git from the `camlistore/git` Docker container; if false, the system `git` is used.")

	adminEmail = flag.String("email", "", "Address that Let's Encrypt will notify about problems with issued certificates")
)

const (
	stagingInstName = "camweb-staging" // name of the GCE instance when testing
	stagingHostname = "staging.camlistore.net"
)

var (
	inProd bool
	// inStaging is whether this instance is the staging server. This should only be true
	// if inProd is also true - they are not mutually exclusive; staging is still prod -
	// because we want to test the same code paths as in production. The code then runs
	// on another GCE instance, and on the stagingHostname host.
	inStaging bool

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
	upstream := "https://camlistore.googlesource.com/camlistore"
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

const gerritURLPrefix = "https://camlistore.googlesource.com/camlistore/+/"

var commitHash = regexp.MustCompile(`^(?i)[0-9a-f]+$`)
var gitwebCommit = regexp.MustCompile(`^p=camlistore.git;a=commit;h=([0-9a-f]+)$`)

// empty return value means don't redirect.
func redirectPath(u *url.URL) string {
	// Redirect old gitweb URLs to gerrit. Example:
	// /code/?p=camlistore.git;a=commit;h=b0d2a8f0e5f27bbfc025a96ec3c7896b42d198ed
	if strings.HasPrefix(u.Path, "/code/") {
		m := gitwebCommit.FindStringSubmatch(u.RawQuery)
		if len(m) == 2 {
			return gerritURLPrefix + m[1]
		}
	}

	if strings.HasPrefix(u.Path, "/gw/") {
		path := strings.TrimPrefix(u.Path, "/gw/")
		if commitHash.MatchString(path) {
			// Assume it's a commit
			return gerritURLPrefix + path
		}
		return gerritURLPrefix + "master/" + path
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

	data, err := ioutil.ReadFile(absPath)
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
		if inStaging {
			http.Redirect(rw, r, "https://"+stagingHostname+r.URL.RequestURI(), http.StatusFound)
			return
		}
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

func gceDeployHandlerConfig() (*gce.Config, error) {
	if inProd {
		return deployerCredsFromGCS()
	}
	clientId := os.Getenv("CAMLI_GCE_CLIENTID")
	if clientId != "" {
		return &gce.Config{
			ClientID:       clientId,
			ClientSecret:   os.Getenv("CAMLI_GCE_CLIENTSECRET"),
			Project:        os.Getenv("CAMLI_GCE_PROJECT"),
			ServiceAccount: os.Getenv("CAMLI_GCE_SERVICE_ACCOUNT"),
			DataDir:        os.Getenv("CAMLI_GCE_DATA"),
		}, nil
	}
	configDir, err := osutil.PerkeepConfigDir()
	if err != nil {
		return nil, err
	}
	configFile := filepath.Join(configDir, "launcher-config.json")
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("error reading launcher-config.json (expected of type https://godoc.org/"+prodDomain+"/pkg/deploy/gce#Config): %v", err)
	}
	var config gce.Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

// gceDeployHandler returns an http.Handler for a GCE launcher,
// configured to run at /prefix/ (the trailing slash can be omitted).
// The launcher is not initialized if:
// - in production, the launcher-config.json file is not found in the relevant bucket
// - neither CAMLI_GCE_CLIENTID is set, nor launcher-config.json is found in the
// camlistore server config dir.
func gceDeployHandler(prefix string) (*gce.DeployHandler, error) {
	var hostPort string
	var err error
	scheme := "https"
	if inProd {
		if inStaging {
			hostPort = stagingHostname + ":443"
		} else {
			hostPort = prodDomain + ":443"
		}
	} else {
		addr := *httpsAddr
		if *devMode && *httpsAddr == "" {
			addr = *httpAddr
			scheme = "http"
		}
		hostPort, err = netutil.ListenHostPort(addr)
		if err != nil {
			// the deploy handler needs to know its own
			// hostname or IP for the oauth2 callback.
			return nil, fmt.Errorf("invalid -https flag: %v", err)
		}
	}
	config, err := gceDeployHandlerConfig()
	if config == nil {
		return nil, err
	}
	gceh, err := gce.NewDeployHandlerFromConfig(hostPort, prefix, config)
	if err != nil {
		return nil, fmt.Errorf("NewDeployHandlerFromConfig: %v", err)
	}

	pageBytes, err := ioutil.ReadFile(filepath.Join(*root, "tmpl", "page.html"))
	if err != nil {
		return nil, err
	}
	if err := gceh.AddTemplateTheme(string(pageBytes)); err != nil {
		return nil, fmt.Errorf("AddTemplateTheme: %v", err)
	}
	gceh.SetScheme(scheme)
	log.Printf("Starting Perkeep launcher on %s://%s%s", scheme, hostPort, prefix)
	return gceh, nil
}

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

func checkInProduction() bool {
	if !metadata.OnGCE() {
		return false
	}
	proj, _ := metadata.ProjectID()
	inst, _ := metadata.InstanceName()
	log.Printf("Running on GCE: %v / %v", proj, inst)
	prod := proj == "camlistore-website" && inst == "camweb" || inst == stagingInstName
	inStaging = prod && inst == stagingInstName
	return prod
}

const (
	prodSrcDir     = "/var/camweb/src/" + prodDomain
	prodLECacheDir = "/var/le/letsencrypt.cache"
)

func setProdFlags() {
	inProd = checkInProduction()
	if !inProd {
		return
	}
	if *devMode {
		log.Fatal("can't use dev mode in production")
	}
	log.Printf("Running in production; configuring prod flags & containers")
	*httpAddr = ":80"
	*httpsAddr = ":443"
	// TODO(mpl): investigate why this proxying does not seem to be working (we end up on https://camlistore.org).
	buildbotBackend = "https://travis-ci.org/camlistore/camlistore"
	buildbotHost = "build.camlistore.org"
	*gceLogName = "camweb-access-log"
	if inStaging {
		*gceLogName += "-staging"
	}
	*root = filepath.Join(prodSrcDir, "website")
	*gitContainer = true

	*adminEmail = "mathieu.lonjaret@gmail.com" // for let's encrypt
	*emailsTo = "camlistore-commits@googlegroups.com"
	*mailgunCfgFile = "mailgun-config.json"
	if inStaging {
		// in staging, keep emailsTo so we get in the loop that does the
		// git pull and refreshes the content, but no mailgunCfgFile so
		// we don't actually try to send any e-mail.
		*mailgunCfgFile = ""
	}

	os.RemoveAll(prodSrcDir)
	if err := os.MkdirAll(prodSrcDir, 0755); err != nil {
		log.Fatal(err)
	}
	log.Printf("fetching git docker image...")
	getDockerImage("camlistore/git", "docker-git.tar.gz")
	getDockerImage("camlistore/demoblobserver", "docker-demoblobserver.tar.gz")

	log.Printf("cloning camlistore git tree...")
	cloneArgs := []string{
		"run",
		"--rm",
		"-v", "/var/camweb:/var/camweb",
		"camlistore/git",
		"git",
		"clone",
	}
	if inStaging {
		// We work off the staging branch, so we stay in control of the
		// website contents, regardless of which commits are landing on the
		// master branch in the meantime.
		cloneArgs = append(cloneArgs, "-b", "staging", "https://github.com/perkeep/perkeep.git", prodSrcDir)
	} else {
		cloneArgs = append(cloneArgs, "https://camlistore.googlesource.com/camlistore", prodSrcDir)
	}
	out, err := exec.Command("docker", cloneArgs...).CombinedOutput()
	if err != nil {
		log.Fatalf("git clone: %v, %s", err, out)
	}
	os.Chdir(*root)
	log.Printf("Starting.")
	sendStartingEmail()
}

func randHex(n int) string {
	buf := make([]byte, n/2+1)
	rand.Read(buf)
	return fmt.Sprintf("%x", buf)[:n]
}

func removeContainer(name string) {
	if err := exec.Command("docker", "kill", name).Run(); err == nil {
		// It was actually running.
		log.Printf("Killed old %q container.", name)
	}
	if err := exec.Command("docker", "rm", name).Run(); err == nil {
		// Always try to remove, in case we end up with a stale,
		// non-running one (which has happened in the past).
		log.Printf("Removed old %q container.", name)
	}
}

// runDemoBlobServerContainer runs the demo blobserver as name in a docker
// container. It is not run in daemon mode, so it never returns if successful.
func runDemoBlobServerContainer(name string) error {
	removeContainer(name)
	cmd := exec.Command("docker", "run",
		"--rm",
		"--name="+name,
		"-e", "CAMLI_ROOT="+prodSrcDir+"/website/blobserver-example/root",
		"-e", "CAMLI_PASSWORD="+randHex(20),
		"-v", pkSrcDir()+":"+prodSrcDir,
		"--net=host",
		"--workdir="+prodSrcDir,
		"camlistore/demoblobserver",
		"camlistored",
		"--openbrowser=false",
		"--listen=:3179",
		"--configfile="+prodSrcDir+"/website/blobserver-example/example-blobserver-config.json")
	stderr := &writerutil.PrefixSuffixSaver{N: 32 << 10}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run demo blob server: %v, stderr: %v", err, string(stderr.Bytes()))
	}
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

func sendStartingEmail() {
	if *mailgunCfgFile == "" {
		return
	}
	contentRev, err := exec.Command("docker", "run",
		"--rm",
		"-v", "/var/camweb:/var/camweb",
		"-w", prodSrcDir,
		"camlistore/git",
		"/bin/bash", "-c",
		"git show --pretty=format:'%ad-%h' --abbrev-commit --date=short | head -1").Output()

	cfg, err := mailgunCfgFromGCS()
	if err != nil {
		log.Printf("Failed to get mailgun config: %v", err)
		return
	}
	mailGun = mailgun.NewMailgun(cfg.Domain, cfg.APIKey, cfg.PublicAPIKey)
	contents := `Perkeep website starting with binary XXXXTODO and content at git rev ` + string(contentRev)
	m := mailGun.NewMessage(
		"noreply@perkeep.org (Perkeep Website)",
		"Perkeep camweb restarting",
		contents,
		"brad@danga.com",
		"mathieu.lonjaret@gmail.com",
	)
	if _, _, err := mailGun.Send(m); err != nil {
		log.Printf("Failed to send camweb startup e-mail: %v", err)
	}
}

func getDockerImage(tag, file string) {
	have, err := exec.Command("docker", "inspect", tag).Output()
	if err == nil && len(have) > 0 {
		return // we have it.
	}
	url := "https://storage.googleapis.com/" + prodBucket + "/" + file
	err = exec.Command("/bin/bash", "-c", "curl --silent "+url+" | docker load").Run()
	if err != nil {
		log.Fatal(err)
	}
}

// httpClient returns an http Client suitable for Google Cloud Storage or Google Cloud
// Logging calls with the projID project ID.
func httpClient(projID string) *http.Client {
	if *gceJWTFile == "" {
		log.Fatal("Cannot initialize an authorized http Client without --gce_jwt_file")
	}
	jsonSlurp, err := ioutil.ReadFile(*gceJWTFile)
	if err != nil {
		log.Fatalf("Error reading --gce_jwt_file value: %v", err)
	}
	jwtConf, err := google.JWTConfigFromJSON(jsonSlurp, logging.WriteScope)
	if err != nil {
		log.Fatalf("Error reading --gce_jwt_file value: %v", err)
	}
	return jwtConf.Client(context.Background())
}

// projectID returns the GCE project ID used for running this camweb on GCE
// and/or for logging on Google Cloud Logging, if any.
func projectID() string {
	if *gceProjectID != "" {
		return *gceProjectID
	}
	projID, err := metadata.ProjectID()
	if projID == "" || err != nil {
		log.Fatalf("GCE project ID needed but --gce_project_id not specified (and not running on GCE); metadata error: %v", err)
	}
	return projID
}

func initStaging() error {
	if *flagStaging {
		launchConfig.Name = stagingInstName
		return nil
	}
	// If we are the instance that has just been deployed, we can't rely on
	// *flagStaging, since there's no way to pass flags through launchConfig.
	// And we need to know if we're a staging instance, so we can set
	// launchConfig.Name properly before we get into restartLoop from
	// MaybeDeploy. So we use our own instance name as a hint.
	if !metadata.OnGCE() {
		return nil
	}
	instName, err := metadata.InstanceName()
	if err != nil {
		return fmt.Errorf("Instance could not get its Instance Name: %v", err)
	}
	if instName == stagingInstName {
		launchConfig.Name = stagingInstName
	}
	return nil
}

func main() {
	flag.Parse()
	if err := initStaging(); err != nil {
		log.Fatalf("Error setting up staging: %v", err)
	}
	launchConfig.MaybeDeploy()
	setProdFlags()

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
	if err := initGithubSyncing(); err != nil {
		log.Fatalf("error setting up syncing to github: %v", err)
	}
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

	gceLauncher, err := gceDeployHandler("/launch/")
	if err != nil {
		log.Printf("Not installing GCE /launch/ handler: %v", err)
		mux.HandleFunc("/launch/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, fmt.Sprintf("GCE launcher disabled: %v", err), 500)
		})
	} else {
		mux.Handle("/launch/", gceLauncher)
	}

	var handler http.Handler = &redirectRootHandler{Handler: mux}
	if *logDir != "" || *logStdout {
		handler = NewLoggingHandler(handler, NewApacheLogger(*logDir, *logStdout))
	}
	if *gceLogName != "" {
		projID := projectID()
		var hc *http.Client
		if !metadata.OnGCE() {
			hc = httpClient(projID)
		}
		ctx := context.Background()
		var logc *logging.Client
		if metadata.OnGCE() {
			logc, err = logging.NewClient(ctx, projID)
		} else {
			logc, err = logging.NewClient(ctx, projID, option.WithHTTPClient(hc))
		}
		if err != nil {
			log.Fatal(err)
		}
		if err := logc.Ping(ctx); err != nil {
			log.Fatalf("Failed to ping Google Cloud Logging: %v", err)
		}
		handler = NewLoggingHandler(handler, gceLogger{logc.Logger(*gceLogName)})
		if gceLauncher != nil {
			var logc *logging.Client
			if metadata.OnGCE() {
				logc, err = logging.NewClient(ctx, projID)
			} else {
				logc, err = logging.NewClient(ctx, projID, option.WithHTTPClient(hc))
			}
			if err != nil {
				log.Fatal(err)
			}
			commonLabels := logging.CommonLabels(map[string]string{
				"from": "camli-gce-launcher",
			})
			logger := logc.Logger(*gceLogName, commonLabels).StandardLogger(logging.Default)
			logger.SetPrefix("launcher: ")
			gceLauncher.SetLogger(logger)
		}
	}

	emailErr := make(chan error)
	startEmailCommitLoop(emailErr)

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

	select {
	case err := <-emailErr:
		log.Fatalf("Error sending emails: %v", err)
	case err := <-httpsErr:
		log.Fatalf("Error serving HTTPS: %v", err)
	}
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
	httpsServer := new(http.Server)
	*httpsServer = *httpServer
	httpsServer.Addr = *httpsAddr
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
		if inStaging {
			hostPolicy = autocert.HostWhitelist(stagingHostname)
		} else {
			hostPolicy = autocert.HostWhitelist(prodDomain, "www."+prodDomain,
				"www.camlistore.org", "camlistore.org")
		}
		cacheDir = autocert.DirCache(prodLECacheDir)
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
	httpsServer.TLSConfig = &tls.Config{
		GetCertificate: m.GetCertificate,
	}
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

func fromGCS(filename string) ([]byte, error) {
	ctx := context.Background()
	sc, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	slurp := func(key string) ([]byte, error) {
		rc, err := sc.Bucket(prodBucket).Object(key).NewReader(ctx)
		if err != nil {
			return nil, fmt.Errorf("Error fetching GCS object %q in bucket %q: %v", key, prodBucket, err)
		}
		defer rc.Close()
		return ioutil.ReadAll(rc)
	}
	return slurp(filename)
}

func deployerCredsFromGCS() (*gce.Config, error) {
	var cfg gce.Config
	data, err := fromGCS("launcher-config.json")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("could not JSON decode camli GCE launcher config: %v", err)
	}
	return &cfg, nil
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

func gerritRedirect(w http.ResponseWriter, r *http.Request) {
	dest := "https://camlistore-review.googlesource.com/"
	if len(r.URL.Path) > len("/r/") {
		dest += r.URL.Path[1:]
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
	fmt.Fprintf(w, "%v", time.Now().Sub(startTime))
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

func pkSrcDir() string {
	if inProd {
		return prodSrcDir
	}
	dir, err := osutil.GoPackagePath(prodDomain)
	if err != nil {
		log.Fatalf("Failed to find the root of the %s source code via osutil.GoPackagePath: %v", prodDomain, err)
	}
	return dir
}
