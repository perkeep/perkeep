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

// Package client implements a Camlistore client.
package client

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/client/android"
	"camlistore.org/pkg/hashutil"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/types/camtypes"

	"go4.org/syncutil"
	"golang.org/x/net/http2"
)

// A Client provides access to a Camlistore server.
type Client struct {
	// server is the input from user, pre-discovery.
	// For example "http://foo.com" or "foo.com:1234".
	// It is the responsibility of initPrefix to parse
	// server and set prefix, including doing discovery
	// to figure out what the proper server-declared
	// prefix is.
	server string

	prefixOnce    syncutil.Once // guards init of following 2 fields
	prefixv       string        // URL prefix before "/camli/"
	isSharePrefix bool          // URL is a request for a share blob

	discoOnce      syncutil.Once
	searchRoot     string      // Handler prefix, or "" if none
	downloadHelper string      // or "" if none
	storageGen     string      // storage generation, or "" if not reported
	syncHandlers   []*SyncInfo // "from" and "to" url prefix for each syncHandler
	serverKeyID    string      // Server's GPG public key ID.
	helpRoot       string      // Handler prefix, or "" if none

	signerOnce sync.Once
	signer     *schema.Signer
	signerErr  error

	authMode auth.AuthMode
	// authErr is set when no auth config is found but we want to defer warning
	// until discovery fails.
	authErr error

	httpClient *http.Client
	haveCache  HaveCache

	// If sto is set, it's used before the httpClient or other network operations.
	sto blobserver.Storage

	initTrustedCertsOnce sync.Once

	// We define a certificate fingerprint as the 20 digits lowercase prefix
	// of the SHA256 of the complete certificate (in ASN.1 DER encoding).
	// trustedCerts contains the fingerprints of the self-signed
	// certificates we trust.
	// If not empty, (and if using TLS) the full x509 verification is
	// disabled, and we instead check the server's certificate against
	// this list.
	// The camlistore server prints the fingerprint to add to the config
	// when starting.
	trustedCerts []string

	// insecureAnyTLSCert disables all TLS cert checking,
	// including the trustedCerts field above.
	insecureAnyTLSCert bool

	initIgnoredFilesOnce sync.Once
	// list of files that camput should ignore.
	// Defaults to empty, but camput init creates a config with a non
	// empty list.
	// See IsIgnoredFile for the matching rules.
	ignoredFiles  []string
	ignoreChecker func(path string) bool

	pendStatMu sync.Mutex             // guards pendStat
	pendStat   map[blob.Ref][]statReq // blobref -> reqs; for next batch(es)

	initSignerPublicKeyBlobrefOnce sync.Once
	signerPublicKeyRef             blob.Ref
	publicKeyArmored               string

	statsMutex sync.Mutex
	stats      Stats

	// via maps the access path from a share root to a desired target.
	// It is non-nil when in "sharing" mode, where the Client is fetching
	// a share.
	via map[string]string // target => via (target is referenced from via)

	log             *log.Logger // not nil
	httpGate        *syncutil.Gate
	transportConfig *TransportConfig // or nil

	paramsOnly bool // config file and env vars are ignored.
}

const maxParallelHTTP_h1 = 5
const maxParallelHTTP_h2 = 50

// New returns a new Camlistore Client.
// The provided server is either "host:port" (assumed http, not https) or a URL prefix, with or without a path, or a server alias from the client configuration file. A server alias should not be confused with a hostname, therefore it cannot contain any colon or period.
// Errors are not returned until subsequent operations.
func New(server string, opts ...ClientOption) *Client {
	if !isURLOrHostPort(server) {
		configOnce.Do(parseConfig)
		serverConf, ok := config.Servers[server]
		if !ok {
			log.Fatalf("%q looks like a server alias, but no such alias found in config at %v", server, osutil.UserClientConfigPath())
		}
		server = serverConf.Server
	}
	return newClient(server, auth.None{}, opts...)
}

func NewOrFail(opts ...ClientOption) *Client {
	c := New(serverOrDie(), opts...)
	err := c.SetupAuth()
	if err != nil {
		log.Fatal(err)
	}
	return c
}

// NewPathClient returns a new client accessing a subpath of c.
func (c *Client) NewPathClient(path string) *Client {
	u, err := url.Parse(c.server)
	if err != nil {
		// Better than nothing
		return New(c.server + path)
	}
	u.Path = path
	pc := New(u.String())
	pc.authMode = c.authMode
	pc.discoOnce.Do(noop)
	return pc
}

// NewStorageClient returns a Client that doesn't use HTTP, but uses s
// directly. This exists mainly so all the convenience methods on
// Client (e.g. the Upload variants) are available against storage
// directly.
// When using NewStorageClient, callers should call Close when done,
// in case the storage wishes to do a cleaner shutdown.
func NewStorageClient(s blobserver.Storage) *Client {
	return &Client{
		sto:       s,
		log:       log.New(os.Stderr, "", log.Ldate|log.Ltime),
		haveCache: noHaveCache{},
	}
}

// TransportConfig contains options for SetupTransport.
type TransportConfig struct {
	// Proxy optionally specifies the Proxy for the transport. Useful with
	// camput for debugging even localhost requests.
	Proxy   func(*http.Request) (*url.URL, error)
	Verbose bool // Verbose enables verbose logging of HTTP requests.
}

func (c *Client) useHTTP2(tc *TransportConfig) bool {
	if !c.useTLS() {
		return false
	}
	if android.IsChild() {
		// No particular reason; just untested so far.
		return false
	}
	if os.Getenv("HTTPS_PROXY") != "" || os.Getenv("https_proxy") != "" ||
		(tc != nil && tc.Proxy != nil) {
		// Also just untested. Which proxies support h2 anyway?
		return false
	}
	return true
}

// transportForConfig returns a transport for the client, setting the correct
// Proxy, Dial, and TLSClientConfig if needed. It does not mutate c.
// It is the caller's responsibility to then use that transport to set
// the client's httpClient with SetHTTPClient.
func (c *Client) transportForConfig(tc *TransportConfig) http.RoundTripper {
	if c == nil {
		return nil
	}
	var transport http.RoundTripper
	proxy := http.ProxyFromEnvironment
	if tc != nil && tc.Proxy != nil {
		proxy = tc.Proxy
	}

	if c.useHTTP2(tc) {
		transport = &http2.Transport{
			DialTLS: c.http2DialTLSFunc(),
		}
	} else {
		transport = &http.Transport{
			DialTLS:             c.DialTLSFunc(),
			Dial:                c.DialFunc(),
			Proxy:               proxy,
			MaxIdleConnsPerHost: maxParallelHTTP_h1,
		}
	}
	httpStats := &httputil.StatsTransport{
		Transport: transport,
	}
	if tc != nil {
		httpStats.VerboseLog = tc.Verbose
	}
	transport = httpStats
	if android.IsChild() {
		transport = &android.StatsTransport{transport}
	}
	return transport
}

// HTTPStats returns the client's underlying httputil.StatsTransport, if in use.
// If another transport is being used, nil is returned.
func (c *Client) HTTPStats() *httputil.StatsTransport {
	st, _ := c.httpClient.Transport.(*httputil.StatsTransport)
	return st
}

type ClientOption interface {
	modifyClient(*Client)
}

func OptionTransportConfig(tc *TransportConfig) ClientOption {
	return optionTransportConfig{tc}
}

type optionTransportConfig struct {
	tc *TransportConfig
}

func (o optionTransportConfig) modifyClient(c *Client) {
	c.transportConfig = o.tc
}

func OptionInsecure(v bool) ClientOption {
	return optionInsecure(v)
}

type optionInsecure bool

func (o optionInsecure) modifyClient(c *Client) {
	c.insecureAnyTLSCert = bool(o)
}

func OptionTrustedCert(cert string) ClientOption {
	return optionTrustedCert(cert)
}

type optionTrustedCert string

func (o optionTrustedCert) modifyClient(c *Client) {
	cert := string(o)
	if cert != "" {
		c.initTrustedCertsOnce.Do(func() {})
		c.trustedCerts = []string{string(o)}
	}
}

// noop is for use with syncutil.Onces.
func noop() error { return nil }

var shareURLRx = regexp.MustCompile(`^(.+)/(` + blob.Pattern + ")$")

// NewFromShareRoot uses shareBlobURL to set up and return a client that
// will be used to fetch shared blobs.
func NewFromShareRoot(shareBlobURL string, opts ...ClientOption) (c *Client, target blob.Ref, err error) {
	var root string
	m := shareURLRx.FindStringSubmatch(shareBlobURL)
	if m == nil {
		return nil, blob.Ref{}, fmt.Errorf("Unkown share URL base")
	}
	c = New(m[1], opts...)
	c.discoOnce.Do(noop)
	c.prefixOnce.Do(noop)
	c.prefixv = m[1]
	c.isSharePrefix = true
	c.authMode = auth.None{}
	c.via = make(map[string]string)
	root = m[2]

	req := c.newRequest("GET", shareBlobURL, nil)
	res, err := c.expect2XX(req)
	if err != nil {
		return nil, blob.Ref{}, fmt.Errorf("error fetching %s: %v", shareBlobURL, err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	b, err := schema.BlobFromReader(blob.ParseOrZero(root), io.TeeReader(res.Body, &buf))
	if err != nil {
		return nil, blob.Ref{}, fmt.Errorf("error parsing JSON from %s: %v , with response: %q", shareBlobURL, err, buf.Bytes())
	}
	if b.ShareAuthType() != schema.ShareHaveRef {
		return nil, blob.Ref{}, fmt.Errorf("unknown share authType of %q", b.ShareAuthType())
	}
	target = b.ShareTarget()
	if !target.Valid() {
		return nil, blob.Ref{}, fmt.Errorf("no target.")
	}
	c.via[target.String()] = root
	return c, target, nil
}

// SetHTTPClient sets the Camlistore client's HTTP client.
// If nil, the default HTTP client is used.
func (c *Client) SetHTTPClient(client *http.Client) {
	if client == nil {
		client = http.DefaultClient
	}
	c.httpClient = client
}

// HTTPClient returns the Client's underlying http.Client.
func (c *Client) HTTPClient() *http.Client {
	return c.httpClient
}

// A HaveCache caches whether a remote blobserver has a blob.
type HaveCache interface {
	StatBlobCache(br blob.Ref) (size uint32, ok bool)
	NoteBlobExists(br blob.Ref, size uint32)
}

type noHaveCache struct{}

func (noHaveCache) StatBlobCache(blob.Ref) (uint32, bool) { return 0, false }
func (noHaveCache) NoteBlobExists(blob.Ref, uint32)       {}

func (c *Client) SetHaveCache(cache HaveCache) {
	if cache == nil {
		cache = noHaveCache{}
	}
	c.haveCache = cache
}

func (c *Client) SetLogger(logger *log.Logger) {
	if logger == nil {
		c.log = log.New(ioutil.Discard, "", 0)
	} else {
		c.log = logger
	}
}

func (c *Client) Stats() Stats {
	c.statsMutex.Lock()
	defer c.statsMutex.Unlock()
	return c.stats // copy
}

// ErrNoSearchRoot is returned by SearchRoot if the server doesn't support search.
var ErrNoSearchRoot = errors.New("client: server doesn't support search")

// ErrNoHelpRoot is returned by HelpRoot if the server doesn't have a help handler.
var ErrNoHelpRoot = errors.New("client: server does not have a help handler")

// ErrNoSigning is returned by ServerKeyID if the server doesn't support signing.
var ErrNoSigning = fmt.Errorf("client: server doesn't support signing")

// ErrNoStorageGeneration is returned by StorageGeneration if the
// server doesn't report a storage generation value.
var ErrNoStorageGeneration = errors.New("client: server doesn't report a storage generation")

// ErrNoSync is returned by SyncHandlers if the server does not advertise syncs.
var ErrNoSync = errors.New("client: server has no sync handlers")

// BlobRoot returns the server's blobroot URL prefix.
// If the client was constructed with an explicit path,
// that path is used. Otherwise the server's
// default advertised blobRoot is used.
func (c *Client) BlobRoot() (string, error) {
	prefix, err := c.prefix()
	if err != nil {
		return "", err
	}
	return prefix + "/", nil
}

// ServerKeyID returns the server's GPG public key ID, in its long (16 capital
// hex digits) format. If the server isn't running a sign handler, the error
// will be ErrNoSigning.
func (c *Client) ServerKeyID() (string, error) {
	if err := c.condDiscovery(); err != nil {
		return "", err
	}
	if c.serverKeyID == "" {
		return "", ErrNoSigning
	}
	return c.serverKeyID, nil
}

// SearchRoot returns the server's search handler.
// If the server isn't running an index and search handler, the error
// will be ErrNoSearchRoot.
func (c *Client) SearchRoot() (string, error) {
	if err := c.condDiscovery(); err != nil {
		return "", err
	}
	if c.searchRoot == "" {
		return "", ErrNoSearchRoot
	}
	return c.searchRoot, nil
}

// HelpRoot returns the server's help handler.
// If the server isn't running a help handler, the error will be
// ErrNoHelpRoot.
func (c *Client) HelpRoot() (string, error) {
	if err := c.condDiscovery(); err != nil {
		return "", err
	}
	if c.helpRoot == "" {
		return "", ErrNoHelpRoot
	}
	return c.helpRoot, nil
}

// StorageGeneration returns the server's unique ID for its storage
// generation, reset whenever storage is reset, moved, or partially
// lost.
//
// This is a value that can be used in client cache keys to add
// certainty that they're talking to the same instance as previously.
//
// If the server doesn't return such a value, the error will be
// ErrNoStorageGeneration.
func (c *Client) StorageGeneration() (string, error) {
	if err := c.condDiscovery(); err != nil {
		return "", err
	}
	if c.storageGen == "" {
		return "", ErrNoStorageGeneration
	}
	return c.storageGen, nil
}

// SyncInfo holds the data that were acquired with a discovery
// and that are relevant to a syncHandler.
type SyncInfo struct {
	From    string
	To      string
	ToIndex bool // whether this sync is from a blob storage to an index
}

// SyncHandlers returns the server's sync handlers "from" and
// "to" prefix URLs.
// If the server isn't running any sync handler, the error
// will be ErrNoSync.
func (c *Client) SyncHandlers() ([]*SyncInfo, error) {
	if err := c.condDiscovery(); err != nil {
		return nil, err
	}
	if c.syncHandlers == nil {
		return nil, ErrNoSync
	}
	return c.syncHandlers, nil
}

var _ search.GetRecentPermanoder = (*Client)(nil)

// GetRecentPermanodes implements search.GetRecentPermanoder against a remote server over HTTP.
func (c *Client) GetRecentPermanodes(req *search.RecentRequest) (*search.RecentResponse, error) {
	sr, err := c.SearchRoot()
	if err != nil {
		return nil, err
	}
	url := sr + req.URLSuffix()
	hreq := c.newRequest("GET", url)
	hres, err := c.expect2XX(hreq)
	if err != nil {
		return nil, err
	}
	res := new(search.RecentResponse)
	if err := httputil.DecodeJSON(hres, res); err != nil {
		return nil, err
	}
	if err := res.Err(); err != nil {
		return nil, err
	}
	return res, nil
}

func (c *Client) GetPermanodesWithAttr(req *search.WithAttrRequest) (*search.WithAttrResponse, error) {
	sr, err := c.SearchRoot()
	if err != nil {
		return nil, err
	}
	url := sr + req.URLSuffix()
	hreq := c.newRequest("GET", url)
	hres, err := c.expect2XX(hreq)
	if err != nil {
		return nil, err
	}
	res := new(search.WithAttrResponse)
	if err := httputil.DecodeJSON(hres, res); err != nil {
		return nil, err
	}
	if err := res.Err(); err != nil {
		return nil, err
	}
	return res, nil
}

func (c *Client) Describe(req *search.DescribeRequest) (*search.DescribeResponse, error) {
	sr, err := c.SearchRoot()
	if err != nil {
		return nil, err
	}
	url := sr + req.URLSuffixPost()
	body, err := json.MarshalIndent(req, "", "\t")
	if err != nil {
		return nil, err
	}
	hreq := c.newRequest("POST", url, bytes.NewReader(body))
	hres, err := c.expect2XX(hreq)
	if err != nil {
		return nil, err
	}
	res := new(search.DescribeResponse)
	if err := httputil.DecodeJSON(hres, res); err != nil {
		return nil, err
	}
	return res, nil
}

func (c *Client) GetClaims(req *search.ClaimsRequest) (*search.ClaimsResponse, error) {
	sr, err := c.SearchRoot()
	if err != nil {
		return nil, err
	}
	url := sr + req.URLSuffix()
	hreq := c.newRequest("GET", url)
	hres, err := c.expect2XX(hreq)
	if err != nil {
		return nil, err
	}
	res := new(search.ClaimsResponse)
	if err := httputil.DecodeJSON(hres, res); err != nil {
		return nil, err
	}
	return res, nil
}

func (c *Client) Query(req *search.SearchQuery) (*search.SearchResult, error) {
	sr, err := c.SearchRoot()
	if err != nil {
		return nil, err
	}
	url := sr + req.URLSuffix()
	body, err := json.MarshalIndent(req, "", "\t")
	if err != nil {
		return nil, err
	}
	hreq := c.newRequest("POST", url, bytes.NewReader(body))
	hres, err := c.expect2XX(hreq)
	if err != nil {
		return nil, err
	}
	res := new(search.SearchResult)
	if err := httputil.DecodeJSON(hres, res); err != nil {
		return nil, err
	}
	return res, nil
}

// SearchExistingFileSchema does a search query looking for an
// existing file with entire contents of wholeRef, then does a HEAD
// request to verify the file still exists on the server.  If so,
// it returns that file schema's blobref.
//
// May return (zero, nil) on ENOENT. A non-nil error is only returned
// if there were problems searching.
func (c *Client) SearchExistingFileSchema(wholeRef blob.Ref) (blob.Ref, error) {
	sr, err := c.SearchRoot()
	if err != nil {
		return blob.Ref{}, err
	}
	url := sr + "camli/search/files?wholedigest=" + wholeRef.String()
	req := c.newRequest("GET", url)
	res, err := c.doReqGated(req)
	if err != nil {
		return blob.Ref{}, err
	}
	if res.StatusCode != 200 {
		body, _ := ioutil.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		return blob.Ref{}, fmt.Errorf("client: got status code %d from URL %s; body %s", res.StatusCode, url, body)
	}
	var ress struct {
		Files []blob.Ref `json:"files"`
	}
	if err := httputil.DecodeJSON(res, &ress); err != nil {
		return blob.Ref{}, fmt.Errorf("client: error parsing JSON from URL %s: %v", url, err)
	}
	if len(ress.Files) == 0 {
		return blob.Ref{}, nil
	}
	for _, f := range ress.Files {
		if c.FileHasContents(f, wholeRef) {
			return f, nil
		}
	}
	return blob.Ref{}, nil
}

// FileHasContents returns true iff f refers to a "file" or "bytes" schema blob,
// the server is configured with a "download helper", and the server responds
// that all chunks of 'f' are available and match the digest of wholeRef.
func (c *Client) FileHasContents(f, wholeRef blob.Ref) bool {
	if err := c.condDiscovery(); err != nil {
		return false
	}
	if c.downloadHelper == "" {
		return false
	}
	req := c.newRequest("HEAD", c.downloadHelper+f.String()+"/?verifycontents="+wholeRef.String())
	res, err := c.expect2XX(req)
	if err != nil {
		log.Printf("download helper HEAD error: %v", err)
		return false
	}
	defer res.Body.Close()
	return res.Header.Get("X-Camli-Contents") == wholeRef.String()
}

// prefix returns the URL prefix before "/camli/", or before
// the blobref hash in case of a share URL.
// Examples: http://foo.com:3179/bs or http://foo.com:3179/share
func (c *Client) prefix() (string, error) {
	if err := c.prefixOnce.Do(c.initPrefix); err != nil {
		return "", err
	}
	return c.prefixv, nil
}

// blobPrefix returns the URL prefix before the blobref hash.
// Example: http://foo.com:3179/bs/camli or http://foo.com:3179/share
func (c *Client) blobPrefix() (string, error) {
	pfx, err := c.prefix()
	if err != nil {
		return "", err
	}
	if !c.isSharePrefix {
		pfx += "/camli"
	}
	return pfx, nil
}

// discoRoot returns the user defined server for this client. It prepends "https://" if no scheme was specified.
func (c *Client) discoRoot() string {
	s := c.server
	if !strings.HasPrefix(s, "http") {
		s = "https://" + s
	}
	return s
}

// initPrefix uses the user provided server URL to define the URL
// prefix to the blobserver root. If the server URL has a path
// component then it is directly used, otherwise the blobRoot
// from the discovery is used as the path.
func (c *Client) initPrefix() error {
	c.isSharePrefix = false
	root := c.discoRoot()
	u, err := url.Parse(root)
	if err != nil {
		return err
	}
	if len(u.Path) > 1 {
		c.prefixv = strings.TrimRight(root, "/")
		return nil
	}
	return c.condDiscovery()
}

func (c *Client) condDiscovery() error {
	if c.sto != nil {
		return errors.New("client not using HTTP")
	}
	return c.discoOnce.Do(c.doDiscovery)
}

// DiscoveryDoc returns the server's JSON discovery document.
// This method exists purely for the "camtool discovery" command.
// Clients shouldn't have to parse this themselves.
func (c *Client) DiscoveryDoc() (io.Reader, error) {
	res, err := c.discoveryResp()
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	const maxSize = 1 << 20
	all, err := ioutil.ReadAll(io.LimitReader(res.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if len(all) > maxSize {
		return nil, errors.New("discovery document oddly large")
	}
	if len(all) > 0 && all[len(all)-1] != '\n' {
		all = append(all, '\n')
	}
	return bytes.NewReader(all), err
}

// HTTPVersion reports the HTTP version in use.
func (c *Client) HTTPVersion() (string, error) {
	req := c.newRequest("HEAD", c.discoRoot(), nil)
	res, err := c.doReqGated(req)
	if err != nil {
		return "", err
	}
	return res.Proto, err
}

func (c *Client) discoveryResp() (*http.Response, error) {
	// If the path is just "" or "/", do discovery against
	// the URL to see which path we should actually use.
	req := c.newRequest("GET", c.discoRoot(), nil)
	req.Header.Set("Accept", "text/x-camli-configuration")
	res, err := c.doReqGated(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != 200 {
		res.Body.Close()
		errMsg := fmt.Sprintf("got status %q from blobserver URL %q during configuration discovery", res.Status, c.discoRoot())
		if res.StatusCode == 401 && c.authErr != nil {
			errMsg = fmt.Sprintf("%v. %v", c.authErr, errMsg)
		}
		return nil, errors.New(errMsg)
	}
	// TODO(bradfitz): little weird in retrospect that we request
	// text/x-camli-configuration and expect to get back
	// text/javascript.  Make them consistent.
	if ct := res.Header.Get("Content-Type"); ct != "text/javascript" {
		res.Body.Close()
		return nil, fmt.Errorf("Blobserver returned unexpected type %q from discovery", ct)
	}
	return res, nil
}

func (c *Client) doDiscovery() error {
	root, err := url.Parse(c.discoRoot())
	if err != nil {
		return err
	}

	res, err := c.discoveryResp()
	if err != nil {
		return err
	}

	var disco camtypes.Discovery
	if err := httputil.DecodeJSON(res, &disco); err != nil {
		return err
	}

	u, err := root.Parse(disco.SearchRoot)
	if err != nil {
		return fmt.Errorf("client: invalid searchRoot %q; failed to resolve", disco.SearchRoot)
	}
	c.searchRoot = u.String()

	u, err = root.Parse(disco.HelpRoot)
	if err != nil {
		return fmt.Errorf("client: invalid helpRoot %q; failed to resolve", disco.HelpRoot)
	}
	c.helpRoot = u.String()

	c.storageGen = disco.StorageGeneration

	u, err = root.Parse(disco.BlobRoot)
	if err != nil {
		return fmt.Errorf("client: error resolving blobRoot: %v", err)
	}
	c.prefixv = strings.TrimRight(u.String(), "/")

	if disco.UIDiscovery != nil {
		u, err = root.Parse(disco.DownloadHelper)
		if err != nil {
			return fmt.Errorf("client: invalid downloadHelper %q; failed to resolve", disco.DownloadHelper)
		}
		c.downloadHelper = u.String()
	}

	if disco.SyncHandlers != nil {
		for _, v := range disco.SyncHandlers {
			ufrom, err := root.Parse(v.From)
			if err != nil {
				return fmt.Errorf("client: invalid %q \"from\" sync; failed to resolve", v.From)
			}
			uto, err := root.Parse(v.To)
			if err != nil {
				return fmt.Errorf("client: invalid %q \"to\" sync; failed to resolve", v.To)
			}
			c.syncHandlers = append(c.syncHandlers, &SyncInfo{
				From:    ufrom.String(),
				To:      uto.String(),
				ToIndex: v.ToIndex,
			})
		}
	}

	if disco.Signing != nil {
		c.serverKeyID = disco.Signing.PublicKeyID
	}
	return nil
}

// GetJSON sends a GET request to url, and unmarshals the returned
// JSON response into data. The URL's host must match the client's
// configured server.
func (c *Client) GetJSON(url string, data interface{}) error {
	if !strings.HasPrefix(url, c.discoRoot()) {
		return fmt.Errorf("wrong URL (%q) for this server", url)
	}
	hreq := c.newRequest("GET", url)
	resp, err := c.expect2XX(hreq)
	if err != nil {
		return err
	}
	return httputil.DecodeJSON(resp, data)
}

// Post is like http://golang.org/pkg/net/http/#Client.Post
// but with implementation details like gated requests. The
// URL's host must match the client's configured server.
func (c *Client) Post(url string, bodyType string, body io.Reader) error {
	if !strings.HasPrefix(url, c.discoRoot()) {
		return fmt.Errorf("wrong URL (%q) for this server", url)
	}
	req := c.newRequest("POST", url, body)
	req.Header.Set("Content-Type", bodyType)
	res, err := c.expect2XX(req)
	if err != nil {
		return err
	}
	return res.Body.Close()
}

// newRequests creates a request with the authentication header, and with the
// appropriate scheme and port in the case of self-signed TLS.
func (c *Client) newRequest(method, url string, body ...io.Reader) *http.Request {
	var bodyR io.Reader
	if len(body) > 0 {
		bodyR = body[0]
	}
	if len(body) > 1 {
		panic("too many body arguments")
	}
	req, err := http.NewRequest(method, url, bodyR)
	if err != nil {
		panic(err.Error())
	}
	// not done by http.NewRequest in Go 1.0:
	if br, ok := bodyR.(*bytes.Reader); ok {
		req.ContentLength = int64(br.Len())
	}
	c.authMode.AddAuthHeader(req)
	return req
}

// expect2XX will doReqGated and promote HTTP response codes outside of
// the 200-299 range to a non-nil error containing the response body.
func (c *Client) expect2XX(req *http.Request) (*http.Response, error) {
	res, err := c.doReqGated(req)
	if err == nil && (res.StatusCode < 200 || res.StatusCode > 299) {
		buf := new(bytes.Buffer)
		io.CopyN(buf, res.Body, 1<<20)
		res.Body.Close()
		return res, fmt.Errorf("client: got status code %d from URL %s; body %s", res.StatusCode, req.URL.String(), buf.String())
	}
	return res, err
}

func (c *Client) doReqGated(req *http.Request) (*http.Response, error) {
	c.httpGate.Start()
	defer c.httpGate.Done()
	return c.httpClient.Do(req)
}

// insecureTLS returns whether the client is using TLS without any
// verification of the server's cert.
func (c *Client) insecureTLS() bool {
	return c.useTLS() && c.insecureAnyTLSCert
}

// DialFunc returns the adequate dial function when we're on android.
func (c *Client) DialFunc() func(network, addr string) (net.Conn, error) {
	if c.useTLS() {
		return nil
	}
	if android.IsChild() {
		return func(network, addr string) (net.Conn, error) {
			return android.Dial(network, addr)
		}
	}
	return nil
}

func (c *Client) http2DialTLSFunc() func(network, addr string, cfg *tls.Config) (net.Conn, error) {
	trustedCerts := c.getTrustedCerts()
	if !c.insecureAnyTLSCert && len(trustedCerts) == 0 {
		// TLS with normal/full verification.
		// nil means http2 uses its default dialer.
		return nil
	}
	return func(network, addr string, cfg *tls.Config) (net.Conn, error) {
		// we own cfg, so we can mutate it:
		cfg.InsecureSkipVerify = true
		conn, err := tls.Dial(network, addr, cfg)
		if err != nil {
			return nil, err
		}
		if c.insecureAnyTLSCert {
			return conn, err
		}
		state := conn.ConnectionState()
		if p := state.NegotiatedProtocol; p != http2.NextProtoTLS {
			return nil, fmt.Errorf("http2: unexpected ALPN protocol %q; want %q", p, http2.NextProtoTLS)
		}
		if !state.NegotiatedProtocolIsMutual {
			return nil, errors.New("http2: could not negotiate protocol mutually")
		}
		certs := state.PeerCertificates
		if len(certs) < 1 {
			return nil, fmt.Errorf("no TLS peer certificates from %s", addr)
		}
		sig := hashutil.SHA256Prefix(certs[0].Raw)
		for _, v := range trustedCerts {
			if v == sig {
				return conn, nil
			}
		}
		return nil, fmt.Errorf("TLS server at %v presented untrusted certificate (signature %q)", addr, sig)
	}
}

// DialTLSFunc returns the adequate dial function, when using SSL, depending on
// whether we're using insecure TLS (certificate verification is disabled), or we
// have some trusted certs, or we're on android.1
// If the client's config has some trusted certs, the server's certificate will
// be checked against those in the config after the TLS handshake.
func (c *Client) DialTLSFunc() func(network, addr string) (net.Conn, error) {
	if !c.useTLS() {
		return nil
	}
	trustedCerts := c.getTrustedCerts()
	var stdTLS bool
	if !c.insecureAnyTLSCert && len(trustedCerts) == 0 {
		// TLS with normal/full verification.
		stdTLS = true
		if !android.IsChild() {
			// Not android, so let the stdlib deal with it
			return nil
		}
	}

	return func(network, addr string) (net.Conn, error) {
		var conn *tls.Conn
		var err error
		if android.IsChild() {
			ac, err := android.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			var tlsConfig *tls.Config
			if stdTLS {
				tlsConfig, err = android.TLSConfig()
				if err != nil {
					return nil, err
				}
			} else {
				tlsConfig = &tls.Config{InsecureSkipVerify: true}
			}
			conn = tls.Client(ac, tlsConfig)
			if err := conn.Handshake(); err != nil {
				return nil, err
			}
		} else {
			conn, err = tls.Dial(network, addr, &tls.Config{InsecureSkipVerify: true})
			if err != nil {
				return nil, err
			}
		}
		if c.insecureAnyTLSCert {
			return conn, nil
		}
		certs := conn.ConnectionState().PeerCertificates
		if len(certs) < 1 {
			return nil, fmt.Errorf("no TLS peer certificates from %s", addr)
		}
		sig := hashutil.SHA256Prefix(certs[0].Raw)
		for _, v := range trustedCerts {
			if v == sig {
				return conn, nil
			}
		}
		return nil, fmt.Errorf("TLS server at %v presented untrusted certificate (signature %q)", addr, sig)
	}
}

func (c *Client) Signer() (*schema.Signer, error) {
	c.signerOnce.Do(c.signerInit)
	return c.signer, c.signerErr
}

func (c *Client) signerInit() {
	c.signer, c.signerErr = c.buildSigner()
}

func (c *Client) buildSigner() (*schema.Signer, error) {
	c.initSignerPublicKeyBlobrefOnce.Do(c.initSignerPublicKeyBlobref)
	if !c.signerPublicKeyRef.Valid() {
		return nil, camtypes.ErrClientNoPublicKey
	}
	return schema.NewSigner(c.signerPublicKeyRef, strings.NewReader(c.publicKeyArmored), c.SecretRingFile())
}

// sigTime optionally specifies the signature time.
// If zero, the current time is used.
func (c *Client) signBlob(bb schema.Buildable, sigTime time.Time) (string, error) {
	signer, err := c.Signer()
	if err != nil {
		return "", err
	}
	return bb.Builder().SignAt(signer, sigTime)
}

// uploadPublicKey uploads the public key (if one is defined), so
// subsequent (likely synchronous) indexing of uploaded signed blobs
// will have access to the public key to verify it. In the normal
// case, the stat cache prevents this from doing anything anyway.
func (c *Client) uploadPublicKey() error {
	sigRef := c.SignerPublicKeyBlobref()
	if !sigRef.Valid() {
		return nil
	}
	var err error
	if _, keyUploaded := c.haveCache.StatBlobCache(sigRef); !keyUploaded {
		_, err = c.uploadString(c.publicKeyArmored, false)
	}
	return err
}

// checkMatchingKeys compares the client's and the server's keys and logs if they differ.
func (c *Client) checkMatchingKeys() {
	serverKey, err := c.ServerKeyID()
	// The server provides the full (16 digit) key fingerprint but schema.Signer only stores
	// the short (8 digit) key ID.
	if err == nil && len(serverKey) >= 8 {
		shortServerKey := serverKey[len(serverKey)-8:]
		if shortServerKey != c.signer.KeyID() {
			log.Printf("Warning: client (%s) and server (%s) keys differ.", c.signer.KeyID(), shortServerKey)
		}
	}
}

func (c *Client) UploadAndSignBlob(b schema.AnyBlob) (*PutResult, error) {
	signed, err := c.signBlob(b.Blob(), time.Time{})
	if err != nil {
		return nil, err
	}
	c.checkMatchingKeys()
	if err := c.uploadPublicKey(); err != nil {
		return nil, err
	}
	return c.uploadString(signed, false)
}

func (c *Client) UploadBlob(b schema.AnyBlob) (*PutResult, error) {
	// TODO(bradfitz): ask the blob for its own blobref, rather
	// than changing the hash function with uploadString?
	return c.uploadString(b.Blob().JSON(), true)
}

func (c *Client) uploadString(s string, stat bool) (*PutResult, error) {
	uh := NewUploadHandleFromString(s)
	uh.SkipStat = !stat
	return c.Upload(uh)
}

func (c *Client) UploadNewPermanode() (*PutResult, error) {
	unsigned := schema.NewUnsignedPermanode()
	return c.UploadAndSignBlob(unsigned)
}

func (c *Client) UploadPlannedPermanode(key string, sigTime time.Time) (*PutResult, error) {
	unsigned := schema.NewPlannedPermanode(key)
	signed, err := c.signBlob(unsigned, sigTime)
	if err != nil {
		return nil, err
	}
	c.checkMatchingKeys()
	if err := c.uploadPublicKey(); err != nil {
		return nil, err
	}
	return c.uploadString(signed, true)
}

// IsIgnoredFile returns whether the file at fullpath should be ignored by camput.
// The fullpath is checked against the ignoredFiles list, trying the following rules in this order:
// 1) star-suffix style matching (.e.g *.jpg).
// 2) Shell pattern match as done by http://golang.org/pkg/path/filepath/#Match
// 3) If the pattern is an absolute path to a directory, fullpath matches if it is that directory or a child of it.
// 4) If the pattern is a relative path, fullpath matches if it has pattern as a path component (i.e the pattern is a part of fullpath that fits exactly between two path separators).
func (c *Client) IsIgnoredFile(fullpath string) bool {
	c.initIgnoredFilesOnce.Do(c.initIgnoredFiles)
	return c.ignoreChecker(fullpath)
}

// Close closes the client. In most cases, it's not necessary to close a Client.
// The exception is for Clients created using NewStorageClient, where the Storage
// might implement io.Closer.
func (c *Client) Close() error {
	if cl, ok := c.sto.(io.Closer); ok {
		return cl.Close()
	}
	return nil
}

// NewFromParams returns a Client that uses the specified server base URL
// and auth but does not use any on-disk config files or environment variables
// for its configuration. It may still use the disk for caches.
func NewFromParams(server string, mode auth.AuthMode, opts ...ClientOption) *Client {
	cl := newClient(server, mode, opts...)
	cl.paramsOnly = true
	return cl
}

// TODO(bradfitz): move auth mode into a ClientOption? And
// OptionNoDiskConfig to delete NewFromParams, etc, and just have New?

func newClient(server string, mode auth.AuthMode, opts ...ClientOption) *Client {
	c := &Client{
		server:    server,
		haveCache: noHaveCache{},
		log:       log.New(os.Stderr, "", log.Ldate|log.Ltime),
		authMode:  mode,
	}
	for _, v := range opts {
		v.modifyClient(c)
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{
			Transport: c.transportForConfig(c.transportConfig),
		}
	}
	c.httpGate = syncutil.NewGate(httpGateSize(c.httpClient.Transport))
	return c
}

func httpGateSize(rt http.RoundTripper) int {
	switch v := rt.(type) {
	case *httputil.StatsTransport:
		return httpGateSize(v.Transport)
	case *http.Transport:
		return maxParallelHTTP_h1
	case *http2.Transport:
		return maxParallelHTTP_h2
	default:
		return maxParallelHTTP_h1 // conservative default
	}
}
