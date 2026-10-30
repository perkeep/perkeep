/*
Copyright 2013 The Perkeep Authors

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
package importer // import "perkeep.org/pkg/importer"

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/health"
	"perkeep.org/pkg/jsonsign/signhandler"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/server"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/types/camtypes"
	"perkeep.org/pkg/webserver"

	"go4.org/ctxutil"
	"go4.org/jsonconfig"
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
	// attrUsePaidAPI is "true" on the accounts of importers with
	// Properties.HasSomePaidAPI whose runs use the paid API(s).
	attrUsePaidAPI = "importUsePaidAPI"
)

// An Importer imports from a third-party site.
type Importer interface {
	// Run runs a full or incremental import.
	//
	// The importer should continually or periodically monitor the
	// RunContext.Context()'s Done channel to exit early if
	// requested. The return value should be ctx.Err() if the
	// importer exits for that reason.
	Run(*RunContext) error

	// Properties returns properties of this importer type.
	Properties() Properties

	// IsAccountReady reports whether the provided account node
	// is configured.
	IsAccountReady(acctNode *Object) (ok bool, err error)
	SummarizeAccount(acctNode *Object) string

	ServeSetup(w http.ResponseWriter, r *http.Request, ctx *SetupContext) error
	ServeCallback(w http.ResponseWriter, r *http.Request, ctx *SetupContext)

	// CallbackRequestAccount extracts the blobref of the importer account from
	// the callback URL parameters of r. For example, it will be encoded as:
	// For Twitter (OAuth1), in its own URL parameter: "acct=sha1-f2b0b7da718b97ce8c31591d8ed4645c777f3ef4"
	// For OAuth2 importers, in the OAuth2 "state" parameter: "state=acct:sha1-97911b1a5887eb5862d1c81666ba839fc1363ea1"
	CallbackRequestAccount(r *http.Request) (acctRef blob.Ref, err error)

	// CallbackURLParameters uses the input importer account blobRef to build
	// and return the URL parameters, that will be appended to the callback URL.
	CallbackURLParameters(acctRef blob.Ref) url.Values
}

// Properties contains the properties of an importer type.
type Properties struct {
	Title       string
	Description string

	// TODOIssue, if non-zero, marks the importer as invalid, but the UI
	// will link to a tracking bug for implementing it.
	TODOIssue int

	// NeedsAPIKey reports whether this importer requires an API key
	// (OAuth2 client_id & client_secret, or equivalent).
	// If the API only requires a username & password, or a flow to get
	// an auth token per-account without an overall API key, importers
	// can return false here.
	NeedsAPIKey bool

	// SupportsIncremental reports whether this importer has been optimized
	// to run efficiently in regular incremental runs. (e.g. every 5 minutes
	// or half hour). Eventually all importers might support this and we'll
	// make it required, in which case we might delete this option.
	// For now, some importers (e.g. Flickr) don't yet support this.
	SupportsIncremental bool

	// ArchiveOnly marks importers whose only input is an uploaded archive.
	// They must implement ArchiveImporter. Their accounts are resolved from
	// archive identity, not created by OAuth setup, and never run periodically.
	// ArchiveImporter alone does not imply ArchiveOnly: an API importer may
	// also support optional archive inputs.
	ArchiveOnly bool

	// PermanodeImporterType optionally specifies the "importerType"
	// permanode attribute value that should be stored for
	// accounts of this type. By default, it is the string that it
	// was registered with. This should only be specified for
	// products that have been rebranded, and then this should be
	// the old branding, to not break people who have been
	// importing the account since before the rebranding.
	// For example, this is "foursquare" for "swarm", so "swarm" shows
	// in the UI and URLs, but it's "foursquare" in permanodes.
	PermanodeImporterType string

	// HasSomePaidAPI specifies whether this service has part of
	// its API requiring payment or quota for optional bits of data
	// (such as Swarm venue photos).
	// Whether or not to use such paid API(s) is determined for each
	// run of each importer's account by the user.
	HasSomePaidAPI bool
}

// LongPoller is optionally implemented by importers which can long
// poll efficiently to wait for new content.
// For example, Twitter uses this to subscribe to the user's stream.
type LongPoller interface {
	Importer

	// LongPoll waits and returns nil when there's new content.
	// It does not fetch the content itself.
	// It returns a non-nil error if it failed to long poll.
	LongPoll(*RunContext) error
}

// TestDataMaker is an optional interface that may be implemented by Importers to
// generate test data locally. The returned Roundtripper will be used as the
// transport of the HTTPClient, in the RunContext that will be passed to Run
// during tests and devcam server --makethings.
// (See http://perkeep.org/issue/417).
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

var (
	importers           = map[string]Importer{}
	reservedImporterKey = map[string]bool{}

	// removedImporters are importers that no longer exist, as the
	// services' APIs they used are gone, whose configuration in an
	// importer handler's arguments is ignored, rather than an error.
	removedImporters = []string{"gphotos", "picasa"}
)

// All returns the map of importer implementation name to implementation. This
// map should not be mutated.
func All() map[string]Importer {
	return importers
}

// Register registers a site-specific importer. It should only be called from init,
// and not from concurrent goroutines. Names must not contain ':'.
func Register(name string, im Importer) {
	if strings.Contains(name, ":") {
		panic(fmt.Sprintf("importer: name %q must not contain ':'", name))
	}
	if _, dup := importers[name]; dup {
		panic("Dup registration of importer " + name)
	}
	if _, dup := reservedImporterKey[name]; dup {
		panic("Dup registration of importer " + name)
	}
	if pt := im.Properties().PermanodeImporterType; pt != "" {
		if _, dup := importers[pt]; dup {
			panic("Dup registration of importer " + pt)
		}
		if _, dup := reservedImporterKey[pt]; dup {
			panic("Dup registration of importer " + pt)
		}
		reservedImporterKey[pt] = true
	}
	importers[name] = im
}

func RegisterTODO(name string, p Properties) {
	Register(name, &todoImp{Props: p})
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

	// Cache optionally supplies a shared backing store for scoped importer
	// caches. The caller owns its lifetime. It cannot be combined with
	// CacheRoot or CacheType. Without Cache or CacheRoot, each scope gets
	// its own memory store.
	// Cached data is disposable and must not be required for correctness.
	Cache sorted.KeyValue

	// CacheRoot optionally names a local directory for persistent importer
	// caches. Each importer and account has its own store, opened lazily.
	// An empty root uses memory only, without looking up a home directory.
	// Persistent cache state is disposable, not authoritative data.
	CacheRoot string

	// CacheType names the local file backend: leveldb, sqlite, or kv.
	// It defaults to sorted.DefaultKVFileType and has no effect without
	// CacheRoot. Applications must register non-default backends they use.
	CacheType string
	// TODO: add more if/when needed
}

func NewHost(hc HostConfig) (*Host, error) {
	importerURL, err := url.Parse(hc.BaseURL + hc.Prefix)
	if err != nil {
		return nil, fmt.Errorf("importer: invalid base URL: %w", err)
	}
	importerPath := importerURL.EscapedPath()
	if importerPath == "" {
		importerPath = "/"
	}
	if !strings.HasPrefix(importerPath, "/") || strings.HasPrefix(importerPath, "//") {
		return nil, fmt.Errorf("importer: invalid handler path %q", importerPath)
	}
	if hc.Cache != nil && (hc.CacheRoot != "" || hc.CacheType != "") {
		return nil, errors.New("importer: Cache cannot be combined with CacheRoot or CacheType")
	}
	if hc.CacheType == "" {
		hc.CacheType = sorted.DefaultKVFileType
	}
	switch hc.CacheType {
	case "leveldb", "sqlite", "kv":
	default:
		return nil, fmt.Errorf("importer: unsupported cacheType %q", hc.CacheType)
	}
	h := &Host{
		baseURL:       hc.BaseURL,
		importerBase:  hc.BaseURL + hc.Prefix,
		importerPath:  importerPath,
		discoveryRoot: "/",
		imp:           make(map[string]*importer),
		cacheRoot:     hc.CacheRoot,
		cacheType:     hc.CacheType,
	}
	h.tmpl, err = tmpl.Clone()
	if err != nil {
		return nil, err
	}
	h.tmpl = h.tmpl.Funcs(map[string]any{
		"bloblink": func(br blob.Ref) template.HTML {
			if h.uiPrefix == "" {
				return template.HTML(br.String())
			}
			return template.HTML(fmt.Sprintf("<a href=\"%s/%s\">%s</a>", h.uiPrefix, br, br))
		},
		"bloburl": func(br blob.Ref) string {
			if h.uiPrefix == "" {
				return ""
			}
			return h.uiPrefix + "/" + br.String()
		},
	})
	for k, impl := range importers {
		h.importers = append(h.importers, k)
		clientId, clientSecret := hc.ClientId[k], hc.ClientSecret[k]
		if clientSecret != "" && clientId == "" {
			return nil, fmt.Errorf("Invalid static configuration for importer %q: clientSecret specified without clientId", k)
		}
		props := impl.Properties()
		if _, ok := impl.(ArchiveImporter); props.ArchiveOnly && !ok {
			return nil, fmt.Errorf("importer %q: ArchiveOnly requires ArchiveImporter", k)
		}
		imp := &importer{
			host:         h,
			name:         k,
			impl:         impl,
			props:        &props,
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
	h.cache = hc.Cache
	h.ctx, h.cancel = context.WithCancel(context.Background())

	return h, nil
}

func newFromConfig(ld blobserver.Loader, cfg jsonconfig.Obj) (http.Handler, error) {
	hc := HostConfig{
		BaseURL:   ld.BaseURL(),
		Prefix:    ld.MyPrefix(),
		CacheRoot: cfg.OptionalString("cacheRoot", ""),
		CacheType: cfg.OptionalString("cacheType", ""),
	}
	ClientId := make(map[string]string)
	ClientSecret := make(map[string]string)
	for k := range importers {
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
				return nil, fmt.Errorf("Invalid static configuration for importer %q: %w", k, err)
			}
			ClientId[k] = clientId
			ClientSecret[k] = clientSecret
		}
	}
	for _, k := range removedImporters {
		if cfg.OptionalObject(k) != nil {
			log.Printf("importer: ignoring the configuration of the %q importer, which no longer exists", k)
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
	host.health = ld.Health()
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

// ImporterCache returns local mutable state shared by this importer's accounts.
// Keys are automatically isolated from other importers and account caches.
// Persistent stores open lazily and may return an error. Entries are disposable
// optimizations, not authoritative data; absence must not affect correctness.
// Closing the view is a no-op and never closes the backing store.
func (sc *SetupContext) ImporterCache() (sorted.KeyValue, error) { return sc.ia.im.cache() }

// AccountCache returns local mutable state isolated to this importer account.
// Persistent stores open lazily and may return an error. Entries are disposable
// optimizations, not authoritative data; absence must not affect correctness.
// Closing the view is a no-op and never closes the backing store.
func (sc *SetupContext) AccountCache() (sorted.KeyValue, error) { return sc.ia.cache() }

func (sc *SetupContext) Credentials() (clientID, clientSecret string, err error) {
	return sc.ia.im.credentials()
}

// CallbackURL returns the fully qualified callback URL for OAuth providers.
// Use CallbackPath for forms and redirects handled by this server.
func (sc *SetupContext) CallbackURL() string {
	return sc.Host.ImporterBaseURL() + sc.callbackSuffix()
}

// CallbackPath returns the same-origin absolute URL path to the callback,
// including its account-identifying query parameters.
func (sc *SetupContext) CallbackPath() string {
	return sc.Host.ImporterBasePath() + sc.callbackSuffix()
}

func (sc *SetupContext) callbackSuffix() string {
	params := sc.ia.im.impl.CallbackURLParameters(sc.AccountNode.PermanodeRef()).Encode()
	if params != "" {
		params = "?" + params
	}
	return sc.ia.im.name + "/callback" + params
}

// AccountURL returns the same-origin absolute URL path to an importer account
// (for example, /importer/TYPE/sha1-sd8fsd7f8sdf7).
func (sc *SetupContext) AccountURL() string {
	return sc.ia.im.URL() + "/" + sc.AccountNode.PermanodeRef().String()
}

// RunContext is the context provided for a given Run of an importer, importing
// a certain account on a certain importer.
type RunContext struct {
	ctx    context.Context
	cancel context.CancelFunc // for when we stop/pause the importing
	Host   *Host

	ia *importerAcct

	mu         sync.Mutex // guards following
	archiveJob *camtypes.ArchiveImportStatus
}

// UsePaidAPI reports whether the run is going to use the paid API(s) when retrieving data.
func (rc *RunContext) UsePaidAPI() bool {
	return rc.ia.UsePaidAPI()
}

// Context returns the run's context. It is always non-nil.
func (rc *RunContext) Context() context.Context {
	if rc.ctx != nil {
		return rc.ctx
	}
	return context.Background()
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
		return nil, fmt.Errorf("could not create new account for importer %v: %w", impl, err)
	}
	rc := &RunContext{
		Host: ia.im.host,
		ia:   ia,
	}
	rc.ctx, rc.cancel = context.WithCancel(context.WithValue(h.ctx, ctxutil.HTTPClient, h.HTTPClient()))
	return rc, nil

}

// Credentials returns the credentials for the importer. This is
// typically the OAuth1, OAuth2, or equivalent client ID (api token)
// and client secret (api secret).
func (rc *RunContext) Credentials() (clientID, clientSecret string, err error) {
	return rc.ia.im.credentials()
}

// ImporterCache returns local mutable state shared by this importer's accounts.
// Keys are automatically isolated from other importers and account caches.
// Persistent stores open lazily and may return an error. Entries are disposable
// optimizations, not authoritative data; absence must not affect correctness.
// Closing the view is a no-op and never closes the backing store.
func (rc *RunContext) ImporterCache() (sorted.KeyValue, error) { return rc.ia.im.cache() }

// AccountCache returns local mutable state isolated to this importer account.
// Persistent stores open lazily and may return an error. Entries are disposable
// optimizations, not authoritative data; absence must not affect correctness.
// Closing the view is a no-op and never closes the backing store.
func (rc *RunContext) AccountCache() (sorted.KeyValue, error) { return rc.ia.cache() }

// ImporterNode returns the configuration permanode shared by all accounts of
// this importer. Use ImporterCache for local disposable state rather than
// creating a permanent claim for each update.
// Do not change its camliNodeType, importerType, or credential attributes.
func (rc *RunContext) ImporterNode() (*Object, error) { return rc.ia.im.Node() }

// AccountNode returns the permanode storing account information for this permanode.
// It will contain the attributes:
//   - camliNodeType = "importerAccount"
//   - importerType = "registered-type"
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
	tmpl          *template.Template
	importers     []string // sorted; e.g. dummy flickr foursquare picasa twitter
	imp           map[string]*importer
	baseURL       string
	importerBase  string
	importerPath  string
	discoveryRoot string // root handler prefix, used for browser discovery
	target        blobserver.StatReceiver
	blobSource    blob.Fetcher // e.g. twitter reading zip file
	search        search.QueryDescriber
	signer        *schema.Signer
	uiPrefix      string          // or empty if no UI handler
	health        *health.Monitor // or nil; no runs start while it's read-only

	cache     sorted.KeyValue // explicitly injected caller-owned backing; otherwise nil
	cacheRoot string
	cacheType string

	cacheMu     sync.Mutex // guards opening scopes and the cache-close phase
	cacheClosed bool
	cacheStores []sorted.KeyValue // owned stores, closed only after managed work returns

	// Managed runs and long polls inherit ctx. workMu serializes adding work
	// with cancellation, so Close can safely wait before closing the cache.
	ctx       context.Context
	cancel    context.CancelFunc
	workMu    sync.Mutex
	work      sync.WaitGroup
	closeOnce sync.Once
	closeErr  error

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

// Close cancels managed importer runs and long polls and waits up to one second
// for them to return and the owned caches to close. If shutdown times out, Close
// returns an error; cleanup continues in the background, leaving the cache open
// until managed work has returned. Importers should honor cancellation.
// Callers must stop any direct uses of the Host (including runs made outside
// RunImporterAccount) before calling Close. Repeated calls return the same result.
func (h *Host) Close() error {
	return h.closeWithTimeout(time.Second)
}

func (h *Host) closeWithTimeout(timeout time.Duration) error {
	h.closeOnce.Do(func() {
		// Gate new work synchronously before starting the waiter. Every
		// successful beginWork has added to work before we release workMu.
		h.workMu.Lock()
		h.cancel()
		h.workMu.Unlock()

		done := make(chan error, 1)
		go func() {
			h.work.Wait()
			done <- h.closeCaches()
		}()
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case h.closeErr = <-done:
		case <-timer.C:
			h.closeErr = fmt.Errorf("importer host shutdown timed out after %v; cleanup continues in background", timeout)
		}
	})
	return h.closeErr
}

func (h *Host) beginWork() bool {
	h.workMu.Lock()
	defer h.workMu.Unlock()
	if h.ctx.Err() != nil {
		return false
	}
	h.work.Add(1)
	return true
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
func (h *Host) AccountsStatus() (any, []camtypes.StatusError) {
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

// RunImporterAccount runs the importerType importer on the account described in
// accountNode.
func (h *Host) RunImporterAccount(importerType string, accountNode blob.Ref) error {
	h.didInit.Wait()
	imp, ok := h.imp[importerType]
	if !ok {
		return fmt.Errorf("no %q importer for this account", importerType)
	}
	accounts, err := imp.Accounts()
	if err != nil {
		return err
	}
	for _, ia := range accounts {
		if ia.acct.pn != accountNode {
			continue
		}
		return ia.run()
	}
	return fmt.Errorf("no %v account matching account in node %v", importerType, accountNode)
}

func (h *Host) InitHandler(hl blobserver.FindHandlerByTyper) error {
	if prefix, _, err := hl.FindHandlerByType("ui"); err == nil {
		h.uiPrefix = prefix
	}

	rootPrefix, handler, err := hl.FindHandlerByType("root")
	if err != nil || handler == nil {
		return errors.New("importer requires a 'root' handler")
	}
	rh := handler.(*server.RootHandler)
	h.discoveryRoot = rootPrefix
	searchHandler, ok := rh.SearchHandler()
	if !ok {
		return errors.New("importer requires a 'root' handler with 'searchRoot' defined")
	}
	h.search = searchHandler
	if rh.Storage == nil {
		return errors.New("importer requires a 'root' handler with 'blobRoot' defined")
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
//
//	http://host/importer/
//	http://host/importer/twitter/
//	http://host/importer/twitter/callback
//	http://host/importer/twitter/sha1-abcabcabcabcabc (single account)
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
	if seg[1] == "archive" && len(seg) == 2 {
		h.serveArchive(w, r, imp)
		return
	}
	if seg[1] == "archive.js" && len(seg) == 2 {
		h.serveArchiveScript(w, r, imp)
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
	if r.Method == "POST" {
		// The form to add an account names its importer.
		imp, ok := h.imp[r.FormValue("importer")]
		if !ok || r.FormValue("mode") != "newacct" {
			http.Error(w, "unknown importer", http.StatusBadRequest)
			return
		}
		h.serveNewAccount(w, r, imp)
		return
	}
	body := importersRootBody{
		Host:      h,
		Importers: make([]*importer, 0, len(h.imp)),
	}
	for _, v := range h.importers {
		body.Importers = append(body.Importers, h.imp[v])
	}
	sort.Slice(body.Importers, func(i, j int) bool {
		return body.Importers[i].Title() < body.Importers[j].Title()
	})
	for _, im := range body.Importers {
		if im.props.TODOIssue != 0 {
			continue
		}
		rows, err := im.accountRows()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		body.Accounts = append(body.Accounts, rows...)
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
	if setuper, ok := imp.impl.(ImporterSetupHTMLer); ok && imp.ShowClientAuthEditForm() {
		setup = setuper.AccountSetupHTML(h)
	}
	accounts, err := imp.accountRows()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	isTailscale := webserver.TailscaleCtxKey.Value(r.Context())
	isTLS := r.TLS != nil

	h.execTemplate(w, r, importerPage{
		Title: imp.Title(),
		Body: importerBody{
			Host:                       h,
			ShowSecureTransportWarning: !isTailscale && !isTLS,
			Importer:                   imp,
			Accounts:                   accounts,
			SetupHelp:                  template.HTML(setup),
			ArchiveUpload:              imp.archiveUpload(nil),
		},
	})
}

// serveNewAccount starts the setup of a new account of imp. The account
// isn't stored until its setup is saved. An importer that needs an API
// key it doesn't have, or whose accounts come only from archives, gets
// its own page instead, to configure it or upload an archive.
func (h *Host) serveNewAccount(w http.ResponseWriter, r *http.Request, imp *importer) {
	if imp.props.ArchiveOnly || !imp.CanAddNewAccount() {
		http.Redirect(w, r, imp.URL(), http.StatusSeeOther)
		return
	}
	ia, err := imp.newPendingAccount()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	ia.setup(w, r)
}

// Serves oauth callback at http://host/importer/TYPE/callback
func (h *Host) serveImporterAcctCallback(w http.ResponseWriter, r *http.Request, imp *importer) {
	if r.Method != "GET" {
		http.Error(w, "invalid method", http.StatusBadRequest)
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
		http.Error(w, "invalid 'acct' param: "+err.Error(), http.StatusBadRequest)
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
		http.Error(w, "Unknown mode.", http.StatusBadRequest)
	case "newacct":
		h.serveNewAccount(w, r, imp)
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
		http.Redirect(w, r, imp.URL(), http.StatusFound)
	}
}

// Serves details of accounts at http://host/importer/twitter/sha1-23098429382934
func (h *Host) serveImporterAccount(w http.ResponseWriter, r *http.Request, imp *importer, acctRef blob.Ref) {
	ia, err := imp.account(acctRef)
	if err != nil {
		http.Error(w, "Unknown or invalid importer account "+acctRef.String()+": "+err.Error(), http.StatusBadRequest)
		return
	}
	ia.ServeHTTP(w, r)
}

func (h *Host) startPeriodicImporters() {
	if !h.beginWork() {
		return
	}
	defer h.work.Done()
	res, err := h.search.Query(h.ctx, &search.SearchQuery{
		Sort:       search.Unsorted,
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
	if ia.im.ArchiveOnly() {
		return
	}
	h := ia.im.host
	if !h.beginWork() {
		return
	}
	defer h.work.Done()
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
		// Kick off long poller wait if supported.
		if lp, ok := ia.im.impl.(LongPoller); ok {
			if !h.beginWork() {
				return
			}
			sleepFor := time.Until(ia.lastRunDone.Add(duration))
			sleepCtx, cancel := context.WithTimeout(h.ctx, sleepFor)
			log.Printf("%v ran recently enough. Sleeping for %v.", ia, sleepFor)
			timer := time.AfterFunc(sleepFor, ia.maybeStart)

			rc := &RunContext{
				ctx:  sleepCtx,
				Host: ia.im.host,
				ia:   ia,
			}
			go func() {
				defer h.work.Done()
				defer cancel()
				defer func() {
					if h.ctx.Err() != nil {
						timer.Stop()
					}
				}()

				if err := lp.LongPoll(rc); err == nil {
					log.Printf("importer: long poll for %s found an update. Starting run...", ia)
					timer.Stop()
					ia.start()
				} else {
					log.Printf("failed to long poll %s: %v", ia, err)
				}
			}()
		}
		return
	}

	log.Printf("importer: starting periodic import for %v", ia)
	go ia.start()
}

// BaseURL returns the root of the whole server, without trailing
// slash.
func (h *Host) BaseURL() string {
	return h.baseURL
}

// ImporterBaseURL returns the fully qualified URL base of the importer handler,
// including trailing slash, for OAuth callbacks and provider configuration.
// Use ImporterBasePath for same-origin browser navigation.
func (h *Host) ImporterBaseURL() string {
	return h.importerBase
}

// ImporterBasePath returns the absolute URL path of the importer handler,
// including trailing slash, for same-origin links and redirects.
// Unlike ImporterBaseURL, it has no scheme or authority.
func (h *Host) ImporterBasePath() string {
	return h.importerPath
}

func (h *Host) Target() blobserver.StatReceiver {
	return h.target
}

func (h *Host) BlobSource() blob.Fetcher {
	return h.blobSource
}

func (h *Host) Searcher() search.QueryDescriber { return h.search }

// Signer returns the signer of the host's permanodes and claims, for
// importers that sign their own, such as planned permanodes.
func (h *Host) Signer() *schema.Signer { return h.signer }

// importer is an importer for a certain site, but not a specific account on that site.
type importer struct {
	cacheOnce  sync.Once
	cacheStore sorted.KeyValue
	cacheErr   error

	host  *Host
	name  string // importer name e.g. "twitter"
	impl  Importer
	props *Properties // impl.Properties; pointer so we crash on & find uninitialized callers

	// If statically configured in config file, else
	// they come from the importer node's attributes.
	clientID     string
	clientSecret string

	nodemu    sync.Mutex // guards nodeCache
	nodeCache *Object    // or nil if unset

	acctmu    sync.Mutex
	acct      map[blob.Ref]*importerAcct // key: account permanode
	allLoaded bool
	// pending are the new accounts whose permanodes aren't stored yet
	// (see newPendingAccount), by account permanode.
	pending map[blob.Ref]*importerAcct

	archiveMu sync.Mutex // serializes archive account resolution and creation
}

func (im *importer) Name() string { return im.name }

func (im *importer) Title() string {
	if im.props.Title != "" {
		return im.props.Title
	}
	return im.name
}

func (im *importer) TODOIssue() int {
	return im.props.TODOIssue
}

func (im *importer) Description() string {
	return im.props.Description
}

func (im *importer) ArchiveOnly() bool { return im.props != nil && im.props.ArchiveOnly }

// ImporterType returns the account permanode's attrImporterType
// value. This is almost always the same as the Name, except in cases
// where a product gets rebranded. (e.g. "foursquare" to "swarm", in
// which case the importer type remains the old branding)
func (im *importer) ImporterType() string {
	if im.props.PermanodeImporterType != "" {
		return im.props.PermanodeImporterType
	}
	return im.name
}

func (im *importer) StaticConfig() bool { return im.clientSecret != "" }

// URL returns the importer's same-origin absolute URL path without trailing slash.
func (im *importer) URL() string { return im.host.ImporterBasePath() + im.name }

func (im *importer) ShowClientAuthEditForm() bool {
	if im.StaticConfig() {
		// Don't expose the server's statically-configured client secret
		// to the user. (e.g. a hosted multi-user configuration)
		return false
	}
	return im.props.NeedsAPIKey
}

func (im *importer) CanAddNewAccount() bool {
	if !im.props.NeedsAPIKey {
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
	if !im.props.NeedsAPIKey {
		return "no configuration required", nil
	}
	if im.StaticConfig() {
		return "API key configured on server", nil
	}
	n, err := im.existingNode()
	if err != nil {
		return
	}
	if n != nil && n.Attr(attrClientID) != "" && n.Attr(attrClientSecret) != "" {
		return "API key configured on node", nil
	}
	return "API key (client ID & Secret) not configured", nil
}

func (im *importer) credentials() (clientID, clientSecret string, err error) {
	if im.StaticConfig() {
		return im.clientID, im.clientSecret, nil
	}
	n, err := im.existingNode()
	if err != nil || n == nil {
		return "", "", err
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
	if !ok {
		ia, ok = im.pending[nodeRef]
	}
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
	if acct.Attr(attrImporterType) != im.ImporterType() {
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
	// Another request may have loaded this account while we were describing
	// its objects. Use the canonical instance so account state and importer
	// synchronization are not split between independently cached Objects.
	if existing := im.acct[nodeRef]; existing != nil {
		return existing, nil
	}
	im.addAccountLocked(ia)
	return ia, nil
}

func (im *importer) newAccount() (*importerAcct, error) {
	return im.newAccountWithAttrs()
}

// accountAttrs returns the attributes of a new account permanode with
// import root root, followed by attrs.
func (im *importer) accountAttrs(root blob.Ref, attrs ...string) []string {
	return append([]string{
		"title", fmt.Sprintf("%s account", im.name),
		attrNodeType, nodeTypeImporterAccount,
		attrImporterType, im.ImporterType(),
		attrImportRoot, root.String(),
	}, attrs...)
}

// Pending accounts are kept for pendingAccountTTL, and at most
// maxPendingAccounts of them.
const (
	pendingAccountTTL  = time.Hour
	maxPendingAccounts = 100
)

// newPendingAccount returns a new account, for its setup, whose
// permanode (and import root's) isn't stored until its first write,
// such as when its setup is saved. Until then it isn't listed, and
// it's forgotten after pendingAccountTTL.
func (im *importer) newPendingAccount() (*importerAcct, error) {
	acct, err := im.host.newPendingObject()
	if err != nil {
		return nil, err
	}
	root, err := im.host.newPendingObject()
	if err != nil {
		return nil, err
	}
	base := im.accountAttrs(root.PermanodeRef())
	acct.attr = make(map[string][]string)
	for i := 0; i < len(base); i += 2 {
		acct.attr[base[i]] = []string{base[i+1]}
	}
	ia := &importerAcct{im: im, acct: acct, root: root, pendingSince: time.Now()}
	storeAcct := acct.store
	acct.store = func(ctx context.Context) error {
		if err := root.storeIfPending(ctx); err != nil {
			return err
		}
		if err := storeAcct(ctx); err != nil {
			return err
		}
		var claims []*schema.Builder
		for i := 0; i < len(base); i += 2 {
			claims = append(claims, schema.NewSetAttributeClaim(blob.Ref{}, base[i], base[i+1]))
		}
		if err := acct.uploadAttributeClaims(ctx, claims); err != nil {
			return err
		}
		im.acctmu.Lock()
		defer im.acctmu.Unlock()
		delete(im.pending, acct.PermanodeRef())
		im.addAccountLocked(ia)
		return nil
	}

	im.acctmu.Lock()
	defer im.acctmu.Unlock()
	if im.pending == nil {
		im.pending = make(map[blob.Ref]*importerAcct)
	}
	for ref, p := range im.pending {
		if time.Since(p.pendingSince) > pendingAccountTTL {
			delete(im.pending, ref)
		}
	}
	for len(im.pending) >= maxPendingAccounts {
		var oldest *importerAcct
		for _, p := range im.pending {
			if oldest == nil || p.pendingSince.Before(oldest.pendingSince) {
				oldest = p
			}
		}
		delete(im.pending, oldest.acct.PermanodeRef())
	}
	im.pending[acct.PermanodeRef()] = ia
	return ia, nil
}

// forgetPending forgets ia, if it's a pending account, and reports
// whether it was.
func (im *importer) forgetPending(ia *importerAcct) bool {
	if !ia.acct.isPending() {
		return false
	}
	im.acctmu.Lock()
	defer im.acctmu.Unlock()
	delete(im.pending, ia.acct.PermanodeRef())
	return true
}

func (im *importer) newAccountWithAttrs(attrs ...string) (*importerAcct, error) {
	acct, err := im.host.NewObject()
	if err != nil {
		return nil, err
	}
	root, err := im.host.NewObject()
	if err != nil {
		return nil, err
	}
	attrs = im.accountAttrs(root.PermanodeRef(), attrs...)
	if err := acct.SetAttrs(attrs...); err != nil {
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
	im.acctmu.Lock()
	needQuery := !im.allLoaded
	im.acctmu.Unlock()

	if needQuery {
		res, err := im.host.search.Query(context.TODO(), &search.SearchQuery{
			Sort:  search.Unsorted,
			Limit: -1, // Account identity resolution must not stop at the default result limit.
			Expression: fmt.Sprintf("attr:%s:%s attr:%s:%s",
				attrNodeType, nodeTypeImporterAccount,
				attrImporterType, im.ImporterType(),
			),
		})
		if err != nil {
			return nil, err
		}
		for _, res := range res.Blobs {
			_, err := im.account(res.Blob) // caches account
			if err != nil {
				return nil, err
			}
		}
	}

	im.acctmu.Lock()
	defer im.acctmu.Unlock()
	im.allLoaded = true

	accts := make([]*importerAcct, 0, len(im.acct))
	for _, ia := range im.acct {
		accts = append(accts, ia)
	}
	sort.Sort(byImporterAccountPermanode(accts))
	return accts, nil
}

// node returns the importer node. (not specific to a certain account
// on that importer site)
//
// It is a permanode with:
//
//	camliNodeType: "importer"
//	importerType: "twitter"
//
// And optionally:
//
//	authClientID:     "xxx"    // e.g. api token
//	authClientSecret: "sdkojfsldfjlsdkf"
//
// Node makes it if it doesn't exist.
func (im *importer) Node() (*Object, error) { return im.node(true) }

// existingNode is like Node, but returns nil if the node doesn't exist,
// rather than make it, so that merely looking doesn't write anything.
func (im *importer) existingNode() (*Object, error) { return im.node(false) }

func (im *importer) node(create bool) (*Object, error) {
	im.nodemu.Lock()
	defer im.nodemu.Unlock()
	if im.nodeCache != nil {
		return im.nodeCache, nil
	}

	expr := fmt.Sprintf("attr:%s:%s attr:%s:%s",
		attrNodeType, nodeTypeImporter,
		attrImporterType, im.ImporterType(),
	)
	res, err := im.host.search.Query(context.TODO(), &search.SearchQuery{
		Sort:       search.Unsorted,
		Limit:      10, // might be more than one because of multiple blob hash types
		Expression: expr,
	})
	if err != nil {
		return nil, err
	}
	best, err := im.bestNode(res)
	if err != nil {
		return nil, err
	}
	if best != nil {
		im.nodeCache = best
		return best, nil
	}
	if !create {
		return nil, nil
	}

	o, err := im.host.NewObject()
	if err != nil {
		return nil, err
	}
	if err := o.SetAttrs(
		"title", fmt.Sprintf("%s importer", im.name),
		attrNodeType, nodeTypeImporter,
		attrImporterType, im.ImporterType(),
	); err != nil {
		return nil, err
	}

	im.nodeCache = o
	return o, nil
}

// bestNode picks the most populated importer node amongst res.Blobs, or nil if
// len(res.Blobs) == 0
// It is the caller's responsibility to create the correct request so that
// res.Blobs are all importer nodes.
func (im *importer) bestNode(res *search.SearchResult) (*Object, error) {
	if len(res.Blobs) == 0 {
		// let the caller create a fresh node
		return nil, nil
	}
	var best *Object
	for _, desb := range res.Blobs {
		o, err := im.host.ObjectFromRef(desb.Blob)
		if err != nil {
			return nil, err
		}
		if best == nil {
			best = o
			continue
		}
		if best.Attr(attrClientID) == "" && o.Attr(attrClientID) != "" {
			best = o
			continue
		}
		if best.Attr(attrClientSecret) == "" && o.Attr(attrClientSecret) != "" {
			best = o
			continue
		}
		if best.Attr("title") == "" && o.Attr("title") != "" {
			best = o
			continue
		}
		// all other things being equal, pick the most recent one, as it's more likely
		// to be a sha224 than a sha1
		if best.modtime.Before(o.modtime) {
			best = o
			continue
		}
	}
	return best, nil
}

// importerAcct is a long-lived type representing account
type importerAcct struct {
	cacheOnce  sync.Once
	cacheStore sorted.KeyValue
	cacheErr   error

	im   *importer
	acct *Object
	root *Object

	mu           sync.Mutex
	current      *RunContext // or nil if not running
	stopped      bool        // stop requested (context canceled)
	lastRunErr   error
	lastRunStart time.Time
	lastRunDone  time.Time
	lastArchive  *camtypes.ArchiveImportStatus
	// pendingSince, for an account whose permanode isn't stored yet
	// (see importer.newPendingAccount), is when it was made.
	pendingSince time.Time

	log accountLog // of its runs, for its page
}

func (ia *importerAcct) String() string {
	return fmt.Sprintf("%v importer account, %v", ia.im.name, ia.acct.PermanodeRef())
}

func (ia *importerAcct) delete() error {
	if ia.im.forgetPending(ia) {
		// It was never stored, so there's nothing to mark deleted.
		return nil
	}
	// Archive resolution, deletion and starting an account share this order:
	// importer archiveMu, then account mu, then Object attribute locks.
	ia.im.archiveMu.Lock()
	defer ia.im.archiveMu.Unlock()
	ia.mu.Lock()
	defer ia.mu.Unlock()
	if ia.current != nil {
		ia.current.cancel()
		ia.stopped = true
	}
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
	if old == "" && (ia.im.props.ArchiveOnly || !ia.im.props.SupportsIncremental) {
		return fmt.Errorf("Importer %q doesn't support automatic mode", ia.im.name)
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

// UsePaidAPI reports whether the importer is going to use the paid API(s)
// when retrieving data, as set on the account's page and kept on its
// permanode.
func (ia *importerAcct) UsePaidAPI() bool {
	return ia.im.props.HasSomePaidAPI && ia.acct.Attr(attrUsePaidAPI) == "true"
}

// setUsePaidAPI sets whether the account's runs use the paid API(s).
func (ia *importerAcct) setUsePaidAPI(use bool) error {
	if use {
		return ia.acct.SetAttrs(attrUsePaidAPI, "true")
	}
	return ia.acct.SetAttrsWithDeletes([]string{attrUsePaidAPI})
}

func (ia *importerAcct) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		ia.serveHTTPPost(w, r)
		return
	}
	if v := r.FormValue("log"); v != "" {
		ia.serveLog(w, v)
		return
	}
	status, running, _ := ia.status()
	ia.mu.Lock()
	defer ia.mu.Unlock()
	body := acctBody{
		Acct:           ia,
		Importer:       ia.im,
		Status:         status,
		Running:        running,
		HasSomePaidAPI: ia.im.props.HasSomePaidAPI,
		ArchiveUpload:  ia.im.archiveUpload(ia),
		ArchiveOnly:    ia.im.props.ArchiveOnly,
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
		if ia.im.props.ArchiveOnly {
			http.Redirect(w, r, ia.im.URL(), http.StatusSeeOther)
			return
		}
		ia.setup(w, r)
		return
	case "toggleauto":
		if err := ia.toggleAuto(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	case "togglepaidapi":
		if !ia.im.props.HasSomePaidAPI {
			http.Error(w, "importer has no paid API", http.StatusBadRequest)
			return
		}
		if err := ia.setUsePaidAPI(r.FormValue("usePaidAPI") == "on"); err != nil {
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
		http.Error(w, "Unknown mode", http.StatusBadRequest)
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
	if err := ia.startLocked(blob.Ref{}); err != nil {
		log.Printf("Not starting %v: %v", ia, err)
		ia.lastRunErr = err
	}
}

// startLocked starts the normal managed lifecycle, optionally selecting an
// archive input. ia.mu protects input selection and the active run together.
func (ia *importerAcct) startLocked(archiveRef blob.Ref, attrs ...string) error {
	h := ia.im.host
	if _, ok := ia.im.impl.(ArchiveImporter); ok && ia.acct.Attr(attrNodeType) != nodeTypeImporterAccount {
		return errors.New("archive importer account has been deleted or changed type")
	}
	if !h.beginWork() {
		return errors.New("importer host is closed")
	}
	if ia.current != nil {
		h.work.Done()
		return nil
	}
	if err := h.health.ReadOnlyError(); err != nil {
		h.work.Done()
		return err
	}
	job, err := ia.prepareArchiveLocked(archiveRef, attrs...)
	if err != nil {
		h.work.Done()
		return err
	}
	rc := &RunContext{
		Host:       ia.im.host,
		ia:         ia,
		archiveJob: job,
	}
	rc.ctx, rc.cancel = context.WithCancel(context.WithValue(h.ctx, ctxutil.HTTPClient, h.HTTPClient()))
	ia.current = rc
	ia.stopped = false
	ia.lastRunStart = time.Now()
	go func() {
		defer h.work.Done()
		defer rc.cancel()
		ia.logf("Starting import: %s", ia.AccountLinkSummary())
		err := rc.runManaged()
		if err == nil {
			err = rc.Context().Err()
		}
		err = rc.finishArchive(err)
		if err != nil {
			ia.logf("Import failed: %v", err)
		} else {
			ia.logf("Import finished.")
		}
		ia.mu.Lock()
		defer ia.mu.Unlock()
		rc.mu.Lock()
		if rc.archiveJob != nil {
			status := *rc.archiveJob
			ia.lastArchive = &status
		}
		rc.mu.Unlock()
		ia.current = nil
		ia.stopped = false
		ia.lastRunDone = time.Now()
		ia.lastRunErr = err
		go ia.maybeStart()
	}()
	return nil
}

// TODO(mpl): review this code more carefully. mostly copied from start().
func (ia *importerAcct) run() error {
	h := ia.im.host
	if !h.beginWork() {
		return errors.New("importer host is closed")
	}
	defer h.work.Done()
	ia.mu.Lock()
	defer ia.mu.Unlock()
	if ia.current != nil {
		return errors.New("whatever")
	}
	if err := h.health.ReadOnlyError(); err != nil {
		return err
	}
	rc := &RunContext{
		Host: ia.im.host,
		ia:   ia,
	}
	rc.ctx, rc.cancel = context.WithCancel(context.WithValue(h.ctx, ctxutil.HTTPClient, h.HTTPClient()))
	defer rc.cancel()
	ia.current = rc
	ia.stopped = false
	ia.lastRunStart = time.Now()
	ia.logf("Starting import: %s", ia.AccountLinkSummary())
	err := ia.im.impl.Run(rc)
	if err != nil {
		return err
	}
	ia.logf("Import finished.")
	ia.current = nil
	ia.stopped = false
	ia.lastRunDone = time.Now()
	return ia.lastRunErr
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

func (h *Host) upload(ctx context.Context, bb *schema.Builder) (br blob.Ref, err error) {
	unsigned, err := bb.JSON()
	if err != nil {
		return
	}
	// Sign adds envelope metadata and a signature. Leave headroom rather than
	// letting its schema parsing panic on an oversized batch; also check the
	// actual signed size before storing a blob the index would ignore.
	if len(unsigned) > schema.MaxSchemaBlobSize-1024 {
		return blob.Ref{}, fmt.Errorf("importer: schema blob too large to sign (%d bytes)", len(unsigned))
	}
	signed, err := bb.Sign(ctx, h.signer)
	if err != nil {
		return
	}
	if len(signed) > schema.MaxSchemaBlobSize {
		return blob.Ref{}, fmt.Errorf("importer: signed schema blob too large (%d bytes)", len(signed))
	}
	sb, err := blobserver.ReceiveString(ctx, h.target, signed)
	if err != nil {
		return
	}
	return sb.Ref, nil
}

// NewObject creates a new permanode and returns its Object wrapper.
func (h *Host) NewObject() (*Object, error) {
	ctx := context.TODO() // TODO: move the NewObject method to some "ImportRun" type with a context field?
	pn, err := h.upload(ctx, schema.NewUnsignedPermanode())
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

	mu      sync.RWMutex
	attr    map[string][]string
	modtime time.Time
	// store, if non-nil, stores the object's permanode (and whatever
	// else it was made with), which isn't stored yet. The object's
	// first write calls it, with mu held. See Host.newPendingObject.
	store func(context.Context) error
}

// newPendingObject returns an object of a new permanode that isn't
// stored until the object's first write, so that a form, say, can refer
// to it without leaving an empty permanode behind if it's never saved.
func (h *Host) newPendingObject() (*Object, error) {
	signed, err := schema.NewUnsignedPermanode().Sign(context.TODO(), h.signer)
	if err != nil {
		return nil, err
	}
	o := &Object{h: h, pn: blob.RefFromString(signed)}
	o.store = func(ctx context.Context) error {
		_, err := blobserver.ReceiveString(ctx, h.target, signed)
		return err
	}
	return o, nil
}

// isPending reports whether o's permanode isn't stored yet.
func (o *Object) isPending() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.store != nil
}

// storeIfPending stores o's permanode, if it isn't yet.
func (o *Object) storeIfPending(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.prepareWriteLocked(ctx)
}

// prepareWriteLocked stores o's permanode before a write, if it isn't
// stored yet. o.mu must be held.
func (o *Object) prepareWriteLocked(ctx context.Context) error {
	store := o.store
	if store == nil {
		return nil
	}
	// Clear it first, so that store can write to o.
	o.store = nil
	if err := store(ctx); err != nil {
		o.store = store
		return err
	}
	return nil
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

// DelAttr removes value from the values set for the attribute attr of
// permaNode. If value is empty then all the values for attribute are cleared.
func (o *Object) DelAttr(key, value string) error {
	ctx := context.TODO()
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.prepareWriteLocked(ctx); err != nil {
		return err
	}
	_, err := o.h.upload(ctx, schema.NewDelAttributeClaim(o.pn, key, value))
	if err != nil {
		return err
	}
	if o.attr == nil {
		o.attr = make(map[string][]string)
		return nil
	}
	if value == "" {
		delete(o.attr, key)
		return nil
	}
	var values []string
	for _, v := range o.attr[key] {
		if v != value {
			values = append(values, v)
		}
	}
	o.attr[key] = values
	return nil
}

// SetAttr sets the attribute key to value.
func (o *Object) SetAttr(key, value string) error {
	ctx := context.TODO() // TODO: make it possible to get a context via Object; either new context field, or via some "ImportRun" field?
	o.mu.Lock()
	defer o.mu.Unlock()
	old := ""
	if values := o.attr[key]; len(values) > 0 {
		old = values[0]
	}
	if old == value {
		return nil
	}
	if err := o.prepareWriteLocked(ctx); err != nil {
		return err
	}
	_, err := o.h.upload(ctx, schema.NewSetAttributeClaim(o.pn, key, value))
	if err != nil {
		return err
	}
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

// SetAttrs2 sets multiple attributes in one signed claim and returns whether
// any changes were attempted. Multiple changes use an atomic multi claim.
// The provided keyval should be an even number of alternating key/value pairs
// to set; repeated keys are processed in argument order.
func (o *Object) SetAttrs2(keyval ...string) (changes bool, err error) {
	return o.setAttrsWithDeletes(nil, keyval...)
}

// SetAttrsWithDeletes atomically removes whole attributes and sets attributes
// in one claim. Deletions are applied first, in deleteKeys order, then sets in
// keyval order. Repeated deletions are ignored; repeated sets retain their
// order. A key in both lists is therefore removed before it is set again.
//
// Unchanged operations are skipped. Cached attributes change only after the
// upload succeeds. The keyval list must contain alternating key/value pairs.
func (o *Object) SetAttrsWithDeletes(deleteKeys []string, keyval ...string) error {
	_, err := o.setAttrsWithDeletes(deleteKeys, keyval...)
	return err
}

func (o *Object) setAttrsWithDeletes(deleteKeys []string, keyval ...string) (changes bool, err error) {
	if len(keyval)%2 == 1 {
		panic("importer.SetAttrs: odd argument count")
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	updates := make(map[string][]string)
	var claims []*schema.Builder
	for _, key := range deleteKeys {
		if _, pending := updates[key]; pending || len(o.attr[key]) == 0 {
			continue
		}
		updates[key] = nil
		claims = append(claims, schema.NewDelAttributeClaim(blob.Ref{}, key, ""))
	}
	for i := 0; i < len(keyval); i += 2 {
		key, val := keyval[i], keyval[i+1]
		values, pending := updates[key]
		if !pending {
			values = o.attr[key]
		}
		old := ""
		if len(values) > 0 {
			old = values[0]
		}
		if val == old {
			continue
		}
		updates[key] = []string{val}
		claims = append(claims, schema.NewSetAttributeClaim(blob.Ref{}, key, val))
	}
	if len(claims) == 0 {
		return false, nil
	}
	if err := o.uploadAttributeClaims(context.TODO(), claims); err != nil {
		return true, err
	}
	if o.attr == nil {
		o.attr = make(map[string][]string)
	}
	for key, values := range updates {
		if values == nil {
			delete(o.attr, key)
		} else {
			o.attr[key] = values
		}
	}
	return true, nil
}

// SetAttrValues sets a multi-valued attribute. Multiple additions or deletions
// are uploaded together as an atomic multi claim.
func (o *Object) SetAttrValues(key string, attrs []string) error {
	ctx := context.TODO() // TODO: make it possible to get a context via Object; either new context field, or via some "ImportRun" field?

	o.mu.Lock()
	defer o.mu.Unlock()
	exists := asSet(o.attr[key])
	actual := asSet(attrs)
	var claims []*schema.Builder
	if exists[""] && !actual[""] {
		// An empty deletion value means delete-all, not delete an empty
		// string. Reset first, then rebuild the requested set in one batch.
		claims = append(claims, schema.NewDelAttributeClaim(blob.Ref{}, key, ""))
		exists = nil
	}
	// Add new values in input order, ignoring duplicates.
	added := make(map[string]bool)
	for _, v := range attrs {
		if !exists[v] && !added[v] {
			claims = append(claims, schema.NewAddAttributeClaim(blob.Ref{}, key, v))
			added[v] = true
		}
	}
	// Remove old values in their existing order, ignoring duplicates.
	for _, v := range o.attr[key] {
		if exists[v] && !actual[v] {
			claims = append(claims, schema.NewDelAttributeClaim(blob.Ref{}, key, v))
			delete(exists, v)
		}
	}
	if err := o.uploadAttributeClaims(ctx, claims); err != nil {
		return err
	}
	if o.attr == nil {
		o.attr = make(map[string][]string)
	}
	o.attr[key] = slices.Clone(attrs)
	return nil
}

// uploadAttributeClaims uploads one ordinary attribute claim or one compact
// multi claim. The caller holds o.mu and updates cached attributes only after
// this succeeds. A zero-length list does not upload a blob.
func (o *Object) uploadAttributeClaims(ctx context.Context, claims []*schema.Builder) error {
	if len(claims) == 0 {
		return nil
	}
	if err := o.prepareWriteLocked(ctx); err != nil {
		return err
	}
	claim := claims[0]
	if len(claims) == 1 {
		claim.SetRawStringField("permaNode", o.pn.String())
	} else {
		var err error
		claim, err = schema.NewMultiClaim(claims...)
		if err != nil {
			return err
		}
		claim.SetMultiClaimPermanode(o.pn)
	}
	_, err := o.h.upload(ctx, claim)
	return err
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

// ChildPathObjectOrFunc returns the child object from the permanode o,
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
	ctx := context.TODO()
	res, err := h.search.Describe(ctx, &search.DescribeRequest{
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
		h:       h,
		pn:      permanodeRef,
		attr:    map[string][]string(db.Permanode.Attr),
		modtime: db.Permanode.ModTime,
	}, nil
}

type byImporterAccountPermanode []*importerAcct

func (s byImporterAccountPermanode) Len() int { return len(s) }
func (s byImporterAccountPermanode) Less(i, j int) bool {
	return s[i].acct.PermanodeRef().Less(s[j].acct.PermanodeRef())
}
func (s byImporterAccountPermanode) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
