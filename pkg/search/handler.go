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

package search

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"math"
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
	"camlistore.org/pkg/images"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/syncutil"
	"camlistore.org/pkg/types"
	"camlistore.org/pkg/types/camtypes"
)

const buffered = 32     // arbitrary channel buffer size
const maxResults = 1000 // arbitrary limit on the number of search results returned
const defaultNumResults = 50

// MaxImageSize is the maximum width or height in pixels that we will serve image
// thumbnails at. It is used in the search result UI.
const MaxImageSize = 2000

func init() {
	blobserver.RegisterHandlerConstructor("search", newHandlerFromConfig)
}

// Handler handles search queries.
type Handler struct {
	index index.Interface
	owner blob.Ref

	// Corpus optionally specifies the full in-memory metadata corpus
	// to use.
	// TODO: this may be required in the future, or folded into the index
	// interface.
	corpus *index.Corpus
}

// IGetRecentPermanodes is the interface encapsulating the GetRecentPermanodes query.
type IGetRecentPermanodes interface {
	// GetRecentPermanodes returns recently-modified permanodes.
	// This is a higher-level query returning more metadata than the index.GetRecentPermanodes,
	// which only scans the blobrefs but doesn't return anything about the permanodes.
	// TODO: rename this one?
	GetRecentPermanodes(*RecentRequest) (*RecentResponse, error)
}

var (
	_ IGetRecentPermanodes = (*Handler)(nil)
)

func NewHandler(index index.Interface, owner blob.Ref) *Handler {
	return &Handler{index: index, owner: owner}
}

func (h *Handler) SetCorpus(c *index.Corpus) {
	h.corpus = c
}

func newHandlerFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (http.Handler, error) {
	indexPrefix := conf.RequiredString("index") // TODO: add optional help tips here?
	ownerBlobStr := conf.RequiredString("owner")
	devBlockStartupPrefix := conf.OptionalString("devBlockStartupOn", "")
	slurpToMemory := conf.OptionalBool("slurpToMemory", false)
	if err := conf.Validate(); err != nil {
		return nil, err
	}

	if devBlockStartupPrefix != "" {
		_, err := ld.GetHandler(devBlockStartupPrefix)
		if err != nil {
			return nil, fmt.Errorf("search handler references bogus devBlockStartupOn handler %s: %v", devBlockStartupPrefix, err)
		}
	}

	indexHandler, err := ld.GetHandler(indexPrefix)
	if err != nil {
		return nil, fmt.Errorf("search config references unknown handler %q", indexPrefix)
	}
	indexer, ok := indexHandler.(index.Interface)
	if !ok {
		return nil, fmt.Errorf("search config references invalid indexer %q (actually a %T)", indexPrefix, indexHandler)
	}
	ownerBlobRef, ok := blob.Parse(ownerBlobStr)
	if !ok {
		return nil, fmt.Errorf("search 'owner' has malformed blobref %q; expecting e.g. sha1-xxxxxxxxxxxx",
			ownerBlobStr)
	}
	h := &Handler{
		index: indexer,
		owner: ownerBlobRef,
	}
	if slurpToMemory {
		ii := indexer.(*index.Index)
		corpus, err := ii.KeepInMemory()
		if err != nil {
			return nil, fmt.Errorf("error slurping index to memory: %v", err)
		}
		h.corpus = corpus
	}
	return h, nil
}

// Owner returns Handler owner's public key blobref.
func (h *Handler) Owner() blob.Ref {
	// TODO: figure out a plan for an owner having multiple active public keys, or public
	// key rotation
	return h.owner
}

func (h *Handler) Index() index.Interface {
	return h.index
}

func jsonMap() map[string]interface{} {
	return make(map[string]interface{})
}

func (sh *Handler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	ret := jsonMap()
	suffix := httputil.PathSuffix(req)

	if httputil.IsGet(req) {
		switch suffix {
		case "camli/search/recent":
			sh.serveRecentPermanodes(rw, req)
			return
		case "camli/search/permanodeattr":
			sh.servePermanodesWithAttr(rw, req)
			return
		case "camli/search/describe":
			sh.serveDescribe(rw, req)
			return
		case "camli/search/claims":
			sh.serveClaims(rw, req)
			return
		case "camli/search/files":
			sh.serveFiles(rw, req)
			return
		case "camli/search/signerattrvalue":
			sh.serveSignerAttrValue(rw, req)
			return
		case "camli/search/signerpaths":
			sh.serveSignerPaths(rw, req)
			return
		case "camli/search/edgesto":
			sh.serveEdgesTo(rw, req)
			return
		}
	}
	if req.Method == "POST" {
		switch suffix {
		case "camli/search/query":
			sh.serveQuery(rw, req)
			return
		}
	}

	// TODO: discovery for the endpoints & better error message with link to discovery info
	ret["error"] = "Unsupported search path or method"
	ret["errorType"] = "input"
	httputil.ReturnJSON(rw, ret)
}

// sanitizeNumResults takes n as a requested number of search results and sanitizes it.
func sanitizeNumResults(n int) int {
	if n <= 0 || n > maxResults {
		return defaultNumResults
	}
	return n
}

// RecentRequest is a request to get a RecentResponse.
type RecentRequest struct {
	N             int       // if zero, default number of results
	Before        time.Time // if zero, now
	ThumbnailSize int       // if zero, no thumbnails
}

func (r *RecentRequest) URLSuffix() string {
	// TODO: Before
	return fmt.Sprintf("camli/search/recent?n=%d&thumbnails=%d", r.n(), r.thumbnailSize())
}

// fromHTTP panics with an httputil value on failure
func (r *RecentRequest) fromHTTP(req *http.Request) {
	r.N, _ = strconv.Atoi(req.FormValue("n"))
	r.ThumbnailSize = thumbnailSize(req)
	// TODO: populate Before
}

// n returns the sanitized maximum number of search results.
func (r *RecentRequest) n() int {
	return sanitizeNumResults(r.N)
}

func (r *RecentRequest) thumbnailSize() int {
	v := r.ThumbnailSize
	if v == 0 {
		return 0
	}
	if v < minThumbSize || v > maxThumbSize {
		return defThumbSize
	}
	return v
}

// WithAttrRequest is a request to get a WithAttrResponse.
type WithAttrRequest struct {
	N      int      // max number of results
	Signer blob.Ref // if nil, will use the server's default owner (if configured)
	// Requested attribute. If blank, all attributes are searched (for Value)
	// as fulltext.
	Attr string
	// Value of the requested attribute. If blank, permanodes which have
	// request.Attr as an attribute are searched.
	Value         string
	Fuzzy         bool // fulltext search (if supported).
	ThumbnailSize int  // if zero, no thumbnails
}

func (r *WithAttrRequest) URLSuffix() string {
	return fmt.Sprintf("camli/search/permanodeattr?signer=%v&value=%v&fuzzy=%v&attr=%v&max=%v&thumbnails=%v",
		r.Signer, url.QueryEscape(r.Value), r.Fuzzy, r.Attr, r.N, r.ThumbnailSize)
}

// fromHTTP panics with an httputil value on failure
func (r *WithAttrRequest) fromHTTP(req *http.Request) {
	r.Signer = blob.ParseOrZero(req.FormValue("signer"))
	r.Value = req.FormValue("value")
	fuzzy := req.FormValue("fuzzy") // exact match if empty
	fuzzyMatch := false
	if fuzzy != "" {
		lowered := strings.ToLower(fuzzy)
		if lowered == "true" || lowered == "t" {
			fuzzyMatch = true
		}
	}
	r.Attr = req.FormValue("attr") // all attributes if empty
	if r.Attr == "" {              // and force fuzzy in that case.
		fuzzyMatch = true
	}
	r.Fuzzy = fuzzyMatch
	r.ThumbnailSize = thumbnailSize(req)
	max := req.FormValue("max")
	if max != "" {
		maxR, err := strconv.Atoi(max)
		if err != nil {
			panic(httputil.InvalidParameterError("max"))
		}
		r.N = maxR
	}
	r.N = r.n()
}

// n returns the sanitized maximum number of search results.
func (r *WithAttrRequest) n() int {
	return sanitizeNumResults(r.N)
}

func (r *WithAttrRequest) thumbnailSize() int {
	v := r.ThumbnailSize
	if v == 0 {
		return 0
	}
	if v < minThumbSize {
		return minThumbSize
	}
	if v > maxThumbSize {
		return maxThumbSize
	}
	return v
}

// ClaimsRequest is a request to get a ClaimsResponse.
type ClaimsRequest struct {
	Permanode blob.Ref
}

// fromHTTP panics with an httputil value on failure
func (r *ClaimsRequest) fromHTTP(req *http.Request) {
	r.Permanode = httputil.MustGetBlobRef(req, "permanode")
}

// SignerPathsRequest is a request to get a SignerPathsResponse.
type SignerPathsRequest struct {
	Signer blob.Ref
	Target blob.Ref
}

// fromHTTP panics with an httputil value on failure
func (r *SignerPathsRequest) fromHTTP(req *http.Request) {
	r.Signer = httputil.MustGetBlobRef(req, "signer")
	r.Target = httputil.MustGetBlobRef(req, "target")
}

// EdgesRequest is a request to get an EdgesResponse.
type EdgesRequest struct {
	// The blob we want to find as a reference.
	ToRef blob.Ref
}

// fromHTTP panics with an httputil value on failure
func (r *EdgesRequest) fromHTTP(req *http.Request) {
	r.ToRef = httputil.MustGetBlobRef(req, "blobref")
}

// A MetaMap is a map from blobref to a DescribedBlob.
type MetaMap map[string]*DescribedBlob

func (m MetaMap) Get(br blob.Ref) *DescribedBlob {
	if !br.Valid() {
		return nil
	}
	return m[br.String()]
}

// RecentResponse is the JSON response from $searchRoot/camli/search/recent.
type RecentResponse struct {
	Recent []*RecentItem `json:"recent"`
	Meta   MetaMap       `json:"meta"`

	Error     string `json:"error,omitempty"`
	ErrorType string `json:"errorType,omitempty"`
}

func (r *RecentResponse) Err() error {
	if r.Error != "" || r.ErrorType != "" {
		if r.ErrorType != "" {
			return fmt.Errorf("%s: %s", r.ErrorType, r.Error)
		}
		return errors.New(r.Error)
	}
	return nil
}

// DescribeResponse is the JSON response from $searchRoot/camli/search/describe.
type DescribeResponse struct {
	Meta MetaMap `json:"meta"`
}

// WithAttrResponse is the JSON response from $searchRoot/camli/search/permanodeattr.
type WithAttrResponse struct {
	WithAttr []*WithAttrItem `json:"withAttr"`
	Meta     MetaMap         `json:"meta"`

	Error     string `json:"error,omitempty"`
	ErrorType string `json:"errorType,omitempty"`
}

func (r *WithAttrResponse) Err() error {
	if r.Error != "" || r.ErrorType != "" {
		if r.ErrorType != "" {
			return fmt.Errorf("%s: %s", r.ErrorType, r.Error)
		}
		return errors.New(r.Error)
	}
	return nil
}

// ClaimsResponse is the JSON response from $searchRoot/camli/search/claims.
type ClaimsResponse struct {
	Claims []*ClaimsItem `json:"claims"`
}

// SignerPathsResponse is the JSON response from $searchRoot/camli/search/signerpaths.
type SignerPathsResponse struct {
	Paths []*SignerPathsItem `json:"paths"`
	Meta  MetaMap            `json:"meta"`
}

// A RecentItem is an item returned from $searchRoot/camli/search/recent in the "recent" list.
type RecentItem struct {
	BlobRef blob.Ref       `json:"blobref"`
	ModTime types.Time3339 `json:"modtime"`
	Owner   blob.Ref       `json:"owner"`
}

// A WithAttrItem is an item returned from $searchRoot/camli/search/permanodeattr.
type WithAttrItem struct {
	Permanode blob.Ref `json:"permanode"`
}

// A ClaimsItem is an item returned from $searchRoot/camli/search/claims.
type ClaimsItem struct {
	BlobRef   blob.Ref       `json:"blobref"`
	Signer    blob.Ref       `json:"signer"`
	Permanode blob.Ref       `json:"permanode"`
	Date      types.Time3339 `json:"date"`
	Type      string         `json:"type"`
	Attr      string         `json:"attr,omitempty"`
	Value     string         `json:"value,omitempty"`
}

// A SignerPathsItem is an item returned from $searchRoot/camli/search/signerpaths.
type SignerPathsItem struct {
	ClaimRef blob.Ref `json:"claimRef"`
	BaseRef  blob.Ref `json:"baseRef"`
	Suffix   string   `json:"suffix"`
}

// EdgesResponse is the JSON response from $searchRoot/camli/search/edgesto.
type EdgesResponse struct {
	ToRef   blob.Ref    `json:"toRef"`
	EdgesTo []*EdgeItem `json:"edgesTo"`
}

// An EdgeItem is an item returned from $searchRoot/camli/search/edgesto.
type EdgeItem struct {
	From     blob.Ref `json:"from"`
	FromType string   `json:"fromType"`
}

func thumbnailSize(r *http.Request) int {
	return thumbnailSizeStr(r.FormValue("thumbnails"))
}

const (
	minThumbSize = 25
	defThumbSize = 50
	maxThumbSize = 800
)

func thumbnailSizeStr(s string) int {
	if s == "" {
		return 0
	}
	if i, _ := strconv.Atoi(s); i >= minThumbSize && i <= maxThumbSize {
		return i
	}
	return defThumbSize
}

var testHookBug121 = func() {}

// GetRecentPermanodes returns recently-modified permanodes.
func (sh *Handler) GetRecentPermanodes(req *RecentRequest) (*RecentResponse, error) {
	ch := make(chan camtypes.RecentPermanode)
	errch := make(chan error, 1)
	go func() {
		errch <- sh.index.GetRecentPermanodes(ch, sh.owner, req.n())
	}()

	dr := sh.NewDescribeRequest()

	var recent []*RecentItem
	for res := range ch {
		dr.Describe(res.Permanode, 2)
		recent = append(recent, &RecentItem{
			BlobRef: res.Permanode,
			Owner:   res.Signer,
			ModTime: types.Time3339(res.LastModTime),
		})
		testHookBug121() // http://camlistore.org/issue/121
	}

	if err := <-errch; err != nil {
		return nil, err
	}

	metaMap, err := dr.metaMapThumbs(req.thumbnailSize())
	if err != nil {
		return nil, err
	}

	res := &RecentResponse{
		Recent: recent,
		Meta:   metaMap,
	}
	return res, nil
}

func (sh *Handler) serveRecentPermanodes(rw http.ResponseWriter, req *http.Request) {
	defer httputil.RecoverJSON(rw, req)
	var rr RecentRequest
	rr.fromHTTP(req)
	res, err := sh.GetRecentPermanodes(&rr)
	if err != nil {
		httputil.ServeJSONError(rw, err)
		return
	}
	httputil.ReturnJSON(rw, res)
}

// GetPermanodesWithAttr returns permanodes with attribute req.Attr
// having the req.Value as a value.
// See WithAttrRequest for more details about the query.
func (sh *Handler) GetPermanodesWithAttr(req *WithAttrRequest) (*WithAttrResponse, error) {
	ch := make(chan blob.Ref, buffered)
	errch := make(chan error, 1)
	go func() {
		signer := req.Signer
		if !signer.Valid() {
			signer = sh.owner
		}
		errch <- sh.index.SearchPermanodesWithAttr(ch,
			&camtypes.PermanodeByAttrRequest{
				Attribute:  req.Attr,
				Query:      req.Value,
				Signer:     signer,
				FuzzyMatch: req.Fuzzy,
				MaxResults: req.N,
			})
	}()

	dr := sh.NewDescribeRequest()

	var withAttr []*WithAttrItem
	for res := range ch {
		dr.Describe(res, 2)
		withAttr = append(withAttr, &WithAttrItem{
			Permanode: res,
		})
	}

	metaMap, err := dr.metaMapThumbs(req.thumbnailSize())
	if err != nil {
		return nil, err
	}

	if err := <-errch; err != nil {
		return nil, err
	}

	res := &WithAttrResponse{
		WithAttr: withAttr,
		Meta:     metaMap,
	}
	return res, nil
}

// servePermanodesWithAttr uses the indexer to search for the permanodes matching
// the request.
// The valid values for the "attr" key in the request (i.e the only attributes
// for a permanode which are actually indexed as such) are "tag" and "title".
func (sh *Handler) servePermanodesWithAttr(rw http.ResponseWriter, req *http.Request) {
	defer httputil.RecoverJSON(rw, req)
	var wr WithAttrRequest
	wr.fromHTTP(req)
	res, err := sh.GetPermanodesWithAttr(&wr)
	if err != nil {
		httputil.ServeJSONError(rw, err)
		return
	}
	httputil.ReturnJSON(rw, res)
}

// GetClaims returns the claims on req.Permanode signed by sh.owner.
func (sh *Handler) GetClaims(req *ClaimsRequest) (*ClaimsResponse, error) {
	if !req.Permanode.Valid() {
		return nil, errors.New("Error getting claims: nil permanode.")
	}
	var claims []camtypes.Claim
	claims, err := sh.index.AppendClaims(claims, req.Permanode, sh.owner, "")
	if err != nil {
		return nil, fmt.Errorf("Error getting claims of %s: %v", req.Permanode.String(), err)
	}
	sort.Sort(camtypes.ClaimsByDate(claims))
	var jclaims []*ClaimsItem
	for _, claim := range claims {
		jclaim := &ClaimsItem{
			BlobRef:   claim.BlobRef,
			Signer:    claim.Signer,
			Permanode: claim.Permanode,
			Date:      types.Time3339(claim.Date),
			Type:      claim.Type,
			Attr:      claim.Attr,
			Value:     claim.Value,
		}
		jclaims = append(jclaims, jclaim)
	}

	res := &ClaimsResponse{
		Claims: jclaims,
	}
	return res, nil
}

func (sh *Handler) serveClaims(rw http.ResponseWriter, req *http.Request) {
	defer httputil.RecoverJSON(rw, req)
	var cr ClaimsRequest
	cr.fromHTTP(req)
	res, err := sh.GetClaims(&cr)
	if err != nil {
		httputil.ServeJSONError(rw, err)
		return
	}
	httputil.ReturnJSON(rw, res)
}

type DescribeRequest struct {
	// BlobRefs are the blobs to describe. If length zero, BlobRef
	// is used.
	BlobRefs []blob.Ref

	// BlobRef is the blob to describe.
	BlobRef blob.Ref

	// Depth is the optional traversal depth to describe from the
	// root BlobRef. If zero, a default is used.
	Depth int
	// MaxDirChildren is the requested optional limit to the number
	// of children that should be fetched when describing a static
	// directory. If zero, a default is used.
	MaxDirChildren int

	ThumbnailSize int // or zero for none

	// Internal details, used while loading.
	// Initialized by sh.initDescribeRequest.
	sh   *Handler
	mu   sync.Mutex // protects following:
	m    MetaMap
	done map[string]bool  // blobref -> described
	errs map[string]error // blobref -> error

	wg *sync.WaitGroup // for load requests
}

func (r *DescribeRequest) URLSuffix() string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "camli/search/describe?depth=%d&maxdirchildren=%d", r.depth(), r.maxDirChildren())
	for _, br := range r.BlobRefs {
		buf.WriteString("&blobref=")
		buf.WriteString(br.String())
	}
	if len(r.BlobRefs) == 0 && r.BlobRef.Valid() {
		buf.WriteString("&blobref=")
		buf.WriteString(r.BlobRef.String())
	}
	return buf.String()
}

// fromHTTP panics with an httputil value on failure
func (r *DescribeRequest) fromHTTP(req *http.Request) {
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
	r.ThumbnailSize = thumbnailSize(req)
}

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
	// if camliType "directory"
	DirChildren []blob.Ref `json:"dirChildren,omitempty"`

	Thumbnail       string `json:"thumbnailSrc,omitempty"`
	ThumbnailWidth  int    `json:"thumbnailWidth,omitempty"`
	ThumbnailHeight int    `json:"thumbnailHeight,omitempty"`

	// Stub is set if this is not loaded, but referenced.
	Stub bool `json:"-"`
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

// HasSecureLinkTo returns true if there's a valid link from this blob
// to the other blob. This is used in access control (hence the
// somewhat redundant "Secure" in the name) and should be paranoid
// against e.g. random user/attacker-control attributes making links
// to other blobs.
//
// TODO: don't linear scan here.  rewrite this in terms of ResolvePrefixHop,
// passing down some policy perhaps?  or maybe that's enough.
func (b *DescribedBlob) HasSecureLinkTo(other blob.Ref) bool {
	if b == nil || !other.Valid() {
		return false
	}
	ostr := other.String()
	if b.Permanode != nil {
		if b.Permanode.Attr.Get("camliContent") == ostr {
			return true
		}
		for _, mstr := range b.Permanode.Attr["camliMember"] {
			if mstr == ostr {
				return true
			}
		}
	}
	return false
}

func (b *DescribedBlob) isPermanode() bool {
	return b.Permanode != nil
}

// returns a path relative to the UI handler.
//
// Locking: requires that DescribedRequest is done loading or that
// Request.mu is held (as it is from metaMap)
func (b *DescribedBlob) thumbnail(thumbSize int) (path string, width, height int, ok bool) {
	if thumbSize <= 0 || !b.isPermanode() {
		return
	}
	if b.Stub {
		return "node.png", thumbSize, thumbSize, true
	}
	pnAttr := b.Permanode.Attr

	if members := pnAttr["camliMember"]; len(members) > 0 {
		return "folder.png", thumbSize, thumbSize, true
	}

	if content, ok := b.ContentRef(); ok {
		peer := b.peerBlob(content)
		if peer.File != nil {
			if peer.File.IsImage() {
				image := fmt.Sprintf("thumbnail/%s/%s?mh=%d", peer.BlobRef,
					url.QueryEscape(peer.File.FileName), thumbSize)
				if peer.Image != nil {
					mw, mh := images.ScaledDimensions(
						peer.Image.Width, peer.Image.Height,
						MaxImageSize, thumbSize)
					return image, mw, mh, true
				}
				return image, thumbSize, thumbSize, true
			}

			// TODO: different thumbnails based on peer.File.MIMEType.
			const fileIconAspectRatio = 260.0 / 300.0
			var width = int(math.Floor(float64(thumbSize)*fileIconAspectRatio + 0.5))
			return "file.png", width, thumbSize, true
		}
		if peer.Dir != nil {
			return "folder.png", thumbSize, thumbSize, true
		}
	}

	return "node.png", thumbSize, thumbSize, true
}

type DescribedPermanode struct {
	Attr    url.Values `json:"attr"` // a map[string][]string
	ModTime time.Time  `json:"modtime,omitempty"`
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

// Given a blobref and a few hex characters of the digest of the next hop, return the complete
// blobref of the prefix, if that's a valid next hop.
func (sh *Handler) ResolvePrefixHop(parent blob.Ref, prefix string) (child blob.Ref, err error) {
	// TODO: this is a linear scan right now. this should be
	// optimized to use a new database table of members so this is
	// a quick lookup.  in the meantime it should be in memcached
	// at least.
	if len(prefix) < 8 {
		return blob.Ref{}, fmt.Errorf("Member prefix %q too small", prefix)
	}
	dr := sh.NewDescribeRequest()
	dr.Describe(parent, 1)
	res, err := dr.Result()
	if err != nil {
		return
	}
	des, ok := res[parent.String()]
	if !ok {
		return blob.Ref{}, fmt.Errorf("Failed to describe member %q in parent %q", prefix, parent)
	}
	if des.Permanode != nil {
		cr, ok := des.ContentRef()
		if ok && strings.HasPrefix(cr.Digest(), prefix) {
			return cr, nil
		}
		for _, member := range des.Members() {
			if strings.HasPrefix(member.BlobRef.Digest(), prefix) {
				return member.BlobRef, nil
			}
		}
		_, err := dr.DescribeSync(cr)
		if err != nil {
			return blob.Ref{}, fmt.Errorf("Failed to describe content %q of parent %q", cr, parent)
		}
		if _, _, ok := des.PermanodeDir(); ok {
			return sh.ResolvePrefixHop(cr, prefix)
		}
	} else if des.Dir != nil {
		for _, child := range des.DirChildren {
			if strings.HasPrefix(child.Digest(), prefix) {
				return child, nil
			}
		}
	}
	return blob.Ref{}, fmt.Errorf("Member prefix %q not found in %q", prefix, parent)
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
	return 4
}

func (dr *DescribeRequest) maxDirChildren() int {
	return sanitizeNumResults(dr.MaxDirChildren)
}

func (dr *DescribeRequest) metaMap() (map[string]*DescribedBlob, error) {
	return dr.metaMapThumbs(0)
}

func (dr *DescribeRequest) metaMapThumbs(thumbSize int) (map[string]*DescribedBlob, error) {
	// thumbSize of zero means to not include the thumbnails.
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
		if src, w, h, ok := desb.thumbnail(thumbSize); ok {
			desb.Thumbnail = src
			desb.ThumbnailWidth = w
			desb.ThumbnailHeight = h
		}
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
		dr.done = make(map[string]bool)
	}
	brefAndDepth := fmt.Sprintf("%s-%d", br, depth)
	if dr.done[brefAndDepth] {
		return
	}
	dr.done[brefAndDepth] = true
	dr.wg.Add(1)
	go func() {
		defer dr.wg.Done()
		dr.describeReally(br, depth)
	}()
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
				if os.IsNotExist(err) {
					log.Printf("index.GetImageInfo(file %s) failed; index stale?", br)
				} else {
					dr.addError(br, err)
				}
			}
			des.Image = &imgInfo
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

func (sh *Handler) Describe(dr *DescribeRequest) (*DescribeResponse, error) {
	sh.initDescribeRequest(dr)
	if dr.BlobRef.Valid() {
		dr.Describe(dr.BlobRef, dr.depth())
	}
	for _, br := range dr.BlobRefs {
		dr.Describe(br, dr.depth())
	}
	metaMap, err := dr.metaMapThumbs(dr.ThumbnailSize)
	if err != nil {
		return nil, err
	}
	return &DescribeResponse{metaMap}, nil
}

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

func (sh *Handler) serveFiles(rw http.ResponseWriter, req *http.Request) {
	ret := jsonMap()
	defer httputil.ReturnJSON(rw, ret)

	br, ok := blob.Parse(req.FormValue("wholedigest"))
	if !ok {
		ret["error"] = "Missing or invalid 'wholedigest' param"
		ret["errorType"] = "input"
		return
	}

	files, err := sh.index.ExistingFileSchemas(br)
	if err != nil {
		ret["error"] = err.Error()
		ret["errorType"] = "server"
		return
	}

	strList := []string{}
	for _, br := range files {
		strList = append(strList, br.String())
	}
	ret["files"] = strList
	return
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

	// If the content permanode is now known, look up its type
	if content, ok := attr["camliContent"]; ok && len(content) > 0 {
		if cbr, ok := blob.Parse(content[len(content)-1]); ok {
			dr.Describe(cbr, depth-1)
		}
	}

	// Resolve children
	if members, ok := attr["camliMember"]; ok {
		for _, member := range members {
			if membr, ok := blob.Parse(member); ok {
				dr.Describe(membr, depth-1)
			}
		}
	}

	// Resolve path elements
	for k, vv := range attr {
		if !strings.HasPrefix(k, "camliPath:") {
			continue
		}
		for _, brs := range vv {
			if br, ok := blob.Parse(brs); ok {
				dr.Describe(br, depth-1)
			}
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

// SignerAttrValueResponse is the JSON response to $search/camli/search/signerattrvalue
type SignerAttrValueResponse struct {
	Permanode blob.Ref `json:"permanode"`
	Meta      MetaMap  `json:"meta"`
}

func (sh *Handler) serveSignerAttrValue(rw http.ResponseWriter, req *http.Request) {
	defer httputil.RecoverJSON(rw, req)
	signer := httputil.MustGetBlobRef(req, "signer")
	attr := httputil.MustGet(req, "attr")
	value := httputil.MustGet(req, "value")

	pn, err := sh.index.PermanodeOfSignerAttrValue(signer, attr, value)
	if err != nil {
		httputil.ServeJSONError(rw, err)
		return
	}

	dr := sh.NewDescribeRequest()
	dr.Describe(pn, 2)
	metaMap, err := dr.metaMap()
	if err != nil {
		httputil.ServeJSONError(rw, err)
		return
	}

	httputil.ReturnJSON(rw, &SignerAttrValueResponse{
		Permanode: pn,
		Meta:      metaMap,
	})
}

// EdgesTo returns edges that reference req.RefTo.
// It filters out since-deleted permanode edges.
func (sh *Handler) EdgesTo(req *EdgesRequest) (*EdgesResponse, error) {
	toRef := req.ToRef
	toRefStr := toRef.String()
	var edgeItems []*EdgeItem

	edges, err := sh.index.EdgesTo(toRef, nil)
	if err != nil {
		panic(err)
	}

	type edgeOrError struct {
		edge *EdgeItem // or nil
		err  error
	}
	resc := make(chan edgeOrError)
	verify := func(edge *camtypes.Edge) {
		db, err := sh.NewDescribeRequest().DescribeSync(edge.From)
		if err != nil {
			resc <- edgeOrError{err: err}
			return
		}
		found := false
		if db.Permanode != nil {
			for attr, vv := range db.Permanode.Attr {
				if index.IsBlobReferenceAttribute(attr) {
					for _, v := range vv {
						if v == toRefStr {
							found = true
						}
					}
				}
			}
		}
		var ei *EdgeItem
		if found {
			ei = &EdgeItem{
				From:     edge.From,
				FromType: "permanode",
			}
		}
		resc <- edgeOrError{edge: ei}
	}
	verifying := 0
	for _, edge := range edges {
		if edge.FromType == "permanode" {
			verifying++
			go verify(edge)
			continue
		}
		ei := &EdgeItem{
			From:     edge.From,
			FromType: edge.FromType,
		}
		edgeItems = append(edgeItems, ei)
	}
	for i := 0; i < verifying; i++ {
		res := <-resc
		if res.err != nil {
			return nil, res.err
		}
		if res.edge != nil {
			edgeItems = append(edgeItems, res.edge)
		}
	}

	return &EdgesResponse{
		ToRef:   toRef,
		EdgesTo: edgeItems,
	}, nil
}

// Unlike the index interface's EdgesTo method, the "edgesto" Handler
// here additionally filters out since-deleted permanode edges.
func (sh *Handler) serveEdgesTo(rw http.ResponseWriter, req *http.Request) {
	defer httputil.RecoverJSON(rw, req)
	var er EdgesRequest
	er.fromHTTP(req)
	res, err := sh.EdgesTo(&er)
	if err != nil {
		httputil.ServeJSONError(rw, err)
		return
	}
	httputil.ReturnJSON(rw, res)
}

func (sh *Handler) serveQuery(rw http.ResponseWriter, req *http.Request) {
	defer httputil.RecoverJSON(rw, req)

	var sq SearchQuery
	if err := sq.fromHTTP(req); err != nil {
		httputil.ServeJSONError(rw, err)
		return
	}

	sr, err := sh.Query(&sq)
	if err != nil {
		httputil.ServeJSONError(rw, err)
		return
	}

	httputil.ReturnJSON(rw, sr)
}

// GetSignerPaths returns paths with a target of req.Target.
func (sh *Handler) GetSignerPaths(req *SignerPathsRequest) (*SignerPathsResponse, error) {
	if !req.Signer.Valid() {
		return nil, errors.New("Error getting signer paths: nil signer.")
	}
	if !req.Target.Valid() {
		return nil, errors.New("Error getting signer paths: nil target.")
	}
	paths, err := sh.index.PathsOfSignerTarget(req.Signer, req.Target)
	if err != nil {
		return nil, fmt.Errorf("Error getting paths of %s: %v", req.Target.String(), err)
	}
	var jpaths []*SignerPathsItem
	for _, path := range paths {
		jpaths = append(jpaths, &SignerPathsItem{
			ClaimRef: path.Claim,
			BaseRef:  path.Base,
			Suffix:   path.Suffix,
		})
	}

	dr := sh.NewDescribeRequest()
	for _, path := range paths {
		dr.Describe(path.Base, 2)
	}
	metaMap, err := dr.metaMap()
	if err != nil {
		return nil, err
	}

	res := &SignerPathsResponse{
		Paths: jpaths,
		Meta:  metaMap,
	}
	return res, nil
}

func (sh *Handler) serveSignerPaths(rw http.ResponseWriter, req *http.Request) {
	defer httputil.RecoverJSON(rw, req)
	var sr SignerPathsRequest
	sr.fromHTTP(req)

	res, err := sh.GetSignerPaths(&sr)
	if err != nil {
		httputil.ServeJSONError(rw, err)
		return
	}
	httputil.ReturnJSON(rw, res)
}

const camliTypePrefix = "application/json; camliType="

func (d *DescribedBlob) setMIMEType(mime string) {
	if strings.HasPrefix(mime, camliTypePrefix) {
		d.CamliType = strings.TrimPrefix(mime, camliTypePrefix)
	}
}
