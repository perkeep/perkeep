/*
Copyright 2026 The Perkeep Authors

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

// Package access serves the server's Client access page, which manages
// the access keys that clients, such as mobile apps, use to reach the
// server with limited permissions, and an optional tailcat listener for
// them.
//
// Clients send an access key as the password of HTTP basic auth, with any
// username, and get only the parts of the server that its permissions
// allow (see [Perm]). Access keys work on the server's usual listeners, as
// an auth mode (see [Handler.AllowedAccess]), and on the tailcat listener.
//
// Tailcat (https://github.com/tailscale/tailcat) gives clients a
// WireGuard tunnel to the server through a DERP relay, upgraded to a
// direct connection when NAT traversal allows, without a Tailscale
// account or VPN. The listener serves plain HTTP inside the tunnel, on
// port 80, in addition to the server's usual listener. It serves nothing
// without an access key; the server's other auth modes don't apply to it.
//
// The page shows a QR code for each access key, for clients to scan, and
// a link for clients to open, of a perkeep://access URL, which tells
// clients how to reach the server: through the tailcat listener, if it's
// listening, or else at the server's HTTPS base URL. See
// doc/access-url.md for its format.
//
// The listener's identity uses the nearest region of tailcat's default
// DERP map, picked when it's first enabled. To use a DERP server of your
// own instead, set "derpHost" to its hostname, in place of "regionID", in
// the identity in the state file, and restart the server.
package access // import "perkeep.org/pkg/server/access"

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tailscale/tailcat"
	"go4.org/wkfs"
	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/health"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/logger"
)

// Config configures a Handler.
type Config struct {
	// Prefix is the URL prefix of the Client access page, like "/access/".
	Prefix string

	// StateFile is the path of the JSON file in which the handler keeps
	// the tailcat identity and the access keys. It's created, mode 0600,
	// when first needed.
	StateFile string

	// Roots are the handlers that the tailcat listener serves parts of.
	Roots Roots

	// Serve serves the requests from the tailcat listener that an access
	// key allows, with an [auth.GrantCtxKey] in their context.
	Serve http.Handler

	// Health, if non-nil, is the monitor to register the listener's
	// health check with.
	Health *health.Monitor

	// BaseURL, if non-nil, returns the server's base URL, whose host, if
	// it's served over HTTPS, is the default name clients show for the
	// server. If it's HTTPS, clients can reach the server there, if the
	// tailcat listener isn't listening. It may block until the URL is
	// known.
	BaseURL func() string

	// TLSCertFile, if non-empty, is the PEM file of the certificate the
	// server serves its HTTPS base URL with. If clients can't verify it
	// with their usual roots, as with a self-signed one, clients reaching
	// the server there get its fingerprint, to check it by instead. If
	// empty, the certificate is assumed to be verifiable, such as one
	// from Let's Encrypt or Tailscale.
	TLSCertFile string
}

// Handler serves the Client access page and runs the tailcat listener.
// Its ServeHTTP doesn't check that requests are authorized; callers must.
type Handler struct {
	prefix  string
	file    string
	roots   Roots
	serve   http.Handler
	baseURL func() string // or nil
	// tlsCertFile is the PEM file of the base URL's certificate, if
	// known; see Config.TLSCertFile.
	tlsCertFile string

	mu       sync.Mutex
	st       state
	ts       *tailcat.Server // non-nil while listening
	hs       *http.Server    // serving ts's listener, when ts is non-nil
	addr     tailcat.Addr    // ts's address
	regName  string          // the name of ts's DERP region, if known
	startErr error           // from the last failed start, if not listening
	retry    *time.Timer     // retrying a failed start, or nil
	closed   bool

	check      *health.Check   // or nil
	probeKey   key.NodePrivate // the self-check's client key
	stopProbes func()          // stops the self-checks of ts, when it's non-nil
	probed     bool            // whether a self-check of ts has finished
	probeErr   error           // of the last self-check of ts, if it failed
	probeFails int             // consecutive self-check failures

	testRegion *tailcfg.DERPRegion // if non-nil, the DERP region to use, for tests
}

// state is the contents of the state file.
type state struct {
	// Name, if non-empty, is the name for clients to show for the
	// server, instead of the default; see Handler.name.
	Name string `json:"name,omitempty"`

	Enabled  bool         `json:"enabled"`
	Identity *identity    `json:"identity,omitempty"`
	Keys     []*accessKey `json:"keys,omitempty"`
}

// identity is the tailcat listener's identity, which with its DERP
// region determines its address. It's kept while the listener is disabled,
// so that clients can keep using the address when it's enabled again.
type identity struct {
	Key          key.NodePrivate      `json:"key"`
	PresharedKey tailcat.PresharedKey `json:"presharedKey"`

	// Exactly one of RegionID and DERPHost is set. The Client access
	// page only makes identities with a RegionID; DERPHost is for owners
	// who edit the state file.
	RegionID tailcfg.DERPRegionID `json:"regionID,omitempty"` // in tailcat's default DERP map
	DERPHost string               `json:"derpHost,omitempty"` // of a DERP server the owner runs
}

type accessKey struct {
	ID      string    `json:"id"` // not secret; identifies the key in forms
	Name    string    `json:"name"`
	Secret  string    `json:"secret"`
	Perms   []string  `json:"perms"`
	Created time.Time `json:"created"`

	perm Perm // from Perms
}

// New returns a Handler, and starts its tailcat listener in the
// background if the state file says it's enabled.
func New(c Config) (*Handler, error) {
	if !strings.HasSuffix(c.Prefix, "/") || c.StateFile == "" || c.Serve == nil {
		return nil, errors.New("access: Prefix, StateFile, and Serve are required")
	}
	h := &Handler{
		prefix:      c.Prefix,
		file:        c.StateFile,
		roots:       c.Roots,
		serve:       c.Serve,
		baseURL:     c.BaseURL,
		tlsCertFile: c.TLSCertFile,
		probeKey:    key.NewNode(),
	}
	if err := h.load(); err != nil {
		return nil, err
	}
	h.check = c.Health.Register(health.ID("access.tailcat", c.Prefix), "tailcat listener: reachable through its DERP relay")
	h.mu.Lock()
	h.updateHealthLocked()
	h.mu.Unlock()
	if h.st.Enabled && h.st.Identity != nil {
		go func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.startOrRetryLocked()
		}()
	}
	return h, nil
}

func (h *Handler) load() error {
	data, err := os.ReadFile(h.file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("access: %w", err)
	}
	if err := json.Unmarshal(data, &h.st); err != nil {
		return fmt.Errorf("access: parsing %s: %w", h.file, err)
	}
	h.st.Keys = slices.DeleteFunc(h.st.Keys, func(k *accessKey) bool {
		if k.Secret == "" {
			log.Printf("access: ignoring access key %q in %s, which has no secret", k.Name, h.file)
			return true
		}
		return false
	})
	for _, k := range h.st.Keys {
		if k.perm, err = ParsePerms(k.Perms); err != nil {
			return fmt.Errorf("access: key %q in %s: %w", k.Name, h.file, err)
		}
	}
	return nil
}

// saveLocked writes the state file, replacing it atomically.
func (h *Handler) saveLocked() error {
	data, err := json.MarshalIndent(&h.st, "", "\t")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(h.file), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(h.file), ".tailcat-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), h.file)
}

// Close stops the tailcat listener.
func (h *Handler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	h.stopLocked()
	return nil
}

// startTimeout bounds starting the tailcat listener: fetching the DERP map
// and connecting to the relay.
const startTimeout = 30 * time.Second

// retryDelay is how long to wait to start the listener again, after a
// start failed.
const retryDelay = time.Minute

var verbose, _ = strconv.ParseBool(os.Getenv("PK_TAILCAT_VERBOSE"))

// startLocked starts the tailcat listener with the state's identity.
func (h *Handler) startLocked() error {
	if h.ts != nil {
		return nil
	}
	id := h.st.Identity
	logf := logger.Discard
	if verbose {
		logf = logger.WithPrefix(log.Printf, "tailcat: ")
	}
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	ts := &tailcat.Server{
		Key:          id.Key,
		PresharedKey: id.PresharedKey,
		Logf:         logf,
	}
	switch {
	case h.testRegion != nil:
		ts.Region = h.testRegion
	case id.DERPHost != "":
		ts.Region = customRegion(id.DERPHost)
	default:
		// Look up the region, as the server would, to learn its name.
		ci := &tailcat.ConnInfo{RegionID: id.RegionID}
		if err := ci.Expand(ctx, tailcat.ExpandForServer); err != nil {
			return err
		}
		ts.Region = ci.Region[0]
	}
	ln, err := ts.Listen(ctx, "tcp", ":80")
	if err != nil {
		ts.Close()
		return err
	}
	hs := &http.Server{
		Handler: http.HandlerFunc(h.serveTailcat),
		BaseContext: func(net.Listener) context.Context {
			// Grant nothing until an access key does.
			return auth.GrantCtxKey.WithValue(context.Background(), 0)
		},
		ReadHeaderTimeout: time.Minute,
	}
	go func() {
		if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("access: tailcat listener: %v", err)
		}
	}()
	h.ts, h.hs, h.addr, h.startErr = ts, hs, ts.TailcatAddr(), nil
	h.regName = ts.Region.RegionName
	h.startProbesLocked()
	h.updateHealthLocked()
	log.Printf("access: tailcat listener started")
	return nil
}

// startOrRetryLocked starts the listener, and if that fails, tries again
// later, until it starts or the owner disables it.
func (h *Handler) startOrRetryLocked() {
	h.retry = nil
	if h.closed || !h.st.Enabled || h.st.Identity == nil {
		return
	}
	if err := h.startLocked(); err != nil {
		h.startErr = err
		h.updateHealthLocked()
		log.Printf("access: starting tailcat listener: %v; retrying in %v", err, retryDelay)
		h.retry = time.AfterFunc(retryDelay, func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.startOrRetryLocked()
		})
	}
}

func (h *Handler) stopLocked() {
	if h.retry != nil {
		h.retry.Stop()
		h.retry = nil
	}
	h.startErr = nil
	defer h.updateHealthLocked()
	if h.ts == nil {
		return
	}
	h.stopProbes()
	h.hs.Close()
	h.ts.Close()
	h.ts, h.hs, h.addr = nil, nil, ""
	h.stopProbes, h.probed, h.probeErr, h.probeFails = nil, false, nil, 0
	log.Printf("access: tailcat listener stopped")
}

// customRegion returns a DERP region of the DERP server at host.
func customRegion(host string) *tailcfg.DERPRegion {
	// Any non-zero ID does, as the address embeds the region; it's what
	// tailcat uses for addresses with custom DERP servers.
	const id = 1
	return &tailcfg.DERPRegion{
		RegionID: id,
		Nodes:    []*tailcfg.DERPNode{{Name: "1", RegionID: id, HostName: host}},
	}
}

// newIdentity returns a new identity using the nearest region of
// tailcat's default DERP map.
func newIdentity(ctx context.Context) (*identity, error) {
	ci := &tailcat.ConnInfo{RegionID: -1} // the nearest
	if err := ci.Expand(ctx, tailcat.ExpandForServer); err != nil {
		return nil, fmt.Errorf("picking a DERP region: %w", err)
	}
	return &identity{
		Key:          key.NewNode(),
		PresharedKey: tailcat.NewPresharedKey(),
		RegionID:     ci.Region[0].RegionID,
	}, nil
}

// target is how clients reach the server: by exactly one of the tailcat
// listener's address or the server's HTTPS base URL.
type target struct {
	tailcat tailcat.Addr
	url     string
	certFP  string // with url, the hex SHA-256 of its certificate, if clients can't verify it otherwise
}

// clientTarget returns how clients reach the server: through the tailcat
// listener, if it's listening, or else at the server's base URL, if it's
// HTTPS. It returns the zero target if they can't.
func (h *Handler) clientTarget() (target, error) {
	h.mu.Lock()
	addr := h.addr
	h.mu.Unlock()
	if addr != "" {
		return target{tailcat: addr}, nil
	}
	if h.baseURL == nil {
		return target{}, nil
	}
	base := h.baseURL()
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" {
		return target{}, nil
	}
	t := target{url: base}
	if h.tlsCertFile != "" {
		if t.certFP, err = untrustedCertFP(h.tlsCertFile, u.Hostname()); err != nil {
			return target{}, err
		}
	}
	return t, nil
}

// untrustedCertFP returns the hex SHA-256 of the certificate in the PEM
// file certFile, if it's not one that clients can verify for host with
// their usual roots, such as a self-signed one, or else "".
func untrustedCertFP(certFile, host string) (string, error) {
	data, err := wkfs.ReadFile(certFile)
	if err != nil {
		return "", err
	}
	var certs []*x509.Certificate
	for {
		var b *pem.Block
		b, data = pem.Decode(data)
		if b == nil {
			break
		}
		if b.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			return "", fmt.Errorf("parsing %s: %w", certFile, err)
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		return "", fmt.Errorf("no certificate in %s", certFile)
	}
	opts := x509.VerifyOptions{DNSName: host, Intermediates: x509.NewCertPool()}
	for _, c := range certs[1:] {
		opts.Intermediates.AddCert(c)
	}
	if _, err := certs[0].Verify(opts); err == nil {
		return "", nil
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), nil
}

// clientURL returns the URL for a client to reach the server at t with
// the access key k, and to show the server as name. The Client access
// page links to it, and shows it as a QR code. See doc/access-url.md.
func clientURL(t target, k *accessKey, name string) string {
	v := url.Values{
		"key":  {k.Secret},
		"name": {name},
	}
	if t.tailcat != "" {
		v.Set("tailcat", string(t.tailcat))
	} else {
		v.Set("url", t.url)
		if t.certFP != "" {
			v.Set("certfp", t.certFP)
		}
	}
	return "perkeep://access?" + v.Encode()
}

// name returns the name for clients to show for the server: the one the
// owner set, or else the default.
func (h *Handler) name() string {
	h.mu.Lock()
	name := h.st.Name
	h.mu.Unlock()
	if name != "" {
		return name
	}
	return h.defaultName()
}

// defaultName returns the default name for clients to show for the server:
// the name of its TLS certificate, the host of its base URL if that's
// HTTPS and not an IP address, or else the machine's hostname.
func (h *Handler) defaultName() string {
	if h.baseURL != nil {
		if u, err := url.Parse(h.baseURL()); err == nil && u.Scheme == "https" {
			if host := u.Hostname(); host != "" && net.ParseIP(host) == nil {
				return host
			}
		}
	}
	host, err := os.Hostname()
	if err != nil {
		return "Perkeep"
	}
	return host
}

// setName sets the name for clients to show for the server, or if empty,
// has them show the default.
func (h *Handler) setName(name string) error {
	name = strings.TrimSpace(name)
	if len(name) > 100 {
		return errors.New("the name is too long")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	old := h.st.Name
	h.st.Name = name
	if err := h.saveLocked(); err != nil {
		h.st.Name = old
		return err
	}
	return nil
}

// keyFor returns the access key whose secret is r's basic auth password,
// or nil if none is.
func (h *Handler) keyFor(r *http.Request) *accessKey {
	_, pass, err := httputil.BasicAuth(r)
	if err != nil || pass == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, k := range h.st.Keys {
		if subtle.ConstantTimeCompare([]byte(pass), []byte(k.Secret)) == 1 {
			return k
		}
	}
	return nil
}

// The Handler is also an auth mode, for access keys on the server's usual
// listeners: see [Handler.AllowedAccess].
var _ auth.ScopedMode = (*Handler)(nil)

// AllowedAccess implements [auth.AuthMode]. It allows all operations to
// the requests whose access key allows them, so that the handlers' own
// checks pass, and nothing to other requests.
func (h *Handler) AllowedAccess(r *http.Request) auth.Operation {
	k := h.keyFor(r)
	if k == nil {
		return 0
	}
	if need, ok := h.roots.need(r); ok && (need == 0 || k.perm&need != 0) {
		return auth.OpAll
	}
	return 0
}

// AddAuthHeader implements [auth.AuthMode]. It adds nothing, as the
// server doesn't make requests with access keys.
func (h *Handler) AddAuthHeader(*http.Request) {}

// ScopedOnly implements [auth.ScopedMode].
func (h *Handler) ScopedOnly() {}

// serveTailcat serves the requests from the tailcat listener: those an
// access key allows, with h.serve.
func (h *Handler) serveTailcat(w http.ResponseWriter, r *http.Request) {
	k := h.keyFor(r)
	if k == nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="Perkeep access key"`)
		http.Error(w, "An access key is required, as the password of HTTP basic auth.", http.StatusUnauthorized)
		return
	}
	need, ok := h.roots.need(r)
	if !ok {
		http.Error(w, "Not served to access keys.", http.StatusForbidden)
		return
	}
	if need != 0 && k.perm&need == 0 {
		var names []string
		for _, pi := range permInfo {
			if need.Has(pi.perm) {
				names = append(names, strconv.Quote(pi.title))
			}
		}
		http.Error(w, fmt.Sprintf("Access key %q needs the %s permission.", k.Name, strings.Join(names, " or ")), http.StatusForbidden)
		return
	}
	// The policy decided; let the handlers' own checks pass.
	r = r.WithContext(auth.GrantCtxKey.WithValue(r.Context(), auth.OpAll))
	h.serve.ServeHTTP(w, r)
}

// enable makes the listener's identity if it has none, and starts it.
func (h *Handler) enable() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.st.Identity == nil {
		ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
		defer cancel()
		id, err := newIdentity(ctx)
		if err != nil {
			return err
		}
		h.st.Identity = id
	}
	h.st.Enabled = true
	if err := h.saveLocked(); err != nil {
		return err
	}
	if h.retry != nil {
		h.retry.Stop()
	}
	h.startOrRetryLocked()
	if h.ts == nil {
		return fmt.Errorf("starting the tailcat listener: %w; retrying", h.startErr)
	}
	return nil
}

func (h *Handler) disable() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopLocked()
	h.st.Enabled = false
	h.updateHealthLocked()
	return h.saveLocked()
}

// forgetIdentity deletes the disabled listener's identity, so that the next
// enable makes a new one, with a new address.
func (h *Handler) forgetIdentity() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.st.Enabled {
		return errors.New("disable the tailcat listener first")
	}
	h.st.Identity = nil
	h.regName = ""
	return h.saveLocked()
}

// createKey adds an access key, returning its ID.
func (h *Handler) createKey(name string, perm Perm) (id string, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("an access key needs a name")
	}
	k := &accessKey{
		ID:      rand.Text()[:10],
		Name:    name,
		Secret:  rand.Text(),
		Perms:   perm.Names(),
		Created: time.Now().UTC().Truncate(time.Second),
		perm:    perm,
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.st.Keys = append(h.st.Keys, k)
	if err := h.saveLocked(); err != nil {
		h.st.Keys = h.st.Keys[:len(h.st.Keys)-1]
		return "", err
	}
	return k.ID, nil
}

func (h *Handler) setKeyPerms(id string, perm Perm) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	i := slices.IndexFunc(h.st.Keys, func(k *accessKey) bool { return k.ID == id })
	if i < 0 {
		return errors.New("no such access key")
	}
	// Replace rather than modify the key, which keyFor's callers may
	// be using.
	k := *h.st.Keys[i]
	k.Perms, k.perm = perm.Names(), perm
	old := h.st.Keys[i]
	h.st.Keys[i] = &k
	if err := h.saveLocked(); err != nil {
		h.st.Keys[i] = old
		return err
	}
	return nil
}

func (h *Handler) deleteKey(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	old := h.st.Keys
	h.st.Keys = slices.DeleteFunc(slices.Clone(old), func(k *accessKey) bool { return k.ID == id })
	if len(h.st.Keys) == len(old) {
		return errors.New("no such access key")
	}
	if err := h.saveLocked(); err != nil {
		h.st.Keys = old
		return err
	}
	return nil
}
