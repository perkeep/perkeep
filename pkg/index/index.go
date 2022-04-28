/*
Copyright 2011 The Perkeep Authors

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

package index

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/types/camtypes"

	"go4.org/jsonconfig"
	"go4.org/strutil"
	"go4.org/types"
)

func init() {
	blobserver.RegisterStorageConstructor("index", newFromConfig)
}

type Index struct {
	*blobserver.NoImplStorage
	reindex   bool // whether "reindex" was set in config (likely via perkeepd flag)
	keepGoing bool // whether "keepGoing" was set in config (likely via perkeepd flag)
	s         sorted.KeyValue

	KeyFetcher blob.Fetcher // for verifying claims

	// TODO(mpl): do not init and use deletes when we have a corpus. Since corpus has its own deletes now, they are redundant.

	// deletes is a cache to keep track of the deletion status (deleted vs undeleted)
	// of the blobs in the index. It makes for faster reads than the otherwise
	// recursive calls on the index.
	deletes *deletionCache

	corpus *Corpus // or nil, if not being kept in memory

	mu sync.RWMutex // guards following
	//mu syncdebug.RWMutexTracker  // (when debugging)

	// needs maps from a blob to the missing blobs it needs to
	// finish indexing.
	needs map[blob.Ref][]blob.Ref
	// neededBy is the inverse of needs. The keys are missing blobs
	// and the value(s) are blobs waiting to be reindexed.
	neededBy     map[blob.Ref][]blob.Ref
	readyReindex map[blob.Ref]bool // set of things ready to be re-indexed
	// reindexWg is used to make sure that we wait for all asynchronous, out
	// of order, indexing to be finished, at the end of reindexing.
	reindexWg sync.WaitGroup
	// oooDisabled reports whether out of order indexing is disabled. It
	// should only be the case in some very specific tests.
	oooDisabled bool
	// blobSource is used for fetching blobs when indexing files and other
	// blobs types that reference other objects.
	// The only write access to blobSource should be its initialization (transition
	// from nil to non-nil), once, and protected by mu.
	blobSource blobserver.FetcherEnumerator

	hasWiped bool // whether Wipe has been called on s. So we don't redo it in Reindex() for nothing.
}

func (x *Index) Lock()    { x.mu.Lock() }
func (x *Index) Unlock()  { x.mu.Unlock() }
func (x *Index) RLock()   { x.mu.RLock() }
func (x *Index) RUnlock() { x.mu.RUnlock() }

var (
	_ blobserver.Storage = (*Index)(nil)
	_ Interface          = (*Index)(nil)
)

func (x *Index) logf(format string, args ...interface{}) {
	log.Printf("index: "+format, args...)
}

var aboutToReindex = false

// SignerRefSet is the set of all blob Refs (of different hashes) that represent
// the same signer GPG identity. They are stored as strings for allocation reasons:
// we favor allocating when updating SignerRefSets in the corpus over when reading
// them.
type SignerRefSet []string

// Owner is the set of methods that identify, through their GPG key, a signer of
// claims and permanodes.
type Owner struct {
	keyID []string
	// blobByKeyID maps an owner GPG ID to all its owner blobs (because different hashes).
	// refs are stored as strings for allocation reasons.
	blobByKeyID map[string]SignerRefSet
}

// NewOwner returns an Owner that associates keyID with ref.
func NewOwner(keyID string, ref blob.Ref) *Owner {
	return &Owner{
		keyID:       []string{keyID},
		blobByKeyID: map[string]SignerRefSet{keyID: SignerRefSet{ref.String()}},
	}
}

// KeyID returns the GPG key ID (e.g. 2931A67C26F5ABDA) of the owner. Its
// signature might change when support for multiple GPG keys is introduced.
func (o *Owner) KeyID() string {
	if o == nil || len(o.keyID) == 0 {
		return ""
	}
	return o.keyID[0]
}

// RefSet returns the set of refs that represent the same owner as keyID.
func (o *Owner) RefSet(keyID string) SignerRefSet {
	if o == nil || len(o.blobByKeyID) == 0 {
		return nil
	}
	refs := o.blobByKeyID[keyID]
	if len(refs) == 0 {
		return nil
	}
	return refs
}

// BlobRef returns the currently recommended ref implementation of the owner GPG
// key blob. Its signature might change when support for multiple hashes and/or
// multiple GPG keys is introduced.
func (o *Owner) BlobRef() blob.Ref {
	if o == nil || len(o.blobByKeyID) == 0 {
		return blob.Ref{}
	}
	refs := o.blobByKeyID[o.KeyID()]
	if len(refs) == 0 {
		return blob.Ref{}
	}
	ref, ok := blob.Parse(refs[0])
	if !ok {
		return blob.Ref{}
	}
	return ref
}

// TODO(mpl): I'm not sure there are any cases where we don't want the index to
// have a blobSource, so maybe we should phase out InitBlobSource and integrate it
// to New or something. But later.

// InitBlobSource sets the index's blob source and starts the background
// out-of-order indexing loop. It panics if the blobSource is already set.
// If the index's key fetcher is nil, it is also set to the blobSource
// argument.
func (x *Index) InitBlobSource(blobSource blobserver.FetcherEnumerator) {
	x.Lock()
	defer x.Unlock()
	if x.blobSource != nil {
		panic("blobSource of Index already set")
	}
	x.blobSource = blobSource
	if x.KeyFetcher == nil {
		x.KeyFetcher = blobSource
	}
}

// New returns a new index using the provided key/value storage implementation.
func New(s sorted.KeyValue) (*Index, error) {
	idx := &Index{
		s:            s,
		needs:        make(map[blob.Ref][]blob.Ref),
		neededBy:     make(map[blob.Ref][]blob.Ref),
		readyReindex: make(map[blob.Ref]bool),
	}
	if aboutToReindex {
		idx.deletes = newDeletionCache()
		return idx, nil
	}

	schemaVersion := idx.schemaVersion()
	switch {
	case schemaVersion == 0 && idx.isEmpty():
		// New index.
		err := idx.s.Set(keySchemaVersion.name, fmt.Sprint(requiredSchemaVersion))
		if err != nil {
			return nil, fmt.Errorf("Could not write index schema version %q: %v", requiredSchemaVersion, err)
		}
	case schemaVersion != requiredSchemaVersion:
		tip := ""
		if env.IsDev() {
			// Good signal that we're using the devcam server, so help out
			// the user with a more useful tip:
			tip = `(For the dev server, run "devcam server --wipe" to wipe both your blobs and index)`
		} else {
			if is4To5SchemaBump(schemaVersion) {
				return idx, errMissingWholeRef
			}
			tip = "Run 'perkeepd --reindex' (it might take awhile, but shows status). Alternative: 'camtool dbinit' (or just delete the file for a file based index), and then 'camtool sync --all'"
		}
		return nil, fmt.Errorf("index schema version is %d; required one is %d. You need to reindex. %s",
			schemaVersion, requiredSchemaVersion, tip)
	}
	if err := idx.initDeletesCache(); err != nil {
		return nil, fmt.Errorf("Could not initialize index's deletes cache: %v", err)
	}
	if err := idx.initNeededMaps(); err != nil {
		return nil, fmt.Errorf("Could not initialize index's missing blob maps: %v", err)
	}
	return idx, nil
}

func is4To5SchemaBump(schemaVersion int) bool {
	return schemaVersion == 4 && requiredSchemaVersion == 5
}

var errMissingWholeRef = errors.New("missing wholeRef field in fileInfo rows")

// fixMissingWholeRef appends the wholeRef to all the keyFileInfo rows values. It should
// only be called to upgrade a version 4 index schema to version 5.
func (x *Index) fixMissingWholeRef(fetcher blob.Fetcher) (err error) {
	// We did that check from the caller, but double-check again to prevent from misuse
	// of that function.
	if x.schemaVersion() != 4 || requiredSchemaVersion != 5 {
		panic("fixMissingWholeRef should only be used when upgrading from v4 to v5 of the index schema")
	}
	x.logf("fixing the missing wholeRef in the fileInfo rows...")
	defer func() {
		if err != nil {
			x.logf("fixing the fileInfo rows failed: %v", err)
			return
		}
		x.logf("successfully fixed wholeRef in FileInfo rows.")
	}()

	// first build a reverted keyWholeToFileRef map, so we can get the wholeRef from the fileRef easily.
	fileRefToWholeRef := make(map[blob.Ref]blob.Ref)
	it := x.queryPrefix(keyWholeToFileRef)
	var keyA [3]string
	for it.Next() {
		keyPart := strutil.AppendSplitN(keyA[:0], it.Key(), "|", 3)
		if len(keyPart) != 3 {
			return fmt.Errorf("bogus keyWholeToFileRef key: got %q, wanted \"wholetofile|wholeRef|fileRef\"", it.Key())
		}
		wholeRef, ok1 := blob.Parse(keyPart[1])
		fileRef, ok2 := blob.Parse(keyPart[2])
		if !ok1 || !ok2 {
			return fmt.Errorf("bogus part in keyWholeToFileRef key: %q", it.Key())
		}
		fileRefToWholeRef[fileRef] = wholeRef
	}
	if err := it.Close(); err != nil {
		return err
	}

	var fixedEntries, missedEntries int
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	// We record the mutations and set them all after the iteration because of the sqlite locking:
	// since BeginBatch takes a lock, and Find too, we would deadlock at queryPrefix if we
	// started a batch mutation before.
	mutations := make(map[string]string)
	keyPrefix := keyFileInfo.name + "|"
	it = x.queryPrefix(keyFileInfo)
	defer it.Close()
	var valA [3]string
	for it.Next() {
		select {
		case <-t.C:
			x.logf("recorded %d missing wholeRef that we'll try to fix, and %d that we can't fix.", fixedEntries, missedEntries)
		default:
		}
		br, ok := blob.ParseBytes(it.KeyBytes()[len(keyPrefix):])
		if !ok {
			return fmt.Errorf("invalid blobRef %q", it.KeyBytes()[len(keyPrefix):])
		}
		wholeRef, ok := fileRefToWholeRef[br]
		if !ok {
			missedEntries++
			x.logf("WARNING: wholeRef for %v not found in index. You should probably rebuild the whole index.", br)
			continue
		}
		valPart := strutil.AppendSplitN(valA[:0], it.Value(), "|", 3)
		// The old format we're fixing should be: size|filename|mimetype
		if len(valPart) != 3 {
			return fmt.Errorf("bogus keyFileInfo value: got %q, wanted \"size|filename|mimetype\"", it.Value())
		}
		size_s, filename, mimetype := valPart[0], valPart[1], urld(valPart[2])
		if strings.Contains(mimetype, "|") {
			// I think this can only happen for people migrating from a commit at least as recent as
			// 8229c1985079681a652cb65551b4e80a10d135aa, when wholeRef was introduced to keyFileInfo
			// but there was no migration code yet.
			// For the "production" migrations between 0.8 and 0.9, the index should not have any wholeRef
			// in the keyFileInfo entries. So if something goes wrong and is somehow linked to that happening,
			// I'd like to know about it, hence the logging.
			x.logf("%v: %v already has a wholeRef, not fixing it", it.Key(), it.Value())
			continue
		}
		size, err := strconv.Atoi(size_s)
		if err != nil {
			return fmt.Errorf("bogus size in keyFileInfo value %v: %v", it.Value(), err)
		}
		mutations[keyFileInfo.Key(br)] = keyFileInfo.Val(size, filename, mimetype, wholeRef)
		fixedEntries++
	}
	if err := it.Close(); err != nil {
		return err
	}
	x.logf("starting to commit the missing wholeRef fixes (%d entries) now, this can take a while.", fixedEntries)
	bm := x.s.BeginBatch()
	for k, v := range mutations {
		bm.Set(k, v)
	}
	bm.Set(keySchemaVersion.name, "5")
	if err := x.s.CommitBatch(bm); err != nil {
		return err
	}
	if missedEntries > 0 {
		x.logf("some missing wholeRef entries were not fixed (%d), you should do a full reindex.", missedEntries)
	}
	return nil
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	blobPrefix := config.RequiredString("blobSource")
	kvConfig := config.RequiredObject("storage")
	reindex := config.OptionalBool("reindex", false)
	keepGoing := config.OptionalBool("keepGoing", false)

	if err := config.Validate(); err != nil {
		return nil, err
	}

	kv, err := sorted.NewKeyValue(kvConfig)
	if err != nil {
		if _, ok := err.(sorted.NeedWipeError); !ok {
			return nil, err
		}
		if !reindex {
			return nil, err
		}
	}
	if reindex {
		aboutToReindex = true
		wiper, ok := kv.(sorted.Wiper)
		if !ok {
			return nil, fmt.Errorf("index's storage type %T doesn't support sorted.Wiper", kv)
		}
		if err := wiper.Wipe(); err != nil {
			return nil, fmt.Errorf("error wiping index's sorted key/value type %T: %v", kv, err)
		}
		log.Printf("Index wiped.")
	}

	sto, err := ld.GetStorage(blobPrefix)
	if err != nil {
		return nil, err
	}

	ix, err := New(kv)
	// TODO(mpl): next time we need to do another fix, make a new error
	// type that lets us apply the needed fix depending on its value or
	// something. For now just one value/fix.
	if err == errMissingWholeRef {
		// TODO: maybe we don't want to do that automatically. Brad says
		// we have to think about the case on GCE/CoreOS in particular.
		if err := ix.fixMissingWholeRef(sto); err != nil {
			ix.Close()
			return nil, fmt.Errorf("could not fix missing wholeRef entries: %v", err)
		}
		ix, err = New(kv)
	}
	ix.keepGoing = keepGoing
	ix.reindex = reindex
	if reindex {
		ix.hasWiped = true
	}
	if err != nil {
		return nil, err
	}
	ix.InitBlobSource(sto)

	if !reindex {
		if err := ix.integrityCheck(3 * time.Second); err != nil {
			return nil, err
		}
	}

	return ix, err
}

func (x *Index) String() string {
	return fmt.Sprintf("Perkeep index, using key/value implementation %T", x.s)
}

func (x *Index) isEmpty() bool {
	iter := x.s.Find("", "")
	hasRows := iter.Next()
	if err := iter.Close(); err != nil {
		panic(err)
	}
	return !hasRows
}

// reindexMaxProcs is the number of concurrent goroutines that will be used for reindexing.
var reindexMaxProcs = struct {
	sync.RWMutex
	v int
}{v: 4}

// SetReindexMaxProcs sets the maximum number of concurrent goroutines that are
// used during reindexing.
func SetReindexMaxProcs(n int) {
	reindexMaxProcs.Lock()
	defer reindexMaxProcs.Unlock()
	reindexMaxProcs.v = n
}

// ReindexMaxProcs returns the maximum number of concurrent goroutines that are
// used during reindexing.
func ReindexMaxProcs() int {
	reindexMaxProcs.RLock()
	defer reindexMaxProcs.RUnlock()
	return reindexMaxProcs.v
}

func (x *Index) WantsReindex() bool   { return x.reindex }
func (x *Index) WantsKeepGoing() bool { return x.keepGoing }

func (x *Index) Reindex() error {
	x.Lock()
	if x.blobSource == nil {
		x.Unlock()
		return errors.New("index: can't re-index: no blobSource")
	}
	x.Unlock()
	reindexMaxProcs.RLock()
	defer reindexMaxProcs.RUnlock()
	ctx := context.Background()

	if !x.hasWiped {
		wiper, ok := x.s.(sorted.Wiper)
		if !ok {
			return fmt.Errorf("index's storage type %T doesn't support sorted.Wiper", x.s)
		}
		log.Printf("Wiping index storage type %T ...", x.s)
		if err := wiper.Wipe(); err != nil {
			return fmt.Errorf("error wiping index's sorted key/value type %T: %v", x.s, err)
		}
		log.Printf("Index wiped.")
	}
	log.Printf("Rebuilding index...")

	reindexStart, _ := blob.Parse(os.Getenv("CAMLI_REINDEX_START"))

	err := x.s.Set(keySchemaVersion.name, fmt.Sprintf("%d", requiredSchemaVersion))
	if err != nil {
		return err
	}

	var nerrmu sync.Mutex
	nerr := 0

	blobc := make(chan blob.Ref, 32)

	enumCtx := context.Background()
	enumErr := make(chan error, 1)
	go func() {
		defer close(blobc)
		donec := enumCtx.Done()
		var lastTick time.Time
		enumErr <- blobserver.EnumerateAll(enumCtx, x.blobSource, func(sb blob.SizedRef) error {
			now := time.Now()
			if lastTick.Before(now.Add(-1 * time.Second)) {
				log.Printf("Reindexing at %v", sb.Ref)
				lastTick = now
			}
			if reindexStart.Valid() && sb.Ref.Less(reindexStart) {
				return nil
			}
			select {
			case <-donec:
				return ctx.Err()
			case blobc <- sb.Ref:
				return nil
			}
		})
	}()
	var wg sync.WaitGroup
	for i := 0; i < reindexMaxProcs.v; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for br := range blobc {
				if err := x.indexBlob(ctx, br); err != nil {
					log.Printf("Error reindexing %v: %v", br, err)
					nerrmu.Lock()
					nerr++
					nerrmu.Unlock()
					// TODO: flag (or default?) to stop the EnumerateAll above once
					// there's any error with reindexing?
				}
			}
		}()
	}
	if err := <-enumErr; err != nil {
		return err
	}

	wg.Wait()
	x.reindexWg.Wait()

	x.RLock()
	readyCount := len(x.readyReindex)
	needed := len(x.needs)
	var needSample bytes.Buffer
	if needed > 0 {
		n := 0
	Sample:
		for x, needs := range x.needs {
			for _, need := range needs {
				n++
				if n == 10 {
					break Sample
				}
				if n > 1 {
					fmt.Fprintf(&needSample, ", ")
				}
				fmt.Fprintf(&needSample, "%v needs %v", x, need)
			}
		}
	}
	x.RUnlock()
	if readyCount > 0 {
		return fmt.Errorf("%d blobs were ready to reindex in out-of-order queue, but not yet ran", readyCount)
	}
	if needed > 0 {
		return fmt.Errorf("%d blobs are still needed as dependencies; a sample: %s", needed, needSample.Bytes())
	}

	nerrmu.Lock() // no need to unlock
	if nerr != 0 {
		return fmt.Errorf("%d blobs failed to re-index", nerr)
	}
	if err := x.initDeletesCache(); err != nil {
		return err
	}
	log.Printf("Index rebuild complete.")
	return nil
}

// integrityCheck enumerates blobs through x.blobSource during timeout, and
// verifies for each of them that it has a meta row in the index. It logs a message
// if any of them is not found. It only returns an error if something went wrong
// during the enumeration.
func (x *Index) integrityCheck(timeout time.Duration) error {
	t0 := time.Now()
	x.logf("starting integrity check...")
	defer func() {
		x.logf("integrity check done (after %v)", time.Since(t0).Round(10*time.Millisecond))
	}()
	if x.blobSource == nil {
		return errors.New("index: can't check sanity of index: no blobSource")
	}

	// we don't actually need seen atm, but I anticipate we'll return it at some
	// point, so we can display the blobs that were tested/seen/missed on the web UI.
	seen := make([]blob.Ref, 0)
	notFound := make([]blob.Ref, 0)
	enumCtx := context.TODO()
	stopTime := time.NewTimer(timeout)
	defer stopTime.Stop()
	var errEOT = errors.New("time's out")
	if err := blobserver.EnumerateAll(enumCtx, x.blobSource, func(sb blob.SizedRef) error {
		select {
		case <-stopTime.C:
			return errEOT
		default:
		}
		if _, err := x.GetBlobMeta(enumCtx, sb.Ref); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			notFound = append(notFound, sb.Ref)
			return nil
		}
		seen = append(seen, sb.Ref)
		return nil
	}); err != nil && err != errEOT {
		return err
	}
	if len(notFound) > 0 {
		// TODO(mpl): at least on GCE, display that message and maybe more on a web UI page as well.
		x.logf("WARNING: sanity checking of the index found %d non-indexed blobs out of %d tested blobs. Reindexing is advised.", len(notFound), len(notFound)+len(seen))
	}
	return nil
}

func queryPrefixString(s sorted.KeyValue, prefix string) sorted.Iterator {
	if prefix == "" {
		return s.Find("", "")
	}
	lastByte := prefix[len(prefix)-1]
	if lastByte == 0xff {
		panic("unsupported query prefix ending in 0xff")
	}
	end := prefix[:len(prefix)-1] + string(lastByte+1)
	return s.Find(prefix, end)
}

func (x *Index) queryPrefixString(prefix string) sorted.Iterator {
	return queryPrefixString(x.s, prefix)
}

func queryPrefix(s sorted.KeyValue, key *keyType, args ...interface{}) sorted.Iterator {
	return queryPrefixString(s, key.Prefix(args...))
}

func (x *Index) queryPrefix(key *keyType, args ...interface{}) sorted.Iterator {
	return x.queryPrefixString(key.Prefix(args...))
}

func closeIterator(it sorted.Iterator, perr *error) {
	err := it.Close()
	if err != nil && *perr == nil {
		*perr = err
	}
}

// schemaVersion returns the version of schema as it is found
// in the currently used index. If not found, it returns 0.
func (x *Index) schemaVersion() int {
	schemaVersionStr, err := x.s.Get(keySchemaVersion.name)
	if err != nil {
		if err == sorted.ErrNotFound {
			return 0
		}
		panic(fmt.Sprintf("Could not get index schema version: %v", err))
	}
	schemaVersion, err := strconv.Atoi(schemaVersionStr)
	if err != nil {
		panic(fmt.Sprintf("Bogus index schema version: %q", schemaVersionStr))
	}
	return schemaVersion
}

type deletion struct {
	deleter blob.Ref
	when    time.Time
}

type byDeletionDate []deletion

func (d byDeletionDate) Len() int           { return len(d) }
func (d byDeletionDate) Swap(i, j int)      { d[i], d[j] = d[j], d[i] }
func (d byDeletionDate) Less(i, j int) bool { return d[i].when.Before(d[j].when) }

type deletionCache struct {
	sync.RWMutex
	m map[blob.Ref][]deletion
}

func newDeletionCache() *deletionCache {
	return &deletionCache{
		m: make(map[blob.Ref][]deletion),
	}
}

// initDeletesCache creates and populates the deletion status cache used by the index
// for faster calls to IsDeleted and DeletedAt. It is called by New.
func (x *Index) initDeletesCache() (err error) {
	x.deletes = newDeletionCache()
	it := x.queryPrefix(keyDeleted)
	defer closeIterator(it, &err)
	for it.Next() {
		cl, ok := kvDeleted(it.Key())
		if !ok {
			return fmt.Errorf("Bogus keyDeleted entry key: want |\"deleted\"|<deleted blobref>|<reverse claimdate>|<deleter claim>|, got %q", it.Key())
		}
		targetDeletions := append(x.deletes.m[cl.Target],
			deletion{
				deleter: cl.BlobRef,
				when:    cl.Date,
			})
		sort.Sort(sort.Reverse(byDeletionDate(targetDeletions)))
		x.deletes.m[cl.Target] = targetDeletions
	}
	return err
}

func kvDeleted(k string) (c camtypes.Claim, ok bool) {
	// TODO(bradfitz): garbage
	keyPart := strings.Split(k, "|")
	if len(keyPart) != 4 {
		return
	}
	if keyPart[0] != "deleted" {
		return
	}
	target, ok := blob.Parse(keyPart[1])
	if !ok {
		return
	}
	claimRef, ok := blob.Parse(keyPart[3])
	if !ok {
		return
	}
	date, err := time.Parse(time.RFC3339, unreverseTimeString(keyPart[2]))
	if err != nil {
		return
	}
	return camtypes.Claim{
		BlobRef: claimRef,
		Target:  target,
		Date:    date,
		Type:    string(schema.DeleteClaim),
	}, true
}

// IsDeleted reports whether the provided blobref (of a permanode or
// claim) should be considered deleted.
func (x *Index) IsDeleted(br blob.Ref) bool {
	if x.deletes == nil {
		// We still allow the slow path, in case someone creates
		// their own Index without a deletes cache.
		return x.isDeletedNoCache(br)
	}
	x.deletes.RLock()
	defer x.deletes.RUnlock()
	return x.isDeleted(br)
}

// The caller must hold x.deletes.mu for read.
func (x *Index) isDeleted(br blob.Ref) bool {
	deletes, ok := x.deletes.m[br]
	if !ok {
		return false
	}
	for _, v := range deletes {
		if !x.isDeleted(v.deleter) {
			return true
		}
	}
	return false
}

// Used when the Index has no deletes cache (x.deletes is nil).
func (x *Index) isDeletedNoCache(br blob.Ref) bool {
	var err error
	it := x.queryPrefix(keyDeleted, br)
	for it.Next() {
		cl, ok := kvDeleted(it.Key())
		if !ok {
			panic(fmt.Sprintf("Bogus keyDeleted entry key: want |\"deleted\"|<deleted blobref>|<reverse claimdate>|<deleter claim>|, got %q", it.Key()))
		}
		if !x.isDeletedNoCache(cl.BlobRef) {
			closeIterator(it, &err)
			if err != nil {
				// TODO: Do better?
				panic(fmt.Sprintf("Could not close iterator on keyDeleted: %v", err))
			}
			return true
		}
	}
	closeIterator(it, &err)
	if err != nil {
		// TODO: Do better?
		panic(fmt.Sprintf("Could not close iterator on keyDeleted: %v", err))
	}
	return false
}

// GetRecentPermanodes sends results to dest filtered by owner, limit, and
// before.  A zero value for before will default to the current time.  The
// results will have duplicates suppressed, with most recent permanode
// returned.
// Note, permanodes more recent than before will still be fetched from the
// index then skipped. This means runtime scales linearly with the number of
// nodes more recent than before.
func (x *Index) GetRecentPermanodes(ctx context.Context, dest chan<- camtypes.RecentPermanode, owner blob.Ref, limit int, before time.Time) (err error) {
	defer close(dest)

	keyId, err := x.KeyId(ctx, owner)
	if err == sorted.ErrNotFound {
		x.logf("no recent permanodes because keyId for owner %v not found", owner)
		return nil
	}
	if err != nil {
		x.logf("error fetching keyId for owner %v: %v", owner, err)
		return err
	}

	sent := 0
	var seenPermanode dupSkipper

	if before.IsZero() {
		before = time.Now()
	}
	// TODO(bradfitz): handle before efficiently. don't use queryPrefix.
	it := x.queryPrefix(keyRecentPermanode, keyId)
	defer closeIterator(it, &err)
	for it.Next() {
		permaStr := it.Value()
		parts := strings.SplitN(it.Key(), "|", 4)
		if len(parts) != 4 {
			continue
		}
		mTime, _ := time.Parse(time.RFC3339, unreverseTimeString(parts[2]))
		permaRef, ok := blob.Parse(permaStr)
		if !ok {
			continue
		}
		if x.IsDeleted(permaRef) {
			continue
		}
		if seenPermanode.Dup(permaStr) {
			continue
		}
		// Skip entries with an mTime less than or equal to before.
		if !mTime.Before(before) {
			continue
		}
		dest <- camtypes.RecentPermanode{
			Permanode:   permaRef,
			Signer:      owner, // TODO(bradfitz): kinda. usually. for now.
			LastModTime: mTime,
		}
		sent++
		if sent == limit {
			break
		}
	}
	return nil
}

func (x *Index) AppendClaims(ctx context.Context, dst []camtypes.Claim, permaNode blob.Ref,
	signerFilter string,
	attrFilter string) ([]camtypes.Claim, error) {
	if x.corpus != nil {
		return x.corpus.AppendClaims(ctx, dst, permaNode, signerFilter, attrFilter)
	}
	var (
		err error
		it  sorted.Iterator
	)
	var signerRefs SignerRefSet
	if signerFilter != "" {
		signerRefs, err = x.signerRefs(ctx, signerFilter)
		if err != nil {
			return dst, err
		}
		if len(signerRefs) == 0 {
			return dst, nil
		}
		it = x.queryPrefix(keyPermanodeClaim, permaNode, signerFilter)
	} else {
		it = x.queryPrefix(keyPermanodeClaim, permaNode)
	}
	defer closeIterator(it, &err)

	// In the common case, an attribute filter is just a plain
	// token ("camliContent") unescaped. If so, fast path that
	// check to skip the row before we even split it.
	var mustHave string
	if attrFilter != "" && urle(attrFilter) == attrFilter {
		mustHave = attrFilter
	}

	for it.Next() {
		val := it.Value()
		if mustHave != "" && !strings.Contains(val, mustHave) {
			continue
		}
		cl, ok := kvClaim(it.Key(), val, blob.Parse)
		if !ok {
			continue
		}
		if x.IsDeleted(cl.BlobRef) {
			continue
		}
		if attrFilter != "" && cl.Attr != attrFilter {
			continue
		}
		// TODO(mpl): if we ever pass an Owner to AppendClaims, then we could have a
		// Matches method on it, that we would use here.
		if signerFilter != "" && !signerRefs.blobMatches(cl.Signer) {
			continue
		}
		dst = append(dst, cl)
	}
	return dst, nil
}

func kvClaim(k, v string, blobParse func(string) (blob.Ref, bool)) (c camtypes.Claim, ok bool) {
	const nKeyPart = 5
	const nValPart = 4
	var keya [nKeyPart]string
	var vala [nValPart]string
	keyPart := strutil.AppendSplitN(keya[:0], k, "|", -1)
	valPart := strutil.AppendSplitN(vala[:0], v, "|", -1)
	if len(keyPart) < nKeyPart || len(valPart) < nValPart {
		return
	}
	signerRef, ok := blobParse(valPart[3])
	if !ok {
		return
	}
	permaNode, ok := blobParse(keyPart[1])
	if !ok {
		return
	}
	claimRef, ok := blobParse(keyPart[4])
	if !ok {
		return
	}
	date, err := time.Parse(time.RFC3339, keyPart[3])
	if err != nil {
		return
	}
	return camtypes.Claim{
		BlobRef:   claimRef,
		Signer:    signerRef,
		Permanode: permaNode,
		Date:      date,
		Type:      urld(valPart[0]),
		Attr:      urld(valPart[1]),
		Value:     urld(valPart[2]),
	}, true
}

func (x *Index) GetBlobMeta(ctx context.Context, br blob.Ref) (camtypes.BlobMeta, error) {
	if x.corpus != nil {
		return x.corpus.GetBlobMeta(ctx, br)
	}
	key := "meta:" + br.String()
	meta, err := x.s.Get(key)
	if err == sorted.ErrNotFound {
		err = os.ErrNotExist
	}
	if err != nil {
		return camtypes.BlobMeta{}, err
	}
	pos := strings.Index(meta, "|")
	if pos < 0 {
		panic(fmt.Sprintf("Bogus index row for key %q: got value %q", key, meta))
	}
	size, err := strconv.ParseUint(meta[:pos], 10, 32)
	if err != nil {
		return camtypes.BlobMeta{}, err
	}
	mime := meta[pos+1:]
	return camtypes.BlobMeta{
		Ref:       br,
		Size:      uint32(size),
		CamliType: camliTypeFromMIME(mime),
	}, nil
}

// HasLegacySHA1 reports whether the index has legacy SHA-1 blobs.
func (x *Index) HasLegacySHA1() (ok bool, err error) {
	if x.corpus != nil {
		return x.corpus.hasLegacySHA1, err
	}
	it := x.queryPrefix(keyWholeToFileRef, "sha1-")
	defer closeIterator(it, &err)
	for it.Next() {
		return true, err
	}
	return false, err
}

func (x *Index) KeyId(ctx context.Context, signer blob.Ref) (string, error) {
	if x.corpus != nil {
		return x.corpus.KeyId(ctx, signer)
	}
	return x.s.Get("signerkeyid:" + signer.String())
}

// signerRefs returns the set of signer blobRefs matching the signer keyID. It
// does not return an error if none is found.
func (x *Index) signerRefs(ctx context.Context, keyID string) (SignerRefSet, error) {
	if x.corpus != nil {
		return x.corpus.signerRefs[keyID], nil
	}
	it := x.queryPrefixString(keySignerKeyID.name)
	var err error
	var refs SignerRefSet
	defer closeIterator(it, &err)
	prefix := keySignerKeyID.name + ":"
	for it.Next() {
		if it.Value() == keyID {
			refs = append(refs, strings.TrimPrefix(it.Key(), prefix))
		}
	}
	return refs, nil
}

func (x *Index) PermanodeOfSignerAttrValue(ctx context.Context, signer blob.Ref, attr, val string) (permaNode blob.Ref, err error) {
	keyId, err := x.KeyId(ctx, signer)
	if err == sorted.ErrNotFound {
		return blob.Ref{}, os.ErrNotExist
	}
	if err != nil {
		return blob.Ref{}, err
	}
	it := x.queryPrefix(keySignerAttrValue, keyId, attr, val)
	defer closeIterator(it, &err)
	for it.Next() {
		permaRef, ok := blob.Parse(it.Value())
		if ok && !x.IsDeleted(permaRef) {
			return permaRef, nil
		}
	}
	return blob.Ref{}, os.ErrNotExist
}

// SearchPermanodesWithAttr is just like PermanodeOfSignerAttrValue
// except we return multiple and dup-suppress.  If request.Query is
// "", it is not used in the prefix search.
func (x *Index) SearchPermanodesWithAttr(ctx context.Context, dest chan<- blob.Ref, request *camtypes.PermanodeByAttrRequest) (err error) {
	defer close(dest)
	if request.FuzzyMatch {
		// TODO(bradfitz): remove this for now? figure out how to handle it generically?
		return errors.New("TODO: SearchPermanodesWithAttr: generic indexer doesn't support FuzzyMatch on PermanodeByAttrRequest")
	}
	if request.Attribute == "" {
		return errors.New("index: missing Attribute in SearchPermanodesWithAttr")
	}
	if !IsIndexedAttribute(request.Attribute) {
		return fmt.Errorf("SearchPermanodesWithAttr: called with a non-indexed attribute %q", request.Attribute)
	}

	keyId, err := x.KeyId(ctx, request.Signer)
	if err == sorted.ErrNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	var it sorted.Iterator
	if request.Query == "" {
		it = x.queryPrefix(keySignerAttrValue, keyId, request.Attribute)
	} else {
		it = x.queryPrefix(keySignerAttrValue, keyId, request.Attribute, request.Query)
	}
	defer closeIterator(it, &err)
	before := request.At
	if before.IsZero() {
		before = time.Now()
	}
	for it.Next() {
		cl, ok := kvSignerAttrValue(it.Key(), it.Value())
		if !ok {
			continue
		}
		if x.IsDeleted(cl.BlobRef) {
			continue
		}
		if x.IsDeleted(cl.Permanode) {
			continue
		}
		if cl.Date.After(before) {
			continue
		}
		pnstr := cl.Permanode.String()
		if seen[pnstr] {
			continue
		}
		seen[pnstr] = true

		dest <- cl.Permanode
		if len(seen) == request.MaxResults {
			break
		}
	}
	return nil
}

func kvSignerAttrValue(k, v string) (c camtypes.Claim, ok bool) {
	// TODO(bradfitz): garbage
	keyPart := strings.Split(k, "|")
	valPart := strings.Split(v, "|")
	if len(keyPart) != 6 || len(valPart) != 1 {
		// TODO(mpl): use glog
		log.Printf("bogus keySignerAttrValue index entry: %q = %q", k, v)
		return
	}
	if keyPart[0] != "signerattrvalue" {
		return
	}
	date, err := time.Parse(time.RFC3339, unreverseTimeString(keyPart[4]))
	if err != nil {
		log.Printf("bogus time in keySignerAttrValue index entry: %q", keyPart[4])
		return
	}
	claimRef, ok := blob.Parse(keyPart[5])
	if !ok {
		log.Printf("bogus claim in keySignerAttrValue index entry: %q", keyPart[5])
		return
	}
	permaNode, ok := blob.Parse(valPart[0])
	if !ok {
		log.Printf("bogus permanode in keySignerAttrValue index entry: %q", valPart[0])
		return
	}
	return camtypes.Claim{
		BlobRef:   claimRef,
		Permanode: permaNode,
		Date:      date,
		Attr:      urld(keyPart[2]),
		Value:     urld(keyPart[3]),
	}, true
}

func (x *Index) PathsOfSignerTarget(ctx context.Context, signer, target blob.Ref) (paths []*camtypes.Path, err error) {
	paths = []*camtypes.Path{}
	keyId, err := x.KeyId(ctx, signer)
	if err != nil {
		if err == sorted.ErrNotFound {
			err = nil
		}
		return
	}

	mostRecent := make(map[string]*camtypes.Path)
	maxClaimDates := make(map[string]time.Time)

	it := x.queryPrefix(keyPathBackward, keyId, target)
	defer closeIterator(it, &err)
	for it.Next() {
		p, ok, active := kvPathBackward(it.Key(), it.Value())
		if !ok {
			continue
		}
		if x.IsDeleted(p.Claim) {
			continue
		}
		if x.IsDeleted(p.Base) {
			continue
		}

		key := p.Base.String() + "/" + p.Suffix
		if p.ClaimDate.After(maxClaimDates[key]) {
			maxClaimDates[key] = p.ClaimDate
			if active {
				mostRecent[key] = &p
			} else {
				delete(mostRecent, key)
			}
		}
	}
	for _, v := range mostRecent {
		paths = append(paths, v)
	}
	return paths, nil
}

func kvPathBackward(k, v string) (p camtypes.Path, ok bool, active bool) {
	// TODO(bradfitz): garbage
	keyPart := strings.Split(k, "|")
	valPart := strings.Split(v, "|")
	if len(keyPart) != 4 || len(valPart) != 4 {
		// TODO(mpl): use glog
		log.Printf("bogus keyPathBackward index entry: %q = %q", k, v)
		return
	}
	if keyPart[0] != "signertargetpath" {
		return
	}
	target, ok := blob.Parse(keyPart[2])
	if !ok {
		log.Printf("bogus target in keyPathBackward index entry: %q", keyPart[2])
		return
	}
	claim, ok := blob.Parse(keyPart[3])
	if !ok {
		log.Printf("bogus claim in keyPathBackward index entry: %q", keyPart[3])
		return
	}
	date, err := time.Parse(time.RFC3339, valPart[0])
	if err != nil {
		log.Printf("bogus date in keyPathBackward index entry: %q", valPart[0])
		return
	}
	base, ok := blob.Parse(valPart[1])
	if !ok {
		log.Printf("bogus base in keyPathBackward index entry: %q", valPart[1])
		return
	}
	if valPart[2] == "Y" {
		active = true
	}
	return camtypes.Path{
		Claim:     claim,
		Base:      base,
		Target:    target,
		ClaimDate: date,
		Suffix:    urld(valPart[3]),
	}, true, active
}

func (x *Index) PathsLookup(ctx context.Context, signer, base blob.Ref, suffix string) (paths []*camtypes.Path, err error) {
	paths = []*camtypes.Path{}
	keyId, err := x.KeyId(ctx, signer)
	if err != nil {
		if err == sorted.ErrNotFound {
			err = nil
		}
		return
	}

	it := x.queryPrefix(keyPathForward, keyId, base, suffix)
	defer closeIterator(it, &err)
	for it.Next() {
		p, ok, active := kvPathForward(it.Key(), it.Value())
		if !ok {
			continue
		}
		if x.IsDeleted(p.Claim) {
			continue
		}
		if x.IsDeleted(p.Target) {
			continue
		}

		// TODO(bradfitz): investigate what's up with deleted
		// forward path claims here.  Needs docs with the
		// interface too, and tests.
		_ = active

		paths = append(paths, &p)
	}
	return
}

func kvPathForward(k, v string) (p camtypes.Path, ok bool, active bool) {
	// TODO(bradfitz): garbage
	keyPart := strings.Split(k, "|")
	valPart := strings.Split(v, "|")
	if len(keyPart) != 6 || len(valPart) != 2 {
		// TODO(mpl): use glog
		log.Printf("bogus keyPathForward index entry: %q = %q", k, v)
		return
	}
	if keyPart[0] != "path" {
		return
	}
	base, ok := blob.Parse(keyPart[2])
	if !ok {
		log.Printf("bogus base in keyPathForward index entry: %q", keyPart[2])
		return
	}
	date, err := time.Parse(time.RFC3339, unreverseTimeString(keyPart[4]))
	if err != nil {
		log.Printf("bogus date in keyPathForward index entry: %q", keyPart[4])
		return
	}
	claim, ok := blob.Parse(keyPart[5])
	if !ok {
		log.Printf("bogus claim in keyPathForward index entry: %q", keyPart[5])
		return
	}
	if valPart[0] == "Y" {
		active = true
	}
	target, ok := blob.Parse(valPart[1])
	if !ok {
		log.Printf("bogus target in keyPathForward index entry: %q", valPart[1])
		return
	}
	return camtypes.Path{
		Claim:     claim,
		Base:      base,
		Target:    target,
		ClaimDate: date,
		Suffix:    urld(keyPart[3]),
	}, true, active
}

func (x *Index) PathLookup(ctx context.Context, signer, base blob.Ref, suffix string, at time.Time) (*camtypes.Path, error) {
	paths, err := x.PathsLookup(ctx, signer, base, suffix)
	if err != nil {
		return nil, err
	}
	var (
		newest    = int64(0)
		atSeconds = int64(0)
		best      *camtypes.Path
	)

	if !at.IsZero() {
		atSeconds = at.Unix()
	}

	for _, path := range paths {
		t := path.ClaimDate
		secs := t.Unix()
		if atSeconds != 0 && secs > atSeconds {
			// Too new
			continue
		}
		if newest > secs {
			// Too old
			continue
		}
		// Just right
		newest, best = secs, path
	}
	if best == nil {
		return nil, os.ErrNotExist
	}
	return best, nil
}

func (x *Index) existingFileSchemas(wholeRef blob.Ref) (schemaRefs []blob.Ref, err error) {
	it := x.queryPrefix(keyWholeToFileRef, wholeRef)
	defer closeIterator(it, &err)
	for it.Next() {
		keyPart := strings.Split(it.Key(), "|")[1:]
		if len(keyPart) < 2 {
			continue
		}
		ref, ok := blob.Parse(keyPart[1])
		if ok {
			schemaRefs = append(schemaRefs, ref)
		}
	}
	return schemaRefs, nil
}

// WholeRefToFile maps a file contents blobRef (a "wholeRef"), to the file schemas with those contents.
type WholeRefToFile map[string][]blob.Ref

// ExistingFileSchemas returns the file schemas for the provided file contents refs.
func (x *Index) ExistingFileSchemas(wholeRef ...blob.Ref) (WholeRefToFile, error) {
	schemaRefs := make(WholeRefToFile)
	for _, v := range wholeRef {
		newRefs, err := x.existingFileSchemas(v)
		if err != nil {
			return nil, err
		}
		schemaRefs[v.String()] = newRefs
	}
	return schemaRefs, nil
}

func (x *Index) loadKey(key string, val *string, err *error, wg *sync.WaitGroup) {
	defer wg.Done()
	*val, *err = x.s.Get(key)
}

func (x *Index) GetFileInfo(ctx context.Context, fileRef blob.Ref) (camtypes.FileInfo, error) {
	if x.corpus != nil {
		return x.corpus.GetFileInfo(ctx, fileRef)
	}
	ikey := "fileinfo|" + fileRef.String()
	tkey := keyFileTimes.name + "|" + fileRef.String()
	// TODO: switch this to use syncutil.Group
	wg := new(sync.WaitGroup)
	wg.Add(2)
	var iv, tv string // info value, time value
	var ierr, terr error
	go x.loadKey(ikey, &iv, &ierr, wg)
	go x.loadKey(tkey, &tv, &terr, wg)
	wg.Wait()

	if ierr == sorted.ErrNotFound {
		return camtypes.FileInfo{}, os.ErrNotExist
	}
	if ierr != nil {
		return camtypes.FileInfo{}, ierr
	}
	valPart := strings.Split(iv, "|")
	if len(valPart) < 3 {
		x.logf("bogus key %q = %q", ikey, iv)
		return camtypes.FileInfo{}, os.ErrNotExist
	}
	var wholeRef blob.Ref
	if len(valPart) >= 4 {
		wholeRef, _ = blob.Parse(valPart[3])
	}
	size, err := strconv.ParseInt(valPart[0], 10, 64)
	if err != nil {
		x.logf("bogus integer at position 0 in key %q = %q", ikey, iv)
		return camtypes.FileInfo{}, os.ErrNotExist
	}
	fileName := urld(valPart[1])
	fi := camtypes.FileInfo{
		Size:     size,
		FileName: fileName,
		MIMEType: urld(valPart[2]),
		WholeRef: wholeRef,
	}

	if tv != "" {
		times := strings.Split(urld(tv), ",")
		updateFileInfoTimes(&fi, times)
	}

	return fi, nil
}

func updateFileInfoTimes(fi *camtypes.FileInfo, times []string) {
	if len(times) == 0 {
		return
	}
	fi.Time = types.ParseTime3339OrNil(times[0])
	if len(times) == 2 {
		fi.ModTime = types.ParseTime3339OrNil(times[1])
	}
}

// v is "width|height"
func kvImageInfo(v []byte) (ii camtypes.ImageInfo, ok bool) {
	pipei := bytes.IndexByte(v, '|')
	if pipei < 0 {
		return
	}
	w, err := strutil.ParseUintBytes(v[:pipei], 10, 16)
	if err != nil {
		return
	}
	h, err := strutil.ParseUintBytes(v[pipei+1:], 10, 16)
	if err != nil {
		return
	}
	ii.Width = uint16(w)
	ii.Height = uint16(h)
	return ii, true
}

func (x *Index) GetImageInfo(ctx context.Context, fileRef blob.Ref) (camtypes.ImageInfo, error) {
	if x.corpus != nil {
		return x.corpus.GetImageInfo(ctx, fileRef)
	}
	// it might be that the key does not exist because image.DecodeConfig failed earlier
	// (because of unsupported JPEG features like progressive mode).
	key := keyImageSize.Key(fileRef.String())
	v, err := x.s.Get(key)
	if err == sorted.ErrNotFound {
		err = os.ErrNotExist
	}
	if err != nil {
		return camtypes.ImageInfo{}, err
	}
	ii, ok := kvImageInfo([]byte(v))
	if !ok {
		return camtypes.ImageInfo{}, fmt.Errorf("index: bogus key %q = %q", key, v)
	}
	return ii, nil
}

func (x *Index) GetMediaTags(ctx context.Context, fileRef blob.Ref) (tags map[string]string, err error) {
	if x.corpus != nil {
		return x.corpus.GetMediaTags(ctx, fileRef)
	}
	fi, err := x.GetFileInfo(ctx, fileRef)
	if err != nil {
		return nil, err
	}
	it := x.queryPrefix(keyMediaTag, fi.WholeRef.String())
	defer closeIterator(it, &err)
	for it.Next() {
		if tags == nil {
			tags = make(map[string]string)
		}
		tags[it.Key()] = it.Value()
	}
	return tags, nil
}

func (x *Index) GetFileLocation(ctx context.Context, fileRef blob.Ref) (camtypes.Location, error) {
	if x.corpus != nil {
		lat, long, ok := x.corpus.FileLatLong(fileRef)
		if !ok {
			return camtypes.Location{}, os.ErrNotExist
		}
		// TODO(mpl): Brad says to move this check lower, in corpus func and/or when building corpus from index rows.
		if math.IsNaN(long) || math.IsNaN(lat) {
			return camtypes.Location{}, fmt.Errorf("latitude or longitude in corpus for %v is NaN. Reindex to fix it", fileRef)
		}
		return camtypes.Location{Latitude: lat, Longitude: long}, nil
	}
	fi, err := x.GetFileInfo(ctx, fileRef)
	if err != nil {
		return camtypes.Location{}, err
	}
	it := x.queryPrefixString(keyEXIFGPS.Key(fi.WholeRef.String()))
	defer closeIterator(it, &err)
	if !it.Next() {
		return camtypes.Location{}, os.ErrNotExist
	}

	var lat, long float64
	key, v := it.Key(), it.Value()
	pipe := strings.Index(v, "|")
	if pipe < 0 {
		return camtypes.Location{}, fmt.Errorf("index: bogus key %q = %q", key, v)
	}
	lat, err = strconv.ParseFloat(v[:pipe], 64)
	if err != nil {
		return camtypes.Location{}, fmt.Errorf("index: bogus value at position 0 in key %q = %q", key, v)
	}
	long, err = strconv.ParseFloat(v[pipe+1:], 64)
	if err != nil {
		return camtypes.Location{}, fmt.Errorf("index: bogus value at position 1 in key %q = %q", key, v)
	}
	if math.IsNaN(long) || math.IsNaN(lat) {
		return camtypes.Location{}, fmt.Errorf("latitude or longitude in index for %v is NaN. Reindex to fix it", fileRef)
	}
	return camtypes.Location{Latitude: lat, Longitude: long}, nil
}

func (x *Index) EdgesTo(ref blob.Ref, opts *camtypes.EdgesToOpts) (edges []*camtypes.Edge, err error) {
	it := x.queryPrefix(keyEdgeBackward, ref)
	defer closeIterator(it, &err)
	permanodeParents := make(map[string]*camtypes.Edge)
	for it.Next() {
		edge, ok := kvEdgeBackward(it.Key(), it.Value())
		if !ok {
			continue
		}
		if x.IsDeleted(edge.From) {
			continue
		}
		if x.IsDeleted(edge.BlobRef) {
			continue
		}
		edge.To = ref
		if edge.FromType == schema.TypePermanode {
			permanodeParents[edge.From.String()] = edge
		} else {
			edges = append(edges, edge)
		}
	}
	for _, e := range permanodeParents {
		edges = append(edges, e)
	}
	return edges, nil
}

func kvEdgeBackward(k, v string) (edge *camtypes.Edge, ok bool) {
	// TODO(bradfitz): garbage
	keyPart := strings.Split(k, "|")
	valPart := strings.Split(v, "|")
	if len(keyPart) != 4 || len(valPart) != 2 {
		// TODO(mpl): use glog
		log.Printf("bogus keyEdgeBackward index entry: %q = %q", k, v)
		return
	}
	if keyPart[0] != "edgeback" {
		return
	}
	parentRef, ok := blob.Parse(keyPart[2])
	if !ok {
		log.Printf("bogus parent in keyEdgeBackward index entry: %q", keyPart[2])
		return
	}
	blobRef, ok := blob.Parse(keyPart[3])
	if !ok {
		log.Printf("bogus blobref in keyEdgeBackward index entry: %q", keyPart[3])
		return
	}
	return &camtypes.Edge{
		From:      parentRef,
		FromType:  schema.CamliType(valPart[0]),
		FromTitle: valPart[1],
		BlobRef:   blobRef,
	}, true
}

// GetDirMembers sends on dest the children of the static directory dir.
func (x *Index) GetDirMembers(ctx context.Context, dir blob.Ref, dest chan<- blob.Ref, limit int) (err error) {
	defer close(dest)

	sent := 0
	if x.corpus != nil {
		children, err := x.corpus.GetDirChildren(ctx, dir)
		if err != nil {
			return err
		}
		for child := range children {
			dest <- child
			sent++
			if sent == limit {
				break
			}
		}
		return nil
	}

	it := x.queryPrefix(keyStaticDirChild, dir.String())
	defer closeIterator(it, &err)
	for it.Next() {
		keyPart := strings.Split(it.Key(), "|")
		if len(keyPart) != 3 {
			return fmt.Errorf("index: bogus key keyStaticDirChild = %q", it.Key())
		}

		child, ok := blob.Parse(keyPart[2])
		if !ok {
			continue
		}
		dest <- child
		sent++
		if sent == limit {
			break
		}
	}
	return nil
}

func kvBlobMeta(k, v string) (bm camtypes.BlobMeta, ok bool) {
	refStr := k[len("meta:"):]
	br, ok := blob.Parse(refStr)
	if !ok {
		return
	}
	pipe := strings.Index(v, "|")
	if pipe < 0 {
		return
	}
	size, err := strconv.ParseUint(v[:pipe], 10, 32)
	if err != nil {
		return
	}
	return camtypes.BlobMeta{
		Ref:       br,
		Size:      uint32(size),
		CamliType: camliTypeFromMIME(v[pipe+1:]),
	}, true
}

func kvBlobMeta_bytes(k, v []byte) (bm camtypes.BlobMeta, ok bool) {
	ref := k[len("meta:"):]
	br, ok := blob.ParseBytes(ref)
	if !ok {
		return
	}
	pipe := bytes.IndexByte(v, '|')
	if pipe < 0 {
		return
	}
	size, err := strutil.ParseUintBytes(v[:pipe], 10, 32)
	if err != nil {
		return
	}
	return camtypes.BlobMeta{
		Ref:       br,
		Size:      uint32(size),
		CamliType: camliTypeFromMIME_bytes(v[pipe+1:]),
	}, true
}

func enumerateBlobMeta(s sorted.KeyValue, cb func(camtypes.BlobMeta) error) (err error) {
	it := queryPrefixString(s, "meta:")
	defer closeIterator(it, &err)
	for it.Next() {
		bm, ok := kvBlobMeta(it.Key(), it.Value())
		if !ok {
			continue
		}
		if err := cb(bm); err != nil {
			return err
		}
	}
	return nil
}

var errStopIteration = errors.New("stop iteration") // local error, doesn't escape this package

// EnumerateBlobMeta calls fn for all known meta blobs.
// If fn returns false, iteration stops and an nil error is returned.
// If ctx becomes done, iteration stops and ctx.Err() is returned.
func (x *Index) EnumerateBlobMeta(ctx context.Context, fn func(camtypes.BlobMeta) bool) error {
	if x.corpus != nil {
		var err error
		var n int
		done := ctx.Done()
		x.corpus.EnumerateBlobMeta(func(m camtypes.BlobMeta) bool {
			// Every so often, check context.
			n++
			if n%256 == 0 {
				select {
				case <-done:
					err = ctx.Err()
					return false
				default:

				}
			}
			return fn(m)
		})
		return err
	}
	done := ctx.Done()
	err := enumerateBlobMeta(x.s, func(bm camtypes.BlobMeta) error {
		select {
		case <-done:
			return ctx.Err()
		default:
			if !fn(bm) {
				return errStopIteration
			}
			return nil
		}
	})
	if err == errStopIteration {
		err = nil
	}
	return err
}

// Storage returns the index's underlying Storage implementation.
func (x *Index) Storage() sorted.KeyValue { return x.s }

// Close closes the underlying sorted.KeyValue, if the storage has a Close method.
// The return value is the return value of the underlying Close, or
// nil otherwise.
func (x *Index) Close() error {
	if cl, ok := x.s.(io.Closer); ok {
		return cl.Close()
	}
	return nil
}

// initNeededMaps initializes x.needs and x.neededBy on start-up.
func (x *Index) initNeededMaps() (err error) {
	x.deletes = newDeletionCache()
	it := x.queryPrefix(keyMissing)
	defer closeIterator(it, &err)
	for it.Next() {
		key := it.KeyBytes()
		pair := key[len("missing|"):]
		pipe := bytes.IndexByte(pair, '|')
		if pipe < 0 {
			return fmt.Errorf("Bogus missing key %q", key)
		}
		have, ok1 := blob.ParseBytes(pair[:pipe])
		missing, ok2 := blob.ParseBytes(pair[pipe+1:])
		if !ok1 || !ok2 {
			return fmt.Errorf("Bogus missing key %q", key)
		}
		x.noteNeededMemory(have, missing)
	}
	return
}

func (x *Index) noteNeeded(have, missing blob.Ref) error {
	if err := x.s.Set(keyMissing.Key(have, missing), "1"); err != nil {
		return err
	}
	x.noteNeededMemory(have, missing)
	return nil
}

func (x *Index) noteNeededMemory(have, missing blob.Ref) {
	x.needs[have] = append(x.needs[have], missing)
	x.neededBy[missing] = append(x.neededBy[missing], have)
}

const camliTypeMIMEPrefix = "application/json; camliType="

var camliTypeMIMEPrefixBytes = []byte(camliTypeMIMEPrefix)

// "application/json; camliType=file" => "file"
// "image/gif" => ""
func camliTypeFromMIME(mime string) schema.CamliType {
	if v := strings.TrimPrefix(mime, camliTypeMIMEPrefix); v != mime {
		return schema.CamliType(v)
	}
	return ""
}

func camliTypeFromMIME_bytes(mime []byte) schema.CamliType {
	if v := bytes.TrimPrefix(mime, camliTypeMIMEPrefixBytes); len(v) != len(mime) {
		return schema.CamliType(strutil.StringFromBytes(v))
	}
	return ""
}

// TODO(bradfitz): rename this? This is really about signer-attr-value
// (PermanodeOfSignerAttrValue), and not about indexed attributes in general.
func IsIndexedAttribute(attr string) bool {
	switch attr {
	case "camliRoot", "camliImportRoot", "tag", "title":
		return true
	}
	return false
}

// IsBlobReferenceAttribute returns whether attr is an attribute whose
// value is a blob reference (e.g. camliMember) and thus something the
// indexers should keep inverted indexes on for parent/child-type
// relationships.
func IsBlobReferenceAttribute(attr string) bool {
	switch attr {
	case "camliMember":
		return true
	}
	return false
}

func IsFulltextAttribute(attr string) bool {
	switch attr {
	case "tag", "title":
		return true
	}
	return false
}
