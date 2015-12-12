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

package server

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/constants"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/types/camtypes"
	"camlistore.org/third_party/code.google.com/p/xsrftoken"
	"go4.org/jsonconfig"
	"golang.org/x/net/context"

	"go4.org/syncutil"
)

const (
	maxRecentErrors   = 20
	queueSyncInterval = 5 * time.Second
)

type blobReceiverEnumerator interface {
	blobserver.BlobReceiver
	blobserver.BlobEnumerator
}

// The SyncHandler handles async replication in one direction between
// a pair storage targets, a source and target.
//
// SyncHandler is a BlobReceiver but doesn't actually store incoming
// blobs; instead, it records blobs it has received and queues them
// for async replication soon, or whenever it can.
type SyncHandler struct {
	// TODO: rate control tunables
	fromName, toName string
	from             blobserver.Storage
	to               blobReceiverEnumerator
	queue            sorted.KeyValue
	toIndex          bool // whether this sync is from a blob storage to an index
	idle             bool // if true, the handler does nothing other than providing the discovery.
	copierPoolSize   int

	// wakec wakes up the blob syncer loop when a blob is received.
	wakec chan bool

	mu             sync.Mutex // protects following
	status         string
	copying        map[blob.Ref]*copyStatus // to start time
	needCopy       map[blob.Ref]uint32      // blobs needing to be copied. some might be in lastFail too.
	lastFail       map[blob.Ref]failDetail  // subset of needCopy that previously failed, and why
	bytesRemain    int64                    // sum of needCopy values
	recentErrors   []blob.Ref               // up to maxRecentErrors, recent first. valid if still in lastFail.
	recentCopyTime time.Time
	totalCopies    int64
	totalCopyBytes int64
	totalErrors    int64
	vshards        []string // validation shards. if 0, validation not running
	vshardDone     int      // shards validated
	vshardErrs     []string
	vmissing       int64 // missing blobs found during validat
	vdestCount     int   // number of blobs seen on dest during validate
	vdestBytes     int64 // number of blob bytes seen on dest during validate
	vsrcCount      int   // number of blobs seen on src during validate
	vsrcBytes      int64 // number of blob bytes seen on src during validate
}

var (
	_ blobserver.Storage       = (*SyncHandler)(nil)
	_ blobserver.HandlerIniter = (*SyncHandler)(nil)
)

func (sh *SyncHandler) String() string {
	return fmt.Sprintf("[SyncHandler %v -> %v]", sh.fromName, sh.toName)
}

func (sh *SyncHandler) logf(format string, args ...interface{}) {
	log.Printf(sh.String()+" "+format, args...)
}

func init() {
	blobserver.RegisterHandlerConstructor("sync", newSyncFromConfig)
}

// TODO: this is is temporary. should delete, or decide when it's on by default (probably always).
// Then need genconfig option to disable it.
var validateOnStartDefault, _ = strconv.ParseBool(os.Getenv("CAMLI_SYNC_VALIDATE"))

func newSyncFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (http.Handler, error) {
	var (
		from           = conf.RequiredString("from")
		to             = conf.RequiredString("to")
		fullSync       = conf.OptionalBool("fullSyncOnStart", false)
		blockFullSync  = conf.OptionalBool("blockingFullSyncOnStart", false)
		idle           = conf.OptionalBool("idle", false)
		queueConf      = conf.OptionalObject("queue")
		copierPoolSize = conf.OptionalInt("copierPoolSize", 5)
		validate       = conf.OptionalBool("validateOnStart", validateOnStartDefault)
	)
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	if idle {
		return newIdleSyncHandler(from, to), nil
	}
	if len(queueConf) == 0 {
		return nil, errors.New(`Missing required "queue" object`)
	}
	q, err := sorted.NewKeyValue(queueConf)
	if err != nil {
		return nil, err
	}

	isToIndex := false
	fromBs, err := ld.GetStorage(from)
	if err != nil {
		return nil, err
	}
	toBs, err := ld.GetStorage(to)
	if err != nil {
		return nil, err
	}
	if _, ok := fromBs.(*index.Index); !ok {
		if _, ok := toBs.(*index.Index); ok {
			isToIndex = true
		}
	}

	sh := newSyncHandler(from, to, fromBs, toBs, q)
	sh.toIndex = isToIndex
	sh.copierPoolSize = copierPoolSize
	if err := sh.readQueueToMemory(); err != nil {
		return nil, fmt.Errorf("Error reading sync queue to memory: %v", err)
	}

	if fullSync || blockFullSync {
		sh.logf("Doing full sync")
		didFullSync := make(chan bool, 1)
		go func() {
			for {
				n := sh.runSync("pending blobs queue", sh.enumeratePendingBlobs)
				if n > 0 {
					sh.logf("Queue sync copied %d blobs", n)
					continue
				}
				break
			}
			n := sh.runSync("full", blobserverEnumerator(context.TODO(), fromBs))
			sh.logf("Full sync copied %d blobs", n)
			didFullSync <- true
			sh.syncLoop()
		}()
		if blockFullSync {
			sh.logf("Blocking startup, waiting for full sync from %q to %q", from, to)
			<-didFullSync
			sh.logf("Full sync complete.")
		}
	} else {
		go sh.syncLoop()
	}

	if validate {
		go sh.startFullValidation()
	}

	blobserver.GetHub(fromBs).AddReceiveHook(sh.enqueue)
	return sh, nil
}

func (sh *SyncHandler) InitHandler(hl blobserver.FindHandlerByTyper) error {
	_, h, err := hl.FindHandlerByType("root")
	if err == blobserver.ErrHandlerTypeNotFound {
		// It's optional. We register ourselves if it's there.
		return nil
	}
	if err != nil {
		return err
	}
	h.(*RootHandler).registerSyncHandler(sh)
	return nil
}

func newSyncHandler(fromName, toName string,
	from blobserver.Storage, to blobReceiverEnumerator,
	queue sorted.KeyValue) *SyncHandler {
	return &SyncHandler{
		copierPoolSize: 2,
		from:           from,
		to:             to,
		fromName:       fromName,
		toName:         toName,
		queue:          queue,
		wakec:          make(chan bool),
		status:         "not started",
		needCopy:       make(map[blob.Ref]uint32),
		lastFail:       make(map[blob.Ref]failDetail),
		copying:        make(map[blob.Ref]*copyStatus),
	}
}

func newIdleSyncHandler(fromName, toName string) *SyncHandler {
	return &SyncHandler{
		fromName: fromName,
		toName:   toName,
		idle:     true,
		status:   "disabled",
	}
}

func (sh *SyncHandler) discovery() camtypes.SyncHandlerDiscovery {
	return camtypes.SyncHandlerDiscovery{
		From:    sh.fromName,
		To:      sh.toName,
		ToIndex: sh.toIndex,
	}
}

// syncStatus is a snapshot of the current status, for display by the
// status handler (status.go) in both JSON and HTML forms.
type syncStatus struct {
	sh *SyncHandler

	From           string `json:"from"`
	FromDesc       string `json:"fromDesc"`
	To             string `json:"to"`
	ToDesc         string `json:"toDesc"`
	DestIsIndex    bool   `json:"destIsIndex,omitempty"`
	BlobsToCopy    int    `json:"blobsToCopy"`
	BytesToCopy    int64  `json:"bytesToCopy"`
	LastCopySecAgo int    `json:"lastCopySecondsAgo,omitempty"`
}

func (sh *SyncHandler) currentStatus() syncStatus {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	ago := 0
	if !sh.recentCopyTime.IsZero() {
		ago = int(time.Now().Sub(sh.recentCopyTime).Seconds())
	}
	return syncStatus{
		sh:             sh,
		From:           sh.fromName,
		FromDesc:       storageDesc(sh.from),
		To:             sh.toName,
		ToDesc:         storageDesc(sh.to),
		DestIsIndex:    sh.toIndex,
		BlobsToCopy:    len(sh.needCopy),
		BytesToCopy:    sh.bytesRemain,
		LastCopySecAgo: ago,
	}
}

// readQueueToMemory slurps in the pending queue from disk (or
// wherever) to memory.  Even with millions of blobs, it's not much
// memory. The point of the persistent queue is to survive restarts if
// the "fullSyncOnStart" option is off. With "fullSyncOnStart" set to
// true, this is a little pointless (we'd figure out what's missing
// eventually), but this might save us a few minutes (let us start
// syncing missing blobs a few minutes earlier) since we won't have to
// wait to figure out what the destination is missing.
func (sh *SyncHandler) readQueueToMemory() error {
	errc := make(chan error, 1)
	blobs := make(chan blob.SizedRef, 16)
	intr := make(chan struct{})
	defer close(intr)
	go func() {
		errc <- sh.enumerateQueuedBlobs(blobs, intr)
	}()
	n := 0
	for sb := range blobs {
		sh.addBlobToCopy(sb)
		n++
	}
	sh.logf("Added %d pending blobs from sync queue to pending list", n)
	return <-errc
}

func (sh *SyncHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method == "POST" {
		if req.FormValue("mode") == "validate" {
			token := req.FormValue("token")
			if xsrftoken.Valid(token, auth.ProcessRandom(), "user", "runFullValidate") {
				sh.startFullValidation()
				http.Redirect(rw, req, "./", http.StatusFound)
				return
			}
		}
		http.Error(rw, "Bad POST request", http.StatusBadRequest)
		return
	}

	// TODO: remove this lock and instead just call currentStatus,
	// and transition to using that here.
	sh.mu.Lock()
	defer sh.mu.Unlock()
	f := func(p string, a ...interface{}) {
		fmt.Fprintf(rw, p, a...)
	}
	now := time.Now()
	f("<h1>Sync Status (for %s to %s)</h1>", sh.fromName, sh.toName)
	f("<p><b>Current status: </b>%s</p>", html.EscapeString(sh.status))
	if sh.idle {
		return
	}

	f("<h2>Stats:</h2><ul>")
	f("<li>Source: %s</li>", html.EscapeString(storageDesc(sh.from)))
	f("<li>Target: %s</li>", html.EscapeString(storageDesc(sh.to)))
	f("<li>Blobs synced: %d</li>", sh.totalCopies)
	f("<li>Bytes synced: %d</li>", sh.totalCopyBytes)
	f("<li>Blobs yet to copy: %d</li>", len(sh.needCopy))
	f("<li>Bytes yet to copy: %d</li>", sh.bytesRemain)
	if !sh.recentCopyTime.IsZero() {
		f("<li>Most recent copy: %s (%v ago)</li>", sh.recentCopyTime.Format(time.RFC3339), now.Sub(sh.recentCopyTime))
	}
	clarification := ""
	if len(sh.needCopy) == 0 && sh.totalErrors > 0 {
		clarification = "(all since resolved)"
	}
	f("<li>Previous copy errors: %d %s</li>", sh.totalErrors, clarification)
	f("</ul>")

	f("<h2>Validation</h2>")
	if len(sh.vshards) == 0 {
		f("Validation disabled")
		token := xsrftoken.Generate(auth.ProcessRandom(), "user", "runFullValidate")
		f("<form method='POST'><input type='hidden' name='mode' value='validate'><input type='hidden' name='token' value='%s'><input type='submit' value='Start validation'></form>", token)
	} else {
		f("<p>Background scan of source and destination to ensure that the destination has everything the source does, or is at least enqueued to sync.</p>")
		f("<ul>")
		f("<li>Shards complete: %d/%d (%.1f%%)</li>",
			sh.vshardDone,
			len(sh.vshards),
			100*float64(sh.vshardDone)/float64(len(sh.vshards)))
		f("<li>Source blobs seen: %d</li>", sh.vsrcCount)
		f("<li>Source bytes seen: %d</li>", sh.vsrcBytes)
		f("<li>Dest blobs seen: %d</li>", sh.vdestCount)
		f("<li>Dest bytes seen: %d</li>", sh.vdestBytes)
		f("<li>Blobs found missing &amp; enqueued: %d</li>", sh.vmissing)
		if len(sh.vshardErrs) > 0 {
			f("<li>Validation errors: %s</li>", sh.vshardErrs)
		}
		f("</ul>")
	}

	if len(sh.copying) > 0 {
		f("<h2>Currently Copying</h2><ul>")
		copying := make([]blob.Ref, 0, len(sh.copying))
		for br := range sh.copying {
			copying = append(copying, br)
		}
		sort.Sort(blob.ByRef(copying))
		for _, br := range copying {
			f("<li>%s</li>\n", sh.copying[br])
		}
		f("</ul>")
	}

	recentErrors := make([]blob.Ref, 0, len(sh.recentErrors))
	for _, br := range sh.recentErrors {
		if _, ok := sh.needCopy[br]; ok {
			// Only show it in the web UI if it's still a problem. Blobs that
			// have since succeeded just confused people.
			recentErrors = append(recentErrors, br)
		}
	}
	if len(recentErrors) > 0 {
		f("<h2>Recent Errors</h2><p>Blobs that haven't successfully copied over yet, and their last errors:</p><ul>")
		for _, br := range recentErrors {
			fail := sh.lastFail[br]
			f("<li>%s: %s: %s</li>\n",
				br,
				fail.when.Format(time.RFC3339),
				html.EscapeString(fail.err.Error()))
		}
		f("</ul>")
	}
}

func (sh *SyncHandler) setStatusf(s string, args ...interface{}) {
	s = time.Now().UTC().Format(time.RFC3339) + ": " + fmt.Sprintf(s, args...)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	sh.status = s
}

type copyResult struct {
	sb  blob.SizedRef
	err error
}

func blobserverEnumerator(ctx context.Context, src blobserver.BlobEnumerator) func(chan<- blob.SizedRef, <-chan struct{}) error {
	return func(dst chan<- blob.SizedRef, intr <-chan struct{}) error {
		return blobserver.EnumerateAll(ctx, src, func(sb blob.SizedRef) error {
			select {
			case dst <- sb:
			case <-intr:
				return errors.New("interrupted")
			}
			return nil
		})
	}
}

// enumeratePendingBlobs yields blobs from the in-memory pending list (needCopy).
// This differs from enumerateQueuedBlobs, which pulls in the on-disk sorted.KeyValue store.
func (sh *SyncHandler) enumeratePendingBlobs(dst chan<- blob.SizedRef, intr <-chan struct{}) error {
	defer close(dst)
	sh.mu.Lock()
	var toSend []blob.SizedRef
	{
		n := len(sh.needCopy)
		const maxBatch = 1000
		if n > maxBatch {
			n = maxBatch
		}
		toSend = make([]blob.SizedRef, 0, n)
		for br, size := range sh.needCopy {
			toSend = append(toSend, blob.SizedRef{br, size})
			if len(toSend) == n {
				break
			}
		}
	}
	sh.mu.Unlock()
	for _, sb := range toSend {
		select {
		case dst <- sb:
		case <-intr:
			return nil
		}
	}
	return nil
}

// enumerateQueuedBlobs yields blobs from the on-disk sorted.KeyValue store.
// This differs from enumeratePendingBlobs, which sends from the in-memory pending list.
func (sh *SyncHandler) enumerateQueuedBlobs(dst chan<- blob.SizedRef, intr <-chan struct{}) error {
	defer close(dst)
	it := sh.queue.Find("", "")
	for it.Next() {
		br, ok := blob.Parse(it.Key())
		size, err := strconv.ParseUint(it.Value(), 10, 32)
		if !ok || err != nil {
			sh.logf("ERROR: bogus sync queue entry: %q => %q", it.Key(), it.Value())
			continue
		}
		select {
		case dst <- blob.SizedRef{br, uint32(size)}:
		case <-intr:
			return it.Close()
		}
	}
	return it.Close()
}

func (sh *SyncHandler) runSync(syncType string, enumSrc func(chan<- blob.SizedRef, <-chan struct{}) error) int {
	enumch := make(chan blob.SizedRef, 8)
	errch := make(chan error, 1)
	intr := make(chan struct{})
	defer close(intr)
	go func() { errch <- enumSrc(enumch, intr) }()

	nCopied := 0
	toCopy := 0

	workch := make(chan blob.SizedRef, 1000)
	resch := make(chan copyResult, 8)
FeedWork:
	for sb := range enumch {
		if toCopy < sh.copierPoolSize {
			go sh.copyWorker(resch, workch)
		}
		select {
		case workch <- sb:
			toCopy++
		default:
			// Buffer full. Enough for this batch. Will get it later.
			break FeedWork
		}
	}
	close(workch)
	for i := 0; i < toCopy; i++ {
		sh.setStatusf("Copying blobs")
		res := <-resch
		if res.err == nil {
			nCopied++
		}
	}

	if err := <-errch; err != nil {
		sh.logf("error enumerating for %v sync: %v", syncType, err)
	}
	return nCopied
}

func (sh *SyncHandler) syncLoop() {
	for {
		t0 := time.Now()

		for sh.runSync(sh.fromName, sh.enumeratePendingBlobs) > 0 {
			// Loop, before sleeping.
		}
		sh.setStatusf("Sleeping briefly before next long poll.")

		d := queueSyncInterval - time.Since(t0)
		select {
		case <-time.After(d):
		case <-sh.wakec:
		}
	}
}

func (sh *SyncHandler) copyWorker(res chan<- copyResult, work <-chan blob.SizedRef) {
	for sb := range work {
		res <- copyResult{sb, sh.copyBlob(sb)}
	}
}

func (sh *SyncHandler) copyBlob(sb blob.SizedRef) (err error) {
	cs := sh.newCopyStatus(sb)
	defer func() { cs.setError(err) }()
	br := sb.Ref

	sh.mu.Lock()
	sh.copying[br] = cs
	sh.mu.Unlock()

	if sb.Size > constants.MaxBlobSize {
		return fmt.Errorf("blob size %d too large; max blob size is %d", sb.Size, constants.MaxBlobSize)
	}

	cs.setStatus(statusFetching)
	rc, fromSize, err := sh.from.Fetch(br)
	if err != nil {
		return fmt.Errorf("source fetch: %v", err)
	}
	if fromSize != sb.Size {
		rc.Close()
		return fmt.Errorf("source fetch size mismatch: get=%d, enumerate=%d", fromSize, sb.Size)
	}

	buf := make([]byte, fromSize)
	hash := br.Hash()
	cs.setStatus(statusReading)
	n, err := io.ReadFull(io.TeeReader(rc,
		io.MultiWriter(
			incrWriter{cs, &cs.nread},
			hash,
		)), buf)
	rc.Close()
	if err != nil {
		return fmt.Errorf("Read error after %d/%d bytes: %v", n, fromSize, err)
	}
	if !br.HashMatches(hash) {
		return fmt.Errorf("Read data has unexpected digest %x", hash.Sum(nil))
	}

	cs.setStatus(statusWriting)
	newsb, err := sh.to.ReceiveBlob(br, io.TeeReader(bytes.NewReader(buf), incrWriter{cs, &cs.nwrite}))
	if err != nil {
		return fmt.Errorf("dest write: %v", err)
	}
	if newsb.Size != sb.Size {
		return fmt.Errorf("write size mismatch: source_read=%d but dest_write=%d", sb.Size, newsb.Size)
	}
	return nil
}

func (sh *SyncHandler) ReceiveBlob(br blob.Ref, r io.Reader) (sb blob.SizedRef, err error) {
	n, err := io.Copy(ioutil.Discard, r)
	if err != nil {
		return
	}
	sb = blob.SizedRef{br, uint32(n)}
	return sb, sh.enqueue(sb)
}

// addBlobToCopy adds a blob to copy to memory (not to disk: that's enqueue).
// It returns true if it was added, or false if it was a duplicate.
func (sh *SyncHandler) addBlobToCopy(sb blob.SizedRef) bool {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if _, dup := sh.needCopy[sb.Ref]; dup {
		return false
	}

	sh.needCopy[sb.Ref] = sb.Size
	sh.bytesRemain += int64(sb.Size)

	// Non-blocking send to wake up looping goroutine if it's
	// sleeping...
	select {
	case sh.wakec <- true:
	default:
	}
	return true
}

func (sh *SyncHandler) enqueue(sb blob.SizedRef) error {
	if !sh.addBlobToCopy(sb) {
		// Dup
		return nil
	}
	// TODO: include current time in encoded value, to attempt to
	// do in-order delivery to remote side later? Possible
	// friendly optimization later. Might help peer's indexer have
	// less missing deps.
	if err := sh.queue.Set(sb.Ref.String(), fmt.Sprint(sb.Size)); err != nil {
		return err
	}
	return nil
}

func (sh *SyncHandler) startFullValidation() {
	sh.mu.Lock()
	if len(sh.vshards) != 0 {
		sh.mu.Unlock()
		return
	}
	sh.mu.Unlock()

	sh.logf("Running full validation; determining validation shards...")
	shards := sh.shardPrefixes()

	sh.mu.Lock()
	if len(sh.vshards) != 0 {
		sh.mu.Unlock()
		return
	}
	sh.vshards = shards
	sh.mu.Unlock()

	go sh.runFullValidation()
}

func (sh *SyncHandler) runFullValidation() {
	var wg sync.WaitGroup

	sh.mu.Lock()
	shards := sh.vshards
	wg.Add(len(shards))
	sh.mu.Unlock()

	sh.logf("full validation beginning with %d shards", len(shards))

	const maxShardWorkers = 30 // arbitrary
	gate := syncutil.NewGate(maxShardWorkers)

	for _, pfx := range shards {
		pfx := pfx
		gate.Start()
		go func() {
			wg.Done()
			defer gate.Done()
			sh.validateShardPrefix(pfx)
		}()
	}
	wg.Wait()
	sh.logf("Validation complete")
}

func (sh *SyncHandler) validateShardPrefix(pfx string) (err error) {
	defer func() {
		sh.mu.Lock()
		if err != nil {
			errs := fmt.Sprintf("Failed to validate prefix %s: %v", pfx, err)
			sh.logf("%s", errs)
			sh.vshardErrs = append(sh.vshardErrs, errs)
		} else {
			sh.vshardDone++
		}
		sh.mu.Unlock()
	}()
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	src, serrc := sh.startValidatePrefix(ctx, pfx, false)
	dst, derrc := sh.startValidatePrefix(ctx, pfx, true)
	srcErr := &chanError{
		C: serrc,
		Wrap: func(err error) error {
			return fmt.Errorf("Error enumerating source %s for validating shard %s: %v", sh.fromName, pfx, err)
		},
	}
	dstErr := &chanError{
		C: derrc,
		Wrap: func(err error) error {
			return fmt.Errorf("Error enumerating target %s for validating shard %s: %v", sh.toName, pfx, err)
		},
	}

	missingc := make(chan blob.SizedRef, 8)
	go blobserver.ListMissingDestinationBlobs(missingc, func(blob.Ref) {}, src, dst)

	var missing []blob.SizedRef
	for sb := range missingc {
		missing = append(missing, sb)
	}

	if err := srcErr.Get(); err != nil {
		return err
	}
	if err := dstErr.Get(); err != nil {
		return err
	}

	for _, sb := range missing {
		if enqErr := sh.enqueue(sb); enqErr != nil {
			if err == nil {
				err = enqErr
			}
		} else {
			sh.mu.Lock()
			sh.vmissing += 1
			sh.mu.Unlock()
		}
	}
	return err
}

var errNotPrefix = errors.New("sentinel error: hit blob into the next shard")

// doDest is false for source and true for dest.
func (sh *SyncHandler) startValidatePrefix(ctx context.Context, pfx string, doDest bool) (<-chan blob.SizedRef, <-chan error) {
	var e blobserver.BlobEnumerator
	if doDest {
		e = sh.to
	} else {
		e = sh.from
	}
	c := make(chan blob.SizedRef, 64)
	errc := make(chan error, 1)
	go func() {
		defer close(c)
		var last string // last blobref seen; to double check storage's enumeration works correctly.
		err := blobserver.EnumerateAllFrom(ctx, e, pfx, func(sb blob.SizedRef) error {
			// Just double-check that the storage target is returning sorted results correctly.
			brStr := sb.Ref.String()
			if brStr < pfx {
				log.Fatalf("Storage target %T enumerate not behaving: %q < requested prefix %q", e, brStr, pfx)
			}
			if last != "" && last >= brStr {
				log.Fatalf("Storage target %T enumerate not behaving: previous %q >= current %q", e, last, brStr)
			}
			last = brStr

			// TODO: could add a more efficient method on blob.Ref to do this,
			// that doesn't involve call String().
			if !strings.HasPrefix(brStr, pfx) {
				return errNotPrefix
			}
			select {
			case c <- sb:
				sh.mu.Lock()
				if doDest {
					sh.vdestCount++
					sh.vdestBytes += int64(sb.Size)
				} else {
					sh.vsrcCount++
					sh.vsrcBytes += int64(sb.Size)
				}
				sh.mu.Unlock()
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if err == errNotPrefix {
			err = nil
		}
		if err != nil {
			// Send a zero value to shut down ListMissingDestinationBlobs.
			c <- blob.SizedRef{}
		}
		errc <- err
	}()
	return c, errc
}

func (sh *SyncHandler) shardPrefixes() []string {
	var pfx []string
	// TODO(bradfitz): do limit=1 enumerates against sh.from and sh.to with varying
	// "after" values to determine all the blobref types on both sides.
	// For now, be lazy and assume only sha1:
	for i := 0; i < 256; i++ {
		pfx = append(pfx, fmt.Sprintf("sha1-%02x", i))
	}
	return pfx
}

func (sh *SyncHandler) newCopyStatus(sb blob.SizedRef) *copyStatus {
	now := time.Now()
	return &copyStatus{
		sh:    sh,
		sb:    sb,
		state: statusStarting,
		start: now,
		t:     now,
	}
}

// copyStatus is an in-progress copy.
type copyStatus struct {
	sh    *SyncHandler
	sb    blob.SizedRef
	start time.Time

	mu     sync.Mutex
	state  string    // one of statusFoo, below
	t      time.Time // last status update time
	nread  uint32
	nwrite uint32
}

const (
	statusStarting = "starting"
	statusFetching = "fetching source"
	statusReading  = "reading"
	statusWriting  = "writing"
)

func (cs *copyStatus) setStatus(s string) {
	now := time.Now()
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.state = s
	cs.t = now
}

func (cs *copyStatus) setError(err error) {
	now := time.Now()
	sh := cs.sh
	br := cs.sb.Ref
	if err == nil {
		// This is somewhat slow, so do it before we acquire the lock.
		// The queue is thread-safe.
		if derr := sh.queue.Delete(br.String()); derr != nil {
			sh.logf("queue delete of %v error: %v", cs.sb.Ref, derr)
		}
	}

	sh.mu.Lock()
	defer sh.mu.Unlock()
	if _, needCopy := sh.needCopy[br]; !needCopy {
		sh.logf("IGNORING DUPLICATE UPLOAD of %v = %v", br, err)
		return
	}
	delete(sh.copying, br)
	if err == nil {
		delete(sh.needCopy, br)
		delete(sh.lastFail, br)
		sh.recentCopyTime = now
		sh.totalCopies++
		sh.totalCopyBytes += int64(cs.sb.Size)
		sh.bytesRemain -= int64(cs.sb.Size)
		return
	}

	sh.totalErrors++
	sh.logf("error copying %v: %v", br, err)
	sh.lastFail[br] = failDetail{
		when: now,
		err:  err,
	}

	// Kinda lame. TODO: use a ring buffer or container/list instead.
	if len(sh.recentErrors) == maxRecentErrors {
		copy(sh.recentErrors[1:], sh.recentErrors)
		sh.recentErrors = sh.recentErrors[:maxRecentErrors-1]
	}
	sh.recentErrors = append(sh.recentErrors, br)
}

func (cs *copyStatus) String() string {
	var buf bytes.Buffer
	now := time.Now()
	buf.WriteString(cs.sb.Ref.String())
	buf.WriteString(": ")

	cs.mu.Lock()
	defer cs.mu.Unlock()
	sinceStart := now.Sub(cs.start)
	sinceLast := now.Sub(cs.t)

	switch cs.state {
	case statusReading:
		buf.WriteString(cs.state)
		fmt.Fprintf(&buf, " (%d/%dB)", cs.nread, cs.sb.Size)
	case statusWriting:
		if cs.nwrite == cs.sb.Size {
			buf.WriteString("wrote all, waiting ack")
		} else {
			buf.WriteString(cs.state)
			fmt.Fprintf(&buf, " (%d/%dB)", cs.nwrite, cs.sb.Size)
		}
	default:
		buf.WriteString(cs.state)

	}
	if sinceLast > 5*time.Second {
		fmt.Fprintf(&buf, ", last change %v ago (total elapsed %v)", sinceLast, sinceStart)
	}
	return buf.String()
}

type failDetail struct {
	when time.Time
	err  error
}

// incrWriter is an io.Writer that locks mu and increments *n.
type incrWriter struct {
	cs *copyStatus
	n  *uint32
}

func (w incrWriter) Write(p []byte) (n int, err error) {
	w.cs.mu.Lock()
	*w.n += uint32(len(p))
	w.cs.t = time.Now()
	w.cs.mu.Unlock()
	return len(p), nil
}

func storageDesc(v interface{}) string {
	if s, ok := v.(fmt.Stringer); ok {
		return s.String()
	}
	return fmt.Sprintf("%T", v)
}

// TODO(bradfitz): implement these? what do they mean? possibilities:
// a) proxy to sh.from
// b) proxy to sh.to
// c) merge intersection of sh.from, sh.to, and sh.queue: that is, a blob this pair
//    currently or eventually will have. The only missing blob would be one that
//    sh.from has, sh.to doesn't have, and isn't in the queue to be replicated.
//
// For now, don't implement them. Wait until we need them.

func (sh *SyncHandler) Fetch(blob.Ref) (file io.ReadCloser, size uint32, err error) {
	panic("Unimplemeted blobserver.Fetch called")
}

func (sh *SyncHandler) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	sh.logf("Unexpected StatBlobs call")
	return nil
}

func (sh *SyncHandler) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	sh.logf("Unexpected EnumerateBlobs call")
	return nil
}

func (sh *SyncHandler) RemoveBlobs(blobs []blob.Ref) error {
	panic("Unimplemeted RemoveBlobs")
}

// chanError is a Future around an incoming error channel of one item.
// It can also wrap its error in something more descriptive.
type chanError struct {
	C        <-chan error
	Wrap     func(error) error // optional
	err      error
	received bool
}

func (ce *chanError) Set(err error) {
	if ce.Wrap != nil && err != nil {
		err = ce.Wrap(err)
	}
	ce.err = err
	ce.received = true
}

func (ce *chanError) Get() error {
	if ce.received {
		return ce.err
	}
	ce.Set(<-ce.C)
	return ce.err
}
