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
	"errors"
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/readerutil"
	"camlistore.org/pkg/sorted"
)

var queueSyncInterval = 5 * time.Second

const (
	maxErrors = 20
)

// The SyncHandler handles async replication in one direction between
// a pair storage targets, a source and target.
//
// SyncHandler is a BlobReceiver but doesn't actually store incoming
// blobs; instead, it records blobs it has received and queues them
// for async replication soon, or whenever it can.
type SyncHandler struct {
	// TODO: rate control + tunable
	// TODO: expose copierPoolSize as tunable

	blobserver.NoImplStorage

	fromName, toName string
	from             blobserver.Storage
	to               blobserver.BlobReceiver
	queue            sorted.KeyValue
	toIndex          bool // whether this sync is from a blob storage to an index

	idle bool // if true, the handler does nothing other than providing the discovery.

	copierPoolSize int

	// blobc receives a blob to copy. It's an optimization only to wake up
	// the syncer from idle sleep periods and sends are non-blocking and may
	// drop blobs. The queue is the actual source of truth.
	blobc chan blob.SizedRef

	lk             sync.Mutex // protects following
	status         string
	blobStatus     map[string]fmt.Stringer // stringer called with lk held
	recentErrors   []timestampedError
	recentCopyTime time.Time
	totalCopies    int64
	totalCopyBytes int64
	totalErrors    int64
}

func (sh *SyncHandler) String() string {
	return fmt.Sprintf("[SyncHandler %v -> %v]", sh.fromName, sh.toName)
}

func (sh *SyncHandler) logf(format string, args ...interface{}) {
	log.Printf(sh.String()+" "+format, args...)
}

var (
	_ blobserver.Storage       = (*SyncHandler)(nil)
	_ blobserver.HandlerIniter = (*SyncHandler)(nil)
)

func init() {
	blobserver.RegisterHandlerConstructor("sync", newSyncFromConfig)
}

func newSyncFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (http.Handler, error) {
	var (
		from          = conf.RequiredString("from")
		to            = conf.RequiredString("to")
		fullSync      = conf.OptionalBool("fullSyncOnStart", false)
		blockFullSync = conf.OptionalBool("blockingFullSyncOnStart", false)
		idle          = conf.OptionalBool("idle", false)
		queueConf     = conf.OptionalObject("queue")
	)
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	if idle {
		synch, err := createIdleSyncHandler(from, to)
		if err != nil {
			return nil, err
		}
		return synch, nil
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

	sh, err := createSyncHandler(from, to, fromBs, toBs, q, isToIndex)
	if err != nil {
		return nil, err
	}

	if fullSync || blockFullSync {
		didFullSync := make(chan bool, 1)
		go func() {
			n := sh.runSync("queue", sh.enumerateQueuedBlobs)
			sh.logf("Queue sync copied %d blobs", n)
			n = sh.runSync("full", blobserverEnumerator(context.TODO(), fromBs))
			sh.logf("Full sync copied %d blobs", n)
			didFullSync <- true
			sh.syncQueueLoop()
		}()
		if blockFullSync {
			sh.logf("Blocking startup, waiting for full sync from %q to %q", from, to)
			<-didFullSync
			sh.logf("Full sync complete.")
		}
	} else {
		go sh.syncQueueLoop()
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

type timestampedError struct {
	t   time.Time
	err error
}

func createSyncHandler(fromName, toName string,
	from blobserver.Storage, to blobserver.BlobReceiver,
	queue sorted.KeyValue, isToIndex bool) (*SyncHandler, error) {

	h := &SyncHandler{
		copierPoolSize: 3,
		from:           from,
		to:             to,
		fromName:       fromName,
		toName:         toName,
		queue:          queue,
		toIndex:        isToIndex,
		blobc:          make(chan blob.SizedRef, 8),
		status:         "not started",
		blobStatus:     make(map[string]fmt.Stringer),
	}
	return h, nil
}

func createIdleSyncHandler(fromName, toName string) (*SyncHandler, error) {
	h := &SyncHandler{
		fromName: fromName,
		toName:   toName,
		idle:     true,
		status:   "disabled",
	}
	return h, nil
}

func (sh *SyncHandler) discoveryMap() map[string]interface{} {
	// TODO(mpl): more status info
	return map[string]interface{}{
		"from":    sh.fromName,
		"to":      sh.toName,
		"toIndex": sh.toIndex,
	}
}

func (sh *SyncHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	sh.lk.Lock()
	defer sh.lk.Unlock()

	fmt.Fprintf(rw, "<h1>%s to %s Sync Status</h1><p><b>Current status: </b>%s</p>",
		sh.fromName, sh.toName, html.EscapeString(sh.status))
	if sh.idle {
		return
	}

	fmt.Fprintf(rw, "<h2>Stats:</h2><ul>")
	fmt.Fprintf(rw, "<li>Blobs copied: %d</li>", sh.totalCopies)
	fmt.Fprintf(rw, "<li>Bytes copied: %d</li>", sh.totalCopyBytes)
	if !sh.recentCopyTime.IsZero() {
		fmt.Fprintf(rw, "<li>Most recent copy: %s</li>", sh.recentCopyTime.Format(time.RFC3339))
	}
	fmt.Fprintf(rw, "<li>Copy errors: %d</li>", sh.totalErrors)
	fmt.Fprintf(rw, "</ul>")

	if len(sh.blobStatus) > 0 {
		fmt.Fprintf(rw, "<h2>Current Copies:</h2><ul>")
		for blobstr, sfn := range sh.blobStatus {
			fmt.Fprintf(rw, "<li>%s: %s</li>\n",
				blobstr, html.EscapeString(sfn.String()))
		}
		fmt.Fprintf(rw, "</ul>")
	}

	if len(sh.recentErrors) > 0 {
		fmt.Fprintf(rw, "<h2>Recent Errors:</h2><ul>")
		for _, te := range sh.recentErrors {
			fmt.Fprintf(rw, "<li>%s: %s</li>\n",
				te.t.Format(time.RFC3339),
				html.EscapeString(te.err.Error()))
		}
		fmt.Fprintf(rw, "</ul>")
	}
}

func (sh *SyncHandler) setStatus(s string, args ...interface{}) {
	s = time.Now().UTC().Format(time.RFC3339) + ": " + fmt.Sprintf(s, args...)
	sh.lk.Lock()
	defer sh.lk.Unlock()
	sh.status = s
}

func (sh *SyncHandler) setBlobStatus(blobref string, s fmt.Stringer) {
	sh.lk.Lock()
	defer sh.lk.Unlock()
	if s != nil {
		sh.blobStatus[blobref] = s
	} else {
		delete(sh.blobStatus, blobref)
	}
}

func (sh *SyncHandler) addErrorToLog(err error) {
	sh.logf("%v", err)
	sh.lk.Lock()
	defer sh.lk.Unlock()
	sh.recentErrors = append(sh.recentErrors, timestampedError{time.Now().UTC(), err})
	if len(sh.recentErrors) > maxErrors {
		// Kinda lame, but whatever. Only for errors, rare.
		copy(sh.recentErrors[:maxErrors], sh.recentErrors[1:maxErrors+1])
		sh.recentErrors = sh.recentErrors[:maxErrors]
	}
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

func (sh *SyncHandler) enumerateQueuedBlobs(dst chan<- blob.SizedRef, intr <-chan struct{}) error {
	defer close(dst)
	it := sh.queue.Find("", "")
	for it.Next() {
		br, ok := blob.Parse(it.Key())
		size, err := strconv.ParseInt(it.Value(), 10, 64)
		if !ok || err != nil {
			sh.logf("ERROR: bogus sync queue entry: %q => %q", it.Key(), it.Value())
			continue
		}
		select {
		case dst <- blob.SizedRef{br, size}:
		case <-intr:
			return it.Close()
		}
	}
	return it.Close()
}

func (sh *SyncHandler) enumerateBlobc(first blob.SizedRef) func(chan<- blob.SizedRef, <-chan struct{}) error {
	return func(dst chan<- blob.SizedRef, intr <-chan struct{}) error {
		defer close(dst)
		dst <- first
		for {
			select {
			case sb := <-sh.blobc:
				dst <- sb
			default:
				return nil
			}
		}
	}
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
	for sb := range enumch {
		toCopy++
		workch <- sb
		if toCopy <= sh.copierPoolSize {
			go sh.copyWorker(resch, workch)
		}
		sh.setStatus("Enumerating queued blobs: %d", toCopy)
	}
	close(workch)
	for i := 0; i < toCopy; i++ {
		sh.setStatus("Copied %d/%d of batch of queued blobs", nCopied, toCopy)
		res := <-resch
		sh.lk.Lock()
		if res.err == nil {
			nCopied++
			sh.totalCopies++
			sh.totalCopyBytes += res.sb.Size
			sh.recentCopyTime = time.Now().UTC()
		} else {
			sh.totalErrors++
		}
		sh.lk.Unlock()
	}

	if err := <-errch; err != nil {
		sh.addErrorToLog(fmt.Errorf("replication error for source %q, enumerate from source: %v", srcName, err))
		return nCopied
	}
	return nCopied
}

func (sh *SyncHandler) syncQueueLoop() {
	for {
		t0 := time.Now()

		for sh.runSync(sh.fromName, sh.enumerateQueuedBlobs) > 0 {
			// Loop, before sleeping.
		}
		sh.setStatus("Sleeping briefly before next long poll.")

		d := queueSyncInterval - time.Since(t0)
		select {
		case <-time.After(d):
		case sb := <-sh.blobc:
			// Blob arrived.
			sh.runSync(sh.fromName, sh.enumerateBlobc(sb))
		}
	}
}

func (sh *SyncHandler) copyWorker(res chan<- copyResult, work <-chan blob.SizedRef) {
	for sb := range work {
		res <- copyResult{sb, sh.copyBlob(sb, 0)}
	}
}

type statusFunc func() string

func (sf statusFunc) String() string { return sf() }

type status string

func (s status) String() string { return string(s) }

func (sh *SyncHandler) copyBlob(sb blob.SizedRef, tryCount int) error {
	key := sb.Ref.String()
	set := func(s fmt.Stringer) {
		sh.setBlobStatus(key, s)
	}
	defer set(nil)

	errorf := func(s string, args ...interface{}) error {
		// TODO: increment error stats
		err := fmt.Errorf("replication error for blob %s: "+s,
			append([]interface{}{sb.Ref}, args...)...)
		sh.addErrorToLog(err)
		return err
	}

	set(status("sending GET to source"))
	rc, fromSize, err := sh.from.FetchStreaming(sb.Ref)
	if err != nil {
		return errorf("source fetch: %v", err)
	}
	defer rc.Close()
	if fromSize != sb.Size {
		return errorf("source fetch size mismatch: get=%d, enumerate=%d", fromSize, sb.Size)
	}

	bytesCopied := int64(0) // TODO: data race, accessed without locking in statusFunc below.
	set(statusFunc(func() string {
		return fmt.Sprintf("copying: %d/%d bytes", bytesCopied, sb.Size)
	}))
	newsb, err := sh.to.ReceiveBlob(sb.Ref, readerutil.CountingReader{rc, &bytesCopied})
	if err != nil {
		return errorf("dest write: %v", err)
	}
	if newsb.Size != sb.Size {
		return errorf("write size mismatch: source_read=%d but dest_write=%d", sb.Size, newsb.Size)
	}
	set(status("copied; removing from queue"))
	if err := sh.queue.Delete(sb.Ref.String()); err != nil {
		return errorf("queue delete: %v", err)
	}
	return nil
}

func (sh *SyncHandler) ReceiveBlob(br blob.Ref, r io.Reader) (sb blob.SizedRef, err error) {
	n, err := io.Copy(ioutil.Discard, r)
	if err != nil {
		return
	}
	sb = blob.SizedRef{br, n}
	return sb, sh.enqueue(sb)
}

func (sh *SyncHandler) enqueue(sb blob.SizedRef) error {
	// TODO: include current time in encoded value, to attempt to
	// do in-order delivery to remote side later? Possible
	// friendly optimization later. Might help peer's indexer have
	// less missing deps.
	if err := sh.queue.Set(sb.Ref.String(), fmt.Sprint(sb.Size)); err != nil {
		return err
	}
	// Non-blocking send to wake up looping goroutine if it's
	// sleeping...
	select {
	case sh.blobc <- sb:
	default:
	}
	return nil
}

// TODO(bradfitz): implement these? what do they mean? possibilities:
// a) proxy to sh.from
// b) proxy to sh.to
// c) merge intersection of sh.from, sh.to, and sh.queue: that is, a blob this pair
//    currently or eventually will have. The only missing blob would be one that
//    sh.from has, sh.to doesn't have, and isn't in the queue to be replicated.
//
// For now, don't implement them. Wait until we need them.
//
// func (sh *SyncHandler) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
// func (sh *SyncHandler) FetchStreaming(br blob.Ref) (io.ReadCloser, int64, error) {
// func (sh *SyncHandler) EnumerateBlobs(dest chan<- blob.SizedRef, after string, limit int) error {
