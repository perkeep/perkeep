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

package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go4.org/jsonconfig"
	"golang.org/x/net/xsrftoken"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/constants"
	"perkeep.org/pkg/health"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/types/camtypes"

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
	vshardDone     int      // shards already processed (incl. errors)
	vshardErrs     []string
	vmissing       int64    // missing blobs found during validate
	vdestCount     int      // number of blobs seen on dest during validate
	vdestBytes     int64    // number of blob bytes seen on dest during validate
	vsrcCount      int      // number of blobs seen on src during validate
	vsrcBytes      int64    // number of blob bytes seen on src during validate
	comparedBlobs  int      // total number of blobs compared by hourly runs
	comparedBytes  uint64   // total number of bytes compared by hourly runs
	comparedRounds int      // total number of hourly compare runs
	compareErrors  []string // all errors encountered by hourly runs
	compLastBlob   string   // last blob compared by hourly runs

	// The health checks of the handler, or nil if it has no health
	// monitor (see newSyncFromConfig).
	health        *health.Monitor
	validateCheck *health.Check // runs validate
	validateID    string        // of validateCheck
	compareCheck  *health.Check // runs compareRound, if the destination is a Fetcher
	sampleCheck   *health.Check // runs sample, if the destination isn't an index
	copyCheck     *health.Check // reports blobs that fail to copy
	statusPrefix  string        // of the status handler, if any, which shows the checks

	// syncLoop tries to send on alarmIdlec each time we've slept for a full
	// queueSyncInterval. Initialized as a synchronous chan if we're not an
	// idle sync handler, otherwise nil.
	alarmIdlec chan struct{}
}

var (
	_ blobserver.Storage       = (*SyncHandler)(nil)
	_ blobserver.HandlerIniter = (*SyncHandler)(nil)
)

func (sh *SyncHandler) String() string {
	return fmt.Sprintf("[SyncHandler %v -> %v]", sh.fromName, sh.toName)
}

func (sh *SyncHandler) fromToString() string {
	return fmt.Sprintf("%v -> %v", sh.fromName, sh.toName)
}

func (sh *SyncHandler) logf(format string, args ...any) {
	log.Printf("sync: "+sh.fromToString()+": "+format, args...)
}

func init() {
	blobserver.RegisterHandlerConstructor("sync", newSyncFromConfig)
}

// TODO: this is is temporary. should delete, or decide when it's on by default (probably always).
// Then need genconfig option to disable it.
var validateOnStartDefault, _ = strconv.ParseBool(os.Getenv("CAMLI_SYNC_VALIDATE"))

func newSyncFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (http.Handler, error) {
	prefix := ld.MyPrefix() // before loading other storage changes it
	var (
		from           = conf.RequiredString("from")
		to             = conf.RequiredString("to")
		fullSync       = conf.OptionalBool("fullSyncOnStart", false)
		blockFullSync  = conf.OptionalBool("blockingFullSyncOnStart", false)
		idle           = conf.OptionalBool("idle", false)
		queueConf      = conf.OptionalObject("queue")
		copierPoolSize = conf.OptionalInt("copierPoolSize", 5)
		validate       = conf.OptionalBool("validateOnStart", validateOnStartDefault)
		hourlyCompare  = conf.OptionalInt("hourlyCompareBytes", 0)
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
	q, err := sorted.NewKeyValueMaybeWipe(queueConf)
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
		return nil, fmt.Errorf("Error reading sync queue to memory: %w", err)
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

	_, toFetcher := sh.to.(blob.Fetcher)
	if hourlyCompare != 0 && !toFetcher {
		return nil, errors.New(`can't specify "hourlyCompareBytes" if destination is not a Fetcher`)
	}
	if mon := ld.Health(); mon != nil {
		sh.registerChecks(mon, prefix, validate, toFetcher, hourlyCompare)
	} else {
		if validate {
			go sh.startFullValidation()
		}
		if hourlyCompare != 0 {
			go sh.hourlyCompare(uint64(hourlyCompare))
		}
	}

	blobserver.GetHub(fromBs).AddReceiveBlobsHook(sh.enqueueBlobs)
	return sh, nil
}

func (sh *SyncHandler) InitHandler(hl blobserver.FindHandlerByTyper) error {
	if pfx, _, err := hl.FindHandlerByType("status"); err == nil {
		sh.statusPrefix = pfx
	}
	_, h, err := hl.FindHandlerByType("root")
	if errors.Is(err, blobserver.ErrHandlerTypeNotFound) {
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
		copierPoolSize: 5,
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
		alarmIdlec:     make(chan struct{}),
	}
}

// NewSyncHandler returns a handler that will asynchronously and continuously
// copy blobs from src to dest, if missing on dest.
// Blobs waiting to be copied are stored on pendingQueue. srcName and destName are
// only used for status and debugging messages.
// N.B: blobs should be added to src with a method that notifies the blob hub,
// such as blobserver.Receive.
func NewSyncHandler(srcName, destName string,
	src blobserver.Storage, dest blobReceiverEnumerator,
	pendingQueue sorted.KeyValue) *SyncHandler {
	sh := newSyncHandler(srcName, destName, src, dest, pendingQueue)
	go sh.syncLoop()
	blobserver.GetHub(sh.from).AddReceiveBlobsHook(sh.enqueueBlobs)
	return sh
}

// IdleWait waits until the sync handler has finished processing the currently
// queued blobs.
func (sh *SyncHandler) IdleWait() {
	if sh.idle {
		return
	}
	<-sh.alarmIdlec
}

func (sh *SyncHandler) signalIdle() {
	select {
	case sh.alarmIdlec <- struct{}{}:
	default:
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
		ago = int(time.Since(sh.recentCopyTime) / time.Second)
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
	sh.logf("added %d pending blobs from sync queue to pending list", n)
	return <-errc
}

func (sh *SyncHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method == "POST" {
		if req.FormValue("mode") == "validate" {
			token := req.FormValue("token")
			if xsrftoken.Valid(token, auth.Token(), "user", "runFullValidate") {
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
	f := func(p string, a ...any) {
		fmt.Fprintf(rw, p, a...)
	}
	now := time.Now()
	f("<h1>Sync Status (for %s to %s)</h1>", sh.fromName, sh.toName)
	f("<p><b>Current status: </b>%s</p>", html.EscapeString(sh.status))
	if sh.idle {
		return
	}
	if sh.health != nil && sh.statusPrefix != "" {
		f("<p>This sync's checks are also on the <a href='%s#health'>status page</a>, where you can run them, see their progress, and change how often they run.</p>", sh.statusPrefix)
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

	if sh.comparedRounds > 0 || sh.comparedBlobs > 0 {
		f("<h2>Hourly compares</h2><ul>")
		f("<li>Hourly rounds: %d</li>", sh.comparedRounds)
		f("<li>Compared blobs: %d</li>", sh.comparedBlobs)
		f("<li>Compared bytes: %d</li>", sh.comparedBytes)
		f("<li>Latest blob: %s</li>", sh.compLastBlob)
		f("</ul>")
		if len(sh.compareErrors) > 0 {
			f("<h3>Compare failures</h3><ul>")
			for _, err := range sh.compareErrors {
				f("<li><strong>%s</strong></li>", err)
			}
			f("</ul>")
		}
	}

	f("<h2>Validation</h2>")
	f("<p>Background scan of source and destination to ensure that the destination has everything the source does, or is at least enqueued to sync.</p>")
	if sh.validateCheck != nil {
		if sh.statusPrefix != "" {
			f("<p>It's the <a href='%s#health'>%s</a> health check, which validates one shard at a time, those that most need it first.</p>", sh.statusPrefix, html.EscapeString(sh.validateID))
		}
	} else if len(sh.vshards) == 0 || sh.vshardDone == len(sh.vshards) {
		token := xsrftoken.Generate(auth.Token(), "user", "runFullValidate")
		f("<form method='POST'><input type='hidden' name='mode' value='validate'><input type='hidden' name='token' value='%s'><input type='submit' value='Start validation'></form>", token)
	}
	if len(sh.vshards) != 0 {
		f("<ul>")
		f("<li>Shards processed: %d/%d (%.1f%%)</li>",
			sh.vshardDone,
			len(sh.vshards),
			100*float64(sh.vshardDone)/float64(len(sh.vshards)))
		f("<li>Source blobs seen: %d</li>", sh.vsrcCount)
		f("<li>Source bytes seen: %d</li>", sh.vsrcBytes)
		f("<li>Dest blobs seen: %d</li>", sh.vdestCount)
		f("<li>Dest bytes seen: %d</li>", sh.vdestBytes)
		f("<li>Blobs found missing &amp; enqueued: %d</li>", sh.vmissing)
		if len(sh.vshardErrs) > 0 {
			f("<li>Validation errors:<ul>\n")
			for _, e := range sh.vshardErrs {
				f("  <li>%s</li>\n", html.EscapeString(e))
			}
			f("</li>\n")
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

func (sh *SyncHandler) setStatusf(s string, args ...any) {
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
			toSend = append(toSend, blob.SizedRef{Ref: br, Size: size})
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
	for r := range sorted.ScanPaged(sh.queue, "", "") {
		row, err := r.Value()
		if err != nil {
			return err
		}
		k, v := row.Key, row.Value
		br, ok := blob.Parse(k)
		size, err := strconv.ParseUint(v, 10, 32)
		if !ok || err != nil {
			sh.logf("ERROR: bogus sync queue entry: %q => %q", k, v)
			continue
		}
		select {
		case dst <- blob.SizedRef{Ref: br, Size: uint32(size)}:
		case <-intr:
			return nil
		}
	}
	return nil
}

func (sh *SyncHandler) runSync(syncType string, enumSrc func(chan<- blob.SizedRef, <-chan struct{}) error) int {
	enumch := make(chan blob.SizedRef, 8)
	errch := make(chan error, 1)
	intr := make(chan struct{})
	go func() { errch <- enumSrc(enumch, intr) }()

	nCopied := 0
	toCopy := 0

	workch := make(chan blob.SizedRef, 1000)
	resch := make(chan copyResult, 8)
	// Copy workers copy blobs in parallel, then hand them to
	// finishCopies, which deletes the copied blobs' queue entries in
	// batches.
	copiedc := make(chan copiedBlob, cap(workch))
	var workers sync.WaitGroup
	go sh.finishCopies(copiedc, resch)
FeedWork:
	for sb := range enumch {
		if toCopy < sh.copierPoolSize {
			workers.Go(func() { sh.copyWorker(copiedc, workch) })
		}
		select {
		case workch <- sb:
			toCopy++
		default:
			// Buffer full. Enough for this batch. Will get it later.
			break FeedWork
		}
	}
	// If the work buffer filled, stop the enumerator before waiting for its
	// result: it may be blocked sending blobs we will no longer receive.
	close(intr)
	close(workch)
	go func() {
		workers.Wait()
		close(copiedc)
	}()
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
			sh.signalIdle()
		case <-sh.wakec:
		}
	}
}

// A copiedBlob is the result of copying a blob, not yet finished.
type copiedBlob struct {
	cs  *copyStatus
	err error
}

func (sh *SyncHandler) copyWorker(copied chan<- copiedBlob, work <-chan blob.SizedRef) {
	for sb := range work {
		cs := sh.newCopyStatus(sb)
		copied <- copiedBlob{cs, sh.copyBlob(context.TODO(), cs)}
	}
}

// finishCopyGroup is how many copied blobs finishCopies deletes from the
// queue at a time, at most.
const finishCopyGroup = 256

// finishCopies finishes the blobs from copied, sending their results to
// res. It deletes the queue entries of those copied in batches: as many
// as have been copied, up to finishCopyGroup, rather than syncing a
// delete for each, which uploads' enqueues would wait behind.
func (sh *SyncHandler) finishCopies(copied <-chan copiedBlob, res chan<- copyResult) {
	for first := range copied {
		group := []copiedBlob{first}
	Fill:
		for len(group) < finishCopyGroup {
			select {
			case c, ok := <-copied:
				if !ok {
					break Fill
				}
				group = append(group, c)
			default:
				break Fill
			}
		}
		batch := sh.queue.BeginBatch()
		n := 0
		for _, c := range group {
			if c.err == nil {
				batch.Delete(c.cs.sb.Ref.String())
				n++
			}
		}
		// A blob whose delete is lost stays queued, so it's copied again
		// later; the destination already has it.
		if n > 0 {
			if err := sh.queue.CommitBatch(batch); err != nil {
				sh.logf("queue delete of %d copied blobs error: %v", n, err)
			}
		}
		for _, c := range group {
			c.cs.finish(c.err)
			res <- copyResult{c.cs.sb, c.err}
		}
	}
}

// copyBlob copies cs's blob to the destination, without finishing it.
func (sh *SyncHandler) copyBlob(ctx context.Context, cs *copyStatus) (err error) {
	sb := cs.sb
	br := sb.Ref

	sh.mu.Lock()
	sh.copying[br] = cs
	sh.mu.Unlock()

	if sb.Size > constants.MaxBlobSize {
		return fmt.Errorf("blob size %d too large; max blob size is %d", sb.Size, constants.MaxBlobSize)
	}

	cs.setStatus(statusFetching)
	rc, fromSize, err := sh.from.Fetch(ctx, br)
	if err != nil {
		return fmt.Errorf("source fetch: %w", err)
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
		return fmt.Errorf("Read error after %d/%d bytes: %w", n, fromSize, err)
	}
	if !br.HashMatches(hash) {
		return fmt.Errorf("Read data has unexpected digest %x", hash.Sum(nil))
	}

	cs.setStatus(statusWriting)
	newsb, err := sh.to.ReceiveBlob(ctx, br, io.TeeReader(bytes.NewReader(buf), incrWriter{cs, &cs.nwrite}))
	if err != nil {
		return fmt.Errorf("dest write: %w", err)
	}
	if newsb.Size != sb.Size {
		return fmt.Errorf("write size mismatch: source_read=%d but dest_write=%d", sb.Size, newsb.Size)
	}
	return nil
}

func (sh *SyncHandler) ReceiveBlob(ctx context.Context, br blob.Ref, r io.Reader) (sb blob.SizedRef, err error) {
	// TODO: use ctx?
	n, err := io.Copy(io.Discard, r)
	if err != nil {
		return
	}
	sb = blob.SizedRef{Ref: br, Size: uint32(n)}
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

// enqueue durably adds a blob to the queue, and then to memory.
// Everything in needCopy has thus been written to the queue, so a blob
// already there needs no write.
func (sh *SyncHandler) enqueue(sb blob.SizedRef) error {
	sh.mu.Lock()
	_, dup := sh.needCopy[sb.Ref]
	sh.mu.Unlock()
	if dup {
		return nil
	}
	// TODO: include current time in encoded value, to attempt to
	// do in-order delivery to remote side later? Possible
	// friendly optimization later. Might help peer's indexer have
	// less missing deps.
	if err := sh.queue.Set(sb.Ref.String(), fmt.Sprint(sb.Size)); err != nil {
		return err
	}
	sh.addBlobToCopy(sb)
	return nil
}

// enqueueBlobs is like enqueue for several blobs, writing the new ones to
// the queue with one batch, so they share its sync to disk. It's the
// receive hook for the source's blob hub, and returns an error as
// described by blobserver.BlobsReceiver.
func (sh *SyncHandler) enqueueBlobs(sbs []blob.SizedRef) error {
	if len(sbs) == 1 {
		return sh.enqueue(sbs[0])
	}
	var todo []blob.SizedRef
	seen := make(map[blob.Ref]bool, len(sbs))
	sh.mu.Lock()
	for _, sb := range sbs {
		if _, dup := sh.needCopy[sb.Ref]; !dup && !seen[sb.Ref] {
			seen[sb.Ref] = true
			todo = append(todo, sb)
		}
	}
	sh.mu.Unlock()
	if len(todo) == 0 {
		return nil
	}
	batch := sh.queue.BeginBatch()
	for _, sb := range todo {
		batch.Set(sb.Ref.String(), fmt.Sprint(sb.Size))
	}
	if err := sh.queue.CommitBatch(batch); err != nil {
		be := &blobserver.BlobsError{Op: "enqueue"}
		for _, sb := range todo {
			be.Add(sb.Ref, err)
		}
		return be
	}
	for _, sb := range todo {
		sh.addBlobToCopy(sb)
	}
	return nil
}

// compareBytesDefault is how many bytes a compare round fetches from the
// destination when it's run from the status page without the
// "hourlyCompareBytes" config option, kept small for storage that
// charges for reads.
const compareBytesDefault = 16 << 20

// registerChecks registers the handler's health checks with mon, under
// the handler's prefix. Validation runs at startup if validate is set,
// and compares (if the destination is a Fetcher) run hourly if
// hourlyCompare, their size in bytes, is non-zero.
func (sh *SyncHandler) registerChecks(mon *health.Monitor, prefix string, validate, toFetcher bool, hourlyCompare int) {
	sh.health = mon
	desc := sh.fromToString()

	sh.validateID = health.ID("sync.validate", prefix)
	sh.validateCheck = mon.Register(sh.validateID, "sync "+desc+": the destination has every blob in the source")
	def := health.ScheduleOff
	if validate {
		def = health.ScheduleStartup
	}
	shards := func(ctx context.Context) []string {
		hashes := sh.sourceHashes(ctx)
		sh.validateCheck.SetProgressHint(fmt.Sprintf("shards that don't need checking, of %d: 256 digest prefixes × {%s}, the source's hash types", 256*len(hashes), strings.Join(hashes, ", ")))
		return shardPrefixes(hashes)
	}
	const maxShardWorkers = 30 // arbitrary
	sh.validateCheck.SetShardedRunner(shards, sh.validateShard, maxShardWorkers, def)

	if toFetcher {
		sh.compareCheck = mon.Register(health.ID("sync.compare", prefix), "sync "+desc+": a sample of the destination's blobs match their digests")
		def, size := health.ScheduleOff, uint64(compareBytesDefault)
		if hourlyCompare != 0 {
			def, size = health.ScheduleHourly, uint64(hourlyCompare)
		}
		sh.compareCheck.SetProgressHint(fmt.Sprintf("bytes compared, of at most %d", size))
		sh.compareCheck.SetRunner(func(ctx context.Context) error {
			return sh.compareRound(ctx, size)
		}, def)
	}

	if !sh.toIndex {
		sh.sampleCheck = mon.Register(health.ID("sync.sample", prefix), "sync "+desc+": a random sample of the source's blobs are in the destination")
		sh.sampleCheck.SetProgressHint(fmt.Sprintf("source blobs checked, of at most %d, for at most %v", sampleMaxBlobs, sampleDuration))
		sh.sampleCheck.SetRunner(sh.sample, health.ScheduleOff)
	}

	sh.copyCheck = mon.Register(health.ID("sync.copy", prefix), "sync "+desc+": the source's new blobs are copying to the destination without errors")
	sh.copyCheck.OK()

	_, fromPriced := blobserver.PricingOf(sh.from)
	_, toPriced := blobserver.PricingOf(sh.to)
	if fromPriced || toPriced {
		sh.validateCheck.SetCostEstimator(sh.validateCost)
		sh.sampleCheck.SetCostEstimator(sh.sampleCost)
		if sh.compareCheck != nil {
			size := uint64(compareBytesDefault)
			if hourlyCompare != 0 {
				size = uint64(hourlyCompare)
			}
			sh.compareCheck.SetCostEstimator(func() string { return sh.compareCost(size) })
		}
	}
}

// census returns the storage census's estimates of the blobs in the
// storage at prefix, or false if it hasn't counted them.
func (sh *SyncHandler) census(prefix string) ([]blobserver.HashEstimate, bool) {
	ests, ok := sh.health.Details(blobserver.CensusID(prefix)).([]blobserver.HashEstimate)
	return ests, ok
}

// pricedSides returns the source and destination storage, with their
// prefixes and pricing, if known.
func (sh *SyncHandler) pricedSides() (sides []pricedSide) {
	for _, side := range []struct {
		prefix string
		sto    any
	}{{sh.fromName, sh.from}, {sh.toName, sh.to}} {
		if p, ok := blobserver.PricingOf(side.sto); ok {
			sides = append(sides, pricedSide{side.prefix, p, side.sto == any(sh.to)})
		}
	}
	return sides
}

type pricedSide struct {
	prefix string
	p      blobserver.Pricing
	isDest bool
}

// unknownCost is the cost estimate of an operation on storage the census
// hasn't counted.
func unknownCost(prefix string) string {
	return "unknown until the storage census has counted the blobs in " + prefix
}

// validateCost estimates what a full pass of sync.validate costs: on
// each side with a price, listing each shard, in as many requests as its
// blobs need.
func (sh *SyncHandler) validateCost() string {
	srcEsts, ok := sh.census(sh.fromName)
	if !ok {
		return unknownCost(sh.fromName)
	}
	var e blobserver.CostEstimate
	for _, side := range sh.pricedSides() {
		ests, ok := sh.census(side.prefix)
		if !ok {
			return unknownCost(side.prefix)
		}
		var blobs, requests int64
		for _, src := range srcEsts {
			if src.Blobs == 0 {
				continue
			}
			hash := src.Hash
			var n int64
			for _, est := range ests {
				if est.Hash == hash {
					n = est.Blobs
				}
			}
			const shards = 256 // digest prefixes per hash type; see shardPrefixes
			perShard := (n/shards + int64(side.p.ListPage) - 1) / int64(side.p.ListPage)
			requests += shards * max(1, perShard)
			blobs += n
		}
		e.AddListing(side.p, blobs, requests)
	}
	return "a full pass: " + e.String()
}

// sampleCost estimates what sync.sample costs: on the source, if it has
// a price, listing the sample (and the estimate of its hash types that
// picks where it starts); on the destination, if it has a price,
// statting each blob in the sample.
func (sh *SyncHandler) sampleCost() string {
	n := int64(sampleMaxBlobs)
	if ests, ok := sh.census(sh.fromName); ok {
		var total int64
		for _, est := range ests {
			total += est.Blobs
		}
		n = min(n, total)
	}
	var e blobserver.CostEstimate
	for _, side := range sh.pricedSides() {
		if side.isDest {
			e.AddGets(side.p, n, 0)
			continue
		}
		types := int64(len(blob.HashFuncs()))
		e.AddListing(side.p, n+100*types, n/int64(side.p.ListPage)+1+types)
	}
	return e.String()
}

// compareCost estimates what a sync.compare round of size bytes costs:
// on the source, if it has a price, listing the blobs; on the
// destination, if it has a price, fetching them.
func (sh *SyncHandler) compareCost(size uint64) string {
	ests, ok := sh.census(sh.toName)
	if !ok {
		return unknownCost(sh.toName)
	}
	var blobs, bytes int64
	for _, est := range ests {
		blobs += est.Blobs
		bytes += est.Bytes
	}
	n := int64(1)
	if blobs > 0 && bytes > 0 {
		n = max(1, int64(size)/(bytes/blobs))
	}
	var e blobserver.CostEstimate
	for _, side := range sh.pricedSides() {
		if side.isDest {
			e.AddGets(side.p, n, int64(size))
			continue
		}
		types := int64(len(blob.HashFuncs()))
		e.AddListing(side.p, n+100*types, n/int64(side.p.ListPage)+1+types)
	}
	return "a round of " + blobserver.HashEstimate{Bytes: int64(size)}.BytesString() + ": " + e.String()
}

// errValidating is returned by validate when a validation is already
// running.
var errValidating = errors.New("a validation is already running")

// startFullValidation starts a full validation in the background, unless
// one is running.
func (sh *SyncHandler) startFullValidation() {
	if sh.validateCheck != nil {
		if err := sh.health.RunNow(sh.validateID); err != nil {
			sh.logf("not starting validation: %v", err)
		}
		return
	}
	go func() {
		if err := sh.validate(context.Background()); err != nil && !errors.Is(err, errValidating) {
			sh.logf("validation: %v", err)
		}
	}()
}

// validate checks that the destination has every blob in the source, or
// that it's queued to copy, and queues any it's missing, for a handler
// without a health monitor (which instead validates one shard at a time
// with validateShard). The sync page shows its progress.
func (sh *SyncHandler) validate(ctx context.Context) error {
	sh.mu.Lock()
	if len(sh.vshards) != 0 && sh.vshardDone < len(sh.vshards) {
		sh.mu.Unlock()
		return errValidating
	}
	sh.vshards, sh.vshardErrs = nil, nil
	sh.vshardDone, sh.vmissing = 0, 0
	sh.vdestCount, sh.vdestBytes = 0, 0
	sh.vsrcCount, sh.vsrcBytes = 0, 0
	shards := shardPrefixes(sh.sourceHashes(ctx))
	sh.vshards = shards
	sh.mu.Unlock()

	sh.logf("full validation beginning with %d shards...", len(shards))
	sh.runFullValidation(ctx, shards)

	sh.mu.Lock()
	errs, missing := sh.vshardErrs, sh.vmissing
	// Mark any shards not started because of cancelation as done.
	sh.vshardDone = len(sh.vshards)
	sh.mu.Unlock()
	if err := ctx.Err(); err != nil {
		sh.logf("validation canceled")
		return err
	}
	sh.logf("validation complete")
	if len(errs) > 0 {
		return fmt.Errorf("%d of %d shards failed to validate; the first: %s", len(errs), len(shards), errs[0])
	}
	if missing > 0 {
		return fmt.Errorf("found %d blobs missing from the destination, and queued them to copy", missing)
	}
	return nil
}

func (sh *SyncHandler) runFullValidation(ctx context.Context, shards []string) {
	var wg sync.WaitGroup
	const maxShardWorkers = 30 // arbitrary
	gate := syncutil.NewGate(maxShardWorkers)
	for _, pfx := range shards {
		if ctx.Err() != nil {
			break
		}
		gate.Start()
		wg.Go(func() {
			defer gate.Done()
			sh.validateShardPrefix(ctx, pfx)
		})
	}
	wg.Wait()
}

// validateShardPrefix runs validatePrefix for a validation of all the
// shards by validate, recording its progress and results for the sync
// page.
func (sh *SyncHandler) validateShardPrefix(ctx context.Context, pfx string) {
	missing, err := sh.validatePrefix(ctx, pfx)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if err != nil && ctx.Err() == nil {
		errs := fmt.Sprintf("Failed to validate prefix %s: %v", pfx, err)
		sh.logf("%s", errs)
		sh.vshardErrs = append(sh.vshardErrs, errs)
	}
	sh.vshardDone++
	sh.vmissing += int64(missing)
}

// validateShard validates one shard for sh.validateCheck, a sharded
// health check, reporting any blobs it found missing as a problem.
func (sh *SyncHandler) validateShard(ctx context.Context, pfx string) (problem string, err error) {
	missing, err := sh.validatePrefix(ctx, pfx)
	if err != nil {
		return "", err
	}
	if missing > 0 {
		return fmt.Sprintf("found %d blobs missing from the destination; queued them to copy", missing), nil
	}
	return "", nil
}

// validatePrefix checks that the destination has every blob in the
// source whose ref starts with pfx, queues any that it doesn't (and
// that aren't already queued) to copy, and returns how many it queued.
func (sh *SyncHandler) validatePrefix(parent context.Context, pfx string) (queued int, err error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	src, serrc := sh.startValidatePrefix(ctx, pfx, false)
	dst, derrc := sh.startValidatePrefix(ctx, pfx, true)
	srcErr := &chanError{
		C: serrc,
		Wrap: func(err error) error {
			return fmt.Errorf("Error enumerating source %s for validating shard %s: %w", sh.fromName, pfx, err)
		},
	}
	dstErr := &chanError{
		C: derrc,
		Wrap: func(err error) error {
			return fmt.Errorf("Error enumerating target %s for validating shard %s: %w", sh.toName, pfx, err)
		},
	}

	missingc := make(chan blob.SizedRef, 8)
	go blobserver.ListMissingDestinationBlobs(missingc, func(blob.Ref) {}, src, dst)

	var missing []blob.SizedRef
	for sb := range missingc {
		missing = append(missing, sb)
	}
	// The comparison is done, or one side's listing stopped it early, so
	// the other side's listing (such as the destination's, which may go on
	// past the source's last blob) may still be sending; stop it.
	cancel()
	if err := parent.Err(); err != nil {
		return 0, err
	}
	// Report a failure of either side's listing, but not the
	// cancelation of one that the comparison didn't need any more.
	for _, ce := range []*chanError{srcErr, dstErr} {
		if err := ce.Get(); err != nil && !errors.Is(err, context.Canceled) {
			return 0, err
		}
	}

	for _, sb := range missing {
		if enqErr := sh.enqueue(sb); enqErr != nil {
			if err == nil {
				err = enqErr
			}
		} else {
			queued++
		}
	}
	return queued, err
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
		if errors.Is(err, errNotPrefix) {
			err = nil
		}
		if err != nil {
			// Send a zero value to shut down ListMissingDestinationBlobs,
			// unless it's stopped already.
			select {
			case c <- blob.SizedRef{}:
			case <-ctx.Done():
			}
		}
		errc <- err
	}()
	return c, errc
}

// shardPrefixes returns the shards of a validation of the given hash
// types, which should be the source's (see sourceHashes): 256 digest
// prefixes of each. Only the source's types matter, as validation
// checks that the destination has the source's blobs.
func shardPrefixes(hashes []string) []string {
	var pfx []string
	for _, hashType := range hashes {
		for i := range 256 {
			pfx = append(pfx, fmt.Sprintf("%s-%02x", hashType, i))
		}
	}
	return pfx
}

// sourceHashes returns the hash types of the source's blobs: those the
// storage census found, if it has counted the source, or else those that
// listing the first blob of each type finds.
func (sh *SyncHandler) sourceHashes(ctx context.Context) []string {
	var ests []blobserver.HashEstimate
	if e, ok := sh.census(sh.fromName); ok {
		ests = e
	} else {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		e, err := blobserver.EstimateByHashType(ctx, sh.from, 1)
		if err != nil {
			sh.logf("listing the source's hash types: %v; validating all", err)
			return blob.HashFuncs()
		}
		ests = e
	}
	var hashes []string
	for _, e := range ests {
		if e.Blobs > 0 {
			hashes = append(hashes, e.Hash)
		}
	}
	return hashes
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

// setError deletes cs's blob from the queue if err is nil, as it was
// copied, and then finishes it.
func (cs *copyStatus) setError(err error) {
	if err == nil {
		// This is somewhat slow, so do it before finish acquires the
		// lock. The queue is thread-safe.
		if derr := cs.sh.queue.Delete(cs.sb.Ref.String()); derr != nil {
			cs.sh.logf("queue delete of %v error: %v", cs.sb.Ref, derr)
		}
	}
	cs.finish(err)
}

// finish records that copying cs's blob is done, with err if it failed.
// If it was copied, its queue entry must already be deleted.
func (cs *copyStatus) finish(err error) {
	now := time.Now()
	sh := cs.sh
	br := cs.sb.Ref

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
		if len(sh.lastFail) == 0 {
			sh.copyCheck.OK()
		}
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
		copy(sh.recentErrors, sh.recentErrors[1:])
		sh.recentErrors = sh.recentErrors[:maxRecentErrors-1]
	}
	sh.recentErrors = append(sh.recentErrors, br)
	sh.copyCheck.Warn(fmt.Errorf("%d blobs failing to copy; the latest, %v: %w", len(sh.lastFail), br, err))
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

func storageDesc(v any) string {
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

func (sh *SyncHandler) Fetch(context.Context, blob.Ref) (file io.ReadCloser, size uint32, err error) {
	panic("unimplemented blobserver.Fetch called")
}

func (sh *SyncHandler) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	sh.logf("unexpected StatBlobs call")
	return nil
}

func (sh *SyncHandler) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	sh.logf("unexpected EnumerateBlobs call")
	return nil
}

func (sh *SyncHandler) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	panic("unimplemented RemoveBlobs")
}

var errStopEnumerating = errors.New("sentinel error: reached the hourly compare quota")

// hourlyCompare runs compareRound every hour, for a handler with no
// health monitor to schedule it.
func (sh *SyncHandler) hourlyCompare(hourlyBytes uint64) {
	ticker := time.NewTicker(time.Hour).C
	for {
		sh.compareRound(context.TODO(), hourlyBytes)
		<-ticker
	}
}

// randomStart returns a random point among the source's blobrefs, for
// the start of a sample of its blobs: a hash type, chosen in proportion
// to the estimated number of the source's blobs of each type (so that
// the sample is of the blobs it has, rather than, say, of a type it has
// none of, which sorting would turn into a sample of the start of the
// next type), then a random digest prefix.
func (sh *SyncHandler) randomStart(ctx context.Context) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	r := binary.BigEndian.Uint64(b[4:])
	hash := blob.HashFuncs()[int(b[0])%len(blob.HashFuncs())]
	if ests, err := blobserver.EstimateByHashType(ctx, sh.from, 100); err == nil {
		hash = weightedHash(ests, r)
	}
	return fmt.Sprintf("%s-%x", hash, b[1:4])
}

// weightedHash returns the hash type of one of ests, chosen with the
// random number r in proportion to their numbers of blobs, or the
// first hash type (see blob.HashFuncs) if they have none.
func weightedHash(ests []blobserver.HashEstimate, r uint64) string {
	var total uint64
	for _, e := range ests {
		total += uint64(e.Blobs)
	}
	if total == 0 {
		return blob.HashFuncs()[0]
	}
	r %= total
	for _, e := range ests {
		if r < uint64(e.Blobs) {
			return e.Hash
		}
		r -= uint64(e.Blobs)
	}
	return ests[len(ests)-1].Hash
}

// compareRound picks blob names from a random point in the source,
// downloads up to maxBytes of them from the destination, and verifies
// them.
func (sh *SyncHandler) compareRound(ctx context.Context, maxBytes uint64) error {
	after := sh.randomStart(ctx)
	var roundBytes uint64
	var roundBlobs int
	err := blobserver.EnumerateAllFrom(ctx, sh.from, after, func(sr blob.SizedRef) error {
		sh.mu.Lock()
		if _, ok := sh.needCopy[sr.Ref]; ok {
			sh.mu.Unlock()
			return nil // skip blobs in the copy queue
		}
		sh.mu.Unlock()

		if roundBytes+uint64(sr.Size) > maxBytes {
			return errStopEnumerating
		}
		blob, size, err := sh.to.(blob.Fetcher).Fetch(ctx, sr.Ref)
		if err != nil {
			return fmt.Errorf("error fetching %s: %w", sr.Ref, err)
		}
		defer blob.Close()
		if size != sr.Size {
			return fmt.Errorf("%s: expected size %d, got %d", sr.Ref, sr.Size, size)
		}
		h := sr.Ref.Hash()
		if _, err := io.Copy(h, blob); err != nil {
			return fmt.Errorf("error while reading %s: %w", sr.Ref, err)
		}
		if !sr.HashMatches(h) {
			return fmt.Errorf("expected %s, got %x", sr.Ref, h.Sum(nil))
		}

		sh.mu.Lock()
		sh.comparedBlobs++
		sh.comparedBytes += uint64(size)
		sh.compLastBlob = sr.Ref.String()
		sh.mu.Unlock()
		roundBlobs++
		roundBytes += uint64(size)
		sh.compareCheck.Progress(int64(roundBytes), int64(maxBytes))
		return nil
	})
	if errors.Is(err, errStopEnumerating) {
		err = nil
	}
	sh.mu.Lock()
	if err != nil && ctx.Err() == nil {
		sh.compareErrors = append(sh.compareErrors, fmt.Sprintf("%s %v", time.Now(), err))
		sh.logf("!! hourly compare error !!: %v", err)
	}
	sh.comparedRounds++
	sh.mu.Unlock()
	sh.logf("compared %d blobs (%d bytes)", roundBlobs, roundBytes)
	if err != nil {
		return err
	}
	sh.compareCheck.OKMessage(fmt.Sprintf("%d blobs (%d bytes) from %s… matched", roundBlobs, roundBytes, after[:min(len(after), 12)]))
	return nil
}

const (
	sampleDuration = time.Minute // how long sample runs, at most
	sampleMaxBlobs = 10000       // how many blobs sample checks, at most
	sampleBatch    = 100         // how many blobs sample stats in the destination at once
)

var errSampleDone = errors.New("sentinel error: sample done")

// sample checks whether a sample of the source's blobs, from a random
// point, are in the destination, for up to sampleDuration or
// sampleMaxBlobs, and queues any missing ones to copy. It reports what
// fraction of them were there, to sh.sampleCheck.
func (sh *SyncHandler) sample(ctx context.Context) error {
	statter, ok := sh.to.(blobserver.BlobStatter)
	if !ok {
		return fmt.Errorf("destination %T can't stat blobs", sh.to)
	}
	start := time.Now()
	after := sh.randomStart(ctx)
	var seen, missing, queued int
	var batch []blob.SizedRef
	flush := func() error {
		refs := make([]blob.Ref, len(batch))
		for i, sb := range batch {
			refs[i] = sb.Ref
		}
		var mu sync.Mutex
		have := make(map[blob.Ref]bool)
		if err := statter.StatBlobs(ctx, refs, func(sb blob.SizedRef) error {
			mu.Lock()
			defer mu.Unlock()
			have[sb.Ref] = true
			return nil
		}); err != nil {
			return fmt.Errorf("statting destination: %w", err)
		}
		for _, sb := range batch {
			if have[sb.Ref] {
				continue
			}
			missing++
			sh.mu.Lock()
			_, alreadyQueued := sh.needCopy[sb.Ref]
			sh.mu.Unlock()
			if alreadyQueued {
				continue
			}
			if err := sh.enqueue(sb); err != nil {
				return err
			}
			queued++
		}
		batch = batch[:0]
		sh.sampleCheck.Progress(int64(seen), sampleMaxBlobs)
		return nil
	}
	fn := func(sb blob.SizedRef) error {
		seen++
		batch = append(batch, sb)
		if len(batch) == sampleBatch {
			if err := flush(); err != nil {
				return err
			}
		}
		if seen == sampleMaxBlobs || time.Since(start) >= sampleDuration {
			return errSampleDone
		}
		return nil
	}
	err := blobserver.EnumerateAllFrom(ctx, sh.from, after, fn)
	if err == nil && seen < sampleMaxBlobs {
		// Reached the end of the source; continue from its start.
		err = blobserver.EnumerateAllFrom(ctx, sh.from, "", func(sb blob.SizedRef) error {
			if sb.Ref.String() > after {
				return errSampleDone
			}
			return fn(sb)
		})
	}
	if errors.Is(err, errSampleDone) {
		err = nil
	}
	if err == nil && len(batch) > 0 {
		err = flush()
	}
	if err != nil {
		return err
	}
	from := after[:min(len(after), 12)] + "…"
	if seen == 0 {
		sh.sampleCheck.OKMessage("the source has no blobs")
		return nil
	}
	pct := 100 * float64(seen-missing) / float64(seen)
	if missing > 0 {
		sh.sampleCheck.Warn(fmt.Errorf("%d of %d source blobs from %s (%.1f%%) are in the destination; queued the %d missing that weren't already queued", seen-missing, seen, from, pct, queued))
		return nil
	}
	sh.sampleCheck.OKMessage(fmt.Sprintf("all %d source blobs from %s are in the destination", seen, from))
	return nil
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
