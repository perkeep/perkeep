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

package immich

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"go4.org/jsonconfig"
	"go4.org/syncutil"
	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/jsonsign/signhandler"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/server"
	"perkeep.org/pkg/sorted"
)

var immichResizeSem = syncutil.NewSem(256 << 20)

// The existing image resizer buffers header probing before acquiring its pixel
// semaphore. Bound both input size and concurrent probes for untrusted uploads.
var immichThumbnailSem = syncutil.NewSem(128 << 20)

// Handler implements the upload-oriented subset of the Immich mobile
// protocol. By default it requires normal Perkeep authentication; optional
// separate credentials grant access only to this handler. Asset records live
// on photo permanodes, and account/device state lives on an account permanode.
// The metadata view is memory-only and rebuilt from those native objects.
type Handler struct {
	storage        blobserver.Storage
	signer         *schema.Signer
	search         *search.Handler
	metadata       sorted.KeyValue
	email          string
	password       string
	perkeepAuth    bool // Require native auth on every request; ignore login credentials.
	prefix         string
	maxUploadBytes int64
	uid            string

	// Serialize publication of assets and sequence allocation. File bodies are
	// read and written outside this lock; concurrent retries converge here.
	mu sync.Mutex

	// ACK progress is disposable and checkpointed independently of photo
	// publication. Access, timers and shutdown are serialized by mu.
	acks *immichAckState
}

type immichSession struct {
	ID         string
	CreatedAt  time.Time
	DeviceType string
	DeviceOS   string
	// A password or authentication mode change invalidates existing sessions.
	Credential string
}

type immichAsset struct {
	ID               string
	Checksum         string   // hex SHA-1 of original bytes, for Immich only
	WholeRef         blob.Ref // Perkeep hash of the original bytes, independent of file schema metadata.
	FileRef          blob.Ref
	SidecarRef       blob.Ref
	PermanodeRef     blob.Ref
	Name             string
	Type             string
	CreatedAt        time.Time
	FileCreatedAt    time.Time
	FileModifiedAt   time.Time
	Size             int64
	Duration         int64 // milliseconds
	IsFavorite       bool
	Visibility       string
	LivePhotoVideoID string
	Metadata         json.RawMessage
	Sequence         uint64
}

func init() {
	blobserver.RegisterHandlerConstructor("immich", newFromConfig)
}

func newFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (http.Handler, error) {
	blobRoot := conf.RequiredString("blobRoot")
	signRoot := conf.RequiredString("jsonSignRoot")
	searchRoot := conf.RequiredString("searchRoot")
	h := &Handler{
		email:          conf.OptionalString("email", ""),
		password:       conf.OptionalString("password", ""),
		prefix:         ld.MyPrefix(),
		maxUploadBytes: conf.OptionalInt64("maxUploadBytes", 32<<30),
	}
	_, hasLegacy := conf["legacyMetadata"]
	_, hasOldMetadata := conf["metadata"]
	legacyConf := conf.OptionalObject("legacyMetadata")
	oldMetaConf := conf.OptionalObject("metadata")
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	if hasLegacy && hasOldMetadata {
		return nil, errors.New("immich accepts only one of legacyMetadata and the deprecated metadata alias")
	}
	if !hasLegacy {
		legacyConf = oldMetaConf
	}
	if !hasLegacy && !hasOldMetadata {
		legacyConf = nil
	}
	if (h.email == "") != (h.password == "") {
		return nil, errors.New("immich requires both email and password, or neither to use Perkeep authentication")
	}
	if h.maxUploadBytes <= 0 {
		return nil, errors.New("immich requires a positive upload limit")
	}
	h.perkeepAuth = h.email == ""
	if h.perkeepAuth {
		// The adapter has one stable owner, regardless of the app's login fields
		// or which principals the server's authentication policy authorizes.
		h.email = "owner@perkeep.invalid"
	}
	var err error
	h.storage, err = ld.GetStorage(blobRoot)
	if err != nil {
		return nil, fmt.Errorf("immich blobRoot: %w", err)
	}
	sh, err := ld.GetHandler(signRoot)
	if err != nil {
		return nil, fmt.Errorf("immich jsonSignRoot: %w", err)
	}
	sigh, ok := sh.(*signhandler.Handler)
	if !ok || sigh.Signer() == nil {
		return nil, errors.New("immich jsonSignRoot must refer to a signing handler")
	}
	h.signer = sigh.Signer()
	searchHandler, err := ld.GetHandler(searchRoot)
	if err != nil {
		return nil, fmt.Errorf("immich searchRoot: %w", err)
	}
	h.search, ok = searchHandler.(*search.Handler)
	if !ok || h.search.Owner() != sigh.Discovery("").PublicKeyBlobRef {
		return nil, errors.New("immich searchRoot must refer to a search handler with the signing identity as its owner")
	}
	// State initialization publishes signed claims during handler setup,
	// before perkeepd's usual post-InstallHandlers public-key upload.
	if err := sigh.UploadPublicKey(context.Background()); err != nil {
		return nil, fmt.Errorf("immich publishing signing key: %w", err)
	}
	if legacyConf != nil {
		h.metadata, err = newImmichStateForMigration(h)
	} else {
		h.metadata, err = newImmichState(h)
	}
	if err != nil {
		return nil, fmt.Errorf("immich native state: %w", err)
	}
	if legacyConf != nil {
		if err := h.importLegacyImmich(legacyConf); err != nil {
			h.metadata.Close()
			return nil, fmt.Errorf("immich legacy metadata import: %w", err)
		}
	}
	if err := h.initIdentity(); err != nil {
		h.metadata.Close()
		return nil, err
	}
	return h, nil
}

func (h *Handler) initIdentity() error {
	id, err := h.metadata.Get("user|id")
	if errors.Is(err, sorted.ErrNotFound) {
		id, err = immichUUID()
		if err == nil {
			err = h.metadata.Set("user|id", id)
		}
	}
	if err != nil {
		return fmt.Errorf("immich identity: %w", err)
	}
	if !immichValidUUID(id) {
		return errors.New("immich metadata has an invalid user identity")
	}
	h.uid = id
	return nil
}

func (h *Handler) userID() string { return h.uid }

func (h *Handler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var err error
	if h.acks != nil {
		err = h.acks.closeLocked()
	}
	// A failed best-effort checkpoint must not prevent storage cleanup.
	return errors.Join(err, h.metadata.Close())
}

func immichUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// immichAssetID derives an asset identity from the signing public-key blobref
// and the original's whole-file digest, including both refs' hash algorithms.
// Neither the file schema's metadata nor the adapter's login/account state is
// part of the identity. Keep this versioned derivation stable across restarts.
//
// Immich requires UUIDv4 syntax. This is a deterministic UUIDv4-shaped ID, not
// a random UUIDv4 or an authentication token; its remaining 122 bits come from
// a domain-separated SHA-256 digest.
func immichAssetID(owner, wholeRef blob.Ref) string {
	b := sha256.Sum256([]byte("perkeep.org/immich/asset-id/v1\x00" + owner.String() + "\x00" + wholeRef.String()))
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func immichValidUUID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	b, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil && len(b) == 16 && b[6]>>4 == 4 && b[8]>>6 == 2
}

func immichJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func immichError(w http.ResponseWriter, status int, message string) {
	immichJSON(w, status, map[string]any{
		"statusCode": status, "message": message, "error": http.StatusText(status),
	})
}

func immichDecode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	if err := d.Decode(dst); err != nil {
		immichError(w, http.StatusBadRequest, "Invalid JSON request")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		immichError(w, http.StatusBadRequest, "Expected one JSON value")
		return false
	}
	return true
}

func (h *Handler) credentialHash() string {
	if h.perkeepAuth {
		// Distinct from the hex digest used for standalone credentials. Native
		// authentication is checked anew on every request, not cached here.
		return "perkeep:" + h.userID()
	}
	sum := sha256.Sum256([]byte(h.email + "\x00" + h.password))
	return hex.EncodeToString(sum[:])
}

func (h *Handler) sessionKey(r *http.Request) string {
	token := r.Header.Get("x-immich-user-token")
	if token == "" {
		token = r.Header.Get("x-immich-session-token")
	}
	if token == "" {
		scheme, value, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if ok && strings.EqualFold(scheme, "Bearer") {
			token = value
		}
	}
	if token == "" {
		if c, err := r.Cookie("immich_access_token"); err == nil {
			token = c.Value
		}
	}
	if token == "" || len(token) > 512 {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return "session|" + hex.EncodeToString(sum[:])
}

func (h *Handler) session(r *http.Request) (*immichSession, error) {
	key := h.sessionKey(r)
	if key == "" {
		return nil, sorted.ErrNotFound
	}
	data, err := h.metadata.Get(key)
	if err != nil {
		return nil, err
	}
	var s immichSession
	if err := json.Unmarshal([]byte(data), &s); err != nil {
		return nil, err
	}
	if s.Credential != h.credentialHash() {
		return nil, sorted.ErrNotFound
	}
	return &s, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.perkeepAuth && !auth.Allowed(r, auth.OpAll) {
		auth.SendUnauthorized(w, r)
		return
	}
	p := httputil.PathSuffix(r)
	if p == "" {
		p = strings.TrimPrefix(r.URL.Path, h.prefix)
	}
	p = strings.Trim(p, "/")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	switch {
	case r.Method == "POST" && p == "auth/login":
		h.serveLogin(w, r)
		return
	case r.Method == "GET" && (p == "server/ping" || p == "server/version" ||
		p == "server/features" || p == "server/config" || p == "server/about"):
		h.serveMobile(w, r, p)
		return
	}
	if _, err := h.session(r); err != nil {
		if !errors.Is(err, sorted.ErrNotFound) {
			h.fail(w, "reading session", err)
		} else {
			immichError(w, http.StatusUnauthorized, "Invalid access token")
		}
		return
	}
	switch {
	case r.Method == "POST" && p == "auth/validateToken":
		immichJSON(w, http.StatusOK, map[string]any{"authStatus": true})
	case r.Method == "POST" && p == "auth/logout":
		if err := h.metadata.Delete(h.sessionKey(r)); err != nil {
			h.fail(w, "logging out", err)
			return
		}
		h.setCookies(w, r, "", -1)
		immichJSON(w, http.StatusCreated, map[string]any{"successful": true, "redirectUri": ""})
	case r.Method == "POST" && p == "assets":
		h.serveAssetUpload(w, r)
	case r.Method == "POST" && p == "assets/bulk-upload-check":
		h.serveUploadCheck(w, r)
	case r.Method == "GET" && strings.HasPrefix(p, "assets/"):
		h.serveAsset(w, r, strings.TrimPrefix(p, "assets/"))
	default:
		if !h.serveMobile(w, r, p) {
			// In particular, never report success for unsupported deletion,
			// editing, sharing or album mutations.
			immichError(w, http.StatusNotFound, "Unsupported Immich endpoint")
		}
	}
}

func (h *Handler) fail(w http.ResponseWriter, operation string, err error) {
	log.Printf("immich: %s: %v", operation, err)
	immichError(w, http.StatusInternalServerError, "Perkeep storage error")
}

func (h *Handler) serveLogin(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password string }
	if !immichDecode(w, r, &in) {
		return
	}
	if !h.perkeepAuth {
		email := sha256.Sum256([]byte(in.Email))
		wantEmail := sha256.Sum256([]byte(h.email))
		password := sha256.Sum256([]byte(in.Password))
		wantPassword := sha256.Sum256([]byte(h.password))
		if subtle.ConstantTimeCompare(email[:], wantEmail[:])&subtle.ConstantTimeCompare(password[:], wantPassword[:]) != 1 {
			immichError(w, http.StatusUnauthorized, "Incorrect email or password")
			return
		}
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		h.fail(w, "creating access token", err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(secret[:])
	id, err := immichUUID()
	if err != nil {
		h.fail(w, "creating session", err)
		return
	}
	s := immichSession{
		ID: id, CreatedAt: time.Now().UTC(), Credential: h.credentialHash(),
		DeviceType: r.Header.Get("x-immich-device-type"), DeviceOS: r.Header.Get("x-immich-device-os"),
	}
	if s.DeviceType == "" {
		s.DeviceType = r.Header.Get("deviceType")
	}
	if len(s.DeviceType) > 256 || len(s.DeviceOS) > 256 {
		immichError(w, http.StatusBadRequest, "Device metadata is too large")
		return
	}
	data, _ := json.Marshal(s)
	sum := sha256.Sum256([]byte(token))
	if err := h.metadata.Set("session|"+hex.EncodeToString(sum[:]), string(data)); err != nil {
		h.fail(w, "saving session", err)
		return
	}
	// Native iOS stores these in an app-group cookie store shared with its
	// background workers. Session-only cookies are not shared/persisted there.
	h.setCookies(w, r, token, 400*24*60*60)
	immichJSON(w, http.StatusCreated, map[string]any{
		"accessToken": token, "userId": h.userID(), "userEmail": h.email,
		"name": strings.SplitN(h.email, "@", 2)[0], "profileImagePath": "", "isAdmin": false,
		"shouldChangePassword": false, "isOnboarded": true,
	})
}

func (h *Handler) setCookies(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	for _, c := range []http.Cookie{
		{Name: "immich_access_token", Value: token, HttpOnly: true},
		{Name: "immich_is_authenticated", Value: "true"},
		{Name: "immich_auth_type", Value: "password"},
	} {
		// Native mobile upload workers use the login cookies independently of
		// the JSON accessToken. Restrict browser cookies to this API mount.
		c.Path = h.prefix
		if c.Path == "" {
			c.Path = "/"
		}
		c.Secure = r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
		c.SameSite = http.SameSiteLaxMode
		c.MaxAge = maxAge
		if maxAge > 0 {
			c.Expires = time.Now().UTC().Add(time.Duration(maxAge) * time.Second)
		}
		if maxAge < 0 {
			c.Value = ""
			c.Expires = time.Unix(1, 0).UTC()
		}
		http.SetCookie(w, &c)
	}
}

// immichChecksum normalizes the two checksum encodings accepted by Immich.
// These SHA-1 values are never used as Perkeep blob identities.
func immichChecksum(s string) (string, error) {
	var b []byte
	var err error
	if len(s) == 40 {
		b, err = hex.DecodeString(s)
	} else {
		b, err = base64.StdEncoding.DecodeString(s)
	}
	if err != nil || len(b) != sha1.Size {
		return "", errors.New("expected a hex or base64 SHA-1 checksum")
	}
	return hex.EncodeToString(b), nil
}

func (h *Handler) asset(id string) (*immichAsset, error) {
	if !immichValidUUID(id) {
		return nil, sorted.ErrNotFound
	}
	data, err := h.metadata.Get("asset|" + id)
	if err != nil {
		return nil, err
	}
	var a immichAsset
	if err := json.Unmarshal([]byte(data), &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (h *Handler) serveUploadCheck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Assets []struct{ ID, Checksum string }
	}
	if !immichDecode(w, r, &in) {
		return
	}
	results := make([]map[string]any, 0, len(in.Assets))
	for _, a := range in.Assets {
		checksum, err := immichChecksum(a.Checksum)
		if err != nil {
			immichError(w, http.StatusBadRequest, "Invalid checksum")
			return
		}
		id, err := h.metadata.Get("checksum|" + checksum)
		switch {
		case err == nil:
			results = append(results, map[string]any{"id": a.ID, "action": "reject", "reason": "duplicate", "assetId": id})
		case errors.Is(err, sorted.ErrNotFound):
			results = append(results, map[string]any{"id": a.ID, "action": "accept"})
		default:
			h.fail(w, "checking upload", err)
			return
		}
	}
	immichJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (h *Handler) serveAssetUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadBytes)
	// Parse before ingestion: mobile clients need not put timestamps before
	// file parts. Large files spill to disk, not into the Go heap.
	err := r.ParseMultipartForm(8 << 20)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		status := http.StatusBadRequest
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			status = http.StatusRequestEntityTooLarge
		}
		immichError(w, status, "Invalid or oversized multipart upload")
		return
	}
	form := r.MultipartForm
	if form == nil || len(form.File["assetData"]) != 1 || len(form.File["sidecarData"]) > 1 {
		immichError(w, http.StatusBadRequest, "Exactly one assetData file is required")
		return
	}
	for key := range form.File {
		if key != "assetData" && key != "sidecarData" {
			immichError(w, http.StatusBadRequest, "Unsupported file part")
			return
		}
	}
	for key, values := range form.Value {
		if len(values) != 1 || len(values[0]) > 32<<10 {
			immichError(w, http.StatusBadRequest, "Invalid metadata field "+key)
			return
		}
	}
	created, err := time.Parse(time.RFC3339Nano, r.FormValue("fileCreatedAt"))
	if err != nil {
		immichError(w, http.StatusBadRequest, "Invalid fileCreatedAt")
		return
	}
	modified, err := time.Parse(time.RFC3339Nano, r.FormValue("fileModifiedAt"))
	if err != nil {
		immichError(w, http.StatusBadRequest, "Invalid fileModifiedAt")
		return
	}
	a := immichAsset{
		CreatedAt: time.Now().UTC(), FileCreatedAt: created, FileModifiedAt: modified,
		Visibility: r.FormValue("visibility"), LivePhotoVideoID: r.FormValue("livePhotoVideoId"),
	}
	if a.Visibility == "" {
		a.Visibility = "timeline"
	}
	if a.Visibility != "timeline" && a.Visibility != "hidden" && a.Visibility != "archive" {
		immichError(w, http.StatusBadRequest, "Unsupported visibility")
		return
	}
	if v := r.FormValue("isFavorite"); v != "" {
		a.IsFavorite, err = strconv.ParseBool(v)
		if err != nil {
			immichError(w, http.StatusBadRequest, "Invalid isFavorite")
			return
		}
	}
	if v := r.FormValue("duration"); v != "" {
		a.Duration, err = strconv.ParseInt(v, 10, 64)
		if err != nil || a.Duration < 0 || a.Duration > 1<<31-1 {
			immichError(w, http.StatusBadRequest, "Invalid duration")
			return
		}
	}
	if v := r.FormValue("metadata"); v != "" {
		var items []struct {
			Key   string         `json:"key"`
			Value map[string]any `json:"value"`
		}
		if err := json.Unmarshal([]byte(v), &items); err != nil || items == nil {
			immichError(w, http.StatusBadRequest, "Invalid asset metadata")
			return
		}
		seen := make(map[string]bool)
		for _, item := range items {
			if item.Key == "" || len(item.Key) > 128 || item.Value == nil || seen[item.Key] {
				immichError(w, http.StatusBadRequest, "Invalid asset metadata item")
				return
			}
			seen[item.Key] = true
		}
		a.Metadata = json.RawMessage(v)
	}
	if a.LivePhotoVideoID != "" {
		video, err := h.asset(a.LivePhotoVideoID)
		if errors.Is(err, sorted.ErrNotFound) || err == nil && video.Type != "VIDEO" {
			immichError(w, http.StatusBadRequest, "livePhotoVideoId must refer to an uploaded video")
			return
		}
		if err != nil {
			h.fail(w, "finding live photo video", err)
			return
		}
	}
	file := form.File["assetData"][0]
	a.Name = file.Filename
	if v := r.FormValue("filename"); v != "" {
		a.Name = v
	}
	a.Name = path.Base(strings.ReplaceAll(a.Name, "\\", "/"))
	if a.Name == "." || a.Name == ".." || a.Name == "/" || a.Name == "" || len(a.Name) > 1024 ||
		strings.ContainsAny(a.Name, "\x00\r\n") {
		immichError(w, http.StatusBadRequest, "Invalid filename")
		return
	}
	a.Size = file.Size
	if a.Size == 0 {
		immichError(w, http.StatusBadRequest, "Empty assetData")
		return
	}
	a.Type = "IMAGE"
	if strings.HasPrefix(mime.TypeByExtension(strings.ToLower(path.Ext(a.Name))), "video/") {
		a.Type = "VIDEO"
	}
	switch strings.ToLower(path.Ext(a.Name)) {
	case ".mov", ".mp4", ".m4v", ".avi", ".mkv", ".webm", ".mts", ".m2ts", ".3gp":
		a.Type = "VIDEO"
	}
	wantChecksum := r.Header.Get("x-immich-checksum")
	if wantChecksum != "" {
		wantChecksum, err = immichChecksum(wantChecksum)
		if err != nil {
			immichError(w, http.StatusBadRequest, "Invalid checksum header")
			return
		}
	}
	// Bound the encoded record before ingesting any blobs. HTML escaping in
	// encoding/json can greatly expand otherwise-small metadata form values.
	// Reserve the maximum counter width and normal-length generated identities.
	probe := a
	probe.ID = "00000000-0000-4000-8000-000000000000"
	probe.Checksum = strings.Repeat("0", 40)
	probe.Sequence = ^uint64(0)
	probe.FileRef = blob.RefFromString("")
	probe.WholeRef = probe.FileRef
	probe.PermanodeRef = probe.FileRef
	if len(form.File["sidecarData"]) != 0 {
		probe.SidecarRef = probe.FileRef
	}
	encoded, err := json.Marshal(probe)
	if err != nil || sorted.CheckSizes("asset|"+probe.ID, string(encoded)) != nil {
		immichError(w, http.StatusBadRequest, "Asset metadata is too large")
		return
	}
	src, err := file.Open()
	if err != nil {
		h.fail(w, "opening upload", err)
		return
	}
	// SHA-1 is only an Immich interoperability checksum. Perkeep uses its
	// normal blob hash for chunks, file schemas and the planned permanode.
	checksumHash, contentHash := sha1.New(), blob.NewHash()
	a.FileRef, err = schema.WriteFileFromReaderWithModTime(r.Context(), h.storage, a.Name, modified,
		io.TeeReader(src, io.MultiWriter(checksumHash, contentHash)))
	src.Close()
	if err != nil {
		h.fail(w, "storing original", err)
		return
	}
	a.Checksum = hex.EncodeToString(checksumHash.Sum(nil))
	a.WholeRef = blob.RefFromHash(contentHash)
	if wantChecksum != "" && wantChecksum != a.Checksum {
		immichError(w, http.StatusBadRequest, "Checksum does not match uploaded bytes")
		return
	}
	if files := form.File["sidecarData"]; len(files) == 1 {
		sidecar, err := files[0].Open()
		if err != nil {
			h.fail(w, "opening sidecar", err)
			return
		}
		a.SidecarRef, err = schema.WriteFileFromReaderWithModTime(r.Context(), h.storage, a.Name+".xmp", modified, sidecar)
		sidecar.Close()
		if err != nil {
			h.fail(w, "storing sidecar", err)
			return
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	existing, err := h.existingAsset(r.Context(), a.WholeRef, a.Checksum)
	if err != nil {
		h.fail(w, "checking duplicate", err)
		return
	}
	if existing != nil {
		// Receipts recovered from native claims must not turn a stale index
		// or missing primary data into a successful duplicate response.
		if err := h.verifyPermanode(r.Context(), existing.PermanodeRef, existing.FileRef, existing.WholeRef); err != nil {
			h.fail(w, "verifying existing original", err)
			return
		}
		// No mutation on duplicate uploads. In particular a retry cannot
		// accidentally unhide a Live Photo video or change its association.
		immichJSON(w, http.StatusOK, map[string]any{"id": existing.ID, "status": "duplicate"})
		return
	}
	a.ID = immichAssetID(h.search.Owner(), a.WholeRef)
	seq, err := h.metadata.Get("sequence")
	if err == nil {
		a.Sequence, err = strconv.ParseUint(seq, 10, 64)
	}
	if err != nil && !errors.Is(err, sorted.ErrNotFound) {
		h.fail(w, "reading sequence", err)
		return
	}
	a.Sequence++
	if a.Sequence == 0 {
		h.fail(w, "allocating sequence", errors.New("sequence exhausted"))
		return
	}
	err = nil
	var usage int64
	if v, e := h.metadata.Get("usage"); e == nil {
		usage, err = strconv.ParseInt(v, 10, 64)
	} else if !errors.Is(e, sorted.ErrNotFound) {
		err = e
	}
	if err != nil || usage < 0 || a.Size > (1<<63-1)-usage {
		if err == nil {
			err = errors.New("invalid or overflowing usage counter")
		}
		h.fail(w, "reading usage", err)
		return
	}
	// The adapter's materialized view is not the authority for uniqueness.
	// Find any existing owner permanode with these original bytes, including
	// files uploaded outside Immich or before its metadata was lost.
	a.PermanodeRef, err = h.findPermanode(r.Context(), a.WholeRef)
	if err != nil {
		h.fail(w, "finding existing permanode", err)
		return
	}
	reused := a.PermanodeRef.Valid()
	var signedPN string
	if !reused {
		// Match pk-put --filenodes: both the key and signing time must be
		// stable, even if timestamps change or publication/cache commit fails.
		pn := schema.NewHashPlannedPermanode(contentHash)
		signedPN, err = pn.SignAt(r.Context(), h.signer, time.Unix(0, 0))
		if err != nil {
			h.fail(w, "signing permanode", err)
			return
		}
		a.PermanodeRef = blob.RefFromString(signedPN)
		// A planned filenode can have been deliberately repointed or cleared
		// by its owner. Its content key does not authorize overwriting it.
		ix := h.search.Index()
		ix.RLock()
		claims, lookupErr := ix.AppendClaims(r.Context(), nil, a.PermanodeRef, h.signer.KeyIDLong(), "")
		ix.RUnlock()
		if lookupErr != nil {
			h.fail(w, "checking planned permanode", lookupErr)
			return
		}
		if len(claims) != 0 {
			immichError(w, http.StatusConflict, "Content-planned permanode already has attributes; refusing to overwrite it")
			return
		}
	}
	data, err := json.Marshal(a)
	if err != nil {
		h.fail(w, "encoding asset", err)
		return
	}
	// Some sorted backends silently omit an oversized batch mutation.
	// Check the actual record as well before publishing claims or its checksum.
	if err := sorted.CheckSizes("asset|"+a.ID, string(data)); err != nil {
		immichError(w, http.StatusBadRequest, "Asset metadata is too large")
		return
	}
	// Do not overwrite an existing object's title, dates, camliContent, tags,
	// or associations merely because the phone uploaded the same bytes.
	if !reused {
		_, err = blobserver.Receive(r.Context(), h.storage, a.PermanodeRef, strings.NewReader(signedPN))
		attrs := []struct{ key, value string }{
			{"camliContent", a.FileRef.String()}, {"title", a.Name},
			{"dateCreated", created.Format(time.RFC3339Nano)}, {"immichChecksum", a.Checksum},
		}
		if a.SidecarRef.Valid() {
			attrs = append(attrs, struct{ key, value string }{"immichSidecar", a.SidecarRef.String()})
		}
		if a.LivePhotoVideoID != "" {
			video, e := h.asset(a.LivePhotoVideoID)
			if e != nil {
				err = e
			} else {
				attrs = append(attrs, struct{ key, value string }{"immichLivePhotoVideo", video.FileRef.String()})
			}
		}
		if len(a.Metadata) != 0 {
			attrs = append(attrs, struct{ key, value string }{"immichMetadata", string(a.Metadata)})
		}
		if err == nil {
			claims := make([]*schema.Builder, 0, len(attrs))
			for _, attr := range attrs {
				claims = append(claims, schema.NewSetAttributeClaim(a.PermanodeRef, attr.key, attr.value))
			}
			// Publish attributes atomically: a failed retry must not discover
			// camliContent on a half-initialized object and skip its metadata.
			var claim *schema.Builder
			claim, err = schema.NewMultiClaim(claims...)
			if err == nil {
				_, err = h.storeSigned(r.Context(), claim, created)
			}
		}
	}
	if err != nil {
		h.fail(w, "publishing permanode", err)
		return
	}
	b := h.metadata.BeginBatch()
	b.Set("asset|"+a.ID, string(data))
	b.Set("whole|"+a.WholeRef.String(), a.ID)
	b.Set("checksum|"+a.Checksum, a.ID)
	b.Set("sequence", strconv.FormatUint(a.Sequence, 10))
	b.Set("usage", strconv.FormatInt(usage+a.Size, 10))
	// Sequence keys make incremental streams proportional to new uploads,
	// rather than scanning the entire asset collection on every backup.
	b.Set(fmt.Sprintf("change|%020d", a.Sequence), a.ID)
	if err := h.metadata.CommitBatch(b); err != nil {
		h.fail(w, "committing asset", err)
		return
	}
	if reused {
		immichJSON(w, http.StatusOK, map[string]any{"id": a.ID, "status": "duplicate"})
	} else {
		immichJSON(w, http.StatusCreated, map[string]any{"id": a.ID, "status": "created"})
	}
}

func (h *Handler) storeSigned(ctx context.Context, b *schema.Builder, at time.Time) (blob.Ref, error) {
	text, err := b.SignAt(ctx, h.signer, at)
	if err != nil {
		return blob.Ref{}, err
	}
	ref := blob.RefFromString(text)
	_, err = blobserver.Receive(ctx, h.storage, ref, strings.NewReader(text))
	return ref, err
}

func (h *Handler) serveAsset(w http.ResponseWriter, r *http.Request, suffix string) {
	id, action, _ := strings.Cut(suffix, "/")
	a, err := h.asset(id)
	if errors.Is(err, sorted.ErrNotFound) {
		immichError(w, http.StatusNotFound, "Asset not found")
		return
	}
	if err != nil {
		h.fail(w, "reading asset", err)
		return
	}
	switch action {
	case "":
		immichJSON(w, http.StatusOK, h.assetResponse(a))
	case "original", "video/playback":
		if action == "video/playback" && a.Type != "VIDEO" {
			immichError(w, http.StatusNotFound, "Asset is not a video")
			return
		}
		fr, err := schema.NewFileReader(r.Context(), h.storage, a.FileRef)
		if err != nil {
			h.fail(w, "reading original", err)
			return
		}
		defer fr.Close()
		w.Header().Set("Cache-Control", "private, max-age=3600")
		// Uploaded files are untrusted. Never make an HTML/SVG original an
		// active document on the same origin as Perkeep's authenticated UI.
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
		if action == "video/playback" {
			ct := mime.TypeByExtension(strings.ToLower(path.Ext(a.Name)))
			if strings.HasPrefix(ct, "video/") {
				w.Header().Set("Content-Type", ct)
			}
		}
		http.ServeContent(w, r, a.Name, a.FileModifiedAt, fr)
	case "thumbnail":
		if a.Type == "VIDEO" {
			immichError(w, http.StatusNotFound, "Video thumbnails are not supported")
			return
		}
		if a.Size > 64<<20 {
			immichError(w, http.StatusNotFound, "Asset is too large for thumbnail generation")
			return
		}
		if err := immichThumbnailSem.Acquire(64 << 20); err != nil {
			immichError(w, http.StatusServiceUnavailable, "Thumbnail generation unavailable")
			return
		}
		defer immichThumbnailSem.Release(64 << 20)
		fr, err := schema.NewFileReader(r.Context(), h.storage, a.FileRef)
		if err != nil {
			h.fail(w, "probing thumbnail", err)
			return
		}
		cfg, _, err := image.DecodeConfig(io.LimitReader(fr, 16<<20))
		fr.Close()
		// Use division rather than multiplying arbitrary image dimensions,
		// which could overflow before the existing resizer's pixel admission.
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 ||
			int64(cfg.Width) > (256<<20)/3/int64(cfg.Height) {
			immichError(w, http.StatusNotFound, "Thumbnail unavailable")
			return
		}
		ih := &server.ImageHandler{Fetcher: h.storage, MaxWidth: 720, MaxHeight: 720, ResizeSem: immichResizeSem}
		if r.URL.Query().Get("size") == "preview" || r.URL.Query().Get("size") == "fullsize" {
			ih.MaxWidth, ih.MaxHeight = 1920, 1920
		}
		// Resize the stored file directly, without the HTTP thumbnail handler's
		// search-dependent lookup or any derivative cache publication.
		data, format, err := ih.ScaleImage(r.Context(), a.FileRef)
		if err != nil {
			immichError(w, http.StatusNotFound, "Thumbnail unavailable")
			return
		}
		w.Header().Set("Content-Type", "image/"+format)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		http.ServeContent(w, r, a.Name, a.FileModifiedAt, bytes.NewReader(data))
	default:
		immichError(w, http.StatusNotFound, "Unsupported asset endpoint")
	}
}
