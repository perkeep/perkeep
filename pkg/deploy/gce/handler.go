/*
Copyright 2015 The Camlistore Authors

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

package gce

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/localdisk"
	"camlistore.org/pkg/blobserver/memory"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/leveldb"

	compute "camlistore.org/third_party/code.google.com/p/google-api-go-client/compute/v1"
	"camlistore.org/third_party/code.google.com/p/xsrftoken"
	"camlistore.org/third_party/golang.org/x/oauth2"
)

const (
	// duration after which a progress state is dropped from the progress map
	progressStateExpiration = 7 * 24 * time.Hour
	cookieExpiration        = 24 * time.Hour
)

var (
	helpGenCert      = `A self-signed HTTPS certificate will be generated for the chosen domain name (or for "localhost" if left blank) and set as the default. You will be able to set another HTTPS certificate for Camlistore afterwards.`
	helpDomainName   = "http://en.wikipedia.org/wiki/Fully_qualified_domain_name"
	helpMachineTypes = "https://cloud.google.com/compute/docs/machine-types"
	helpZones        = "https://cloud.google.com/compute/docs/zones#available"
	helpSSH          = "https://cloud.google.com/compute/docs/console#sshkeys"
	helpChangeCert   = `in your project console, navigate to "Storage", "Cloud Storage", "Storage browser", "%s-camlistore", "config". Delete "` + filepath.Base(osutil.DefaultTLSCert()) + `", "` + filepath.Base(osutil.DefaultTLSKey()) + `", and replace them by uploading your own files (with the same names).`

	formDefaults = map[string]string{
		"name":    InstanceName,
		"machine": Machine,
		"zone":    Zone,
	}
	// DevHandler: if true, use HTTP instead of HTTPS, force permissions prompt for OAuth,
	// do not actually create an instance. It has no effect if set after NewHandler is
	// called.
	DevHandler bool
)

// DeployHandler serves a wizard that helps with the deployment of Camlistore on Google
// Compute Engine. It must be initialized with NewDeployHandler.
type DeployHandler struct {
	debug    bool                     // See DevHandler.
	scheme   string                   // URL scheme for the URLs served by this handler. Defaults to "https://".
	host     string                   // URL host for the URLs served by this handler.
	prefix   string                   // prefix is the pattern for which this handler is registered as an http.Handler.
	help     map[string]template.HTML // various help bits used in the served pages, keyed by relevant names.
	xsrfKey  string                   // for XSRF protection.
	piggygif string                   // path to the piggy gif file, defaults to /static/piggy.gif
	mux      *http.ServeMux

	tplMu sync.RWMutex
	tpl   *template.Template

	// Our wizard's credentials, acting on behalf of the user.
	// Obtained from the environment for now.
	clientID     string
	clientSecret string

	// stores the user submitted configuration as a JSON-encoded InstanceConf
	instConf blobserver.Storage
	// key is blobRef of the relevant InstanceConf, value is the current state of
	// the instance creation process, as json encoded creationState
	instState sorted.KeyValue

	recordStateErrMu sync.RWMutex
	// recordStateErr maps the blobRef of the relevant InstanceConf to the error
	// that occurred when recording the creation state.
	recordStateErr map[string]error

	*log.Logger
}

// NewDeployHandler initializes a DeployHandler that serves at https://host/prefix/ and returns it.
// A Google account client ID should be set in CAMLI_GCE_CLIENTID with its corresponding client
// secret in CAMLI_GCE_CLIENTSECRET.
func NewDeployHandler(host string, prefix string) (http.Handler, error) {
	clientID := os.Getenv("CAMLI_GCE_CLIENTID")
	if clientID == "" {
		return nil, errors.New("Need an oauth2 client ID defined in CAMLI_GCE_CLIENTID")
	}
	clientSecret := os.Getenv("CAMLI_GCE_CLIENTSECRET")
	if clientSecret == "" {
		return nil, errors.New("Need an oauth2 client secret defined in CAMLI_GCE_CLIENTSECRET")
	}
	tpl, err := template.New("root").Parse(noTheme + tplHTML)
	if err != nil {
		return nil, fmt.Errorf("could not parse template: %v", err)
	}
	host = strings.TrimSuffix(host, "/")
	prefix = strings.TrimSuffix(prefix, "/")
	scheme := "https://"
	if DevHandler {
		scheme = "http://"
	}
	xsrfKey := os.Getenv("CAMLI_GCE_XSRFKEY")
	if xsrfKey == "" {
		xsrfKey = auth.RandToken(20)
		log.Printf("xsrf key not provided as env var CAMLI_GCE_XSRFKEY, so generating one instead: %v", xsrfKey)
	}
	instConf, instState, err := dataStores()
	if err != nil {
		return nil, fmt.Errorf("could not initialize conf or state storage: %v", err)
	}
	h := &DeployHandler{
		debug:          DevHandler,
		host:           host,
		xsrfKey:        xsrfKey,
		instConf:       instConf,
		instState:      instState,
		recordStateErr: make(map[string]error),
		scheme:         scheme,
		prefix:         prefix,
		help: map[string]template.HTML{
			"createProject": template.HTML(googURLPattern.ReplaceAllString(HelpCreateProject, toHyperlink)),
			"enableAPIs":    template.HTML(HelpEnableAPIs),
			"genCert":       template.HTML(helpGenCert),
			"domainName":    template.HTML(helpDomainName),
			"machineTypes":  template.HTML(helpMachineTypes),
			"zones":         template.HTML(helpZones),
			"ssh":           template.HTML(helpSSH),
			"changeCert":    template.HTML(helpChangeCert),
		},
		clientID:     clientID,
		clientSecret: clientSecret,
		tpl:          tpl,
		piggygif:     "/static/piggy.gif",
	}
	mux := http.NewServeMux()
	mux.HandleFunc(prefix+"/callback", func(w http.ResponseWriter, r *http.Request) {
		h.serveCallback(w, r)
	})
	mux.HandleFunc(prefix+"/instance", func(w http.ResponseWriter, r *http.Request) {
		h.serveInstanceState(w, r)
	})
	mux.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) {
		h.serveRoot(w, r)
	})
	h.mux = mux
	h.Logger = log.New(os.Stderr, "", log.LstdFlags)
	return h, nil
}

func (h *DeployHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.mux == nil {
		http.Error(w, "handler not properly initialized", http.StatusInternalServerError)
		return
	}
	h.mux.ServeHTTP(w, r)
}

func (h *DeployHandler) serveRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		h.serveSetup(w, r)
		return
	}
	_, err := r.Cookie("user")
	if err != nil {
		http.SetCookie(w, newCookie())
	}
	h.tplMu.RLock()
	defer h.tplMu.RUnlock()
	if err := h.tpl.ExecuteTemplate(w, "withform", &TemplateData{
		Prefix:   h.prefix,
		Help:     h.help,
		Defaults: formDefaults,
	}); err != nil {
		h.Print(err)
	}
}

func (h *DeployHandler) serveSetup(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("mode") != "setupproject" {
		httputil.ServeError(w, r, errors.New("bad form"))
		return
	}
	ck, err := r.Cookie("user")
	if err != nil {
		h.serveFormError(w, errors.New("Cookie expired, or CSRF attempt. Please reload and retry."))
		h.Printf("Cookie expired, or CSRF attempt on form.")
		return
	}

	instConf, err := confFromForm(r)
	if err != nil {
		h.serveFormError(w, err)
		return
	}

	br, err := h.storeInstanceConf(instConf)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("could not store instance configuration: %v", err))
		return
	}

	xsrfToken := xsrftoken.Generate(h.xsrfKey, ck.Value, br.String())
	state := fmt.Sprintf("%s:%x", br.String(), xsrfToken)
	redirectURL := h.oAuthConfig().AuthCodeURL(state)
	if h.debug {
		redirectURL = h.oAuthConfig().AuthCodeURL(state, oauth2.ApprovalForce)
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
	return
}

func (h *DeployHandler) serveCallback(w http.ResponseWriter, r *http.Request) {
	ck, err := r.Cookie("user")
	if err != nil {
		http.Error(w,
			fmt.Sprintf("Cookie expired, or CSRF attempt. Restart from %s%s%s", h.scheme, h.host, h.prefix),
			http.StatusBadRequest)
		h.Printf("Cookie expired, or CSRF attempt on callback.")
		return
	}
	code := r.FormValue("code")
	if code == "" {
		httputil.ServeError(w, r, errors.New("No oauth code parameter in callback URL"))
		return
	}
	h.Printf("successful authentication: %v", r.URL.RawQuery)

	br, tk, err := fromState(r)
	if err != nil {
		httputil.ServeError(w, r, err)
		return
	}
	if !xsrftoken.Valid(tk, h.xsrfKey, ck.Value, br.String()) {
		httputil.ServeError(w, r, fmt.Errorf("Invalid xsrf token: %q", tk))
		return
	}

	oAuthConf := h.oAuthConfig()
	tok, err := oAuthConf.Exchange(oauth2.NoContext, code)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("could not obtain a token: %v", err))
		return
	}
	h.Printf("successful authorization with token: %v", tok)

	instConf, err := h.instanceConf(br)
	if err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	depl := &Deployer{
		Cl:   oAuthConf.Client(oauth2.NoContext, tok),
		Conf: instConf,
	}
	if inst, err := depl.Get(context.TODO()); err == nil {
		h.serveError(w,
			fmt.Errorf("Instance already running at %v. You need to manually delete the old one before creating a new one.", addr(inst)),
			HelpDeleteInstance,
		)
		return
	}

	if err := h.recordState(br, &creationState{
		InstConf: br,
	}); err != nil {
		httputil.ServeError(w, r, err)
		return
	}

	if h.debug {
		// We simulate an instance creation, without actually ever doing anything on Google Cloud,
		// by sleeping for a while. Then, as we would do in the real case, we record a creation
		// state (but a made-up one). In the meantime, the progress page/animation is served as
		// usual.
		go func() {
			time.Sleep(7 * time.Second)
			if err := h.recordState(br, &creationState{
				InstConf:        br,
				InstAddr:        "fake.instance.com",
				Success:         true,
				CertFingerprint: "XXXXXXXXXXXXXXXXXXXX",
			}); err != nil {
				h.Printf("Could not record creation state for %v: %v", br, err)
				h.recordStateErrMu.Lock()
				defer h.recordStateErrMu.Unlock()
				h.recordStateErr[br.String()] = err
			}
		}()
		h.serveProgress(w, br)
		return
	}

	go func() {
		inst, err := depl.Create(context.TODO())
		state := &creationState{
			InstConf: br,
		}
		if err != nil {
			h.Printf("could not create instance: %v", err)
			state.Err = err
		} else {
			state.InstAddr = addr(inst)
			state.Success = true
			state.CertFingerprint = depl.CertFingerprint()
		}
		if err := h.recordState(br, state); err != nil {
			h.Printf("Could not record creation state for %v: %v", br, err)
			h.recordStateErrMu.Lock()
			defer h.recordStateErrMu.Unlock()
			h.recordStateErr[br.String()] = err
		}
	}()
	h.serveProgress(w, br)
}

func (h *DeployHandler) serveFormError(w http.ResponseWriter, err error, hints ...string) {
	var topHints []string
	for _, v := range hints {
		topHints = append(topHints, v)
	}
	h.Print(err)
	h.tplMu.RLock()
	defer h.tplMu.RUnlock()
	if tplErr := h.tpl.ExecuteTemplate(w, "withform", &TemplateData{
		Prefix:   h.prefix,
		Help:     h.help,
		Err:      err,
		Hints:    topHints,
		Defaults: formDefaults,
	}); tplErr != nil {
		h.Printf("Could not serve form error %q because: %v", err, tplErr)
	}
}

// serveInstanceState serves the state of the requested Google Cloud Engine VM creation
// process. If the operation was successful, it serves a success page. If it failed, it
// serves an error page. If it isn't finished yet, it replies with "running".
func (h *DeployHandler) serveInstanceState(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httputil.ServeError(w, r, fmt.Errorf("Wrong method: %v", r.Method))
		return
	}
	br := r.URL.Query().Get("instancekey")
	stateValue, err := h.instState.Get(br)
	if err != nil {
		http.Error(w, "unknown instance", http.StatusNotFound)
		return
	}
	var state creationState
	if err := json.Unmarshal([]byte(stateValue), &state); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("could not json decode instance state: %v", err))
		return
	}
	if state.Err != nil {
		// No need to log that error here since we're already doing it in serveCallback
		h.serveError(w, fmt.Errorf("An error occurred while creating your instance: %q. Please file an issue with your instance key (%v) at https://camlistore.org/issue", state.Err, br))
		return
	}
	if state.Success {
		conf, err := h.instanceConf(state.InstConf)
		if err != nil {
			h.Printf("Could not get parameters for success message: %v", err)
			h.serveError(w, fmt.Errorf("Your instance was created and should soon be up at https://%s but there might have been a problem in the creation process. Please file an issue with your instance key (%v) at https://camlistore.org/issue", state.Err, br))
			return
		}
		h.serveSuccess(w, &TemplateData{
			Prefix:            h.prefix,
			Help:              h.help,
			InstanceIP:        state.InstAddr,
			ProjectConsoleURL: fmt.Sprintf("%s/project/%s/compute", ConsoleURL, conf.Project),
			Hostname:          conf.Hostname,
			CertFingerprint:   state.CertFingerprint,
			Project:           conf.Project,
			Defaults:          formDefaults,
		})
		return
	}
	h.recordStateErrMu.RLock()
	defer h.recordStateErrMu.RUnlock()
	if _, ok := h.recordStateErr[br]; ok {
		// No need to log that error here since we're already doing it in serveCallback
		h.serveError(w, fmt.Errorf("An error occurred while recording the state of your instance. Please file an issue with your instance key (%v) at https://camlistore.org/issue", br))
		return
	}
	fmt.Fprintf(w, "running")
}

// serveProgress serves a page with some javascript code that regularly queries
// the server about the progress of the requested Google Cloud Engine VM creation.
// The server replies through serveInstanceState.
func (h *DeployHandler) serveProgress(w http.ResponseWriter, instanceKey blob.Ref) {
	h.tplMu.RLock()
	defer h.tplMu.RUnlock()
	if err := h.tpl.ExecuteTemplate(w, "withform", &TemplateData{
		Prefix:      h.prefix,
		InstanceKey: instanceKey.String(),
		Piggygif:    h.piggygif,
	}); err != nil {
		h.Printf("Could not serve progress: %v", err)
	}
}

func (h *DeployHandler) serveError(w http.ResponseWriter, err error, hints ...string) {
	var topHints []string
	for _, v := range hints {
		topHints = append(topHints, v)
	}
	h.Print(err)
	h.tplMu.RLock()
	defer h.tplMu.RUnlock()
	if tplErr := h.tpl.ExecuteTemplate(w, "noform", &TemplateData{
		Prefix: h.prefix,
		Err:    err,
		Hints:  topHints,
	}); tplErr != nil {
		h.Printf("Could not serve error %q because: %v", err, tplErr)
	}
}

func (h *DeployHandler) serveSuccess(w http.ResponseWriter, data *TemplateData) {
	h.tplMu.RLock()
	defer h.tplMu.RUnlock()
	if err := h.tpl.ExecuteTemplate(w, "noform", data); err != nil {
		h.Printf("Could not serve success: %v", err)
	}
}

func newCookie() *http.Cookie {
	expiration := cookieExpiration
	if DevHandler {
		expiration = 2 * time.Minute
	}
	return &http.Cookie{
		Name:    "user",
		Value:   auth.RandToken(15),
		Expires: time.Now().Add(expiration),
	}
}

func formValueOrDefault(r *http.Request, formField, defValue string) string {
	val := r.FormValue(formField)
	if val == "" {
		return defValue
	}
	return val
}

func confFromForm(r *http.Request) (*InstanceConf, error) {
	project := r.FormValue("project")
	if project == "" {
		return nil, errors.New("missing project parameter")
	}
	return &InstanceConf{
		Name:     formValueOrDefault(r, "name", InstanceName),
		Project:  project,
		Machine:  formValueOrDefault(r, "machine", Machine),
		Zone:     formValueOrDefault(r, "zone", Zone),
		Hostname: formValueOrDefault(r, "hostname", "localhost"),
		SSHPub:   formValueOrDefault(r, "sshPub", ""),
		Ctime:    time.Now(),
	}, nil
}

func (h *DeployHandler) SetLogger(logger *log.Logger) {
	h.Logger = logger
}

func (h *DeployHandler) oAuthConfig() *oauth2.Config {
	oauthConfig := NewOAuthConfig(h.clientID, h.clientSecret)
	oauthConfig.RedirectURL = fmt.Sprintf("%s%s%s/callback", h.scheme, h.host, h.prefix)
	return oauthConfig
}

// fromState parses the oauth state parameter from r to extract the blobRef of the
// instance configuration and the xsrftoken that were stored during serveSetup.
func fromState(r *http.Request) (br blob.Ref, xsrfToken string, err error) {
	params := strings.Split(r.FormValue("state"), ":")
	if len(params) != 2 {
		return br, "", fmt.Errorf("Invalid format for state parameter: %q, wanted blobRef:xsrfToken", r.FormValue("state"))
	}
	br, ok := blob.Parse(params[0])
	if !ok {
		return br, "", fmt.Errorf("Invalid blobRef in state parameter: %q", params[0])
	}
	token, err := hex.DecodeString(params[1])
	if err != nil {
		return br, "", fmt.Errorf("can't decode hex xsrftoken %q: %v", params[1], err)
	}
	return br, string(token), nil
}

func (h *DeployHandler) storeInstanceConf(conf *InstanceConf) (blob.Ref, error) {
	contents, err := json.Marshal(conf)
	if err != nil {
		return blob.Ref{}, fmt.Errorf("could not json encode instance config: %v", err)
	}
	hash := blob.NewHash()
	_, err = io.Copy(hash, bytes.NewReader(contents))
	if err != nil {
		return blob.Ref{}, fmt.Errorf("could not hash blob contents: %v", err)
	}
	br := blob.RefFromHash(hash)
	if _, err := blobserver.Receive(h.instConf, br, bytes.NewReader(contents)); err != nil {
		return blob.Ref{}, fmt.Errorf("could not store instance config blob: %v", err)
	}
	return br, nil
}

func (h *DeployHandler) instanceConf(br blob.Ref) (*InstanceConf, error) {
	rc, _, err := h.instConf.Fetch(br)
	if err != nil {
		return nil, fmt.Errorf("could not fetch conf at %v: %v", br, err)
	}
	defer rc.Close()
	contents, err := ioutil.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("could not read conf in blob %v: %v", br, err)
	}
	var instConf InstanceConf
	if err := json.Unmarshal(contents, &instConf); err != nil {
		return nil, fmt.Errorf("could not json decode instance config: %v", err)
	}
	return &instConf, nil
}

func (h *DeployHandler) recordState(br blob.Ref, state *creationState) error {
	val, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("could not json encode instance state: %v", err)
	}
	if err := h.instState.Set(br.String(), string(val)); err != nil {
		return fmt.Errorf("could not record instance state: %v", err)
	}
	return nil
}

func addr(inst *compute.Instance) string {
	if inst == nil {
		return ""
	}
	if len(inst.NetworkInterfaces) == 0 || inst.NetworkInterfaces[0] == nil {
		return ""
	}
	if len(inst.NetworkInterfaces[0].AccessConfigs) == 0 || inst.NetworkInterfaces[0].AccessConfigs[0] == nil {
		return ""
	}
	return inst.NetworkInterfaces[0].AccessConfigs[0].NatIP
}

// creationState keeps information all along the creation process of the instance. The
// fields are only exported because we json encode them.
type creationState struct {
	Err             error     // if non nil, creation failed.
	Ctime           time.Time // so we can drop each creationState from the state kv after a while.
	InstConf        blob.Ref  // key to the user provided instance configuration.
	InstAddr        string    // ip address of the instance.
	CertFingerprint string    // SHA256 prefix fingerprint of the self-signed HTTPS certificate.
	Success         bool      // whether creation was successful.
}

// dataStores returns the blobserver that stores the instances configurations, and the kv
// store for the instances states.
func dataStores() (blobserver.Storage, sorted.KeyValue, error) {
	if DevHandler {
		return &memory.Storage{}, sorted.NewMemoryKeyValue(), nil
	}
	dataDir := os.Getenv("CAMLI_GCE_DATA")
	if dataDir == "" {
		dataDir = "camli-data"
		log.Printf("data dir not provided as env var CAMLI_GCE_DATA, so defaulting to %v", dataDir)
	}
	blobsDir := filepath.Join(dataDir, "instance-conf")
	if err := os.MkdirAll(blobsDir, 0700); err != nil {
		return nil, nil, err
	}
	instConf, err := localdisk.New(blobsDir)
	if err != nil {
		return nil, nil, err
	}
	instState, err := leveldb.NewStorage(filepath.Join(dataDir, "instance-state"))
	if err != nil {
		return nil, nil, err
	}
	return instConf, instState, nil
}

// AddTemplateTheme allows to enhance the aesthetics of the default template. To that
// effect, text can provide the template definitions for "header", "banner", "toplinks", and
// "footer".
func (h *DeployHandler) AddTemplateTheme(text string) error {
	tpl, err := template.New("root").Parse(text + tplHTML)
	if err != nil {
		return err
	}
	h.tplMu.Lock()
	defer h.tplMu.Unlock()
	h.tpl = tpl
	return nil
}

// TemplateData is the data passed for templates of tplHTML.
type TemplateData struct {
	Title             string
	Help              map[string]template.HTML // help bits within the form.
	Hints             []string                 // helping hints printed in case of an error.
	Defaults          map[string]string        // defaults values for the form fields.
	Err               error
	Prefix            string // handler prefix.
	InstanceKey       string // instance creation identifier, for the JS code to regularly poll for progress.
	Piggygif          string // URI to the piggy gif for progress animation.
	Project           string // Project name provided by the user.
	Hostname          string // FQDN provided by the user.
	InstanceIP        string // instance IP address that we display after successful creation.
	CertFingerprint   string // SHA-256 fingerprint of the self-signed HTTPS certificate.
	ProjectConsoleURL string
}

const toHyperlink = `<a href="$1$3">$1$3</a>`

var googURLPattern = regexp.MustCompile(`(https://([a-zA-Z0-9\-\.]+)?\.google.com)([a-zA-Z0-9\-\_/]+)?`)

// empty definitions for "banner", "toplinks", and "footer" to avoid error on
// ExecuteTemplate when the definitions have not been added with AddTemplateTheme.
var noTheme = `
{{define "header"}}
	<head>
		<title>Camlistore on Google Cloud</title>
	</head>
{{end}}
{{define "banner"}}
{{end}}
{{define "toplinks"}}
{{end}}
{{define "footer"}}
{{end}}
`

var tplHTML = `
	{{define "progress"}}
	{{if .InstanceKey}}
	<script>
		// start of progress animation/message
		var availWidth = window.innerWidth;
		var availHeight = window.innerHeight;
		var w = availWidth * 0.8;
		var h = availHeight * 0.8;
		var piggyWidth = 84;
		var piggyHeight = 56;
		var borderWidth = 18;
		var maskDiv = document.createElement('div');
		maskDiv.style.zIndex = 2;

		var dialogDiv = document.createElement('div');
		dialogDiv.style.position = 'fixed';
		dialogDiv.style.width = w;
		dialogDiv.style.height = h;
		dialogDiv.style.left = (availWidth - w) / 2;
		dialogDiv.style.top = (availHeight - h) / 2;
		dialogDiv.style.borderWidth = borderWidth;
		dialogDiv.style.textAlign = 'center';

		var imgDiv = document.createElement('div');
		imgDiv.style.marginRight = 3;
		imgDiv.style.position = 'relative';
		imgDiv.style.left = w / 2 - (piggyWidth / 2);
		imgDiv.style.top = h * 0.33;
		imgDiv.style.display = 'block';
		imgDiv.style.height = piggyHeight;
		imgDiv.style.width = piggyWidth;
		imgDiv.style.overflow = 'hidden';

		var img = document.createElement('img');
		img.src = {{.Piggygif}};

		var msg = document.createElement('span');
		msg.innerHTML = 'Please wait (up to a couple of minutes) while piggy creates your instance...';
		msg.style.boxSizing = 'border-box';
		msg.style.color = '#444';
		msg.style.display = 'block';
		msg.style.fontFamily = 'Open Sans, sans-serif';
		msg.style.fontSize = '24px';
		msg.style.fontStyle = 'normal';
		msg.style.fontVariant = 'normal';
		msg.style.fontWeight = 'normal';
		msg.style.textAlign = 'center';
		msg.style.position = 'relative';
		msg.style.top = h * 0.33 + piggyHeight;
		msg.style.height = 'auto';
		msg.style.width = 'auto';

		imgDiv.appendChild(img);
		dialogDiv.appendChild(imgDiv);
		dialogDiv.appendChild(msg);
		maskDiv.appendChild(dialogDiv);
		document.getElementsByTagName('body')[0].appendChild(maskDiv);
		// end of progress animation code

		var progress = setInterval(function(){getInstanceState('{{.Prefix}}/instance?instancekey={{.InstanceKey}}')},2000);

		function getInstanceState(progressURL) {
			var xmlhttp = new XMLHttpRequest();
			xmlhttp.open("GET",progressURL,false);
			xmlhttp.send();
			console.log(xmlhttp.responseText);
			if (xmlhttp.responseText != "running") {
				clearInterval(progress);
				window.document.open();
				window.document.write(xmlhttp.responseText);
				window.document.close();
				history.pushState(null, 'Camlistore on Google Cloud', progressURL);
			}
		}
	</script>
	{{end}}
	{{end}}

	{{define "messages"}}
		<div class='content'>
	<h1><a href="{{.Prefix}}">Camlistore on Google Cloud</a></h1>

	{{if .InstanceIP}}
		<p>Creation successful. Your Camlistore instance should soon be up at <a href="https://{{.InstanceIP}}">https://{{.InstanceIP}}</a></p>
	{{end}}
	{{if .ProjectConsoleURL}}
		<p>Manage your instance at <a href="{{.ProjectConsoleURL}}">{{.ProjectConsoleURL}}</a></p>
	{{end}}
	{{if and .InstanceIP (and .Project (and .Hostname .CertFingerprint))}}
		<p>
		A self-signed HTTPS certificate was automatically generated with "{{.Hostname}}" as the common name.<br>
		You will need to add an exception for it in your browser when you get a security warning the first time you connect. At which point you should check that the prefix of the SHA-256 fingerprint of the certificate is indeed <code style="color:blue">{{.CertFingerprint}}</code>.
		</p>
		<p>
		If you want to use your own HTTPS certificate and key: {{printf (print .Help.changeCert) .Project}} Then <a href="https://{{.InstanceIP}}/status">restart</a> Camlistore.
		</p>
	{{end}}
	{{if .Err}}
		<p style="color:red">{{.Err}}</p>
		{{range $hint := .Hints}}
			<p style="color:red">{{$hint}}</p>
		{{end}}
	{{end}}
	{{end}}

{{define "withform"}}
<html>
{{template "header" .}}
<body>
	{{if .InstanceKey}}
		<div style="z-index:0; -webkit-filter: blur(5px);">
	{{end}}
	{{template "banner" .}}
	{{template "toplinks" .}}
	{{template "progress" .}}
	{{template "messages" .}}
	<form method="post" enctype="multipart/form-data">
		<input type='hidden' name="mode" value="setupproject">

		<h3>Create a new Google Cloud project</h3>

		<p> {{.Help.createProject}} </p>
		<p> {{.Help.enableAPIs}} </p>

		<h3>Configure Google Compute Engine</h3>

		<p> {{.Help.genCert}} </p>

		<table border=0 cellpadding=3>
			<tr><td align=right>Project name</td><td><input name="project" size=50 value=""></td></tr>
			<tr><td align=right><a href="{{.Help.domainName}}">Domain name</a></td><td><input name="hostname" size=50 value=""></td></tr>
			<tr><td align=right><a href="{{.Help.zones}}">Zone</a></td><td><input name="zone" size=50 value="{{.Defaults.zone}}"></td></tr>
			<tr><td align=right>Instance name</td><td><input name="name" size=50 value="{{.Defaults.name}}"></td></tr>
			<tr><td align=right><a href="{{.Help.machineTypes}}">Machine type</a></td><td><input name="machine" size=50 value="{{.Defaults.machine}}"></td></tr>
			<tr><td align=right><a href="{{.Help.ssh}}">SSH public key</a></td><td><input name="sshPub" size=50 value=""></td></tr>
		</table>
		<input type='submit' value="Create instance">
	</form>
	</div>
	{{template "footer" .}}
	{{if .InstanceKey}}
		</div>
	{{end}}
</body>
</html>
{{end}}

{{define "noform"}}
<html>
{{template "header" .}}
<body>
	{{if .InstanceKey}}
		<div style="z-index:0; -webkit-filter: blur(5px);">
	{{end}}
	{{template "banner" .}}
	{{template "toplinks" .}}
	{{template "progress" .}}
	{{template "messages" .}}
	{{template "footer" .}}
	{{if .InstanceKey}}
		</div>
	{{end}}
</body>
</html>
{{end}}
`
