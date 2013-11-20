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
//
// TODO(bradfitz): Finish this. Barely started.
package importer

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/jsonsign/signhandler"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/server"
	"camlistore.org/pkg/syncutil"
)

// A Host is the environment hosting an importer.
type Host struct {
	BaseURL string

	imp    Importer
	target blobserver.StatReceiver
	search *search.Handler
	signer *schema.Signer

	// client optionally specifies how to fetch external network
	// resources.  If nil, http.DefaultClient is used.
	client *http.Client

	mu           sync.Mutex
	running      bool
	stopreq      chan struct{} // closed to signal importer to stop and return an error
	lastProgress *ProgressMessage
	lastRunErr   error
}

func (h *Host) String() string {
	return fmt.Sprintf("%T(%s)", h, h.imp)
}

func (h *Host) Target() blobserver.StatReceiver {
	return h.target
}

func (h *Host) Search() *search.Handler {
	return h.search
}

func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if httputil.PathSuffix(r) == "" {
		switch r.FormValue("mode") {
		case "":
		case "start":
			h.start()
		case "stop":
			h.stop()
		default:
			fmt.Fprintf(w, "Unknown mode")
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		fmt.Fprintf(w, "I am an importer of type %T; running=%v; last progress=%#v",
			h.imp, h.running, h.lastProgress)
	} else {
		// TODO(aa): Remove this temporary hack once the UI has a way to configure importers.
		h.imp.ServeHTTP(w, r)
	}
}

func (h *Host) start() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running {
		return
	}
	h.running = true
	stopCh := make(chan struct{})
	h.stopreq = stopCh
	go func() {
		log.Printf("Starting importer %s", h)
		err := h.imp.Run(stopCh)
		if err != nil {
			log.Printf("Importer %s error: %v", h, err)
		} else {
			log.Printf("Importer %s finished.", h)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.running = false
		h.lastRunErr = err
	}()
}

func (h *Host) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.running {
		return
	}
	h.running = false
	close(h.stopreq)
}

// HTTPClient returns the HTTP client to use.
func (h *Host) HTTPClient() *http.Client {
	if h.client == nil {
		return http.DefaultClient
	}
	return h.client
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

// RootObject returns the root permanode for this importer account.
func (h *Host) RootObject() (*Object, error) {
	res, err := h.search.GetPermanodesWithAttr(&search.WithAttrRequest{
		N:     2, // only expect 1
		Attr:  "camliImportRoot",
		Value: h.imp.Prefix(),
	})
	if err != nil {
		log.Printf("RootObject searching GetPermanodesWithAttr: %v", err)
		return nil, err
	}
	if len(res.WithAttr) == 0 {
		obj, err := h.NewObject()
		if err != nil {
			return nil, err
		}
		log.Printf("No root object found. Created %v", obj.pn)
		if err := obj.SetAttr("camliImportRoot", h.imp.Prefix()); err != nil {
			return nil, err
		}
		return obj, nil
	}
	if len(res.WithAttr) > 1 {
		return nil, fmt.Errorf("Found %d import roots for %q; want 1", len(res.WithAttr), h.imp.Prefix())
	}
	pn := res.WithAttr[0].Permanode
	return h.ObjectFromRef(pn)
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

// ErrInterrupted should be returned by importers
// when an Interrupt fires.
var ErrInterrupted = errors.New("import interrupted by request")

// An Interrupt is passed to importers for them to monitor
// requests to stop importing.  The channel is closed as
// a signal to stop.
type Interrupt <-chan struct{}

// ShouldStop returns whether the interrupt has fired.
// If so, importers should return ErrInterrupted.
func (i Interrupt) ShouldStop() bool {
	select {
	case <-i:
		return true
	default:
		return false
	}
}

// An Importer imports from a third-party site.
type Importer interface {
	// Run runs a full or increment import.
	Run(Interrupt) error

	// Prefix returns the unique prefix for this importer.
	// It should be of the form "serviceType:username".
	// Further colons are added to form the names of planned
	// permanodes.
	Prefix() string

	// CanHandleURL returns whether a URL (such as one a user is
	// viewing in their browser and dragged onto Camlistore) is a
	// form recognized by this importer.  If so, its full metadata
	// and full data (e.g. unscaled image) can be fetched, rather
	// than just fetching the HTML of the URL.
	//
	// TODO: implement and use this. For now importers can return
	// stub these and return false/errors. They're unused.
	CanHandleURL(url string) bool
	ImportURL(url string) error

	ServeHTTP(w http.ResponseWriter, r *http.Request)
}

// Constructor is the function type that importers must register at init time.
type Constructor func(jsonconfig.Obj, *Host) (Importer, error)

var (
	mu    sync.Mutex
	ctors = make(map[string]Constructor)
)

func Register(name string, fn Constructor) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := ctors[name]; dup {
		panic("Dup registration of importer " + name)
	}
	ctors[name] = fn
}

func Create(name string, hl blobserver.Loader, baseURL string, cfg jsonconfig.Obj) (*Host, error) {
	mu.Lock()
	defer mu.Unlock()
	fn := ctors[name]
	if fn == nil {
		return nil, fmt.Errorf("Unknown importer type %q", name)
	}
	h := &Host{
		BaseURL: baseURL,
	}
	imp, err := fn(cfg, h)
	if err != nil {
		return nil, err
	}
	h.imp = imp
	return h, nil
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
