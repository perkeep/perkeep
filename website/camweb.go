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
	"net/http/httputil"
	"net/smtp"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	txttemplate "text/template"
	"time"

	"camlistore.org/pkg/deploy/gce"
	"camlistore.org/pkg/netutil"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/types/camtypes"
	"camlistore.org/third_party/github.com/russross/blackfriday"

	"go4.org/cloud/cloudlaunch"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
	storageapi "google.golang.org/api/storage/v1"
	"google.golang.org/cloud"
	"google.golang.org/cloud/compute/metadata"
	"google.golang.org/cloud/datastore"
	"google.golang.org/cloud/logging"
	"google.golang.org/cloud/storage"
)

const defaultAddr = ":31798" // default webserver address

var h1TitlePattern = regexp.MustCompile(`<h1>([^<]+)</h1>`)

var (
	httpAddr        = flag.String("http", defaultAddr, "HTTP address")
	httpsAddr       = flag.String("https", "", "HTTPS address")
	root            = flag.String("root", "", "Website root (parent of 'static', 'content', and 'tmpl")
	logDir          = flag.String("logdir", "", "Directory to write log files to (one per hour), or empty to not log.")
	logStdout       = flag.Bool("logstdout", true, "Whether to log to stdout")
	tlsCertFile     = flag.String("tlscert", "", "TLS cert file")
	tlsKeyFile      = flag.String("tlskey", "", "TLS private key file")
	buildbotBackend = flag.String("buildbot_backend", "", "[optional] Build bot status backend URL")
	buildbotHost    = flag.String("buildbot_host", "", "[optional] Hostname to map to the buildbot_backend. If an HTTP request with this hostname is received, it proxies to buildbot_backend.")
	alsoRun         = flag.String("also_run", "", "[optiona] Path to run as a child process. (Used to run camlistore.org's ./scripts/run-blob-server)")
	devMode         = flag.Bool("dev", false, "in dev mode")

	gceProjectID = flag.String("gce_project_id", "", "GCE project ID; required if not running on GCE and gce_log_name is specified.")
	gceLogName   = flag.String("gce_log_name", "", "GCE Cloud Logging log name; if non-empty, logs go to Cloud Logging instead of Apache-style local disk log files")
	gceJWTFile   = flag.String("gce_jwt_file", "", "If non-empty, a filename to the GCE Service Account's JWT (JSON) config file.")
	gitContainer = flag.Bool("git_container", false, "Use git from the `camlistore/git` Docker container; if false, the system `git` is used.")
)

var (
	inProd bool

	pageHTML, errorHTML, camliErrorHTML *template.Template
	packageHTML                         *txttemplate.Template
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

	if err := pageHTML.ExecuteTemplate(w, "page", &d); err != nil {
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

	data = blackfriday.MarkdownCommon(data)

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
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		http.Redirect(rw, r, scheme+"://camlistore.org"+r.URL.RequestURI(), http.StatusFound)
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

func gceDeployHandlerConfig(ctx context.Context) (*gce.Config, error) {
	if inProd {
		return deployerCredsFromGCS(ctx)
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
	configFile := filepath.Join(osutil.CamliConfigDir(), "launcher-config.json")
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("error reading launcher-config.json (expected of type https://godoc.org/camlistore.org/pkg/deploy/gce#Config): %v", err)
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
func gceDeployHandler(ctx context.Context, prefix string) (*gce.DeployHandler, error) {
	var hostPort string
	var err error
	scheme := "https"
	if inProd {
		hostPort = "camlistore.org:443"
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
	config, err := gceDeployHandlerConfig(ctx)
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
	log.Printf("Starting Camlistore launcher on %s://%s%s", scheme, hostPort, prefix)
	return gceh, nil
}

var launchConfig = &cloudlaunch.Config{
	Name:         "camweb",
	BinaryBucket: "camlistore-website-resource",
	GCEProjectID: "camlistore-website",
	Scopes: []string{
		storageapi.DevstorageFullControlScope,
		compute.ComputeScope,
		logging.Scope,
		datastore.ScopeDatastore,
		datastore.ScopeUserEmail, // whose email? https://github.com/GoogleCloudPlatform/gcloud-golang/issues/201
	},
}

func checkInProduction() bool {
	if !metadata.OnGCE() {
		return false
	}
	proj, _ := metadata.ProjectID()
	inst, _ := metadata.InstanceName()
	log.Printf("Running on GCE: %v / %v", proj, inst)
	return proj == "camlistore-website" && inst == "camweb"
}

const prodSrcDir = "/var/camweb/src/camlistore.org"

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
	*buildbotBackend = "https://travis-ci.org/camlistore/camlistore"
	*buildbotHost = "build.camlistore.org"
	*gceLogName = "camweb-access-log"
	*root = filepath.Join(prodSrcDir, "website")
	*gitContainer = true

	*emailsTo = "camlistore-commits@googlegroups.com"
	*smtpServer = "50.19.239.94:2500" // double firewall: rinetd allow + AWS

	os.RemoveAll(prodSrcDir)
	if err := os.MkdirAll(prodSrcDir, 0755); err != nil {
		log.Fatal(err)
	}
	log.Printf("fetching git docker image...")
	getDockerImage("camlistore/git", "docker-git.tar.gz")
	getDockerImage("camlistore/demoblobserver", "docker-demoblobserver.tar.gz")

	log.Printf("cloning camlistore git tree...")
	out, err := exec.Command("docker", "run",
		"--rm",
		"-v", "/var/camweb:/var/camweb",
		"camlistore/git",
		"git",
		"clone",
		"--depth=1",
		"https://camlistore.googlesource.com/camlistore",
		prodSrcDir).CombinedOutput()
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

func runDemoBlobserverLoop() {
	if runtime.GOOS != "linux" {
		return
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return
	}
	const name = "demoblob3179"
	if err := exec.Command("docker", "kill", name).Run(); err == nil {
		// It was actually running.
		exec.Command("docker", "rm", name).Run()
		log.Printf("Killed, removed old %q container.", name)
	}
	for {
		cmd := exec.Command("docker", "run",
			"--rm",
			"--name="+name,
			"-e", "CAMLI_ROOT="+prodSrcDir+"/website/blobserver-example/root",
			"-e", "CAMLI_PASSWORD="+randHex(20),
			"-v", camSrcDir()+":"+prodSrcDir,
			"--net=host",
			"--workdir="+prodSrcDir,
			"camlistore/demoblobserver",
			"camlistored",
			"--openbrowser=false",
			"--listen=:3179",
			"--configfile="+prodSrcDir+"/website/blobserver-example/example-blobserver-config.json")
		err := cmd.Run()
		if err != nil {
			log.Printf("Failed to run demo blob server: %v", err)
		}
		if !inProd {
			return
		}
		time.Sleep(10 * time.Second)
	}
}

func sendStartingEmail() {
	contentRev, err := exec.Command("docker", "run",
		"--rm",
		"-v", "/var/camweb:/var/camweb",
		"-w", prodSrcDir,
		"camlistore/git",
		"/bin/bash", "-c",
		"git show --pretty=format:'%ad-%h' --abbrev-commit --date=short | head -1").Output()

	cl, err := smtp.Dial(*smtpServer)
	if err != nil {
		log.Printf("Failed to connect to SMTP server: %v", err)
	}
	defer cl.Quit()
	if err = cl.Mail("noreply@camlistore.org"); err != nil {
		return
	}
	if err = cl.Rcpt("brad@danga.com"); err != nil {
		return
	}
	if err = cl.Rcpt("mathieu.lonjaret@gmail.com"); err != nil {
		return
	}
	wc, err := cl.Data()
	if err != nil {
		return
	}
	_, err = fmt.Fprintf(wc, `From: noreply@camlistore.org (Camlistore Website)
To: brad@danga.com, mathieu.lonjaret@gmail.com
Subject: Camlistore camweb restarting

Camlistore website starting with binary XXXXTODO and content at git rev %s
`, contentRev)
	if err != nil {
		return
	}
	wc.Close()
}

func getDockerImage(tag, file string) {
	have, err := exec.Command("docker", "inspect", tag).Output()
	if err == nil && len(have) > 0 {
		return // we have it.
	}
	url := "https://storage.googleapis.com/camlistore-website-resource/" + file
	err = exec.Command("/bin/bash", "-c", "curl --silent "+url+" | docker load").Run()
	if err != nil {
		log.Fatal(err)
	}
}

// ctxt returns a Context suitable for Google Cloud Storage or Google Cloud
// Logging calls with the projID project ID.
func ctxt(projID string) context.Context {
	var hc *http.Client
	if *gceJWTFile != "" {
		jsonSlurp, err := ioutil.ReadFile(*gceJWTFile)
		if err != nil {
			log.Fatalf("Error reading --gce_jwt_file value: %v", err)
		}
		jwtConf, err := google.JWTConfigFromJSON(jsonSlurp, logging.Scope)
		if err != nil {
			log.Fatalf("Error reading --gce_jwt_file value: %v", err)
		}
		hc = jwtConf.Client(context.Background())
	} else {
		if !metadata.OnGCE() {
			log.Fatal("No --gce_jwt_file and not running on GCE.")
		}
		var err error
		hc, err = google.DefaultClient(oauth2.NoContext)
		if err != nil {
			log.Fatal(err)
		}
	}
	return cloud.NewContext(projID, hc)
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

func main() {
	launchConfig.MaybeDeploy()
	flag.Parse()
	setProdFlags()

	if *root == "" {
		var err error
		*root, err = os.Getwd()
		if err != nil {
			log.Fatalf("Failed to getwd: %v", err)
		}
	}
	readTemplates()
	go runDemoBlobserverLoop()

	mux := http.DefaultServeMux
	mux.Handle("/favicon.ico", http.FileServer(http.Dir(filepath.Join(*root, "static"))))
	mux.Handle("/robots.txt", http.FileServer(http.Dir(filepath.Join(*root, "static"))))
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(*root, "static")))))
	mux.Handle("/talks/", http.StripPrefix("/talks/", http.FileServer(http.Dir(filepath.Join(*root, "talks")))))
	mux.Handle(pkgPattern, godocHandler{})
	mux.Handle(cmdPattern, godocHandler{})
	mux.HandleFunc(errPattern, errHandler)

	mux.HandleFunc("/r/", gerritRedirect)
	mux.HandleFunc("/dl/", releaseRedirect)
	mux.HandleFunc("/debug/ip", ipHandler)
	mux.HandleFunc("/debug/uptime", uptimeHandler)
	mux.Handle("/docs/contributing", redirTo("/code#contributing"))
	mux.Handle("/lists", redirTo("/community"))

	mux.HandleFunc("/contributors", contribHandler())
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

	// ctx initialized now, because gceLauncher needs it first (when in prod).
	// Other users are the GCE logger, and serveHTTPS (in prod).
	var ctx context.Context
	var projID string
	if inProd || *gceLogName != "" {
		projID = projectID()
		ctx = ctxt(projID)
	}

	gceLauncher, err := gceDeployHandler(ctx, "/launch/")
	if err != nil {
		log.Printf("Not installing GCE /launch/ handler: %v", err)
		mux.HandleFunc("/launch/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, fmt.Sprintf("GCE launcher disabled: %v", err), 500)
		})
	} else {
		mux.Handle("/launch/", gceLauncher)
	}

	var handler http.Handler = &noWwwHandler{Handler: mux}
	if *logDir != "" || *logStdout {
		handler = NewLoggingHandler(handler, NewApacheLogger(*logDir, *logStdout))
	}
	if *gceLogName != "" {
		logc, err := logging.NewClient(ctx, projID, *gceLogName)
		if err != nil {
			log.Fatal(err)
		}
		if err := logc.Ping(); err != nil {
			log.Fatalf("Failed to ping Google Cloud Logging: %v", err)
		}
		handler = NewLoggingHandler(handler, gceLogger{logc})
		if gceLauncher != nil {
			logc, err := logging.NewClient(ctx, projID, *gceLogName)
			if err != nil {
				log.Fatal(err)
			}
			logc.CommonLabels = map[string]string{
				"from": "camli-gce-launcher",
			}
			logger := logc.Logger(logging.Default)
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

	httpErr := make(chan error)
	go func() {
		log.Printf("Listening for HTTP on %v", *httpAddr)
		httpErr <- httpServer.ListenAndServe()
	}()

	httpsErr := make(chan error)
	if *httpsAddr != "" {
		go func() {
			httpsErr <- serveHTTPS(ctx, httpServer)
		}()
	}

	select {
	case err := <-emailErr:
		log.Fatalf("Error sending emails: %v", err)
	case err := <-httpErr:
		log.Fatalf("Error serving HTTP: %v", err)
	case err := <-httpsErr:
		log.Fatalf("Error serving HTTPS: %v", err)
	}
}

func serveHTTPS(ctx context.Context, httpServer *http.Server) error {
	log.Printf("Starting TLS server on %s", *httpsAddr)
	httpsServer := new(http.Server)
	*httpsServer = *httpServer
	httpsServer.Addr = *httpsAddr
	if !inProd {
		if *tlsCertFile == "" {
			return errors.New("unspecified --tlscert flag")
		}
		if *tlsKeyFile == "" {
			return errors.New("unspecified --tlskey flag")
		}
		return httpsServer.ListenAndServeTLS(*tlsCertFile, *tlsKeyFile)
	}
	cert, err := tlsCertFromGCS(ctx)
	if err != nil {
		return fmt.Errorf("error loading TLS certs from GCS: %v", err)
	}
	httpsServer.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{*cert},
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

func tlsCertFromGCS(ctx context.Context) (*tls.Certificate, error) {
	sc, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	slurp := func(key string) ([]byte, error) {
		const bucket = "camlistore-website-resource"
		rc, err := sc.Bucket(bucket).Object(key).NewReader(ctx)
		if err != nil {
			return nil, fmt.Errorf("Error fetching GCS object %q in bucket %q: %v", key, bucket, err)
		}
		defer rc.Close()
		return ioutil.ReadAll(rc)
	}
	certPem, err := slurp("ssl.crt")
	if err != nil {
		return nil, err
	}
	keyPem, err := slurp("ssl.key")
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(certPem, keyPem)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

func deployerCredsFromGCS(ctx context.Context) (*gce.Config, error) {
	sc, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	slurp := func(key string) ([]byte, error) {
		const bucket = "camlistore-website-resource"
		rc, err := sc.Bucket(bucket).Object(key).NewReader(ctx)
		if err != nil {
			return nil, fmt.Errorf("Error fetching GCS object %q in bucket %q: %v", key, bucket, err)
		}
		defer rc.Close()
		return ioutil.ReadAll(rc)
	}
	var cfg gce.Config
	data, err := slurp("launcher-config.json")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("Could not JSON decode camli GCE launcher config: %v", err)
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
	return "https://github.com/camlistore/camlistore/issues" + suffix, true
}

func gerritRedirect(w http.ResponseWriter, r *http.Request) {
	dest := "https://camlistore-review.googlesource.com/"
	if len(r.URL.Path) > len("/r/") {
		dest += r.URL.Path[1:]
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

func releaseRedirect(w http.ResponseWriter, r *http.Request) {
	dest := "https://storage.googleapis.com/camlistore-release/"
	if len(r.URL.Path) > len("/dl/") {
		dest += r.URL.Path[1:]
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

func redirTo(dest string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest, http.StatusFound)
	})
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

var startTime = time.Now()

func uptimeHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%v", time.Now().Sub(startTime))
}

const (
	errPattern  = "/err/"
	toHyperlink = `<a href="$1$2">$1$2</a>`
)

var camliURLPattern = regexp.MustCompile(`(https?://camlistore.org)([a-zA-Z0-9\-\_/]+)?`)

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
	servePage(w, errString, "", contents)
}

func camSrcDir() string {
	if inProd {
		return prodSrcDir
	}
	dir, err := osutil.GoPackagePath("camlistore.org")
	if err != nil {
		log.Fatalf("Failed to find the root of the Camlistore source code via osutil.GoPackagePath: %v", err)
	}
	return dir
}
