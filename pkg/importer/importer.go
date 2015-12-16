/*
Copyright 2013 Google Inc.

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

// Package importer imports content from third-party websites.
package importer

import (
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonsign/signhandler"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/server"
	"camlistore.org/pkg/types/camtypes"

	"go4.org/ctxutil"
	"go4.org/jsonconfig"
	"go4.org/syncutil"
	"golang.org/x/net/context"
)

const (
	attrNodeType            = "camliNodeType"
	nodeTypeImporter        = "importer"
	nodeTypeImporterAccount = "importerAccount"

	attrImporterType = "importerType" // => "twitter", "foursquare", etc
	attrClientID     = "authClientID"
	attrClientSecret = "authClientSecret"
	attrImportRoot   = "importRoot"
	attrImportAuto   = "importAuto" // => time.Duration value ("30m") or "" for off
)

// An Importer imports from a third-party site.
type Importer interface {
	// Run runs a full or incremental import.
	//
	// The importer should continually or periodically monitor the
	// context's Done channel to exit early if requested. The
	// return value should be ctx.Err() if the importer
	// exits for that reason.
	Run(*RunContext) error

	// NeedsAPIKey reports whether this importer requires an API key
	// (OAuth2 client_id & client_secret, or equivalent).
	// If the API only requires a username & password, or a flow to get
	// an auth token per-account without an overall API key, importers
	// can return false here.
	NeedsAPIKey() bool

	// SupportsIncremental reports whether this importer has been optimized
	// to run efficiently in regular incremental runs. (e.g. every 5 minutes
	// or half hour). Eventually all importers might support this and we'll
	// make it required, in which case we might delete this option.
	// For now, some importers (e.g. Flickr) don't yet support this.
	SupportsIncremental() bool

	// IsAccountReady reports whether the provided account node
	// is configured.
	IsAccountReady(acctNode *Object) (ok bool, err error)
	SummarizeAccount(acctNode *Object) string

	ServeSetup(w http.ResponseWriter, r *http.Request, ctx *SetupContext) error
	ServeCallback(w http.ResponseWriter, r *http.Request, ctx *SetupContext)

	// CallbackRequestAccount extracts the blobref of the importer account from
	// the callback URL parameters of r. For example, it will be encoded as:
	// For Twitter (OAuth1), in its own URL parameter: "acct=sha1-f2b0b7da718b97ce8c31591d8ed4645c777f3ef4"
	// For Picasa: (OAuth2), in the OAuth2 "state" parameter: "state=acct:sha1-97911b1a5887eb5862d1c81666ba839fc1363ea1"
	CallbackRequestAccount(r *http.Request) (acctRef blob.Ref, err error)

	// CallbackURLParameters uses the input importer account blobRef to build
	// and return the URL parameters, that will be appended to the callback URL.
	CallbackURLParameters(acctRef blob.Ref) url.Values
}

// TestDataMaker is an optional interface that may be implemented by Importers to
// generate test data locally. The returned Roundtripper will be used as the
// transport of the HTTPClient, in the RunContext that will be passed to Run
// during tests and devcam server --makethings.
// (See http://camlistore.org/issue/417).
type TestDataMaker interface {
	MakeTestData() http.RoundTripper
	// SetTestAccount allows an importer to set some needed attributes on the importer
	// account node before a run is started.
	SetTestAccount(acctNode *Object) error
}

// ImporterSetupHTMLer is an optional interface that may be implemented by
// Importers to return some HTML to be included on the importer setup page.
type ImporterSetupHTMLer interface {
	AccountSetupHTML(*Host) string
}

var importers = make(map[string]Importer)

// All returns the map of importer implementation name to implementation. This
// map should not be mutated.
func All() map[string]Importer {
	return importers
}

// Register registers a site-specific importer. It should only be called from init,
// and not from concurrent goroutines.
func Register(name string, im Importer) {
	if _, dup := importers[name]; dup {
		panic("Dup registration of importer " + name)
	}
	importers[name] = im
}

func init() {
	// Register the meta "importer" handler, which handles all other handlers.
	blobserver.RegisterHandlerConstructor("importer", newFromConfig)
}

// HostConfig holds the parameters to set up a Host.
type HostConfig struct {
	BaseURL      string
	Prefix       string                  // URL prefix for the importer handler
	Target       blobserver.StatReceiver // storage for the imported object blobs
	BlobSource   blob.Fetcher            // for additional resources, such as twitter zip file
	Signer       *schema.Signer
	Search       search.QueryDescriber
	ClientId     map[string]string // optionally maps importer impl name to a clientId credential
	ClientSecret map[string]string // optionally maps importer impl name to a clientSecret credential

	// HTTPClient optionally specifies how to fetch external network
	// resources. The Host will use http.DefaultClient otherwise.
	HTTPClient *http.Client
	// TODO: add more if/when needed
}

func NewHost(hc HostConfig) (*Host, error) {
	h := &Host{
		baseURL:      hc.BaseURL,
		importerBase: hc.BaseURL + hc.Prefix,
		imp:          make(map[string]*importer),
	}
	var err error
	h.tmpl, err = tmpl.Clone()
	if err != nil {
		return nil, err
	}
	h.tmpl = h.tmpl.Funcs(map[string]interface{}{
		"bloblink": func(br blob.Ref) template.HTML {
			if h.uiPrefix == "" {
				return template.HTML(br.String())
			}
			return template.HTML(fmt.Sprintf("<a href=\"%s/%s\">%s</a>", h.uiPrefix, br, br))
		},
	})
	for k, impl := range importers {
		h.importers = append(h.importers, k)
		clientId, clientSecret := hc.ClientId[k], hc.ClientSecret[k]
		if clientSecret != "" && clientId == "" {
			return nil, fmt.Errorf("Invalid static configuration for importer %q: clientSecret specified without clientId", k)
		}
		imp := &importer{
			host:         h,
			name:         k,
			impl:         impl,
			clientID:     clientId,
			clientSecret: clientSecret,
		}
		h.imp[k] = imp
	}

	sort.Strings(h.importers)

	h.target = hc.Target
	h.blobSource = hc.BlobSource
	h.signer = hc.Signer
	h.search = hc.Search
	h.client = hc.HTTPClient

	return h, nil
}

func newFromConfig(ld blobserver.Loader, cfg jsonconfig.Obj) (http.Handler, error) {
	hc := HostConfig{
		BaseURL: ld.BaseURL(),
		Prefix:  ld.MyPrefix(),
	}
	ClientId := make(map[string]string)
	ClientSecret := make(map[string]string)
	for k, _ := range importers {
		var clientId, clientSecret string
		if impConf := cfg.OptionalObject(k); impConf != nil {
			clientId = impConf.OptionalString("clientID", "")
			clientSecret = impConf.OptionalString("clientSecret", "")
			// Special case: allow clientSecret to be of form "clientId:clientSecret"
			// if the clientId is empty.
			if clientId == "" && strings.Contains(clientSecret, ":") {
				if f := strings.SplitN(clientSecret, ":", 2); len(f) == 2 {
					clientId, clientSecret = f[0], f[1]
				}
			}
			if err := impConf.Validate(); err != nil {
				return nil, fmt.Errorf("Invalid static configuration for importer %q: %v", k, err)
			}
			ClientId[k] = clientId
			ClientSecret[k] = clientSecret
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	hc.ClientId = ClientId
	hc.ClientSecret = ClientSecret
	host, err := NewHost(hc)
	if err != nil {
		return nil, err
	}
	host.didInit.Add(1)
	return host, nil
}

var _ blobserver.HandlerIniter = (*Host)(nil)

type SetupContext struct {
	context.Context
	Host        *Host
	AccountNode *Object

	ia *importerAcct
}

func (sc *SetupContext) Credentials() (clientID, clientSecret string, err error) {
	return sc.ia.im.credentials()
}

func (sc *SetupContext) CallbackURL() string {
	params := sc.ia.im.impl.CallbackURLParameters(sc.AccountNode.PermanodeRef()).Encode()
	if params != "" {
		params = "?" + params
	}
	return sc.Host.ImporterBaseURL() + sc.ia.im.name + "/callback" + params
}

// AccountURL returns the URL to an account of an importer
// (http://host/importer/TYPE/sha1-sd8fsd7f8sdf7).
func (sc *SetupContext) AccountURL() string {
	return sc.Host.ImporterBaseURL() + sc.ia.im.name + "/" + sc.AccountNode.PermanodeRef().String()
}

// RunContext is the context provided for a given Run of an importer, importing
// a certain account on a certain importer.
type RunContext struct {
	context.Context
	cancel context.CancelFunc // for when we stop/pause the importing
	Host   *Host

	ia *importerAcct

	mu           sync.Mutex // guards following
	lastProgress *ProgressMessage
}

// CreateAccount creates a new importer account for the Host h, and the importer
// implementation named impl. It returns a RunContext setup with that account.
func CreateAccount(h *Host, impl string) (*RunContext, error) {
	imp, ok := h.imp[impl]
	if !ok {
		return nil, fmt.Errorf("host does not have a %v importer", impl)
	}
	ia, err := imp.newAccount()
	if err != nil {
		return nil, fmt.Errorf("could not create new account for importer %v: %v", impl, err)
	}
	rc := &RunContext{
		// TODO: context plumbing
		Host: ia.im.host,
		ia:   ia,
	}
	rc.Context, rc.cancel = context.WithCancel(context.WithValue(context.TODO(), ctxutil.HTTPClient, ia.im.host.HTTPClient()))
	return rc, nil

}

// Credentials returns the credentials for the importer. This is
// typically the OAuth1, OAuth2, or equivalent client ID (api token)
// and client secret (api secret).
func (rc *RunContext) Credentials() (clientID, clientSecret string, err error) {
	return rc.ia.im.credentials()
}

// AccountNode returns the permanode storing account information for this permanode.
// It will contain the attributes:
//   * camliNodeType = "importerAccount"
//   * importerType = "registered-type"
//
// You must not change the camliNodeType or importerType.
//
// You should use this permanode to store state about where your
// importer left off, if it can efficiently resume later (without
// missing anything).
func (rc *RunContext) AccountNode() *Object { return rc.ia.acct }

// RootNode returns the initially-empty permanode storing the root
// of this account's data. You can change anything at will. This will
// typically be modeled as a dynamic directory (with camliPath:xxxx
// attributes), where each path element is either a file, object, or
// another dynamic directory.
func (rc *RunContext) RootNode() *Object { return rc.ia.root }

// Host is the HTTP handler and state for managing all the importers
// linked into the binary, even if they're not configured.
type Host struct {
	tmpl         *template.Template
	importers    []string // sorted; e.g. dummy flickr foursquare picasa twitter
	imp          map[string]*importer
	baseURL      string
	importerBase string
	target       blobserver.StatReceiver
	blobSource   blob.Fetcher // e.g. twitter reading zip file
	search       search.QueryDescriber
	signer       *schema.Signer
	uiPrefix     string // or empty if no UI handler

	// didInit is incremented by newFromConfig and marked done
	// after InitHandler. Any method on Host that requires Init
	// then calls didInit.Wait to guard against initialization
	// races where serverinit calls InitHandler in a random
	// order on start-up and different handlers access the
	// not-yet-initialized Host (notably from a goroutine)
	didInit sync.WaitGroup

	// HTTPClient optionally specifies how to fetch external network
	// resources. Defaults to http.DefaultClient.
	client    *http.Client
	transport http.RoundTripper
}

// accountStatus is the JSON representation of the status of a configured importer account.
type accountStatus struct {
	Name string `json:"name"` // display name
	Type string `json:"type"`
	Href string `json:"href"`

	StartedUnixSec      int64  `json:"startedUnixSec"`  // zero if not running
	LastFinishedUnixSec int64  `json:"finishedUnixSec"` // zero if no previous run
	LastError           string `json:"lastRunError"`    // empty if last run was success
}

// AccountsStatus returns the currently configured accounts and their status for
// inclusion in the status.json document, as rendered by the web UI.
func (h *Host) AccountsStatus() (interface{}, []camtypes.StatusError) {
	h.didInit.Wait()
	var s []accountStatus
	var errs []camtypes.StatusError
	for _, impName := range h.importers {
		imp := h.imp[impName]
		accts, _ := imp.Accounts()
		for _, ia := range accts {
			as := accountStatus{
				Type: impName,
				Href: ia.AccountURL(),
				Name: ia.AccountLinkSummary(),
			}
			ia.mu.Lock()
			if ia.current != nil {
				as.StartedUnixSec = ia.lastRunStart.Unix()
			}
			if !ia.lastRunDone.IsZero() {
				as.LastFinishedUnixSec = ia.lastRunDone.Unix()
			}
			if ia.lastRunErr != nil {
				as.LastError = ia.lastRunErr.Error()
				errs = append(errs, camtypes.StatusError{
					Error: ia.lastRunErr.Error(),
					URL:   ia.AccountURL(),
				})
			}
			ia.mu.Unlock()
			s = append(s, as)
		}
	}
	return s, errs
}

func (h *Host) InitHandler(hl blobserver.FindHandlerByTyper) error {
	if prefix, _, err := hl.FindHandlerByType("ui"); err == nil {
		h.uiPrefix = prefix
	}

	_, handler, err := hl.FindHandlerByType("root")
	if err != nil || handler == nil {
		return errors.New("importer requires a 'root' handler")
	}
	rh := handler.(*server.RootHandler)
	searchHandler, ok := rh.SearchHandler()
	if !ok {
		return errors.New("importer requires a 'root' handler with 'searchRoot' defined.")
	}
	h.search = searchHandler
	if rh.Storage == nil {
		return errors.New("importer requires a 'root' handler with 'blobRoot' defined.")
	}
	h.target = rh.Storage
	h.blobSource = rh.Storage

	_, handler, _ = hl.FindHandlerByType("jsonsign")
	if sigh, ok := handler.(*signhandler.Handler); ok {
		h.signer = sigh.Signer()
	}
	if h.signer == nil {
		return errors.New("importer requires a 'jsonsign' handler")
	}
	h.didInit.Done()
	go h.startPeriodicImporters()
	return nil
}

// ServeHTTP serves:
//   http://host/importer/
//   http://host/importer/twitter/
//   http://host/importer/twitter/callback
//   http://host/importer/twitter/sha1-abcabcabcabcabc (single account)
func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	suffix := httputil.PathSuffix(r)
	seg := strings.Split(suffix, "/")
	if suffix == "" || len(seg) == 0 {
		h.serveImportersRoot(w, r)
		return
	}
	impName := seg[0]

	imp, ok := h.imp[impName]
	if !ok {
		http.NotFound(w, r)
		return
	}

	if len(seg) == 1 || seg[1] == "" {
		h.serveImporter(w, r, imp)
		return
	}
	if seg[1] == "callback" {
		h.serveImporterAcctCallback(w, r, imp)
		return
	}
	acctRef, ok := blob.Parse(seg[1])
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.serveImporterAccount(w, r, imp, acctRef)
}

// Serves list of importers at http://host/importer/
func (h *Host) serveImportersRoot(w http.ResponseWriter, r *http.Request) {
	body := importersRootBody{
		Host:      h,
		Importers: make([]*importer, 0, len(h.imp)),
	}
	for _, v := range h.importers {
		body.Importers = append(body.Importers, h.imp[v])
	}
	h.execTemplate(w, r, importersRootPage{
		Title: "Importers",
		Body:  body,
	})
}

// Serves list of accounts at http://host/importer/twitter
func (h *Host) serveImporter(w http.ResponseWriter, r *http.Request, imp *importer) {
	if r.Method == "POST" {
		h.serveImporterPost(w, r, imp)
		return
	}

	var setup string
	node, _ := imp.Node()
	if setuper, ok := imp.impl.(ImporterSetupHTMLer); ok && node != nil {
		setup = setuper.AccountSetupHTML(h)
	}

	h.execTemplate(w, r, importerPage{
		Title: "Importer - " + imp.Name(),
		Body: importerBody{
			Host:      h,
			Importer:  imp,
			SetupHelp: template.HTML(setup),
		},
	})
}

// Serves oauth callback at http://host/importer/TYPE/callback
func (h *Host) serveImporterAcctCallback(w http.ResponseWriter, r *http.Request, imp *importer) {
	if r.Method != "GET" {
		http.Error(w, "invalid method", 400)
		return
	}
	acctRef, err := imp.impl.CallbackRequestAccount(r)
	if err != nil {
		httputil.ServeError(w, r, err)
		return
	}
	if !acctRef.Valid() {
		httputil.ServeError(w, r, errors.New("No valid blobref returned from CallbackRequestAccount(r)"))
		return
	}
	ia, err := imp.account(acctRef)
	if err != nil {
		http.Error(w, "invalid 'acct' param: "+err.Error(), 400)
		return
	}
	imp.impl.ServeCallback(w, r, &SetupContext{
		Context:     context.TODO(),
		Host:        h,
		AccountNode: ia.acct,
		ia:          ia,
	})
}

func (h *Host) serveImporterPost(w http.ResponseWriter, r *http.Request, imp *importer) {
	switch r.FormValue("mode") {
	default:
		http.Error(w, "Unknown mode.", 400)
	case "newacct":
		ia, err := imp.newAccount()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		ia.setup(w, r)
		return
	case "saveclientidsecret":
		n, err := imp.Node()
		if err != nil {
			http.Error(w, "Error getting node: "+err.Error(), 500)
			return
		}
		if err := n.SetAttrs(
			attrClientID, r.FormValue("clientID"),
			attrClientSecret, r.FormValue("clientSecret"),
		); err != nil {
			http.Error(w, "Error saving node: "+err.Error(), 500)
			return
		}
		http.Redirect(w, r, h.ImporterBaseURL()+imp.name, http.StatusFound)
	}
}

// Serves details of accounts at http://host/importer/twitter/sha1-23098429382934
func (h *Host) serveImporterAccount(w http.ResponseWriter, r *http.Request, imp *importer, acctRef blob.Ref) {
	ia, err := imp.account(acctRef)
	if err != nil {
		http.Error(w, "Unknown or invalid importer account "+acctRef.String()+": "+err.Error(), 400)
		return
	}
	ia.ServeHTTP(w, r)
}

func (h *Host) startPeriodicImporters() {
	res, err := h.search.Query(&search.SearchQuery{
		Expression: "attr:camliNodeType:importerAccount",
		Describe: &search.DescribeRequest{
			Depth: 1,
		},
	})
	if err != nil {
		log.Printf("periodic importer search fail: %v", err)
		return
	}
	if res.Describe == nil {
		log.Printf("No describe response in search result")
		return
	}
	for _, resBlob := range res.Blobs {
		blob := resBlob.Blob
		desBlob, ok := res.Describe.Meta[blob.String()]
		if !ok || desBlob.Permanode == nil {
			continue
		}
		attrs := desBlob.Permanode.Attr
		if attrs.Get(attrNodeType) != nodeTypeImporterAccount {
			panic("Search result returned non-importerAccount")
		}
		impType := attrs.Get("importerType")
		imp, ok := h.imp[impType]
		if !ok {
			continue
		}
		ia, err := imp.account(blob)
		if err != nil {
			log.Printf("Can't load importer account %v for regular importing: %v", blob, err)
			continue
		}
		go ia.maybeStart()
	}
}

var disableImporters, _ = strconv.ParseBool(os.Getenv("CAMLI_DISABLE_IMPORTERS"))

func (ia *importerAcct) maybeStart() {
	if disableImporters {
		log.Printf("Importers disabled, per environment.")
		return
	}
	acctObj, err := ia.im.host.ObjectFromRef(ia.acct.PermanodeRef())
	if err != nil {
		log.Printf("Error maybe starting %v: %v", ia.acct.PermanodeRef(), err)
		return
	}
	duration, err := time.ParseDuration(acctObj.Attr(attrImportAuto))
	if duration == 0 || err != nil {
		return
	}
	ia.mu.Lock()
	defer ia.mu.Unlock()
	if ia.current != nil {
		return
	}
	if ia.lastRunDone.After(time.Now().Add(-duration)) {
		sleepFor := ia.lastRunDone.Add(duration).Sub(time.Now())
		log.Printf("%v ran recently enough. Sleeping for %v.", ia, sleepFor)
		time.AfterFunc(sleepFor, ia.maybeStart)
		return
	}

	log.Printf("Starting regular periodic import for %v", ia)
	go ia.start()
}

// BaseURL returns the root of the whole server, without trailing
// slash.
func (h *Host) BaseURL() string {
	return h.baseURL
}

// ImporterBaseURL returns the URL base of the importer handler,
// including trailing slash.
func (h *Host) ImporterBaseURL() string {
	return h.importerBase
}

func (h *Host) Target() blobserver.StatReceiver {
	return h.target
}

func (h *Host) BlobSource() blob.Fetcher {
	return h.blobSource
}

func (h *Host) Searcher() search.QueryDescriber { return h.search }

// importer is an importer for a certain site, but not a specific account on that site.
type importer struct {
	host *Host
	name string // importer name e.g. "twitter"
	impl Importer

	// If statically configured in config file, else
	// they come from the importer node's attributes.
	clientID     string
	clientSecret string

	nodemu    sync.Mutex // guards nodeCache
	nodeCache *Object    // or nil if unset

	acctmu sync.Mutex
	acct   map[blob.Ref]*importerAcct // key: account permanode
}

func (im *importer) Name() string { return im.name }

func (im *importer) StaticConfig() bool { return im.clientSecret != "" }

// URL returns the importer's URL without trailing slash.
func (im *importer) URL() string { return im.host.ImporterBaseURL() + im.name }

func (im *importer) ShowClientAuthEditForm() bool {
	if im.StaticConfig() {
		// Don't expose the server's statically-configured client secret
		// to the user. (e.g. a hosted multi-user configuation)
		return false
	}
	return im.impl.NeedsAPIKey()
}

func (im *importer) CanAddNewAccount() bool {
	if !im.impl.NeedsAPIKey() {
		return true
	}
	id, sec, err := im.credentials()
	return id != "" && sec != "" && err == nil
}

func (im *importer) ClientID() (v string, err error) {
	v, _, err = im.credentials()
	return
}

func (im *importer) ClientSecret() (v string, err error) {
	_, v, err = im.credentials()
	return
}

func (im *importer) Status() (status string, err error) {
	if !im.impl.NeedsAPIKey() {
		return "no configuration required", nil
	}
	if im.StaticConfig() {
		return "API key configured on server", nil
	}
	n, err := im.Node()
	if err != nil {
		return
	}
	if n.Attr(attrClientID) != "" && n.Attr(attrClientSecret) != "" {
		return "API key configured on node", nil
	}
	return "API key (client ID & Secret) not configured", nil
}

func (im *importer) credentials() (clientID, clientSecret string, err error) {
	if im.StaticConfig() {
		return im.clientID, im.clientSecret, nil
	}
	n, err := im.Node()
	if err != nil {
		return
	}
	return n.Attr(attrClientID), n.Attr(attrClientSecret), nil
}

func (im *importer) deleteAccount(acctRef blob.Ref) {
	im.acctmu.Lock()
	delete(im.acct, acctRef)
	im.acctmu.Unlock()
}

func (im *importer) account(nodeRef blob.Ref) (*importerAcct, error) {
	im.acctmu.Lock()
	ia, ok := im.acct[nodeRef]
	im.acctmu.Unlock()
	if ok {
		return ia, nil
	}

	acct, err := im.host.ObjectFromRef(nodeRef)
	if err != nil {
		return nil, err
	}
	if acct.Attr(attrNodeType) != nodeTypeImporterAccount {
		return nil, errors.New("account has wrong node type")
	}
	if acct.Attr(attrImporterType) != im.name {
		return nil, errors.New("account has wrong importer type")
	}
	var root *Object
	if v := acct.Attr(attrImportRoot); v != "" {
		rootRef, ok := blob.Parse(v)
		if !ok {
			return nil, errors.New("invalid import root attribute")
		}
		root, err = im.host.ObjectFromRef(rootRef)
		if err != nil {
			return nil, err
		}
	} else {
		root, err = im.host.NewObject()
		if err != nil {
			return nil, err
		}
		if err := acct.SetAttr(attrImportRoot, root.PermanodeRef().String()); err != nil {
			return nil, err
		}
	}
	ia = &importerAcct{
		im:   im,
		acct: acct,
		root: root,
	}
	im.acctmu.Lock()
	defer im.acctmu.Unlock()
	im.addAccountLocked(ia)
	return ia, nil
}

func (im *importer) newAccount() (*importerAcct, error) {
	acct, err := im.host.NewObject()
	if err != nil {
		return nil, err
	}
	root, err := im.host.NewObject()
	if err != nil {
		return nil, err
	}
	if err := acct.SetAttrs(
		"title", fmt.Sprintf("%s account", im.name),
		attrNodeType, nodeTypeImporterAccount,
		attrImporterType, im.name,
		attrImportRoot, root.PermanodeRef().String(),
	); err != nil {
		return nil, err
	}

	ia := &importerAcct{
		im:   im,
		acct: acct,
		root: root,
	}
	im.acctmu.Lock()
	defer im.acctmu.Unlock()
	im.addAccountLocked(ia)
	return ia, nil
}

func (im *importer) addAccountLocked(ia *importerAcct) {
	if im.acct == nil {
		im.acct = make(map[blob.Ref]*importerAcct)
	}
	im.acct[ia.acct.PermanodeRef()] = ia
}

func (im *importer) Accounts() ([]*importerAcct, error) {
	var accts []*importerAcct

	// TODO: cache this search. invalidate when new accounts are made.
	res, err := im.host.search.Query(&search.SearchQuery{
		Expression: fmt.Sprintf("attr:%s:%s attr:%s:%s",
			attrNodeType, nodeTypeImporterAccount,
			attrImporterType, im.name,
		),
	})
	if err != nil {
		return nil, err
	}
	for _, res := range res.Blobs {
		ia, err := im.account(res.Blob)
		if err != nil {
			return nil, err
		}
		accts = append(accts, ia)
	}
	return accts, nil
}

// node returns the importer node. (not specific to a certain account
// on that importer site)
//
// It is a permanode with:
//   camliNodeType: "importer"
//   importerType: "twitter"
// And optionally:
//   authClientID:     "xxx"    // e.g. api token
//   authClientSecret: "sdkojfsldfjlsdkf"
func (im *importer) Node() (*Object, error) {
	im.nodemu.Lock()
	defer im.nodemu.Unlock()
	if im.nodeCache != nil {
		return im.nodeCache, nil
	}

	expr := fmt.Sprintf("attr:%s:%s attr:%s:%s",
		attrNodeType, nodeTypeImporter,
		attrImporterType, im.name,
	)
	res, err := im.host.search.Query(&search.SearchQuery{
		Limit:      10, // only expect 1
		Expression: expr,
	})
	if err != nil {
		return nil, err
	}
	if len(res.Blobs) > 1 {
		return nil, fmt.Errorf("Ambiguous; too many permanodes matched query %q: %v", expr, res.Blobs)
	}
	if len(res.Blobs) == 1 {
		return im.host.ObjectFromRef(res.Blobs[0].Blob)
	}
	o, err := im.host.NewObject()
	if err != nil {
		return nil, err
	}
	if err := o.SetAttrs(
		"title", fmt.Sprintf("%s importer", im.name),
		attrNodeType, nodeTypeImporter,
		attrImporterType, im.name,
	); err != nil {
		return nil, err
	}

	im.nodeCache = o
	return o, nil
}

// importerAcct is a long-lived type representing account
type importerAcct struct {
	im   *importer
	acct *Object
	root *Object

	mu           sync.Mutex
	current      *RunContext // or nil if not running
	stopped      bool        // stop requested (context canceled)
	lastRunErr   error
	lastRunStart time.Time
	lastRunDone  time.Time
}

func (ia *importerAcct) String() string {
	return fmt.Sprintf("%v importer account, %v", ia.im.name, ia.acct.PermanodeRef())
}

func (ia *importerAcct) delete() error {
	if err := ia.acct.SetAttrs(
		attrNodeType, nodeTypeImporterAccount+"-deleted",
	); err != nil {
		return err
	}
	ia.im.deleteAccount(ia.acct.PermanodeRef())
	return nil
}

func (ia *importerAcct) toggleAuto() error {
	old := ia.acct.Attr(attrImportAuto)
	if old == "" && !ia.im.impl.SupportsIncremental() {
		return fmt.Errorf("Importer %q doesn't support automatic mode.", ia.im.name)
	}
	var new string
	if old == "" {
		new = "30m" // TODO: configurable?
	}
	return ia.acct.SetAttrs(attrImportAuto, new)
}

func (ia *importerAcct) IsAccountReady() (bool, error) {
	return ia.im.impl.IsAccountReady(ia.acct)
}

func (ia *importerAcct) AccountObject() *Object { return ia.acct }
func (ia *importerAcct) RootObject() *Object    { return ia.root }

func (ia *importerAcct) AccountURL() string {
	return ia.im.URL() + "/" + ia.acct.PermanodeRef().String()
}

func (ia *importerAcct) AccountLinkText() string {
	return ia.acct.PermanodeRef().String()
}

func (ia *importerAcct) AccountLinkSummary() string {
	return ia.im.impl.SummarizeAccount(ia.acct)
}

func (ia *importerAcct) RefreshInterval() time.Duration {
	ds := ia.acct.Attr(attrImportAuto)
	if ds == "" {
		return 0
	}
	d, _ := time.ParseDuration(ds)
	return d
}

func (ia *importerAcct) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		ia.serveHTTPPost(w, r)
		return
	}
	ia.mu.Lock()
	defer ia.mu.Unlock()
	body := acctBody{
		Acct:     ia,
		AcctType: fmt.Sprintf("%T", ia.im.impl),
	}
	if run := ia.current; run != nil {
		body.Running = true
		body.StartedAgo = time.Since(ia.lastRunStart)
		run.mu.Lock()
		body.LastStatus = fmt.Sprintf("%+v", run.lastProgress)
		run.mu.Unlock()
	} else if !ia.lastRunDone.IsZero() {
		body.LastAgo = time.Since(ia.lastRunDone)
		if ia.lastRunErr != nil {
			body.LastError = ia.lastRunErr.Error()
		}
	}
	title := fmt.Sprintf("%s account: ", ia.im.name)
	if summary := ia.im.impl.SummarizeAccount(ia.acct); summary != "" {
		title += summary
	} else {
		title += ia.acct.PermanodeRef().String()
	}
	ia.im.host.execTemplate(w, r, acctPage{
		Title: title,
		Body:  body,
	})
}

func (ia *importerAcct) serveHTTPPost(w http.ResponseWriter, r *http.Request) {
	// TODO: XSRF token

	switch r.FormValue("mode") {
	case "":
		// Nothing.
	case "start":
		ia.start()
	case "stop":
		ia.stop()
	case "login":
		ia.setup(w, r)
		return
	case "toggleauto":
		if err := ia.toggleAuto(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	case "delete":
		ia.stop() // can't hurt
		if err := ia.delete(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		http.Redirect(w, r, ia.im.URL(), http.StatusFound)
		return
	default:
		http.Error(w, "Unknown mode", 400)
		return
	}
	http.Redirect(w, r, ia.AccountURL(), http.StatusFound)
}

func (ia *importerAcct) setup(w http.ResponseWriter, r *http.Request) {
	if err := ia.im.impl.ServeSetup(w, r, &SetupContext{
		Context:     context.TODO(),
		Host:        ia.im.host,
		AccountNode: ia.acct,
		ia:          ia,
	}); err != nil {
		log.Printf("%v", err)
	}
}

func (ia *importerAcct) start() {
	ia.mu.Lock()
	defer ia.mu.Unlock()
	if ia.current != nil {
		return
	}
	rc := &RunContext{
		// TODO: context plumbing
		Host: ia.im.host,
		ia:   ia,
	}
	rc.Context, rc.cancel = context.WithCancel(context.WithValue(context.TODO(), ctxutil.HTTPClient, ia.im.host.HTTPClient()))
	ia.current = rc
	ia.stopped = false
	ia.lastRunStart = time.Now()
	go func() {
		log.Printf("Starting %v: %s", ia, ia.AccountLinkSummary())
		err := ia.im.impl.Run(rc)
		if err != nil {
			log.Printf("%v error: %v", ia, err)
		} else {
			log.Printf("%v finished.", ia)
		}
		ia.mu.Lock()
		defer ia.mu.Unlock()
		ia.current = nil
		ia.stopped = false
		ia.lastRunDone = time.Now()
		ia.lastRunErr = err
		go ia.maybeStart()
	}()
}

func (ia *importerAcct) stop() {
	ia.mu.Lock()
	defer ia.mu.Unlock()
	if ia.current == nil || ia.stopped {
		return
	}
	ia.current.cancel()
	ia.stopped = true
}

// HTTPClient returns the HTTP client to use.
func (h *Host) HTTPClient() *http.Client {
	if h.client == nil {
		return http.DefaultClient
	}
	return h.client
}

// HTTPTransport returns the HTTP transport to use.
func (h *Host) HTTPTransport() http.RoundTripper {
	if h.transport == nil {
		return http.DefaultTransport
	}
	return h.transport
}

type ProgressMessage struct {
	ItemsDone, ItemsTotal int
	BytesDone, BytesTotal int64
}

func (h *Host) upload(bb *schema.Builder) (br blob.Ref, err error) {
	signed, err := bb.Sign(h.signer)
	if err != nil {
		return
	}
	sb, err := blobserver.ReceiveString(h.target, signed)
	if err != nil {
		return
	}
	return sb.Ref, nil
}

// NewObject creates a new permanode and returns its Object wrapper.
func (h *Host) NewObject() (*Object, error) {
	pn, err := h.upload(schema.NewUnsignedPermanode())
	if err != nil {
		return nil, err
	}
	// No need to do a describe query against it: we know it's
	// empty (has no claims against it yet).
	return &Object{h: h, pn: pn}, nil
}

// An Object is wrapper around a permanode that the importer uses
// to synchronize.
type Object struct {
	h  *Host
	pn blob.Ref // permanode ref

	mu   sync.RWMutex
	attr map[string][]string
}

// PermanodeRef returns the permanode that this object wraps.
func (o *Object) PermanodeRef() blob.Ref {
	return o.pn
}

// Attr returns the object's attribute value for the provided attr,
// or the empty string if unset.  To distinguish between unset,
// an empty string, or multiple attribute values, use Attrs.
func (o *Object) Attr(attr string) string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if v := o.attr[attr]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// Attrs returns the attribute values for the provided attr.
func (o *Object) Attrs(attr string) []string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.attr[attr]
}

// ForeachAttr runs fn for each of the object's attributes & values.
// There might be multiple values for the same attribute.
// The internal lock is held while running, so no mutations should be
// made or it will deadlock.
func (o *Object) ForeachAttr(fn func(key, value string)) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	for k, vv := range o.attr {
		for _, v := range vv {
			fn(k, v)
		}
	}
}

// SetAttr sets the attribute key to value.
func (o *Object) SetAttr(key, value string) error {
	if o.Attr(key) == value {
		return nil
	}
	_, err := o.h.upload(schema.NewSetAttributeClaim(o.pn, key, value))
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.attr == nil {
		o.attr = make(map[string][]string)
	}
	o.attr[key] = []string{value}
	return nil
}

// SetAttrs sets multiple attributes. The provided keyval should be an
// even number of alternating key/value pairs to set.
func (o *Object) SetAttrs(keyval ...string) error {
	_, err := o.SetAttrs2(keyval...)
	return err
}

// SetAttrs2 sets multiple attributes and returns whether there were
// any changes. The provided keyval should be an even number of
// alternating key/value pairs to set.
func (o *Object) SetAttrs2(keyval ...string) (changes bool, err error) {
	if len(keyval)%2 == 1 {
		panic("importer.SetAttrs: odd argument count")
	}

	g := syncutil.Group{}
	for i := 0; i < len(keyval); i += 2 {
		key, val := keyval[i], keyval[i+1]
		if val != o.Attr(key) {
			changes = true
			g.Go(func() error {
				return o.SetAttr(key, val)
			})
		}
	}
	return changes, g.Err()
}

// SetAttrValues sets multi-valued attribute.
func (o *Object) SetAttrValues(key string, attrs []string) error {
	exists := asSet(o.Attrs(key))
	actual := asSet(attrs)
	o.mu.Lock()
	defer o.mu.Unlock()
	// add new values
	for v := range actual {
		if exists[v] {
			delete(exists, v)
			continue
		}
		_, err := o.h.upload(schema.NewAddAttributeClaim(o.pn, key, v))
		if err != nil {
			return err
		}
	}
	// delete unneeded values
	for v := range exists {
		_, err := o.h.upload(schema.NewDelAttributeClaim(o.pn, key, v))
		if err != nil {
			return err
		}
	}
	if o.attr == nil {
		o.attr = make(map[string][]string)
	}
	o.attr[key] = attrs
	return nil
}

func asSet(elts []string) map[string]bool {
	if len(elts) == 0 {
		return nil
	}
	set := make(map[string]bool, len(elts))
	for _, elt := range elts {
		set[elt] = true
	}
	return set
}

// ChildPathObject returns (creating if necessary) the child object
// from the permanode o, given by the "camliPath:xxxx" attribute,
// where xxx is the provided path.
func (o *Object) ChildPathObject(path string) (*Object, error) {
	return o.ChildPathObjectOrFunc(path, o.h.NewObject)
}

// ChildPathObject returns the child object from the permanode o,
// given by the "camliPath:xxxx" attribute, where xxx is the provided
// path. If the path doesn't exist, the provided func should return an
// appropriate object. If the func fails, the return error is
// returned directly without any attempt to make a permanode.
func (o *Object) ChildPathObjectOrFunc(path string, fn func() (*Object, error)) (*Object, error) {
	attrName := "camliPath:" + path
	if v := o.Attr(attrName); v != "" {
		br, ok := blob.Parse(v)
		if !ok {
			return nil, fmt.Errorf("invalid blobref %q already stored at camliPath %q", br, path)
		}
		return o.h.ObjectFromRef(br)
	}
	newObj, err := fn()
	if err != nil {
		return nil, err
	}
	if err := o.SetAttr(attrName, newObj.PermanodeRef().String()); err != nil {
		return nil, err
	}
	return newObj, nil
}

// ObjectFromRef returns the object given by the named permanode
func (h *Host) ObjectFromRef(permanodeRef blob.Ref) (*Object, error) {
	res, err := h.search.Describe(&search.DescribeRequest{
		BlobRef: permanodeRef,
		Depth:   1,
	})
	if err != nil {
		return nil, err
	}
	db, ok := res.Meta[permanodeRef.String()]
	if !ok {
		return nil, fmt.Errorf("permanode %v wasn't in Describe response", permanodeRef)
	}
	if db.Permanode == nil {
		return nil, fmt.Errorf("permanode %v had no DescribedPermanode in Describe response", permanodeRef)
	}
	return &Object{
		h:    h,
		pn:   permanodeRef,
		attr: map[string][]string(db.Permanode.Attr),
	}, nil
}
