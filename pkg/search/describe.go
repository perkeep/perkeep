/*
Copyright 2014 The Camlistore Authors

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

package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/types"
	"camlistore.org/pkg/types/camtypes"

	"go4.org/syncutil"
)

func (sh *Handler) serveDescribe(rw http.ResponseWriter, req *http.Request) {
	defer httputil.RecoverJSON(rw, req)
	var dr DescribeRequest
	dr.fromHTTP(req)

	res, err := sh.Describe(&dr)
	if err != nil {
		httputil.ServeJSONError(rw, err)
		return
	}
	httputil.ReturnJSON(rw, res)
}

const verboseDescribe = false

func (sh *Handler) Describe(dr *DescribeRequest) (dres *DescribeResponse, err error) {
	if verboseDescribe {
		t0 := time.Now()
		defer func() {
			td := time.Since(t0)
			var num int
			if dres != nil {
				num = len(dres.Meta)
			}
			log.Printf("Described %d blobs in %v", num, td)
		}()
	}
	sh.initDescribeRequest(dr)
	if dr.BlobRef.Valid() {
		dr.Describe(dr.BlobRef, dr.depth())
	}
	for _, br := range dr.BlobRefs {
		dr.Describe(br, dr.depth())
	}
	if err := dr.expandRules(); err != nil {
		return nil, err
	}
	metaMap, err := dr.metaMap()
	if err != nil {
		return nil, err
	}
	return &DescribeResponse{metaMap}, nil
}

type DescribeRequest struct {
	// BlobRefs are the blobs to describe. If length zero, BlobRef
	// is used.
	BlobRefs []blob.Ref `json:"blobrefs,omitempty"`

	// BlobRef is the blob to describe.
	BlobRef blob.Ref `json:"blobref,omitempty"`

	// Depth is the optional traversal depth to describe from the
	// root BlobRef. If zero, a default is used.
	// Depth is deprecated and will be removed. Use Rules instead.
	Depth int `json:"depth,omitempty"`

	// MaxDirChildren is the requested optional limit to the number
	// of children that should be fetched when describing a static
	// directory. If zero, a default is used.
	MaxDirChildren int `json:"maxDirChildren,omitempty"`

	// At specifies the time which we wish to see the state of
	// this blob.  If zero (unspecified), all claims will be
	// considered, otherwise, any claims after this date will not
	// be considered.
	At types.Time3339 `json:"at"`

	// Rules specifies a set of rules to instruct how to keep
	// expanding the described set. All rules are tested and
	// matching rules grow the the response set until all rules no
	// longer match or internal limits are hit.
	Rules []*DescribeRule `json:"rules,omitempty"`

	// Internal details, used while loading.
	// Initialized by sh.initDescribeRequest.
	sh            *Handler
	mu            sync.Mutex // protects following:
	m             MetaMap
	done          map[blobrefAndDepth]bool // blobref -> true
	errs          map[string]error         // blobref -> error
	resFromRule   map[*DescribeRule]map[blob.Ref]bool
	flatRuleCache []*DescribeRule // flattened once, by flatRules

	wg *sync.WaitGroup // for load requests
}

type blobrefAndDepth struct {
	br    blob.Ref
	depth int
}

// Requires dr.mu is held
func (dr *DescribeRequest) flatRules() []*DescribeRule {
	if dr.flatRuleCache == nil {
		dr.flatRuleCache = make([]*DescribeRule, 0)
		for _, rule := range dr.Rules {
			rule.appendToFlatCache(dr)
		}
	}
	return dr.flatRuleCache
}

func (r *DescribeRule) appendToFlatCache(dr *DescribeRequest) {
	dr.flatRuleCache = append(dr.flatRuleCache, r)
	for _, rchild := range r.Rules {
		rchild.parentRule = r
		rchild.appendToFlatCache(dr)
	}
}

// Requires dr.mu is held.
func (dr *DescribeRequest) foreachResultBlob(fn func(blob.Ref)) {
	if dr.BlobRef.Valid() {
		fn(dr.BlobRef)
	}
	for _, br := range dr.BlobRefs {
		fn(br)
	}
	for brStr := range dr.m {
		if br, ok := blob.Parse(brStr); ok {
			fn(br)
		}
	}
}

// Requires dr.mu is held.
func (dr *DescribeRequest) blobInitiallyRequested(br blob.Ref) bool {
	if dr.BlobRef.Valid() && dr.BlobRef == br {
		return true
	}
	for _, br1 := range dr.BlobRefs {
		if br == br1 {
			return true
		}
	}
	return false
}

type DescribeRule struct {
	// All non-zero 'If*' fields in the following set must match
	// for the rule to match:

	// IsResultRoot, if true, only matches if the blob was part of
	// the original search results, not a blob expanded later.
	IfResultRoot bool `json:"ifResultRoot,omitempty"`

	// IfCamliNodeType matches if the "camliNodeType" attribute
	// equals this value.
	IfCamliNodeType string `json:"ifCamliNodeType,omitempty"`

	// Attrs lists attributes to describe. A special case
	// is if the value ends in "*", which matches prefixes
	// (e.g. "camliPath:*" or "*").
	Attrs []string `json:"attrs,omitempty"`

	// Additional rules to run on the described results of Attrs.
	Rules []*DescribeRule `json:"rules,omitempty"`

	parentRule *DescribeRule
}

// DescribeResponse is the JSON response from $searchRoot/camli/search/describe.
type DescribeResponse struct {
	Meta MetaMap `json:"meta"`
}

// A MetaMap is a map from blobref to a DescribedBlob.
type MetaMap map[string]*DescribedBlob

type DescribedBlob struct {
	Request *DescribeRequest `json:"-"`

	BlobRef   blob.Ref `json:"blobRef"`
	CamliType string   `json:"camliType,omitempty"`
	Size      int64    `json:"size,"`

	// if camliType "permanode"
	Permanode *DescribedPermanode `json:"permanode,omitempty"`

	// if camliType "file"
	File *camtypes.FileInfo `json:"file,omitempty"`
	// if camliType "directory"
	Dir *camtypes.FileInfo `json:"dir,omitempty"`
	// if camliType "file", and File.IsImage()
	Image *camtypes.ImageInfo `json:"image,omitempty"`
	// if camliType "file" and media file
	MediaTags map[string]string `json:"mediaTags,omitempty"`

	// if camliType "directory"
	DirChildren []blob.Ref `json:"dirChildren,omitempty"`

	// Stub is set if this is not loaded, but referenced.
	Stub bool `json:"-"`
}

func (m MetaMap) Get(br blob.Ref) *DescribedBlob {
	if !br.Valid() {
		return nil
	}
	return m[br.String()]
}

// URLSuffixPost returns the URL suffix for POST requests.
func (r *DescribeRequest) URLSuffixPost() string {
	return "camli/search/describe"
}

// URLSuffix returns the URL suffix for GET requests.
// This is deprecated.
func (r *DescribeRequest) URLSuffix() string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "camli/search/describe?depth=%d&maxdirchildren=%d",
		r.depth(), r.maxDirChildren())
	for _, br := range r.BlobRefs {
		buf.WriteString("&blobref=")
		buf.WriteString(br.String())
	}
	if len(r.BlobRefs) == 0 && r.BlobRef.Valid() {
		buf.WriteString("&blobref=")
		buf.WriteString(r.BlobRef.String())
	}
	if !r.At.IsZero() {
		buf.WriteString("&at=")
		buf.WriteString(r.At.String())
	}
	return buf.String()
}

// fromHTTP panics with an httputil value on failure
func (r *DescribeRequest) fromHTTP(req *http.Request) {
	switch {
	case httputil.IsGet(req):
		r.fromHTTPGet(req)
	case req.Method == "POST":
		r.fromHTTPPost(req)
	default:
		panic("Unsupported method")
	}
}

func (r *DescribeRequest) fromHTTPPost(req *http.Request) {
	err := json.NewDecoder(req.Body).Decode(r)
	if err != nil {
		panic(err)
	}
}

func (r *DescribeRequest) fromHTTPGet(req *http.Request) {
	req.ParseForm()
	if vv := req.Form["blobref"]; len(vv) > 1 {
		for _, brs := range vv {
			if br, ok := blob.Parse(brs); ok {
				r.BlobRefs = append(r.BlobRefs, br)
			} else {
				panic(httputil.InvalidParameterError("blobref"))
			}
		}
	} else {
		r.BlobRef = httputil.MustGetBlobRef(req, "blobref")
	}
	r.Depth = httputil.OptionalInt(req, "depth")
	r.MaxDirChildren = httputil.OptionalInt(req, "maxdirchildren")
	r.At = types.ParseTime3339OrZero(req.FormValue("at"))
}

// PermanodeFile returns in path the blobref of the described permanode
// and the blobref of its File camliContent.
// If b isn't a permanode, or doesn't have a camliContent that
// is a file blob, ok is false.
func (b *DescribedBlob) PermanodeFile() (path []blob.Ref, fi *camtypes.FileInfo, ok bool) {
	if b == nil || b.Permanode == nil {
		return
	}
	if contentRef := b.Permanode.Attr.Get("camliContent"); contentRef != "" {
		if cdes := b.Request.DescribedBlobStr(contentRef); cdes != nil && cdes.File != nil {
			return []blob.Ref{b.BlobRef, cdes.BlobRef}, cdes.File, true
		}
	}
	return
}

// PermanodeDir returns in path the blobref of the described permanode
// and the blobref of its Directory camliContent.
// If b isn't a permanode, or doesn't have a camliContent that
// is a directory blob, ok is false.
func (b *DescribedBlob) PermanodeDir() (path []blob.Ref, fi *camtypes.FileInfo, ok bool) {
	if b == nil || b.Permanode == nil {
		return
	}
	if contentRef := b.Permanode.Attr.Get("camliContent"); contentRef != "" {
		if cdes := b.Request.DescribedBlobStr(contentRef); cdes != nil && cdes.Dir != nil {
			return []blob.Ref{b.BlobRef, cdes.BlobRef}, cdes.Dir, true
		}
	}
	return
}

func (b *DescribedBlob) DomID() string {
	if b == nil {
		return ""
	}
	return b.BlobRef.DomID()
}

func (b *DescribedBlob) Title() string {
	if b == nil {
		return ""
	}
	if b.Permanode != nil {
		if t := b.Permanode.Attr.Get("title"); t != "" {
			return t
		}
		if contentRef := b.Permanode.Attr.Get("camliContent"); contentRef != "" {
			return b.Request.DescribedBlobStr(contentRef).Title()
		}
	}
	if b.File != nil {
		return b.File.FileName
	}
	if b.Dir != nil {
		return b.Dir.FileName
	}
	return ""
}

func (b *DescribedBlob) Description() string {
	if b == nil {
		return ""
	}
	if b.Permanode != nil {
		return b.Permanode.Attr.Get("description")
	}
	return ""
}

// Members returns all of b's children, as given by b's camliMember and camliPath:*
// attributes. Only the first entry for a given camliPath attribute is used.
func (b *DescribedBlob) Members() []*DescribedBlob {
	if b == nil {
		return nil
	}
	m := make([]*DescribedBlob, 0)
	if b.Permanode != nil {
		for _, bstr := range b.Permanode.Attr["camliMember"] {
			if br, ok := blob.Parse(bstr); ok {
				m = append(m, b.PeerBlob(br))
			}
		}
		for k, bstrs := range b.Permanode.Attr {
			if strings.HasPrefix(k, "camliPath:") && len(bstrs) > 0 {
				if br, ok := blob.Parse(bstrs[0]); ok {
					m = append(m, b.PeerBlob(br))
				}
			}
		}
	}
	return m
}

func (b *DescribedBlob) DirMembers() []*DescribedBlob {
	if b == nil || b.Dir == nil || len(b.DirChildren) == 0 {
		return nil
	}

	m := make([]*DescribedBlob, 0)
	for _, br := range b.DirChildren {
		m = append(m, b.PeerBlob(br))
	}
	return m
}

func (b *DescribedBlob) ContentRef() (br blob.Ref, ok bool) {
	if b != nil && b.Permanode != nil {
		if cref := b.Permanode.Attr.Get("camliContent"); cref != "" {
			return blob.Parse(cref)
		}
	}
	return
}

// Given a blobref string returns a Description or nil.
// dr may be nil itself.
func (dr *DescribeRequest) DescribedBlobStr(blobstr string) *DescribedBlob {
	if dr == nil {
		return nil
	}
	dr.mu.Lock()
	defer dr.mu.Unlock()
	return dr.m[blobstr]
}

// PeerBlob returns a DescribedBlob for the provided blobref.
//
// Unlike DescribedBlobStr, the returned DescribedBlob is never nil.
//
// If the blob was never loaded along with the the receiver (or if the
// receiver is nil), a stub DescribedBlob is returned with its Stub
// field set true.
func (b *DescribedBlob) PeerBlob(br blob.Ref) *DescribedBlob {
	if b.Request == nil {
		return &DescribedBlob{BlobRef: br, Stub: true}
	}
	b.Request.mu.Lock()
	defer b.Request.mu.Unlock()
	return b.peerBlob(br)
}

// version of PeerBlob when b.Request.mu is already held.
func (b *DescribedBlob) peerBlob(br blob.Ref) *DescribedBlob {
	if peer, ok := b.Request.m[br.String()]; ok {
		return peer
	}
	return &DescribedBlob{Request: b.Request, BlobRef: br, Stub: true}
}

func (b *DescribedBlob) isPermanode() bool {
	return b.Permanode != nil
}

type DescribedPermanode struct {
	Attr    url.Values `json:"attr"` // a map[string][]string
	ModTime time.Time  `json:"modtime,omitempty"`
}

// IsContainer returns whether the permanode has either named ("camliPath:"-prefixed) or unnamed
// ("camliMember") member attributes.
func (dp *DescribedPermanode) IsContainer() bool {
	if members := dp.Attr["camliMember"]; len(members) > 0 {
		return true
	}
	for k := range dp.Attr {
		if strings.HasPrefix(k, "camliPath:") {
			return true
		}
	}
	return false
}

func (dp *DescribedPermanode) jsonMap() map[string]interface{} {
	m := jsonMap()

	am := jsonMap()
	m["attr"] = am
	for k, vv := range dp.Attr {
		if len(vv) > 0 {
			vl := make([]string, len(vv))
			copy(vl[:], vv[:])
			am[k] = vl
		}
	}
	return m
}

// NewDescribeRequest returns a new DescribeRequest holding the state
// of blobs and their summarized descriptions.  Use DescribeBlob
// one or more times before calling Result.
func (sh *Handler) NewDescribeRequest() *DescribeRequest {
	dr := new(DescribeRequest)
	sh.initDescribeRequest(dr)
	return dr
}

func (sh *Handler) initDescribeRequest(req *DescribeRequest) {
	if req.sh != nil {
		panic("already initialized")
	}
	req.sh = sh
	req.m = make(MetaMap)
	req.errs = make(map[string]error)
	req.wg = new(sync.WaitGroup)
}

type DescribeError map[string]error

func (de DescribeError) Error() string {
	var buf bytes.Buffer
	for b, err := range de {
		fmt.Fprintf(&buf, "%s: %v; ", b, err)
	}
	return fmt.Sprintf("Errors (%d) describing blobs: %s", len(de), buf.String())
}

// Result waits for all outstanding lookups to complete and
// returns the map of blobref (strings) to their described
// results. The returned error is non-nil if any errors
// occured, and will be of type DescribeError.
func (dr *DescribeRequest) Result() (desmap map[string]*DescribedBlob, err error) {
	dr.wg.Wait()
	// TODO: set "done" / locked flag, so no more DescribeBlob can
	// be called.
	if len(dr.errs) > 0 {
		return dr.m, DescribeError(dr.errs)
	}
	return dr.m, nil
}

func (dr *DescribeRequest) depth() int {
	if dr.Depth > 0 {
		return dr.Depth
	}
	return 1
}

func (dr *DescribeRequest) maxDirChildren() int {
	return sanitizeNumResults(dr.MaxDirChildren)
}

func (dr *DescribeRequest) metaMap() (map[string]*DescribedBlob, error) {
	dr.wg.Wait()
	dr.mu.Lock()
	defer dr.mu.Unlock()
	for k, err := range dr.errs {
		// TODO: include all?
		return nil, fmt.Errorf("error populating %s: %v", k, err)
	}
	m := make(map[string]*DescribedBlob)
	for k, desb := range dr.m {
		m[k] = desb
	}
	return m, nil
}

func (dr *DescribeRequest) describedBlob(b blob.Ref) *DescribedBlob {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	bs := b.String()
	if des, ok := dr.m[bs]; ok {
		return des
	}
	des := &DescribedBlob{Request: dr, BlobRef: b}
	dr.m[bs] = des
	return des
}

func (dr *DescribeRequest) DescribeSync(br blob.Ref) (*DescribedBlob, error) {
	dr.Describe(br, 1)
	res, err := dr.Result()
	if err != nil {
		return nil, err
	}
	return res[br.String()], nil
}

// Describe starts a lookup of br, down to the provided depth.
// It returns immediately.
func (dr *DescribeRequest) Describe(br blob.Ref, depth int) {
	if depth <= 0 {
		return
	}
	dr.mu.Lock()
	defer dr.mu.Unlock()
	if dr.done == nil {
		dr.done = make(map[blobrefAndDepth]bool)
	}
	doneKey := blobrefAndDepth{br, depth}
	if dr.done[doneKey] {
		return
	}
	dr.done[doneKey] = true
	dr.wg.Add(1)
	go func() {
		defer dr.wg.Done()
		dr.describeReally(br, depth)
	}()
}

// requires dr.mu is held
func (dr *DescribeRequest) isDescribedOrError(br blob.Ref) bool {
	brs := br.String()
	if _, ok := dr.m[brs]; ok {
		return true
	}
	if _, ok := dr.errs[brs]; ok {
		return true
	}
	return false
}

// requires dr.mu be held.
func (r *DescribeRule) newMatches(br blob.Ref, dr *DescribeRequest) (brs []blob.Ref) {
	if r.IfResultRoot {
		if !dr.blobInitiallyRequested(br) {
			return nil
		}
	}
	if r.parentRule != nil {
		if _, ok := dr.resFromRule[r.parentRule][br]; !ok {
			return nil
		}
	}
	db, ok := dr.m[br.String()]
	if !ok || db.Permanode == nil {
		return nil
	}
	if t := r.IfCamliNodeType; t != "" {
		gotType := db.Permanode.Attr.Get("camliNodeType")
		if gotType != t {
			return nil
		}
	}
	for attr, vv := range db.Permanode.Attr {
		matches := false
		for _, matchAttr := range r.Attrs {
			if attr == matchAttr {
				matches = true
				break
			}
			if strings.HasSuffix(matchAttr, "*") && strings.HasPrefix(attr, strings.TrimSuffix(matchAttr, "*")) {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		for _, v := range vv {
			if br, ok := blob.Parse(v); ok {
				brs = append(brs, br)
			}
		}
	}
	return brs
}

// dr.mu just be locked.
func (dr *DescribeRequest) noteResultFromRule(rule *DescribeRule, br blob.Ref) {
	if dr.resFromRule == nil {
		dr.resFromRule = make(map[*DescribeRule]map[blob.Ref]bool)
	}
	m, ok := dr.resFromRule[rule]
	if !ok {
		m = make(map[blob.Ref]bool)
		dr.resFromRule[rule] = m
	}
	m[br] = true
}

func (dr *DescribeRequest) expandRules() error {
	loop := true

	for loop {
		loop = false
		dr.wg.Wait()
		dr.mu.Lock()
		len0 := len(dr.m)
		var new []blob.Ref
		for _, rule := range dr.flatRules() {
			dr.foreachResultBlob(func(br blob.Ref) {
				for _, nbr := range rule.newMatches(br, dr) {
					new = append(new, nbr)
					dr.noteResultFromRule(rule, nbr)
				}
			})
		}
		dr.mu.Unlock()
		for _, br := range new {
			dr.Describe(br, 1)
		}
		dr.wg.Wait()
		dr.mu.Lock()
		len1 := len(dr.m)
		dr.mu.Unlock()
		loop = len0 != len1
	}
	return nil
}

func (dr *DescribeRequest) addError(br blob.Ref, err error) {
	if err == nil {
		return
	}
	dr.mu.Lock()
	defer dr.mu.Unlock()
	// TODO: append? meh.
	dr.errs[br.String()] = err
}

func (dr *DescribeRequest) describeReally(br blob.Ref, depth int) {
	meta, err := dr.sh.index.GetBlobMeta(br)
	if err == os.ErrNotExist {
		return
	}
	if err != nil {
		dr.addError(br, err)
		return
	}

	// TODO: convert all this in terms of
	// DescribedBlob/DescribedPermanode/DescribedFile, not json
	// maps.  Then add JSON marhsallers to those types. Add tests.
	des := dr.describedBlob(br)
	if meta.CamliType != "" {
		des.setMIMEType("application/json; camliType=" + meta.CamliType)
	}
	des.Size = int64(meta.Size)

	switch des.CamliType {
	case "permanode":
		des.Permanode = new(DescribedPermanode)
		dr.populatePermanodeFields(des.Permanode, br, dr.sh.owner, depth)
	case "file":
		fi, err := dr.sh.index.GetFileInfo(br)
		if err != nil {
			if os.IsNotExist(err) {
				log.Printf("index.GetFileInfo(file %s) failed; index stale?", br)
			} else {
				dr.addError(br, err)
			}
			return
		}
		des.File = &fi
		if des.File.IsImage() {
			imgInfo, err := dr.sh.index.GetImageInfo(br)
			if err != nil {
				if !os.IsNotExist(err) {
					dr.addError(br, err)
				}
			} else {
				des.Image = &imgInfo
			}
		}
		if mediaTags, err := dr.sh.index.GetMediaTags(br); err == nil {
			des.MediaTags = mediaTags
		}
	case "directory":
		var g syncutil.Group
		g.Go(func() (err error) {
			fi, err := dr.sh.index.GetFileInfo(br)
			if os.IsNotExist(err) {
				log.Printf("index.GetFileInfo(directory %s) failed; index stale?", br)
			}
			if err == nil {
				des.Dir = &fi
			}
			return
		})
		g.Go(func() (err error) {
			des.DirChildren, err = dr.getDirMembers(br, depth)
			return
		})
		if err := g.Err(); err != nil {
			dr.addError(br, err)
		}
	}
}

func (dr *DescribeRequest) populatePermanodeFields(pi *DescribedPermanode, pn, signer blob.Ref, depth int) {
	pi.Attr = make(url.Values)
	attr := pi.Attr

	claims, err := dr.sh.index.AppendClaims(nil, pn, signer, "")
	if err != nil {
		log.Printf("Error getting claims of %s: %v", pn.String(), err)
		dr.addError(pn, fmt.Errorf("Error getting claims of %s: %v", pn.String(), err))
		return
	}

	sort.Sort(camtypes.ClaimsByDate(claims))
claimLoop:
	for _, cl := range claims {
		if !dr.At.IsZero() {
			if cl.Date.After(dr.At.Time()) {
				continue
			}
		}
		switch cl.Type {
		default:
			continue
		case "del-attribute":
			if cl.Value == "" {
				delete(attr, cl.Attr)
			} else {
				sl := attr[cl.Attr]
				filtered := make([]string, 0, len(sl))
				for _, val := range sl {
					if val != cl.Value {
						filtered = append(filtered, val)
					}
				}
				attr[cl.Attr] = filtered
			}
		case "set-attribute":
			delete(attr, cl.Attr)
			fallthrough
		case "add-attribute":
			if cl.Value == "" {
				continue
			}
			sl, ok := attr[cl.Attr]
			if ok {
				for _, exist := range sl {
					if exist == cl.Value {
						continue claimLoop
					}
				}
			} else {
				sl = make([]string, 0, 1)
				attr[cl.Attr] = sl
			}
			attr[cl.Attr] = append(sl, cl.Value)
		}
		pi.ModTime = cl.Date
	}

	// Descend into any references in current attributes.
	for key, vals := range attr {
		dr.describeRefs(key, depth)
		for _, v := range vals {
			dr.describeRefs(v, depth)
		}
	}
}

func (dr *DescribeRequest) getDirMembers(br blob.Ref, depth int) ([]blob.Ref, error) {
	limit := dr.maxDirChildren()
	ch := make(chan blob.Ref)
	errch := make(chan error)
	go func() {
		errch <- dr.sh.index.GetDirMembers(br, ch, limit)
	}()

	var members []blob.Ref
	for child := range ch {
		dr.Describe(child, depth)
		members = append(members, child)
	}
	if err := <-errch; err != nil {
		return nil, err
	}
	return members, nil
}

func (dr *DescribeRequest) describeRefs(str string, depth int) {
	for _, match := range blobRefPattern.FindAllString(str, -1) {
		if ref, ok := blob.ParseKnown(match); ok {
			dr.Describe(ref, depth-1)
		}
	}
}

func (d *DescribedBlob) setMIMEType(mime string) {
	if strings.HasPrefix(mime, camliTypePrefix) {
		d.CamliType = strings.TrimPrefix(mime, camliTypePrefix)
	}
}
