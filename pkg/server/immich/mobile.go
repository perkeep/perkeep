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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"perkeep.org/pkg/sorted"
)

// These DTOs implement the mobile subset of the Immich 3.3.1 protocol. Version
// negotiation selects the V2 asset DTO, whose duration is an integer in ms.
// Features not provided by Perkeep are deliberately advertised as unavailable.
const immichMobileVersion = "3.3.1"

// serveMobile handles paths relative to /api. The caller must authenticate
// non-public routes before calling it. It never handles upload or authentication.
func (h *Handler) serveMobile(w http.ResponseWriter, r *http.Request, path string) bool {
	path = "/" + strings.Trim(path, "/")
	switch path {
	case "/server/ping":
		if immichMobileMethod(w, r, http.MethodGet) {
			immichJSON(w, 200, map[string]any{"res": "pong"})
		}
	case "/server/version":
		if immichMobileMethod(w, r, http.MethodGet) {
			immichJSON(w, 200, map[string]any{"major": 3, "minor": 3, "patch": 1, "prerelease": nil})
		}
	case "/server/features":
		if immichMobileMethod(w, r, http.MethodGet) {
			immichJSON(w, 200, immichMobileFeatures())
		}
	case "/server/config":
		if immichMobileMethod(w, r, http.MethodGet) {
			immichJSON(w, 200, immichMobileConfig())
		}
	case "/server/about":
		if immichMobileMethod(w, r, http.MethodGet) {
			immichJSON(w, 200, map[string]any{"version": immichMobileVersion, "versionUrl": "", "licensed": false, "repository": "perkeep", "repositoryUrl": "https://perkeep.org/"})
		}
	case "/server/version-history":
		if immichMobileMethod(w, r, http.MethodGet) {
			immichJSON(w, 200, []any{})
		}
	case "/users", "/users/me":
		if immichMobileMethod(w, r, http.MethodGet) {
			user, err := h.mobileUser()
			if err != nil {
				immichError(w, 500, "Cannot read user")
				break
			}
			if path == "/users" {
				immichJSON(w, 200, []any{user})
			} else {
				immichJSON(w, 200, user)
			}
		}
	case "/users/me/preferences":
		h.serveMobilePreferences(w, r)
	case "/users/me/onboarding":
		if immichMobileMethod(w, r, http.MethodGet) {
			immichJSON(w, 200, map[string]any{"isOnboarded": true})
		}
	case "/sync/stream":
		if immichMobileMethod(w, r, http.MethodPost) {
			h.serveMobileSync(w, r)
		}
	case "/sync/ack":
		h.serveMobileAck(w, r)
	default:
		if strings.HasPrefix(path, "/users/") && strings.TrimPrefix(path, "/users/") == h.userID() {
			if immichMobileMethod(w, r, http.MethodGet) {
				user, err := h.mobileUser()
				if err != nil {
					immichError(w, 500, "Cannot read user")
				} else {
					immichJSON(w, 200, user)
				}
			}
		} else {
			return false
		}
	}
	return true
}

func immichMobileMethod(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, method := range methods {
		if r.Method == method {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(methods, ", "))
	immichError(w, http.StatusMethodNotAllowed, "Method not allowed")
	return false
}

func immichMobileFeatures() map[string]any {
	m := make(map[string]any)
	for _, name := range strings.Fields("smartSearch duplicateDetection configFile facialRecognition map trash reverseGeocoding importFaces oauth oauthAutoLaunch search email ocr realtimeTranscoding") {
		m[name] = false
	}
	m["passwordLogin"], m["sidecar"] = true, true
	return m
}

func immichMobileConfig() map[string]any {
	return map[string]any{
		"oauthButtonText": "OAuth", "oauthAccountManagementUrl": "", "loginPageMessage": "Perkeep mobile backup",
		"trashDays": 30, "userDeleteDelay": 7, "isInitialized": true, "isOnboarded": true,
		"externalDomain": "", "publicUsers": false, "mapDarkStyleUrl": "", "mapLightStyleUrl": "",
		"maintenanceMode": false, "minFaces": 3,
	}
}

func (h *Handler) mobileSequence() (uint64, error) {
	value, err := h.metadata.Get("sequence")
	if errors.Is(err, sorted.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(value, 10, 64)
}

// mobileChanges reads only the published suffix of the upload log. Completion
// cursors do not participate in determining "after": each requested entity has
// its own independently acknowledged position.
func (h *Handler) mobileChanges(after, sequence uint64, visit func(*immichAsset) error) error {
	if after >= sequence {
		return nil
	}
	start := fmt.Sprintf("change|%020d", after+1)
	end := fmt.Sprintf("change|%020d\x00", sequence)
	it := h.metadata.Find(start, end)
	defer it.Close()
	for it.Next() {
		a, err := h.asset(it.Value())
		if err != nil {
			return err
		}
		if a.Sequence <= sequence {
			if err := visit(a); err != nil {
				return err
			}
		}
	}
	return it.Close()
}

type immichMobileProfile struct {
	CreatedAt time.Time
}

func (h *Handler) mobileUser() (map[string]any, error) {
	id := h.userID() // userID may itself lock h.mu.
	h.mu.Lock()
	value, err := h.metadata.Get("user|mobileProfile")
	var profile immichMobileProfile
	if errors.Is(err, sorted.ErrNotFound) {
		profile.CreatedAt = time.Now().UTC()
		data, _ := json.Marshal(profile)
		err = h.metadata.Set("user|mobileProfile", string(data))
	} else if err == nil {
		err = json.Unmarshal([]byte(value), &profile)
	}
	var usage int64
	if err == nil {
		var stored string
		stored, err = h.metadata.Get("usage")
		if errors.Is(err, sorted.ErrNotFound) {
			err = nil
		} else if err == nil {
			usage, err = strconv.ParseInt(stored, 10, 64)
			if err == nil && usage < 0 {
				err = errors.New("invalid usage counter")
			}
		}
	}
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	date := profile.CreatedAt.UTC().Format(time.RFC3339Nano)
	name := strings.SplitN(h.email, "@", 2)[0]
	return map[string]any{
		"id": id, "clusterGroupId": id, "name": name, "email": h.email,
		"profileImagePath": "", "avatarColor": "primary", "profileChangedAt": date,
		"storageLabel": nil, "shouldChangePassword": false, "isAdmin": false,
		"createdAt": date, "deletedAt": nil, "updatedAt": date, "oauthId": "",
		"quotaSizeInBytes": nil, "quotaUsageInBytes": usage, "status": "active", "license": nil,
	}, nil
}

func immichMobilePreferences() map[string]any {
	return map[string]any{
		"albums":             map[string]any{"defaultAssetOrder": "desc"},
		"cast":               map[string]any{"gCastEnabled": false},
		"download":           map[string]any{"archiveSize": int64(4 << 30), "includeEmbeddedVideos": false},
		"emailNotifications": map[string]any{"enabled": false, "albumInvite": false, "albumUpdate": false},
		"folders":            map[string]any{"enabled": false, "sidebarWeb": false},
		"memories":           map[string]any{"enabled": false, "duration": 5, "sidebarWeb": false},
		"people":             map[string]any{"enabled": false, "sidebarWeb": false, "minimumFaces": 3, "updateStrategy": "everyone"},
		"purchase":           map[string]any{"showSupportBadge": false, "hideBuyButtonUntil": "2100-01-01T00:00:00Z"},
		"ratings":            map[string]any{"enabled": false},
		"recentlyAdded":      map[string]any{"sidebarWeb": false},
		"sharedLinks":        map[string]any{"enabled": false, "sidebarWeb": false},
		"tags":               map[string]any{"enabled": false, "sidebarWeb": false},
	}
}

// mobilePreferencesLocked requires h.mu; callers copy the returned map before
// releasing it, so JSON serialization and network writes cannot block uploads.
func (h *Handler) mobilePreferencesLocked() (map[string]any, error) {
	prefs := immichMobilePreferences()
	stored, err := h.metadata.Get("user|preferences")
	if errors.Is(err, sorted.ErrNotFound) {
		return prefs, nil
	}
	if err == nil {
		err = json.Unmarshal([]byte(stored), &prefs)
	}
	if err == nil && prefs == nil {
		err = errors.New("invalid stored preferences")
	}
	return prefs, err
}

func (h *Handler) serveMobilePreferences(w http.ResponseWriter, r *http.Request) {
	if !immichMobileMethod(w, r, http.MethodGet, http.MethodPut, http.MethodPatch) {
		return
	}
	var update map[string]map[string]any
	if r.Method != http.MethodGet {
		if !immichMobileDecode(w, r, &update) {
			return
		}
		if update == nil {
			immichError(w, 400, "Invalid preferences")
			return
		}
	}
	// Read and validate the bounded request before acquiring the commit mutex.
	status, prefs := func() (int, map[string]any) {
		h.mu.Lock()
		defer h.mu.Unlock()
		prefs, err := h.mobilePreferencesLocked()
		if err != nil {
			return 500, nil
		}
		if r.Method == http.MethodGet {
			return 200, prefs
		}
		before, err := json.Marshal(prefs)
		if err != nil {
			return 500, nil
		}
		for section, values := range update {
			current, ok := prefs[section].(map[string]any)
			if !ok || values == nil {
				return 400, nil
			}
			for key, val := range values {
				old, exists := current[key]
				if !exists || !immichMobilePreferenceValue(key, old, val) {
					return 400, nil
				}
				current[key] = val
			}
		}
		data, err := json.Marshal(prefs)
		if err != nil {
			return 400, nil
		}
		if sorted.CheckSizes("user|preferences", string(data)) != nil {
			return 413, nil
		}
		// Reapplying the current preferences is not a new sync event and
		// must not append an otherwise needless account mutation.
		if bytes.Equal(before, data) {
			return 200, prefs
		}
		sequence, err := h.mobileSequence()
		if err != nil || sequence == ^uint64(0) {
			return 500, nil
		}
		batch := h.metadata.BeginBatch()
		batch.Set("user|preferences", string(data))
		// User metadata changes share the global publication sequence. They have no
		// asset change-log entry; upload sequence allocation remains atomic under mu.
		batch.Set("sequence", strconv.FormatUint(sequence+1, 10))
		if h.metadata.CommitBatch(batch) != nil {
			return 500, nil
		}
		return 200, prefs
	}()
	if status != 200 {
		immichError(w, status, "Cannot update preferences")
		return
	}
	immichJSON(w, 200, prefs)
}

func immichMobilePreferenceValue(key string, old, val any) bool {
	switch old.(type) {
	case bool:
		_, ok := val.(bool)
		return ok
	case string:
		s, ok := val.(string)
		if !ok {
			return false
		}
		switch key {
		case "defaultAssetOrder":
			return s == "asc" || s == "desc"
		case "updateStrategy":
			return s == "self" || s == "everyone"
		}
		return true
	case int, int64, float64:
		n, ok := val.(float64)
		return ok && n >= 1 && n < 1<<53 && n == float64(int64(n))
	}
	return false
}

// Decoding is bounded and rejects trailing JSON, including a second object.
func immichMobileDecode(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(value); err != nil {
		immichError(w, 400, "Invalid JSON body")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		immichError(w, 400, "Invalid JSON body")
		return false
	}
	return true
}

// Metadata may contain multiple items at one sequence. The maximal offset is
// reserved for an explicitly acknowledged end-of-snapshot checkpoint.
const immichMobileSnapshotIndex = ^uint64(0)

type immichMobileCursor struct {
	Type     string
	Sequence uint64
	Index    uint64
	Bundle   []string
}

func immichMobileParseAck(value string) (immichMobileCursor, error) {
	if len(value) > 2048 {
		return immichMobileCursor{}, errors.New("acknowledgement too long")
	}
	parts := strings.Split(value, "|")
	if len(parts) < 2 || len(parts) > 3 || !immichMobileEntityType(parts[0]) {
		return immichMobileCursor{}, errors.New("invalid acknowledgement")
	}
	n, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return immichMobileCursor{}, err
	}
	c := immichMobileCursor{Type: parts[0], Sequence: n}
	if len(parts) == 3 {
		switch c.Type {
		case "AssetMetadataV1":
			c.Index, err = strconv.ParseUint(parts[2], 10, 64)
		case "SyncAckV1":
			c.Bundle = strings.Split(parts[2], ",")
			for _, t := range c.Bundle {
				if !immichMobileCheckpointType(t) {
					return c, errors.New("invalid checkpoint bundle")
				}
			}
		default:
			return c, errors.New("unexpected acknowledgement component")
		}
	}
	return c, err
}

func immichMobileCheckpointType(value string) bool {
	for _, t := range strings.Fields("AuthUserV1 AuthUserV2 UserV1 AssetV1 AssetV2 AssetExifV1 AssetMetadataV1 UserMetadataV1") {
		if t == value {
			return true
		}
	}
	return false
}

func immichMobileAckString(c immichMobileCursor) string {
	value := fmt.Sprintf("%s|%d", c.Type, c.Sequence)
	if c.Type == "AssetMetadataV1" {
		value += "|" + strconv.FormatUint(c.Index, 10)
	}
	if len(c.Bundle) != 0 {
		value += "|" + strings.Join(c.Bundle, ",")
	}
	return value
}

func (c immichMobileCursor) after(other immichMobileCursor) bool {
	return c.Sequence > other.Sequence || c.Sequence == other.Sequence && c.Index > other.Index
}

func immichMobileEntityType(value string) bool {
	for _, t := range strings.Fields("AuthUserV1 AuthUserV2 UserV1 UserDeleteV1 AssetV1 AssetV2 AssetDeleteV1 AssetExifV1 AssetEditV1 AssetEditDeleteV1 AssetMetadataV1 AssetMetadataDeleteV1 AssetOcrV1 AssetOcrDeleteV1 PartnerV1 PartnerDeleteV1 PartnerAssetV1 PartnerAssetV2 PartnerAssetBackfillV1 PartnerAssetBackfillV2 PartnerAssetDeleteV1 PartnerAssetExifV1 PartnerAssetExifBackfillV1 PartnerStackV1 PartnerStackBackfillV1 PartnerStackDeleteV1 AlbumV1 AlbumV2 AlbumDeleteV1 AlbumUserV1 AlbumUserBackfillV1 AlbumUserDeleteV1 AlbumAssetCreateV1 AlbumAssetCreateV2 AlbumAssetUpdateV1 AlbumAssetUpdateV2 AlbumAssetBackfillV1 AlbumAssetBackfillV2 AlbumAssetExifCreateV1 AlbumAssetExifUpdateV1 AlbumAssetExifBackfillV1 AlbumToAssetV1 AlbumToAssetBackfillV1 AlbumToAssetDeleteV1 MemoryV1 MemoryV2 MemoryDeleteV1 MemoryToAssetV1 MemoryToAssetV2 MemoryToAssetDeleteV1 StackV1 StackDeleteV1 PersonV1 PersonDeleteV1 AssetFaceV1 AssetFaceV2 AssetFaceV3 AssetFaceDeleteV1 UserMetadataV1 UserMetadataDeleteV1 SyncAckV1 SyncResetV1 SyncCompleteV1") {
		if t == value {
			return true
		}
	}
	return false
}

func immichMobileRequestType(value string) bool {
	for _, t := range strings.Fields("AuthUsersV1 AuthUsersV2 UsersV1 AssetsV1 AssetsV2 AssetExifsV1 AssetEditsV1 AssetMetadataV1 AssetOcrV1 PartnersV1 PartnerAssetsV1 PartnerAssetsV2 PartnerAssetExifsV1 AlbumsV1 AlbumsV2 AlbumUsersV1 AlbumAssetsV1 AlbumAssetsV2 AlbumAssetExifsV1 AlbumToAssetsV1 MemoriesV1 MemoriesV2 MemoryToAssetsV1 MemoryToAssetsV2 StacksV1 PartnerStacksV1 UserMetadataV1 PeopleV1 AssetFacesV1 AssetFacesV2 AssetFacesV3") {
		if t == value {
			return true
		}
	}
	return false
}

func (h *Handler) mobileAckPrefix(r *http.Request) string {
	return "syncack|" + h.sessionKey(r) + "|"
}

// mobileAcks returns a copy of the runtime checkpoint view. Call with h.mu held.
func (h *Handler) mobileAcks(prefix string) (map[string]string, error) {
	if h.acks == nil {
		var err error
		h.acks, err = newImmichAckState(h)
		if err != nil {
			return nil, err
		}
	}
	return h.acks.getLocked(prefix)
}

func (h *Handler) serveMobileAck(w http.ResponseWriter, r *http.Request) {
	if !immichMobileMethod(w, r, http.MethodGet, http.MethodPost, http.MethodDelete) {
		return
	}
	if _, err := h.session(r); err != nil {
		immichError(w, 401, "Unauthorized")
		return
	}
	prefix := h.mobileAckPrefix(r)
	var set struct {
		Acks []string `json:"acks"`
	}
	var del struct {
		Types []string `json:"types"`
	}
	if r.Method == http.MethodPost {
		if !immichMobileDecode(w, r, &set) {
			return
		}
		if set.Acks == nil || len(set.Acks) > 1000 {
			immichError(w, 400, "Invalid acknowledgements")
			return
		}
	}
	if r.Method == http.MethodDelete && !immichMobileDecode(w, r, &del) {
		return
	}
	status, response := func() (int, any) {
		h.mu.Lock()
		defer h.mu.Unlock()
		acks, err := h.mobileAcks(prefix)
		if err != nil {
			return 500, nil
		}
		if r.Method == http.MethodGet {
			response := make([]map[string]any, 0, len(acks))
			types := make([]string, 0, len(acks))
			for t := range acks {
				types = append(types, t)
			}
			sort.Strings(types)
			for _, t := range types {
				response = append(response, map[string]any{"type": t, "ack": acks[t]})
			}
			return 200, response
		}
		if r.Method == http.MethodPost {
			head, err := h.mobileSequence()
			if err != nil {
				return 500, nil
			}
			// Validate the whole request before committing any checkpoints. A completion
			// ack alone does not advance entities; only explicit checkpoint bundles do.
			for _, ack := range set.Acks {
				cursor, err := immichMobileParseAck(ack)
				if err != nil || cursor.Sequence > head {
					return 400, nil
				}
				expanded := []immichMobileCursor{cursor}
				if len(cursor.Bundle) > 0 {
					expanded = nil
					for _, entity := range cursor.Bundle {
						c := immichMobileCursor{Type: entity, Sequence: cursor.Sequence}
						if entity == "AssetMetadataV1" {
							c.Index = immichMobileSnapshotIndex
						}
						expanded = append(expanded, c)
					}
				}
				for _, c := range expanded {
					if old, exists := acks[c.Type]; exists {
						previous, err := immichMobileParseAck(old)
						if err != nil {
							return 500, nil
						}
						if !c.after(previous) {
							continue
						}
					}
					acks[c.Type] = immichMobileAckString(c)
				}
			}
			for t, ack := range acks {
				if sorted.CheckSizes(prefix+t, ack) != nil {
					return 400, nil
				}
			}
			if err := h.acks.setLocked(prefix, acks); err != nil {
				return 500, nil
			}
		} else {
			for _, t := range del.Types {
				if !immichMobileEntityType(t) {
					return 400, nil
				}
			}
			if err := h.acks.deleteLocked(prefix, del.Types); err != nil {
				return 500, nil
			}
		}
		return 204, nil
	}()
	if status >= 400 {
		immichError(w, status, "Cannot update sync acknowledgements")
		return
	}
	if status == 204 {
		w.WriteHeader(http.StatusNoContent)
	} else {
		immichJSON(w, status, response)
	}
}

type immichMobileSyncEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
	Ack  string `json:"ack"`
}

// serveMobileSync snapshots sequence/checkpoints under the commit mutex, then
// streams each entity's ordered change-log range with bounded memory. Once a
// stream has begun, any storage/encoding failure aborts the HTTP connection:
// never append a JSON error, completion event, or completed-snapshot checkpoint.
func (h *Handler) serveMobileSync(w http.ResponseWriter, r *http.Request) {
	if _, err := h.session(r); err != nil {
		immichError(w, 401, "Unauthorized")
		return
	}
	var request struct {
		Types []string `json:"types"`
		Reset bool     `json:"reset"`
	}
	if !immichMobileDecode(w, r, &request) {
		return
	}
	if request.Types == nil {
		immichError(w, 400, "Sync types are required")
		return
	}
	types := make(map[string]bool)
	for _, t := range request.Types {
		if !immichMobileRequestType(t) {
			immichError(w, 400, "Invalid sync type")
			return
		}
		types[t] = true
	}
	prefix := h.mobileAckPrefix(r)
	h.mu.Lock()
	acks, err := h.mobileAcks(prefix)
	if err == nil && request.Reset {
		err = h.acks.deleteLocked(prefix, nil)
		acks = make(map[string]string)
	}
	sequence, seqErr := h.mobileSequence()
	prefs, prefsErr := h.mobilePreferencesLocked()
	h.mu.Unlock()
	if err != nil || seqErr != nil || prefsErr != nil {
		immichError(w, 500, "Cannot read sync state")
		return
	}
	cursors := make(map[string]immichMobileCursor)
	for t, value := range acks {
		c, err := immichMobileParseAck(value)
		if err != nil {
			immichError(w, 500, "Corrupt sync acknowledgement")
			return
		}
		cursors[t] = c
	}
	user, err := h.mobileUser()
	if err != nil {
		immichError(w, 500, "Cannot read user")
		return
	}
	w.Header().Set("Content-Type", "application/jsonlines+json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(w)
	write := func(event immichMobileSyncEvent) {
		if r.Context().Err() != nil || encoder.Encode(event) != nil {
			panic(http.ErrAbortHandler)
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	emit := func(t string, n, index uint64, data any) {
		cursor := immichMobileCursor{Type: t, Sequence: n, Index: index}
		if previous, exists := cursors[t]; exists && !cursor.after(previous) {
			return
		}
		write(immichMobileSyncEvent{Type: t, Data: data, Ack: immichMobileAckString(cursor)})
	}
	// Each entity has its own cursor. A metadata item cursor resumes inclusively
	// at that asset, whereas a completed-snapshot metadata cursor is exclusive.
	visit := func(entity string, fn func(*immichAsset) error) {
		after := uint64(0)
		if c, exists := cursors[entity]; exists {
			after = c.Sequence
			if entity == "AssetMetadataV1" && c.Index != immichMobileSnapshotIndex && after > 0 {
				after--
			}
		}
		if err := h.mobileChanges(after, sequence, func(a *immichAsset) error {
			if err := r.Context().Err(); err != nil {
				return err
			}
			return fn(a)
		}); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	simpleUser := map[string]any{"id": user["id"], "name": user["name"], "email": user["email"], "avatarColor": user["avatarColor"], "deletedAt": nil, "hasProfileImage": false, "profileChangedAt": user["profileChangedAt"]}
	for _, pair := range [][2]string{{"AuthUsersV1", "AuthUserV1"}, {"AuthUsersV2", "AuthUserV2"}} {
		if !types[pair[0]] {
			continue
		}
		authUser := make(map[string]any)
		for key, value := range simpleUser {
			authUser[key] = value
		}
		authUser["isAdmin"], authUser["pinCode"], authUser["storageLabel"] = false, nil, nil
		authUser["quotaSizeInBytes"], authUser["quotaUsageInBytes"] = nil, user["quotaUsageInBytes"]
		if pair[1] == "AuthUserV2" {
			authUser["oauthId"] = nil
		} else {
			authUser["oauthId"] = ""
		}
		emit(pair[1], sequence, 0, authUser)
	}
	if types["UsersV1"] {
		emit("UserV1", sequence, 0, simpleUser)
	}
	ownerID := h.userID()
	for _, version := range []string{"V1", "V2"} {
		if !types["Assets"+version] {
			continue
		}
		visit("Asset"+version, func(a *immichAsset) error {
			data, err := immichMobileAsset(*a, ownerID, version)
			if err != nil {
				return err
			}
			emit("Asset"+version, a.Sequence, 0, data)
			return nil
		})
	}
	if types["AssetExifsV1"] {
		visit("AssetExifV1", func(a *immichAsset) error {
			emit("AssetExifV1", a.Sequence, 0, immichMobileExif(*a))
			return nil
		})
	}
	if types["UserMetadataV1"] {
		emit("UserMetadataV1", sequence, 0, map[string]any{"userId": ownerID, "key": "preferences", "value": prefs})
	}
	if types["AssetMetadataV1"] {
		visit("AssetMetadataV1", func(a *immichAsset) error {
			if len(a.Metadata) == 0 {
				return nil
			}
			var metadata []struct {
				Key   string         `json:"key"`
				Value map[string]any `json:"value"`
			}
			if err := json.Unmarshal(a.Metadata, &metadata); err != nil {
				return err
			}
			for index, item := range metadata {
				if item.Key == "" || item.Value == nil {
					return errors.New("invalid asset metadata")
				}
				emit("AssetMetadataV1", a.Sequence, uint64(index), map[string]any{"assetId": a.ID, "key": item.Key, "value": item.Value})
			}
			return nil
		})
	}
	// Mobile acknowledges only the last event in a same-type batch. Adjacent
	// SyncAckV1 controls would lose earlier checkpoints, so one opaque token
	// carries all completed entity positions. Mobile does not parse ack contents.
	var bundle []string
	for requestType, entity := range map[string]string{
		"AuthUsersV1": "AuthUserV1", "AuthUsersV2": "AuthUserV2", "UsersV1": "UserV1",
		"AssetsV1": "AssetV1", "AssetsV2": "AssetV2", "AssetExifsV1": "AssetExifV1",
		"AssetMetadataV1": "AssetMetadataV1", "UserMetadataV1": "UserMetadataV1",
	} {
		if !types[requestType] {
			continue
		}
		target := immichMobileCursor{Type: entity, Sequence: sequence}
		if entity == "AssetMetadataV1" {
			target.Index = immichMobileSnapshotIndex
		}
		if previous, exists := cursors[entity]; !exists || target.after(previous) {
			bundle = append(bundle, entity)
		}
	}
	if len(bundle) > 0 {
		sort.Strings(bundle)
		ack := immichMobileAckString(immichMobileCursor{Type: "SyncAckV1", Sequence: sequence, Bundle: bundle})
		write(immichMobileSyncEvent{Type: "SyncAckV1", Data: map[string]any{}, Ack: ack})
	}
	write(immichMobileSyncEvent{Type: "SyncCompleteV1", Data: map[string]any{}, Ack: fmt.Sprintf("SyncCompleteV1|%d", sequence)})
	// Sending is not acknowledging: only POST /sync/ack changes the cursor.
}

func immichMobileAsset(a immichAsset, ownerID, version string) (map[string]any, error) {
	sum, err := hex.DecodeString(a.Checksum)
	if err != nil || len(sum) != 20 {
		return nil, errors.New("invalid asset checksum")
	}
	visibility := a.Visibility
	if visibility == "" {
		visibility = "timeline"
	}
	var livePhoto any
	if a.LivePhotoVideoID != "" {
		livePhoto = a.LivePhotoVideoID
	}
	duration := any(a.Duration)
	if version == "V1" {
		duration = fmt.Sprintf("%02d:%02d:%02d.%03d", a.Duration/3600000, a.Duration/60000%60, a.Duration/1000%60, a.Duration%1000)
	}
	return map[string]any{
		"id": a.ID, "ownerId": ownerID, "originalFileName": a.Name,
		"thumbhash": nil, "checksum": base64.StdEncoding.EncodeToString(sum),
		"fileCreatedAt":  a.FileCreatedAt.UTC().Format(time.RFC3339Nano),
		"fileModifiedAt": a.FileModifiedAt.UTC().Format(time.RFC3339Nano),
		"createdAt":      a.CreatedAt.UTC().Format(time.RFC3339Nano),
		"localDateTime":  a.FileCreatedAt.UTC().Format(time.RFC3339Nano),
		"duration":       duration, "type": a.Type, "deletedAt": nil, "isFavorite": a.IsFavorite,
		"visibility": visibility, "livePhotoVideoId": livePhoto,
		"stackId": nil, "libraryId": nil, "width": nil, "height": nil, "isEdited": false,
	}, nil
}

func immichMobileExif(a immichAsset) map[string]any {
	data := make(map[string]any)
	for _, key := range strings.Fields("description exifImageWidth exifImageHeight orientation dateTimeOriginal modifyDate timeZone latitude longitude projectionType city state country make model lensModel fNumber focalLength iso exposureTime profileDescription rating fps") {
		data[key] = nil
	}
	data["assetId"], data["fileSizeInByte"] = a.ID, a.Size
	return data
}

// assetResponse is the detail DTO (not the upload receipt). Field names and
// nullable values match AssetResponseDto; clients fetch this after manual
// uploads when the local asset has not yet been hashed.
func (h *Handler) assetResponse(a *immichAsset) map[string]any {
	data, err := immichMobileAsset(*a, h.userID(), "V2")
	if err != nil {
		// Upload commits validate and compute the checksum. A corrupt record should
		// not turn a checksum into a different hash or a Perkeep blob reference.
		data = map[string]any{"id": a.ID, "checksum": ""}
	}
	delete(data, "deletedAt")
	delete(data, "stackId")
	data["originalPath"] = a.FileRef.String()
	data["updatedAt"] = a.CreatedAt.UTC().Format(time.RFC3339Nano)
	data["isArchived"] = a.Visibility == "archive"
	data["isTrashed"], data["isOffline"], data["hasMetadata"] = false, false, true
	data["exifInfo"] = map[string]any{"fileSizeInByte": a.Size, "dateTimeOriginal": a.FileCreatedAt.UTC().Format(time.RFC3339Nano)}
	data["people"], data["tags"] = []any{}, []any{}
	data["stack"], data["duplicateId"] = nil, nil
	return data
}
