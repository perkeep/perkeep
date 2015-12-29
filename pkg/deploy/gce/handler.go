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
	"math/rand"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/localdisk"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/leveldb"

	"camlistore.org/third_party/code.google.com/p/xsrftoken"

	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/cloud/compute/metadata"
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

	machineValues = []string{
		"g1-small",
		"n1-highcpu-2",
	}

	backupZones = map[string][]string{
		"us-central1":  []string{"-a", "-b", "-f"},
		"europe-west1": []string{"-b", "-c", "-d"},
		"asia-east1":   []string{"-a", "-b", "-c"},
	}
)

// helpChangeCert returns the template string used for helping with TLS
// certificate files, while sidestepping failInTests panics from osutil.
func helpChangeCert() string {
	return `in your project console, navigate to "Storage", "Cloud Storage", "Storage browser", "%s-camlistore", "config". Delete "` + filepath.Base(osutil.DefaultTLSKey()) + `", "` + filepath.Base(osutil.DefaultTLSCert()) + `", and replace them by uploading your own files (with the same names).`
}

// DeployHandler serves a wizard that helps with the deployment of Camlistore on Google
// Compute Engine. It must be initialized with NewDeployHandler.
type DeployHandler struct {
	scheme   string                   // URL scheme for the URLs served by this handler. Defaults to "https".
	host     string                   // URL host for the URLs served by this handler.
	prefix   string                   // prefix is the pattern for which this handler is registered as an http.Handler.
	help     map[string]template.HTML // various help bits used in the served pages, keyed by relevant names.
	xsrfKey  string                   // for XSRF protection.
	piggyGIF string                   // path to the piggy gif file, defaults to /static/piggy.gif
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
	// the instance creation process, as JSON-encoded creationState
	instState sorted.KeyValue

	recordStateErrMu sync.RWMutex
	// recordStateErr maps the blobRef of the relevant InstanceConf to the error
	// that occurred when recording the creation state.
	recordStateErr map[string]error

	zonesMu sync.RWMutex
	// maps a region to all its zones suffixes (e.g. "asia-east1" -> "-a","-b"). updated in the
	// background every 24 hours. defaults to backupZones.
	zones   map[string][]string
	regions []string

	logger *log.Logger // should not be nil.
}

// Config is the set of parameters to initialize the DeployHandler.
type Config struct {
	ClientID       string `json:"clientID"`       // handler's credentials for OAuth. Required.
	ClientSecret   string `json:"clientSecret"`   // handler's credentials for OAuth. Required.
	Project        string `json:"project"`        // any Google Cloud project we can query to get the valid Google Cloud zones. Optional. Set from metadata on GCE.
	ServiceAccount string `json:"serviceAccount"` // JSON file with credentials to Project. Optional. Unused on GCE.
	DataDir        string `json:"dataDir"`        // where to store the instances configurations and states. Optional.
}

// NewDeployHandlerFromConfig initializes a DeployHandler from cfg.
// Host and prefix have the same meaning as for NewDeployHandler. cfg should not be nil.
func NewDeployHandlerFromConfig(host, prefix string, cfg *Config) (*DeployHandler, error) {
	if cfg == nil {
		panic("NewDeployHandlerFromConfig: nil config")
	}
	if cfg.ClientID == "" {
		return nil, errors.New("oauth2 clientID required in config")
	}
	if cfg.ClientSecret == "" {
		return nil, errors.New("oauth2 clientSecret required in config")
	}
	os.Setenv("CAMLI_GCE_CLIENTID", cfg.ClientID)
	os.Setenv("CAMLI_GCE_CLIENTSECRET", cfg.ClientSecret)
	os.Setenv("CAMLI_GCE_PROJECT", cfg.Project)
	os.Setenv("CAMLI_GCE_SERVICE_ACCOUNT", cfg.ServiceAccount)
	os.Setenv("CAMLI_GCE_DATA", cfg.DataDir)
	return NewDeployHandler(host, prefix)
}

// NewDeployHandler initializes a DeployHandler that serves at https://host/prefix/ and returns it.
// A Google account client ID should be set in CAMLI_GCE_CLIENTID with its corresponding client
// secret in CAMLI_GCE_CLIENTSECRET.
func NewDeployHandler(host, prefix string) (*DeployHandler, error) {
	clientID := os.Getenv("CAMLI_GCE_CLIENTID")
	if clientID == "" {
		return nil, errors.New("Need an oauth2 client ID defined in CAMLI_GCE_CLIENTID")
	}
	clientSecret := os.Getenv("CAMLI_GCE_CLIENTSECRET")
	if clientSecret == "" {
		return nil, errors.New("Need an oauth2 client secret defined in CAMLI_GCE_CLIENTSECRET")
	}
	tpl, err := template.New("root").Parse(noTheme + tplHTML())
	if err != nil {
		return nil, fmt.Errorf("could not parse template: %v", err)
	}
	host = strings.TrimSuffix(host, "/")
	prefix = strings.TrimSuffix(prefix, "/")
	scheme := "https"
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
		host:           host,
		xsrfKey:        xsrfKey,
		instConf:       instConf,
		instState:      instState,
		recordStateErr: make(map[string]error),
		scheme:         scheme,
		prefix:         prefix,
		help: map[string]template.HTML{
			"createProject":   template.HTML(googURLPattern.ReplaceAllString(HelpCreateProject, toHyperlink)),
			"enableAPIs":      template.HTML(HelpEnableAPIs),
			"genCert":         template.HTML(helpGenCert),
			"domainName":      template.HTML(helpDomainName),
			"machineTypes":    template.HTML(helpMachineTypes),
			"zones":           template.HTML(helpZones),
			"ssh":             template.HTML(helpSSH),
			"changeCert":      template.HTML(helpChangeCert()),
			"changeSSH":       template.HTML(HelpManageSSHKeys),
			"changeHTTPCreds": template.HTML(HelpManageHTTPCreds),
		},
		clientID:     clientID,
		clientSecret: clientSecret,
		tpl:          tpl,
		piggyGIF:     "/static/piggy.gif",
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
	h.SetLogger(log.New(os.Stderr, "GCE DEPLOYER: ", log.LstdFlags))
	h.zones = backupZones
	// TODO(mpl): use time.AfterFunc and avoid having a goroutine running all the time almost
	// doing nothing.
	refreshZonesFn := func() {
		for {
			if err := h.refreshZones(); err != nil {
				h.logger.Printf("error while refreshing zones: %v", err)
			}
			time.Sleep(24 * time.Hour)
		}
	}
	go refreshZonesFn()
	return h, nil
}

func (h *DeployHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.mux == nil {
		http.Error(w, "handler not properly initialized", http.StatusInternalServerError)
		return
	}
	h.mux.ServeHTTP(w, r)
}

func (h *DeployHandler) SetScheme(scheme string) { h.scheme = scheme }

// authenticatedClient returns the GCE project running the /launch/
// app (e.g. "camlistore-website" usually for the main instance) and
// an authenticated OAuth2 client acting as that service account.
// This is only used for refreshing the list of valid zones to give to
// the user in a drop-down.

// If we're not running on GCE (e.g. dev mode on localhost) and have
// no other way to get the info, the error value is is errNoRefresh.
func (h *DeployHandler) authenticatedClient() (project string, hc *http.Client, err error) {
	project = os.Getenv("CAMLI_GCE_PROJECT")
	accountFile := os.Getenv("CAMLI_GCE_SERVICE_ACCOUNT")
	if project != "" && accountFile != "" {
		data, errr := ioutil.ReadFile(accountFile)
		err = errr
		if err != nil {
			return
		}
		jwtConf, errr := google.JWTConfigFromJSON(data, "https://www.googleapis.com/auth/compute.readonly")
		err = errr
		if err != nil {
			return
		}
		hc = jwtConf.Client(context.Background())
		return
	}
	if !metadata.OnGCE() {
		err = errNoRefresh
		return
	}
	project, _ = metadata.ProjectID()
	hc, err = google.DefaultClient(oauth2.NoContext)
	return project, hc, err
}

var errNoRefresh error = errors.New("not on GCE, and at least one of CAMLI_GCE_PROJECT or CAMLI_GCE_SERVICE_ACCOUNT not defined.")

func (h *DeployHandler) refreshZones() error {
	h.zonesMu.Lock()
	defer h.zonesMu.Unlock()
	defer func() {
		h.regions = make([]string, 0, len(h.zones))
		for r, _ := range h.zones {
			h.regions = append(h.regions, r)
		}
	}()
	project, hc, err := h.authenticatedClient()
	if err != nil {
		if err == errNoRefresh {
			h.zones = backupZones
			h.logger.Printf("Cannot refresh zones because %v. Using hard-coded ones instead.")
			return nil
		}
		return err
	}
	s, err := compute.New(hc)
	if err != nil {
		return err
	}
	rl, err := compute.NewRegionsService(s).List(project).Do()
	if err != nil {
		return fmt.Errorf("could not get a list of regions: %v", err)
	}
	h.zones = make(map[string][]string)
	for _, r := range rl.Items {
		zones := make([]string, 0, len(r.Zones))
		for _, z := range r.Zones {
			zone := path.Base(z)
			if zone == "europe-west1-a" {
				// Because even though the docs mark it as deprecated, it still shows up here, go figure.
				continue
			}
			zone = strings.Replace(zone, r.Name, "", 1)
			zones = append(zones, zone)
		}
		h.zones[r.Name] = zones
	}
	return nil
}

func (h *DeployHandler) zoneValues() []string {
	h.zonesMu.RLock()
	defer h.zonesMu.RUnlock()
	return h.regions
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
		Prefix:        h.prefix,
		Help:          h.help,
		ZoneValues:    h.zoneValues(),
		MachineValues: machineValues,
	}); err != nil {
		h.logger.Print(err)
	}
}

func (h *DeployHandler) serveSetup(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("mode") != "setupproject" {
		h.serveError(w, r, errors.New("bad form"))
		return
	}
	ck, err := r.Cookie("user")
	if err != nil {
		h.serveFormError(w, errors.New("Cookie expired, or CSRF attempt. Please reload and retry."))
		h.logger.Printf("Cookie expired, or CSRF attempt on form.")
		return
	}

	instConf, err := h.confFromForm(r)
	if err != nil {
		h.serveFormError(w, err)
		return
	}

	br, err := h.storeInstanceConf(instConf)
	if err != nil {
		h.serveError(w, r, fmt.Errorf("could not store instance configuration: %v", err))
		return
	}

	xsrfToken := xsrftoken.Generate(h.xsrfKey, ck.Value, br.String())
	state := fmt.Sprintf("%s:%x", br.String(), xsrfToken)
	redirectURL := h.oAuthConfig().AuthCodeURL(state)
	http.Redirect(w, r, redirectURL, http.StatusFound)
	return
}

func (h *DeployHandler) serveCallback(w http.ResponseWriter, r *http.Request) {
	ck, err := r.Cookie("user")
	if err != nil {
		http.Error(w,
			fmt.Sprintf("Cookie expired, or CSRF attempt. Restart from %s://%s%s", h.scheme, h.host, h.prefix),
			http.StatusBadRequest)
		h.logger.Printf("Cookie expired, or CSRF attempt on callback.")
		return
	}
	code := r.FormValue("code")
	if code == "" {
		h.serveError(w, r, errors.New("No oauth code parameter in callback URL"))
		return
	}
	h.logger.Printf("successful authentication: %v", r.URL.RawQuery)

	br, tk, err := fromState(r)
	if err != nil {
		h.serveError(w, r, err)
		return
	}
	if !xsrftoken.Valid(tk, h.xsrfKey, ck.Value, br.String()) {
		h.serveError(w, r, fmt.Errorf("Invalid xsrf token: %q", tk))
		return
	}

	oAuthConf := h.oAuthConfig()
	tok, err := oAuthConf.Exchange(oauth2.NoContext, code)
	if err != nil {
		h.serveError(w, r, fmt.Errorf("could not obtain a token: %v", err))
		return
	}
	h.logger.Printf("successful authorization with token: %v", tok)

	instConf, err := h.instanceConf(br)
	if err != nil {
		h.serveError(w, r, err)
		return
	}

	depl := &Deployer{
		Client: oAuthConf.Client(oauth2.NoContext, tok),
		Conf:   instConf,
		Logger: h.logger,
	}

	if found := h.serveOldInstance(w, br, depl); found {
		return
	}

	if err := h.recordState(br, &creationState{
		InstConf: br,
	}); err != nil {
		h.serveError(w, r, err)
		return
	}

	go func() {
		inst, err := depl.Create(context.TODO())
		state := &creationState{
			InstConf: br,
		}
		if err != nil {
			h.logger.Printf("could not create instance: %v", err)
			switch e := err.(type) {
			case instanceExistsError:
				state.Err = fmt.Sprintf("%v %v", e, helpDeleteInstance)
			case projectIDError:
				state.Err = fmt.Sprintf("%v", e)
			default:
				state.Err = fmt.Sprintf("%v. %v", err, fileIssue(br.String()))
			}
		} else {
			state.InstAddr = addr(inst)
			state.Success = true
			state.CertFingerprintSHA1 = depl.certFingerprints["SHA-1"]
			state.CertFingerprintSHA256 = depl.certFingerprints["SHA-256"]
		}
		if err := h.recordState(br, state); err != nil {
			h.logger.Printf("Could not record creation state for %v: %v", br, err)
			h.recordStateErrMu.Lock()
			defer h.recordStateErrMu.Unlock()
			h.recordStateErr[br.String()] = err
		}
	}()
	h.serveProgress(w, br)
}

// serveOldInstance looks on GCE for an instance such as defined in depl.Conf, and if
// found, serves the appropriate page depending on whether the instance is usable. It does
// not serve anything if the instance is not found.
func (h *DeployHandler) serveOldInstance(w http.ResponseWriter, br blob.Ref, depl *Deployer) (found bool) {
	inst, err := depl.Get()
	if err != nil {
		// TODO(mpl,bradfitz): log or do something more
		// drastic if the error is something other than
		// instance not found.
		return false
	}
	var sigs map[string]string
	cert, _, err := depl.getInstalledTLS()
	if err == nil {
		sigs, err = httputil.CertFingerprints(cert)
		if err != nil {
			err = fmt.Errorf("could not get fingerprints of certificate: %v", err)
		}
	}
	if err != nil {
		h.logger.Printf("Instance (%v, %v, %v) already exists, but error getting its certificate: %v",
			depl.Conf.Project, depl.Conf.Name, depl.Conf.Zone, err)
		h.serveErrorPage(w,
			fmt.Errorf("Instance already running at %v. You need to manually delete the old one before creating a new one.", addr(inst)),
			helpDeleteInstance,
		)
		return true
	}
	var existPassword string
	for _, item := range inst.Metadata.Items {
		if item.Key == "camlistore-password" {
			existPassword = *(item.Value)
		}
	}
	if depl.Conf.Password != "" && existPassword != depl.Conf.Password {
		h.logger.Printf("Instance (%v, %v, %v) already exists, but with different password",
			depl.Conf.Project, depl.Conf.Name, depl.Conf.Zone)
		h.serveErrorPage(w,
			fmt.Errorf("Instance already running at %v. You need to manually delete the old one before creating a new one.", addr(inst)),
			helpDeleteInstance,
		)
		return true
	}
	h.logger.Printf("Reusing existing instance for (%v, %v, %v)", depl.Conf.Project, depl.Conf.Name, depl.Conf.Zone)

	if err := h.recordState(br, &creationState{
		InstConf:              br,
		InstAddr:              addr(inst),
		CertFingerprintSHA1:   sigs["SHA-1"],
		CertFingerprintSHA256: sigs["SHA-256"],
		Exists:                true,
	}); err != nil {
		h.logger.Printf("Could not record creation state for %v: %v", br, err)
		h.serveErrorPage(w, fmt.Errorf("An error occurred while recording the state of your instance. %v", fileIssue(br.String())))
		return true
	}
	h.serveProgress(w, br)
	return true
}

func (h *DeployHandler) serveFormError(w http.ResponseWriter, err error, hints ...string) {
	var topHints []string
	for _, v := range hints {
		topHints = append(topHints, v)
	}
	h.logger.Print(err)
	h.tplMu.RLock()
	defer h.tplMu.RUnlock()
	if tplErr := h.tpl.ExecuteTemplate(w, "withform", &TemplateData{
		Prefix:        h.prefix,
		Help:          h.help,
		Err:           err,
		Hints:         topHints,
		ZoneValues:    h.zoneValues(),
		MachineValues: machineValues,
	}); tplErr != nil {
		h.logger.Printf("Could not serve form error %q because: %v", err, tplErr)
	}
}

func fileIssue(br string) string {
	return fmt.Sprintf("Please file an issue with your instance key (%v) at https://camlistore.org/issue", br)
}

// serveInstanceState serves the state of the requested Google Cloud Engine VM creation
// process. If the operation was successful, it serves a success page. If it failed, it
// serves an error page. If it isn't finished yet, it replies with "running".
func (h *DeployHandler) serveInstanceState(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		h.serveError(w, r, fmt.Errorf("Wrong method: %v", r.Method))
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
		h.serveError(w, r, fmt.Errorf("could not json decode instance state: %v", err))
		return
	}
	if state.Err != "" {
		// No need to log that error here since we're already doing it in serveCallback
		// TODO(mpl): fix overescaping of double quotes.
		h.serveErrorPage(w, fmt.Errorf("An error occurred while creating your instance: %q. ", state.Err))
		return
	}
	if state.Success || state.Exists {
		conf, err := h.instanceConf(state.InstConf)
		if err != nil {
			h.logger.Printf("Could not get parameters for success message: %v", err)
			h.serveErrorPage(w, fmt.Errorf("Your instance was created and should soon be up at https://%s but there might have been a problem in the creation process. %v", state.Err, fileIssue(br)))
			return
		}
		h.serveSuccess(w, &TemplateData{
			Prefix:                h.prefix,
			Help:                  h.help,
			InstanceIP:            state.InstAddr,
			ProjectConsoleURL:     fmt.Sprintf("%s/project/%s/compute", ConsoleURL, conf.Project),
			Conf:                  conf,
			CertFingerprintSHA1:   state.CertFingerprintSHA1,
			CertFingerprintSHA256: state.CertFingerprintSHA256,
			ZoneValues:            h.zoneValues(),
			MachineValues:         machineValues,
		})
		return
	}
	h.recordStateErrMu.RLock()
	defer h.recordStateErrMu.RUnlock()
	if _, ok := h.recordStateErr[br]; ok {
		// No need to log that error here since we're already doing it in serveCallback
		h.serveErrorPage(w, fmt.Errorf("An error occurred while recording the state of your instance. %v", fileIssue(br)))
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
		PiggyGIF:    h.piggyGIF,
	}); err != nil {
		h.logger.Printf("Could not serve progress: %v", err)
	}
}

func (h *DeployHandler) serveErrorPage(w http.ResponseWriter, err error, hints ...string) {
	var topHints []string
	for _, v := range hints {
		topHints = append(topHints, v)
	}
	h.logger.Print(err)
	h.tplMu.RLock()
	defer h.tplMu.RUnlock()
	if tplErr := h.tpl.ExecuteTemplate(w, "noform", &TemplateData{
		Prefix: h.prefix,
		Err:    err,
		Hints:  topHints,
	}); tplErr != nil {
		h.logger.Printf("Could not serve error %q because: %v", err, tplErr)
	}
}

func (h *DeployHandler) serveSuccess(w http.ResponseWriter, data *TemplateData) {
	h.tplMu.RLock()
	defer h.tplMu.RUnlock()
	if err := h.tpl.ExecuteTemplate(w, "noform", data); err != nil {
		h.logger.Printf("Could not serve success: %v", err)
	}
}

func newCookie() *http.Cookie {
	expiration := cookieExpiration
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

func (h *DeployHandler) confFromForm(r *http.Request) (*InstanceConf, error) {
	project := r.FormValue("project")
	if project == "" {
		return nil, errors.New("missing project parameter")
	}
	var zone string
	zoneReg := formValueOrDefault(r, "zone", DefaultRegion)
	if LooksLikeRegion(zoneReg) {
		region := zoneReg
		zone = h.randomZone(region)
	} else if strings.Count(zoneReg, "-") == 2 {
		zone = zoneReg
	} else {
		return nil, errors.New("invalid zone or region")
	}
	return &InstanceConf{
		Name:     formValueOrDefault(r, "name", DefaultInstanceName),
		Project:  project,
		Machine:  formValueOrDefault(r, "machine", DefaultMachineType),
		Zone:     zone,
		Hostname: formValueOrDefault(r, "hostname", "localhost"),
		SSHPub:   formValueOrDefault(r, "sshPub", ""),
		Password: r.FormValue("password"),
		Ctime:    time.Now(),
		WIP:      r.FormValue("WIP") == "1",
	}, nil
}

// randomZone picks one of the zone suffixes for region and returns it
// appended to region, as a fully-qualified zone name.
// If the given region is invalid, the default Zone is returned instead.
func (h *DeployHandler) randomZone(region string) string {
	h.zonesMu.RLock()
	defer h.zonesMu.RUnlock()
	zones, ok := h.zones[region]
	if !ok {
		return fallbackZone
	}
	return region + zones[rand.Intn(len(zones))]
}

func (h *DeployHandler) SetLogger(lg *log.Logger) {
	h.logger = lg
}

func (h *DeployHandler) serveError(w http.ResponseWriter, r *http.Request, err error) {
	if h.logger != nil {
		h.logger.Printf("%v", err)
	}
	httputil.ServeError(w, r, err)
}

func (h *DeployHandler) oAuthConfig() *oauth2.Config {
	oauthConfig := NewOAuthConfig(h.clientID, h.clientSecret)
	oauthConfig.RedirectURL = fmt.Sprintf("%s://%s%s/callback", h.scheme, h.host, h.prefix)
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
	Err                   string   `json:",omitempty"` // if non blank, creation failed.
	InstConf              blob.Ref // key to the user provided instance configuration.
	InstAddr              string   // ip address of the instance.
	CertFingerprintSHA1   string   // SHA-1 prefix fingerprint of the self-signed HTTPS certificate.
	CertFingerprintSHA256 string   // SHA-256 prefix fingerprint of the self-signed HTTPS certificate.
	Success               bool     // whether new instance creation was successful.
	Exists                bool     // true if an instance with same zone, same project name, and same instance name already exists.
}

// dataStores returns the blobserver that stores the instances configurations, and the kv
// store for the instances states.
func dataStores() (blobserver.Storage, sorted.KeyValue, error) {
	dataDir := os.Getenv("CAMLI_GCE_DATA")
	if dataDir == "" {
		var err error
		dataDir, err = ioutil.TempDir("", "camli-gcedeployer-data")
		if err != nil {
			return nil, nil, err
		}
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
	tpl, err := template.New("root").Parse(text + tplHTML())
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
	Title                 string
	Help                  map[string]template.HTML // help bits within the form.
	Hints                 []string                 // helping hints printed in case of an error.
	Err                   error
	Prefix                string        // handler prefix.
	InstanceKey           string        // instance creation identifier, for the JS code to regularly poll for progress.
	PiggyGIF              string        // URI to the piggy gif for progress animation.
	Conf                  *InstanceConf // Configuration requested by the user
	InstanceIP            string        // instance IP address that we display after successful creation.
	CertFingerprintSHA1   string        // SHA-1 fingerprint of the self-signed HTTPS certificate.
	CertFingerprintSHA256 string        // SHA-256 fingerprint of the self-signed HTTPS certificate.
	ProjectConsoleURL     string
	ZoneValues            []string
	MachineValues         []string
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

func tplHTML() string {
	return `
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
		img.src = {{.PiggyGIF}};

		var msg = document.createElement('span');
		msg.innerHTML = 'Please wait (up to a couple of minutes) while we create your instance...';
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
		<p>Success. Your Camlistore instance should be up at <a href="https://{{.InstanceIP}}">https://{{.InstanceIP}}</a>. It can take a couple of minutes to be ready.</p>
		<p>Please save the information on this page.</p>

		<h4>First connection</h4>
		<p>
		A self-signed HTTPS certificate was automatically generated with "{{.Conf.Hostname}}" as the common name.<br>
		You will need to add an exception for it in your browser when you get a security warning the first time you connect. When you add a trusted certificate, verify that its certificate fingerprint matches one of:
		<table>
			<tr><td align=right>SHA-1</td><td><code style="color:blue">{{.CertFingerprintSHA1}}</code></td></tr>
			<tr><td align=right>SHA-256</td><td><code style="color:blue">{{.CertFingerprintSHA256}}</code></td></tr>
		</table>
		</p>

		<h4>Further configuration</h4>
		<p>
		Manage your instance at <a href="{{.ProjectConsoleURL}}">{{.ProjectConsoleURL}}</a>.
		</p>

		<p>
		To change your login and password, go to the <a href="{{.ProjectConsoleURL}}/instancesDetail/zones/{{.Conf.Zone}}/instances/camlistore-server">camlistore-server instance</a> page. Set camlistore-username and/or camlistore-password in the custom metadata section. Then <a href="https://{{.InstanceIP}}/status">restart</a> Camlistore.
		</p>

		<p>
		If you want to use your own HTTPS certificate and key, go to <a href="https://console.developers.google.com/project/{{.Conf.Project}}/storage/browser/{{.Conf.Project}}-camlistore/config/">the storage browser</a>. Delete "<b>` + certFilename() + `</b>", "<b>` + keyFilename() + `</b>", and replace them by uploading your own files (with the same names). Then <a href="https://{{.InstanceIP}}/status">restart</a> Camlistore.
		</p>

		<p> Camlistore should not require system
administration but to manage/add SSH keys, go to the <a
href="{{.ProjectConsoleURL}}/instancesDetail/zones/{{.Conf.Zone}}/instances/camlistore-server">camlistore-server
instance</a> page. Scroll down to the SSH Keys section. Note that the
machine can be deleted or wiped at any time without losing
information. All state is stored in Cloud Storage. The index, however,
is stored in MySQL on the instance. The index can be rebuilt if lost
or corrupted.</p>

		</p>
	{{end}}
	{{if .Err}}
		<p style="color:red"><b>Error:</b> {{.Err}}</p>
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

		<h3>Deploy Camlistore</h3>

		<p> This tool creates your own private
Camlistore instance running on <a
href="https://cloud.google.com/">Google Cloud Platform</a>. Be sure to
understand <a
href="https://cloud.google.com/compute/pricing#machinetype">Compute Engine pricing</a>
and
<a href="https://cloud.google.com/storage/pricing">Cloud Storage pricing</a>
before proceeding. Note that Camlistore metadata adds overhead on top of the size
of any raw data added to your instance. To delete your
instance and stop paying Google for the virtual machine, visit the <a
href="https://console.developers.google.com/">Google Cloud console</a>
and visit both the "Compute Engine" and "Storage" sections for your project.
</p>

		<table border=0 cellpadding=3 style='margin-top: 2em'>
			<tr valign=top><td align=right><nobr>Google Project ID:</nobr></td><td margin=left><input name="project" size=30 value=""><br>
		<ul style="padding-left:0;margin-left:0;font-size:75%">
			<li>Select a <a href="https://console.developers.google.com/project">Google Project</a> in which to create the VM. If it doesn't already exist, <a href="https://console.developers.google.com/project">create it</a> first before using this Camlistore creation tool.</li>
			<li>Requirements:</li>
			<ul>
				<li>Enable billing. (Billing & settings)</li>
				<li>APIs and auth &gt APIs &gt Google Cloud Storage</li>
				<li>APIs and auth &gt APIs &gt Google Cloud Storage JSON API</li>
				<li>APIs and auth &gt APIs &gt Google Compute Engine</li>
				<li>APIs and auth &gt APIs &gt Google Cloud Logging API</li>
			</ul>
		</ul>
		</td></tr>
			<tr valign=top>
                           <td align=right><nobr>Password:</nobr></td>
                           <td><input name="password" size=30><br/>
                                   <span style="font-size:75%"><i>(Optional)</i> New password for your Camlistore server's <b>camlistore</b> user. <b>NOT</b> your Google account's password. If blank, a random password is generated and instructions for finding it are provided on the next step.</span>
                          </td></tr>
			<tr valign=top><td align=right><nobr><a href="{{.Help.zones}}">Zone</a> or Region</nobr>:</td><td>
				<input name="zone" list="regions" value="` + DefaultRegion + `">
				<datalist id="regions">
				{{range $k, $v := .ZoneValues}}
					<option value={{$v}}>{{$v}}</option>
				{{end}}
				</datalist><br/><span style="font-size:75%">If a region is specified, a random zone (-a, -b, -c, etc) in that region will be selected.</span>
			</td></tr>
			<tr valign=top><td align=right><a href="{{.Help.machineTypes}}">Machine type</a>:</td><td>
				<input name="machine" list="machines" value="g1-small">
				<datalist id="machines">
				{{range $k, $v := .MachineValues}}
					<option value={{$v}}>{{$v}}</option>
				{{end}}
				</datalist><br/><span style="font-size:75%">As of 2015-12-27, a g1-small is $13.88 (USD) per month, before storage usage charges. See <a href="https://cloud.google.com/compute/pricing#machinetype">current pricing</a>.</span>
			</td></tr>
			<tr><td></td><td><input type='submit' value="Create instance" style='background: #ffdb00; padding: 0.5em; font-weight: bold'><br><span style="font-size:75%">(it will ask for permissions)</span></td></tr>
		</table>
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
}

// TODO(bradfitz,mpl): move this to go4.org/cloud/google/gceutil
func ZonesOfRegion(hc *http.Client, project, region string) (zones []string, err error) {
	s, err := compute.New(hc)
	if err != nil {
		return nil, err
	}
	zl, err := compute.NewZonesService(s).List(project).Do()
	if err != nil {
		return nil, fmt.Errorf("could not get a list of zones: %v", err)
	}
	if zl.NextPageToken != "" {
		return nil, errors.New("TODO: more than one page of zones found; use NextPageToken")
	}
	for _, z := range zl.Items {
		if path.Base(z.Region) != region {
			continue
		}
		zones = append(zones, z.Name)
	}
	return zones, nil
}
