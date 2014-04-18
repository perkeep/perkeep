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
	"sort"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/jsonsign/signhandler"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/server"
	"camlistore.org/pkg/syncutil"
)

const (
	attrNodeType            = "camliNodeType"
	nodeTypeImporter        = "importer"
	nodeTypeImporterAccount = "importerAccount"

	attrImporterType = "importerType" // => "twitter", "foursquare", etc
	attrClientID     = "authClientID"
	attrClientSecret = "authClientSecret"
	attrImportRoot   = "importRoot"
)

// An Importer imports from a third-party site.
type Importer interface {
	// Run runs a full or incremental import.
	//
	// The importer should continually or periodically monitor the
	// context's Done channel to exit early if requested. The
	// return value should be context.ErrCanceled if the importer
	// exits for that reason.
	Run(*RunContext) error

	// NeedsAPIKey reports whether this importer requires an API key
	// (OAuth2 client_id & client_secret, or equivalent).
	// If the API only requires a username & password, or a flow to get
	// an auth token per-account without an overall API key, importers
	// can return false here.
	NeedsAPIKey() bool

	// IsAccountReady reports whether the provided account node
	// is configured.
	IsAccountReady(acctNode *Object) (ok bool, err error)
	SummarizeAccount(acctNode *Object) string

	ServeSetup(w http.ResponseWriter, r *http.Request, ctx *SetupContext) error
	ServeCallback(w http.ResponseWriter, r *http.Request, ctx *SetupContext)
}

// ImporterSetupHTMLer is an optional interface that may be implemented by
// Importers to return some HTML to be included on the importer setup page.
type ImporterSetupHTMLer interface {
	AccountSetupHTML(*Host) string
}

var importers = make(map[string]Importer)

func init() {
	Register("flickr", TODOImporter)
	Register("picasa", TODOImporter)
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

func newFromConfig(ld blobserver.Loader, cfg jsonconfig.Obj) (http.Handler, error) {
	h := &Host{
		baseURL:      ld.BaseURL(),
		importerBase: ld.BaseURL() + ld.MyPrefix(),
		imp:          make(map[string]*importer),
	}
	for k, impl := range importers {
		h.importers = append(h.importers, k)
		var clientID, clientSecret string
		if impConf := cfg.OptionalObject(k); impConf != nil {
			clientID = impConf.OptionalString("clientID", "")
			clientSecret = impConf.OptionalString("clientSecret", "")
			// Special case: allow clientSecret to be of form "clientID:clientSecret"
			// if the clientID is empty.
			if clientID == "" && strings.Contains(clientSecret, ":") {
				if f := strings.SplitN(clientSecret, ":", 2); len(f) == 2 {
					clientID, clientSecret = f[0], f[1]
				}
			}
			if err := impConf.Validate(); err != nil {
				return nil, fmt.Errorf("Invalid static configuration for importer %q: %v", k, err)
			}
		}
		if clientSecret != "" && clientID == "" {
			return nil, fmt.Errorf("Invalid static configuration for importer %q: clientSecret specified without clientID", k)
		}
		imp := &importer{
			host:         h,
			name:         k,
			impl:         impl,
			clientID:     clientID,
			clientSecret: clientSecret,
		}
		h.imp[k] = imp
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	sort.Strings(h.importers)
	return h, nil
}

var _ blobserver.HandlerIniter = (*Host)(nil)

type SetupContext struct {
	*context.Context
	Host        *Host
	AccountNode *Object

	ia *importerAcct
}

func (sc *SetupContext) Credentials() (clientID, clientSecret string, err error) {
	return sc.ia.im.credentials()
}

func (sc *SetupContext) CallbackURL() string {
	return sc.Host.ImporterBaseURL() + sc.ia.im.name + "/callback?acct=" + sc.AccountNode.PermanodeRef().String()
}

// AccountURL returns the URL to an account of an importer
// (http://host/importer/TYPE/sha1-sd8fsd7f8sdf7).
func (sc *SetupContext) AccountURL() string {
	return sc.Host.ImporterBaseURL() + sc.ia.im.name + "/" + sc.AccountNode.PermanodeRef().String()
}

// RunContext is the context provided for a given Run of an importer, importing
// a certain account on a certain importer.
type RunContext struct {
	*context.Context
	Host *Host

	ia *importerAcct

	mu           sync.Mutex // guards following
	lastProgress *ProgressMessage
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
	importers    []string // sorted; e.g. dummy flickr foursquare picasa twitter
	imp          map[string]*importer
	baseURL      string
	importerBase string
	target       blobserver.StatReceiver
	search       *search.Handler
	signer       *schema.Signer

	// client optionally specifies how to fetch external network
	// resources.  If nil, http.DefaultClient is used.
	client    *http.Client
	transport http.RoundTripper
}

func (h *Host) InitHandler(hl blobserver.FindHandlerByTyper) error {
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

	_, handler, _ = hl.FindHandlerByType("jsonsign")
	if sigh, ok := handler.(*signhandler.Handler); ok {
		h.signer = sigh.Signer()
	}
	if h.signer == nil {
		return errors.New("importer requires a 'jsonsign' handler")
	}
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
	execTemplate(w, r, importersRootPage{
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

	execTemplate(w, r, importerPage{
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
	acctRef, ok := blob.Parse(r.FormValue("acct"))
	if !ok {
		http.Error(w, "missing 'acct' blobref param", 400)
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

func (h *Host) Search() *search.Handler {
	return h.search
}

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

func (ia *importerAcct) delete() error {
	if err := ia.acct.SetAttrs(
		attrNodeType, nodeTypeImporter+"-deleted",
	); err != nil {
		return err
	}
	ia.im.deleteAccount(ia.acct.PermanodeRef())
	return nil
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
	execTemplate(w, r, acctPage{
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
	ctx := context.New()
	rc := &RunContext{
		Context: ctx,
		Host:    ia.im.host,
		ia:      ia,
	}
	ia.current = rc
	ia.stopped = false
	ia.lastRunStart = time.Now()
	go func() {
		log.Printf("Starting importer %s: %s", ia.im.name, ia.AccountLinkSummary())
		err := ia.im.impl.Run(rc)
		if err != nil {
			log.Printf("Importer %s error: %v", ia.im.name, err)
		} else {
			log.Printf("Importer %s finished.", ia.im.name)
		}
		ia.mu.Lock()
		defer ia.mu.Unlock()
		ia.current = nil
		ia.stopped = false
		ia.lastRunDone = time.Now()
		ia.lastRunErr = err
	}()
}

func (ia *importerAcct) stop() {
	ia.mu.Lock()
	defer ia.mu.Unlock()
	if ia.current == nil || ia.stopped {
		return
	}
	ia.current.Context.Cancel()
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

// SetAttrs sets multiple attributes. The provided keyval should be an even number of alternating key/value pairs to set.
func (o *Object) SetAttrs(keyval ...string) error {
	if len(keyval)%2 == 1 {
		panic("importer.SetAttrs: odd argument count")
	}

	g := syncutil.Group{}
	for i := 0; i < len(keyval); i += 2 {
		key, val := keyval[i], keyval[i+1]
		if val != o.Attr(key) {
			g.Go(func() error {
				return o.SetAttr(key, val)
			})
		}
	}
	return g.Err()
}

// ChildPathObject returns (creating if necessary) the child object
// from the permanode o, given by the "camliPath:xxxx" attribute,
// where xxx is the provided path.
func (o *Object) ChildPathObject(path string) (*Object, error) {
	attrName := "camliPath:" + path
	if v := o.Attr(attrName); v != "" {
		br, ok := blob.Parse(v)
		if ok {
			return o.h.ObjectFromRef(br)
		}
	}

	childBlobRef, err := o.h.upload(schema.NewUnsignedPermanode())
	if err != nil {
		return nil, err
	}

	if err := o.SetAttr(attrName, childBlobRef.String()); err != nil {
		return nil, err
	}

	return &Object{
		h:  o.h,
		pn: childBlobRef,
	}, nil
}

// TODO: auto-migrate people from the old way? It was:
/*
	res, err := h.search.GetPermanodesWithAttr(&search.WithAttrRequest{
		N:     2, // only expect 1
		Attr:  "camliImportRoot",
		Value: "TODO", // h.imp.Prefix(),
	})
*/

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
