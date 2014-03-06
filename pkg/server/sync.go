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
	"sort"
	"strconv"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/constants"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/types"
)

const (
	maxRecentErrors   = 20
	queueSyncInterval = 5 * time.Second
)

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
	to               blobserver.BlobReceiver
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

func newSyncFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (http.Handler, error) {
	var (
		from           = conf.RequiredString("from")
		to             = conf.RequiredString("to")
		fullSync       = conf.OptionalBool("fullSyncOnStart", false)
		blockFullSync  = conf.OptionalBool("blockingFullSyncOnStart", false)
		idle           = conf.OptionalBool("idle", false)
		queueConf      = conf.OptionalObject("queue")
		copierPoolSize = conf.OptionalInt("copierPoolSize", 5)
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
				n := sh.runSync("queue", sh.enumeratePendingBlobs)
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
	from blobserver.Storage, to blobserver.BlobReceiver,
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

func (sh *SyncHandler) discoveryMap() map[string]interface{} {
	// TODO(mpl): more status info
	return map[string]interface{}{
		"from":    sh.fromName,
		"to":      sh.toName,
		"toIndex": sh.toIndex,
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

func blobserverEnumerator(ctx *context.Context, src blobserver.BlobEnumerator) func(chan<- blob.SizedRef, <-chan struct{}) error {
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

func (sh *SyncHandler) runSync(srcName string, enumSrc func(chan<- blob.SizedRef, <-chan struct{}) error) int {
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
			break FeedWork
		}
	}
	close(workch)
	for i := 0; i < toCopy; i++ {
		sh.setStatusf("Copying blobs")
		res := <-resch
		sh.mu.Lock()
		if res.err == nil {
			nCopied++
			sh.totalCopies++
			sh.totalCopyBytes += int64(res.sb.Size)
			sh.recentCopyTime = time.Now().UTC()
		} else {
			sh.totalErrors++
		}
		sh.mu.Unlock()
	}

	if err := <-errch; err != nil {
		sh.logf("error enumerating from source: %v", err)
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
	rc, fromSize, err := sh.from.FetchStreaming(br)
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

func (sh *SyncHandler) Fetch(blob.Ref) (file types.ReadSeekCloser, size uint32, err error) {
	panic("Unimplemeted blobserver.Fetch called")
}

func (sh *SyncHandler) FetchStreaming(blob.Ref) (file io.ReadCloser, size uint32, err error) {
	panic("Unimplemeted blobserver.FetchStreaming called")
}

func (sh *SyncHandler) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	sh.logf("Unexpected StatBlobs call")
	return nil
}

func (sh *SyncHandler) EnumerateBlobs(ctx *context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	sh.logf("Unexpected EnumerateBlobs call")
	return nil
}

func (sh *SyncHandler) RemoveBlobs(blobs []blob.Ref) error {
	panic("Unimplemeted RemoveBlobs")
}
