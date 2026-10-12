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
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
)

// Keep the signed multi-claim bounded independently of the size of the legacy
// collection. Asset records themselves are linked blobs, not claim attributes.
const immichMigrationBatchSize = 100

type immichLegacyState struct {
	rows   map[string]string
	assets []*immichAsset // sorted by sequence, including Live Photo dependencies
	head   uint64
	usage  int64
}

// importLegacyImmich imports a stopped legacy writer's LevelDB as native photo
// and account state. The legacy directory remains an untouched backup. In
// particular, do not replace OpenFile below with sorted.NewKeyValue: that
// constructor can create a database or recover it with writes.
//
// The constructor must use newImmichStateForMigration before calling this
// method. An interrupted import has no user|id, and must not serve requests.
// Ordinary native startup rejects such an incomplete catalog.
func (h *Handler) importLegacyImmich(conf jsonconfig.Obj) error {
	if _, err := h.metadata.Get("user|id"); err == nil {
		// Intentionally precedes even config validation and os.Stat: after
		// activation the old path may be gone, inaccessible, or reused.
		return nil
	} else if !errors.Is(err, sorted.ErrNotFound) {
		return fmt.Errorf("immich legacy import: reading native identity: %w", err)
	}
	if len(conf) != 2 {
		return errors.New("immich legacy import requires only type and file")
	}
	typ, typeOK := conf["type"].(string)
	path, pathOK := conf["file"].(string)
	if !typeOK || typ != "leveldb" || !pathOK || path == "" {
		return errors.New("immich legacy import requires type leveldb and an existing file directory")
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("immich legacy import: %w", err)
	}
	if !st.IsDir() {
		return errors.New("immich legacy import source must be an existing LevelDB directory")
	}
	// Even goleveldb's read-only opener creates a missing LOCK file. Refuse
	// incomplete/non-database directories before handing them to the library.
	for _, name := range []string{"CURRENT", "LOCK"} {
		st, err := os.Lstat(filepath.Join(path, name))
		if err != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("immich legacy import requires an existing regular %s file", name)
		}
	}
	legacy, err := readImmichLegacy(path)
	if err != nil {
		return fmt.Errorf("immich legacy import: %w", err)
	}
	// All rows and all referenced originals must be checked before the first
	// signed claim. A corrupt tail must not publish a believable partial ACK.
	if err := legacy.validate(h); err != nil {
		return fmt.Errorf("immich legacy import: %w", err)
	}
	if err := legacy.publish(h); err != nil {
		return fmt.Errorf("immich legacy import: %w", err)
	}
	return nil
}

// readImmichLegacy copies one read-only snapshot and closes the database before
// returning, so publication can never keep a legacy lock open or mutate it.
func readImmichLegacy(path string) (*immichLegacyState, error) {
	db, err := leveldb.OpenFile(path, &opt.Options{ReadOnly: true, Strict: opt.StrictAll})
	if err != nil {
		return nil, fmt.Errorf("opening read-only LevelDB: %w", err)
	}
	snapshot, err := db.GetSnapshot()
	if err != nil {
		db.Close()
		return nil, err
	}
	state := &immichLegacyState{rows: make(map[string]string)}
	it := snapshot.NewIterator(nil, nil)
	for it.Next() {
		key, value := string(it.Key()), string(it.Value())
		if err = sorted.CheckSizes(key, value); err != nil {
			err = fmt.Errorf("legacy row %q: %w", key, err)
			break
		}
		state.rows[key] = value
	}
	if err == nil {
		err = it.Error()
	}
	it.Release()
	snapshot.Release()
	closeErr := db.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return state, nil
}

func immichLegacyJSON(value string, dst any) error {
	d := json.NewDecoder(strings.NewReader(value))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("invalid trailing legacy JSON")
	}
	return nil
}

func immichLegacyHex(value string, bytes int) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == bytes && value == strings.ToLower(value)
}

func immichLegacyUint(value string) (uint64, error) {
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != value {
		return 0, errors.New("invalid legacy sequence")
	}
	return n, nil
}

func (s *immichLegacyState) validate(h *Handler) error {
	if !immichValidUUID(s.rows["user|id"]) {
		return errors.New("legacy database has no valid owner identity")
	}
	if value, ok := s.rows["sequence"]; ok {
		var err error
		s.head, err = immichLegacyUint(value)
		if err != nil {
			return err
		}
	}
	assets := make(map[string]*immichAsset)
	seenSequence := make(map[uint64]string)
	latestChecksum := make(map[string]*immichAsset)
	seenPN := make(map[blob.Ref]string)
	seenSession := make(map[string]bool)
	for key, value := range s.rows {
		switch {
		case key == "user|id", key == "sequence":
		case key == "usage":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 || strconv.FormatInt(n, 10) != value {
				return errors.New("invalid legacy usage counter")
			}
		case key == "user|mobileProfile":
			var profile immichMobileProfile
			if err := immichLegacyJSON(value, &profile); err != nil || profile.CreatedAt.IsZero() {
				return errors.New("invalid legacy mobile profile")
			}
		case key == "user|preferences":
			if err := validateImmichLegacyPreferences(value); err != nil {
				return err
			}
		case strings.HasPrefix(key, "session|"):
			var session immichSession
			if !immichLegacyHex(strings.TrimPrefix(key, "session|"), 32) ||
				immichLegacyJSON(value, &session) != nil || !immichValidUUID(session.ID) ||
				(!immichLegacyHex(session.Credential, 32) && session.Credential != "perkeep:"+s.rows["user|id"]) ||
				session.CreatedAt.IsZero() ||
				len(session.DeviceType) > 256 || len(session.DeviceOS) > 256 || seenSession[session.ID] {
				return fmt.Errorf("invalid legacy session %q", key)
			}
			seenSession[session.ID] = true
		case strings.HasPrefix(key, "syncack|"):
			parts := strings.Split(key, "|")
			cursor, err := immichMobileParseAck(value)
			if len(parts) != 4 || parts[1] != "session" || !immichLegacyHex(parts[2], 32) ||
				err != nil || cursor.Type != parts[3] || cursor.Sequence > s.head {
				return fmt.Errorf("invalid legacy acknowledgement %q", key)
			}
			// Logout leaves its old hashed-token ACK rows behind; those are
			// valid even when the corresponding session no longer exists.
		case strings.HasPrefix(key, "asset|"):
			var a immichAsset
			if err := immichLegacyJSON(value, &a); err != nil {
				return fmt.Errorf("invalid legacy asset %q: %w", key, err)
			}
			if err := validateImmichLegacyAsset(&a); err != nil {
				return fmt.Errorf("invalid legacy asset %q: %w", key, err)
			}
			if key != "asset|"+a.ID || a.Sequence > s.head ||
				seenSequence[a.Sequence] != "" || seenPN[a.PermanodeRef] != "" {
				return fmt.Errorf("conflicting legacy asset %q", key)
			}
			seenSequence[a.Sequence], seenPN[a.PermanodeRef] = a.ID, a.ID
			if old := latestChecksum[a.Checksum]; old == nil || a.Sequence > old.Sequence {
				latestChecksum[a.Checksum] = &a
			}
			if a.Size > math.MaxInt64-s.usage {
				return errors.New("legacy usage sum overflows")
			}
			s.usage += a.Size
			assets[a.ID] = &a
			s.assets = append(s.assets, &a)
		case strings.HasPrefix(key, "checksum|"), strings.HasPrefix(key, "whole|"), strings.HasPrefix(key, "change|"):
			// Validate both directions after all asset records are decoded.
		default:
			return fmt.Errorf("unknown legacy row %q", key)
		}
	}
	if value, ok := s.rows["usage"]; ok {
		if value != strconv.FormatInt(s.usage, 10) {
			return errors.New("legacy usage counter does not match the asset catalog")
		}
	} else if len(s.assets) != 0 {
		return errors.New("legacy asset catalog has no usage counter")
	}
	for key, id := range s.rows {
		switch {
		case strings.HasPrefix(key, "checksum|"):
			if a := assets[id]; a == nil || key != "checksum|"+a.Checksum || latestChecksum[a.Checksum] != a {
				return fmt.Errorf("invalid legacy checksum mapping %q", key)
			}
		case strings.HasPrefix(key, "whole|"):
			if a := assets[id]; a == nil || !a.WholeRef.Valid() || key != "whole|"+a.WholeRef.String() {
				return fmt.Errorf("invalid legacy whole-file mapping %q", key)
			}
		case strings.HasPrefix(key, "change|"):
			if a := assets[id]; a == nil || key != fmt.Sprintf("change|%020d", a.Sequence) {
				return fmt.Errorf("invalid legacy change mapping %q", key)
			}
		}
	}
	for _, a := range s.assets {
		if s.rows["checksum|"+a.Checksum] != latestChecksum[a.Checksum].ID ||
			s.rows[fmt.Sprintf("change|%020d", a.Sequence)] != a.ID {
			return fmt.Errorf("legacy asset %s has incomplete derived rows", a.ID)
		}
		if a.WholeRef.Valid() && s.rows["whole|"+a.WholeRef.String()] != a.ID {
			return fmt.Errorf("legacy asset %s has no whole-file mapping", a.ID)
		}
		if a.LivePhotoVideoID != "" {
			video := assets[a.LivePhotoVideoID]
			if video == nil || video.Type != "VIDEO" || video.Sequence >= a.Sequence {
				return fmt.Errorf("legacy asset %s has an invalid Live Photo video", a.ID)
			}
		}
	}
	seenWhole := make(map[blob.Ref]string)
	ctx := context.Background()
	for _, a := range s.assets {
		if err := verifyImmichLegacyOriginal(ctx, h, a); err != nil {
			return fmt.Errorf("legacy asset %s: %w", a.ID, err)
		}
		if seenWhole[a.WholeRef] != "" {
			return fmt.Errorf("duplicate legacy whole-file identity %v", a.WholeRef)
		}
		seenWhole[a.WholeRef] = a.ID
		if err := h.verifyPermanode(ctx, a.PermanodeRef, a.FileRef, a.WholeRef); err != nil {
			return err
		}
		// Backward SHA-1-only receipts gain their native content identity
		// only after reading and checking the original, never from an index.
		data, err := json.Marshal(a)
		if err != nil {
			return err
		}
		if err := sorted.CheckSizes("asset|"+a.ID, string(data)); err != nil {
			return err
		}
		s.rows["asset|"+a.ID] = string(data)
		s.rows["whole|"+a.WholeRef.String()] = a.ID
	}
	slices.SortFunc(s.assets, func(a, b *immichAsset) int {
		if a.Sequence < b.Sequence {
			return -1
		}
		if a.Sequence > b.Sequence {
			return 1
		}
		return 0
	})
	return nil
}

func validateImmichLegacyAsset(a *immichAsset) error {
	if !immichValidUUID(a.ID) || !immichLegacyHex(a.Checksum, sha1.Size) ||
		!a.FileRef.Valid() || !a.PermanodeRef.Valid() || a.Sequence == 0 || a.Size <= 0 ||
		a.CreatedAt.IsZero() || a.Name == "" || len(a.Name) > 1024 || strings.ContainsAny(a.Name, "\x00\r\n") ||
		(a.Type != "IMAGE" && a.Type != "VIDEO") ||
		(a.Visibility != "timeline" && a.Visibility != "hidden" && a.Visibility != "archive") ||
		a.Duration < 0 || a.Duration > math.MaxInt32 {
		return errors.New("invalid identity, references, size, sequence, or metadata")
	}
	if a.LivePhotoVideoID != "" && !immichValidUUID(a.LivePhotoVideoID) {
		return errors.New("invalid Live Photo identity")
	}
	if len(a.Metadata) != 0 && strings.TrimSpace(string(a.Metadata)) != "null" {
		var items []struct {
			Key   string
			Value map[string]any
		}
		// Uploads accept extra fields on metadata items and retain their raw
		// JSON. Match that validation here rather than narrowing old receipts.
		if err := json.Unmarshal(a.Metadata, &items); err != nil || items == nil {
			return errors.New("invalid mobile metadata")
		}
		seen := make(map[string]bool)
		for _, item := range items {
			if item.Key == "" || len(item.Key) > 128 || item.Value == nil || seen[item.Key] {
				return errors.New("invalid mobile metadata item")
			}
			seen[item.Key] = true
		}
	}
	return nil
}

func validateImmichLegacyPreferences(value string) error {
	var prefs map[string]map[string]any
	if err := immichLegacyJSON(value, &prefs); err != nil || prefs == nil {
		return errors.New("invalid legacy preferences")
	}
	defaults := immichMobilePreferences()
	for section, values := range prefs {
		expected, ok := defaults[section].(map[string]any)
		if !ok || values == nil {
			return errors.New("unknown legacy preference section")
		}
		for key, value := range values {
			old, ok := expected[key]
			if !ok || !immichMobilePreferenceValue(key, old, value) {
				return errors.New("invalid legacy preference value")
			}
		}
	}
	return nil
}

func verifyImmichLegacyOriginal(ctx context.Context, h *Handler, a *immichAsset) error {
	fr, err := schema.NewFileReader(ctx, h.storage, a.FileRef)
	if err != nil {
		return err
	}
	checksum, whole := sha1.New(), blob.NewHash()
	n, readErr := io.Copy(io.MultiWriter(checksum, whole), fr)
	closeErr := fr.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n != a.Size || n != fr.Size() {
		return errors.New("original size does not match legacy receipt")
	}
	if hex.EncodeToString(checksum.Sum(nil)) != a.Checksum {
		return errors.New("original SHA-1 does not match legacy receipt")
	}
	actual := blob.RefFromHash(whole)
	if a.WholeRef.Valid() && a.WholeRef != actual {
		return errors.New("original whole-file digest does not match legacy receipt")
	}
	a.WholeRef = actual
	if a.SidecarRef.Valid() {
		sidecar, err := schema.NewFileReader(ctx, h.storage, a.SidecarRef)
		if err != nil {
			return fmt.Errorf("opening sidecar: %w", err)
		}
		n, readErr := io.Copy(io.Discard, sidecar)
		closeErr := sidecar.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != sidecar.Size() {
			return errors.New("incomplete legacy sidecar")
		}
	}
	return nil
}

// imported checks the entire partial native view against this source before
// writing anything. This also refuses to resume from a different legacy backup.
func (s *immichLegacyState) imported(h *Handler) (map[string]bool, int64, error) {
	imported := make(map[string]bool)
	partialChecksums := make(map[string]string)
	latestChecksum := make(map[string]*immichAsset)
	var usage int64
	var maxSequence uint64
	it := h.metadata.Find("", "")
	for it.Next() {
		key, value := it.Key(), it.Value()
		switch key {
		case "sequence", "usage":
			continue
		}
		expected, ok := s.rows[key]
		isChecksum := strings.HasPrefix(key, "checksum|")
		if !ok || !isChecksum && value != expected {
			it.Close()
			return nil, 0, fmt.Errorf("native partial import disagrees with legacy row %q", key)
		}
		if isChecksum {
			partialChecksums[key] = value
		}
		if strings.HasPrefix(key, "asset|") {
			var a immichAsset
			if err := json.Unmarshal([]byte(value), &a); err != nil {
				it.Close()
				return nil, 0, err
			}
			imported[a.ID] = true
			if a.Size > math.MaxInt64-usage {
				it.Close()
				return nil, 0, errors.New("partial import usage overflows")
			}
			usage += a.Size
			maxSequence = max(maxSequence, a.Sequence)
			if old := latestChecksum[a.Checksum]; old == nil || a.Sequence > old.Sequence {
				latestChecksum[a.Checksum] = &a
			}
		}
	}
	if err := it.Close(); err != nil {
		return nil, 0, err
	}
	for key, id := range partialChecksums {
		a := latestChecksum[strings.TrimPrefix(key, "checksum|")]
		if a == nil || a.ID != id {
			return nil, 0, fmt.Errorf("inconsistent partial checksum mapping %q", key)
		}
	}
	for _, a := range s.assets {
		if !imported[a.ID] {
			continue
		}
		if partialChecksums["checksum|"+a.Checksum] != latestChecksum[a.Checksum].ID {
			return nil, 0, fmt.Errorf("missing partial checksum mapping for %s", a.ID)
		}
		for _, key := range []string{"whole|" + a.WholeRef.String(), fmt.Sprintf("change|%020d", a.Sequence)} {
			if value, err := h.metadata.Get(key); err != nil || value != a.ID {
				return nil, 0, fmt.Errorf("incomplete native import mapping %q", key)
			}
		}
	}
	if value, err := h.metadata.Get("sequence"); err == nil {
		n, err := immichLegacyUint(value)
		if err != nil || n < maxSequence || n > s.head {
			return nil, 0, errors.New("native partial import has an invalid sequence")
		}
	} else if !errors.Is(err, sorted.ErrNotFound) || maxSequence != 0 {
		return nil, 0, errors.New("native partial import has no sequence")
	}
	if value, err := h.metadata.Get("usage"); err == nil {
		if value != strconv.FormatInt(usage, 10) {
			return nil, 0, errors.New("native partial import has an invalid usage counter")
		}
	} else if !errors.Is(err, sorted.ErrNotFound) || usage != 0 {
		return nil, 0, errors.New("native partial import has no usage counter")
	}
	return imported, usage, nil
}

func (s *immichLegacyState) publish(h *Handler) error {
	imported, usage, err := s.imported(h)
	if err != nil {
		return err
	}
	pending := make([]*immichAsset, 0, len(s.assets)-len(imported))
	for _, a := range s.assets {
		if !imported[a.ID] {
			pending = append(pending, a)
		}
	}
	for len(pending) != 0 {
		n := min(len(pending), immichMigrationBatchSize)
		b := h.metadata.BeginBatch()
		for _, a := range pending[:n] {
			b.Set("asset|"+a.ID, s.rows["asset|"+a.ID])
			b.Set("whole|"+a.WholeRef.String(), a.ID)
			b.Set("checksum|"+a.Checksum, a.ID)
			b.Set(fmt.Sprintf("change|%020d", a.Sequence), a.ID)
			usage += a.Size // validated complete and partial sums above
		}
		b.Set("sequence", strconv.FormatUint(s.head, 10))
		b.Set("usage", strconv.FormatInt(usage, 10))
		if err := h.metadata.CommitBatch(b); err != nil {
			return fmt.Errorf("publishing legacy photos: %w", err)
		}
		pending = pending[n:]
	}
	// Account/session/ACK state is imported only after every photo. The UID is
	// deliberately excluded: its publication is the activation barrier.
	keys := make([]string, 0)
	for key := range s.rows {
		if key == "user|id" || key == "sequence" || key == "usage" ||
			strings.HasPrefix(key, "asset|") || strings.HasPrefix(key, "whole|") ||
			strings.HasPrefix(key, "checksum|") || strings.HasPrefix(key, "change|") {
			continue
		}
		value, err := h.metadata.Get(key)
		if errors.Is(err, sorted.ErrNotFound) {
			keys = append(keys, key)
		} else if err != nil {
			return fmt.Errorf("reading native import row %q: %w", key, err)
		} else if value != s.rows[key] {
			return fmt.Errorf("native import row %q disagrees with legacy source", key)
		}
	}
	slices.Sort(keys)
	for len(keys) != 0 {
		n := min(len(keys), immichMigrationBatchSize)
		b := h.metadata.BeginBatch()
		for _, key := range keys[:n] {
			b.Set(key, s.rows[key])
		}
		if err := h.metadata.CommitBatch(b); err != nil {
			return fmt.Errorf("publishing legacy account state: %w", err)
		}
		keys = keys[n:]
	}
	// Preserve preference-only sequence bumps, including catalogs with no
	// assets. Counter publication must succeed before UID can activate state.
	b := h.metadata.BeginBatch()
	b.Set("sequence", strconv.FormatUint(s.head, 10))
	b.Set("usage", strconv.FormatInt(s.usage, 10))
	if err := h.metadata.CommitBatch(b); err != nil {
		return fmt.Errorf("publishing legacy counters: %w", err)
	}
	if err := h.metadata.Set("user|id", s.rows["user|id"]); err != nil {
		return fmt.Errorf("activating imported legacy state: %w", err)
	}
	return nil
}
