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
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/readerutil"
)

var queueSyncInterval = 5 * time.Second

const maxErrors = 20

// TODO: rate control + tunable
// TODO: expose copierPoolSize as tunable
type SyncHandler struct {
	fromName, fromqName, toName string
	from, fromq, to             blobserver.Storage

	copierPoolSize int

	lk             sync.Mutex // protects following
	status         string
	blobStatus     map[string]fmt.Stringer // stringer called with lk held
	recentErrors   []timestampedError
	recentCopyTime time.Time
	totalCopies    int64
	totalCopyBytes int64
	totalErrors    int64
}

func init() {
	blobserver.RegisterHandlerConstructor("sync", newSyncFromConfig)
}

func newSyncFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err error) {
	from := conf.RequiredString("from")
	to := conf.RequiredString("to")
	fullSync := conf.OptionalBool("fullSyncOnStart", false)
	blockFullSync := conf.OptionalBool("blockingFullSyncOnStart", false)
	if err = conf.Validate(); err != nil {
		return
	}
	fromBs, err := ld.GetStorage(from)
	if err != nil {
		return
	}
	toBs, err := ld.GetStorage(to)
	if err != nil {
		return
	}
	fromQsc, ok := fromBs.(blobserver.StorageQueueCreator)
	if !ok {
		return nil, fmt.Errorf("Prefix %s (type %T) does not support being efficient replication source (queueing)", from, fromBs)
	}
	synch, err := createSyncHandler(from, to, fromQsc, toBs)
	if err != nil {
		return
	}

	if fullSync || blockFullSync {
		didFullSync := make(chan bool, 1)
		go func() {
			n := synch.runSync("queue", fromQsc, 0)
			log.Printf("Queue sync copied %d blobs", n)
			n = synch.runSync("full", fromBs, 0)
			log.Printf("Full sync copied %d blobs", n)
			didFullSync <- true
			synch.syncQueueLoop()
		}()
		if blockFullSync {
			log.Printf("Blocking startup, waiting for full sync from %q to %q", from, to)
			<-didFullSync
			log.Printf("Full sync complete.")
		}
	} else {
		go synch.syncQueueLoop()
	}

	rootPrefix, _, err := ld.FindHandlerByType("root")
	switch err {
	case blobserver.ErrHandlerTypeNotFound:
		// ignore; okay to not have a root handler.
	case nil:
		h, err := ld.GetHandler(rootPrefix)
		if err != nil {
			return nil, err
		}
		h.(*RootHandler).registerSyncHandler(synch)
	default:
		return nil, fmt.Errorf("Error looking for root handler: %v", err)
	}
	return synch, nil
}

type timestampedError struct {
	t   time.Time
	err error
}

func createSyncHandler(fromName, toName string, from blobserver.StorageQueueCreator, to blobserver.Storage) (*SyncHandler, error) {
	h := &SyncHandler{
		copierPoolSize: 3,
		from:           from,
		to:             to,
		fromName:       fromName,
		toName:         toName,
		status:         "not started",
		blobStatus:     make(map[string]fmt.Stringer),
	}
	h.fromqName = strings.Replace(strings.Trim(toName, "/"), "/", "-", -1)
	var err error
	h.fromq, err = from.CreateQueue(h.fromqName)
	if err != nil {
		return nil, fmt.Errorf("Prefix %s (type %T) failed to create queue %q: %v",
			fromName, from, h.fromqName, err)
	}
	return h, nil
}

func (sh *SyncHandler) discoveryMap() map[string]interface{} {
	// TODO(mpl): more status info
	return map[string]interface{}{
		"from": sh.fromName,
		"to":   sh.toName,
	}
}

func (sh *SyncHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	sh.lk.Lock()
	defer sh.lk.Unlock()

	fmt.Fprintf(rw, "<h1>%s to %s Sync Status</h1><p><b>Current status: </b>%s</p>",
		sh.fromName, sh.toName, html.EscapeString(sh.status))

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
	log.Printf(err.Error())
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

func (sh *SyncHandler) runSync(srcName string, enumSrc blobserver.Storage, longPollWait time.Duration) int {
	if longPollWait != 0 {
		sh.setStatus("Idle; waiting for new blobs")
	}
	enumch := make(chan blob.SizedRef)
	errch := make(chan error, 1)
	go func() {
		errch <- enumSrc.EnumerateBlobs(enumch, "", 1000, longPollWait)
	}()

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
		nCopied++
		sh.lk.Lock()
		if res.err == nil {
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
	every(queueSyncInterval, func() {
		for sh.runSync(sh.fromqName, sh.fromq, queueSyncInterval) > 0 {
			// Loop, before sleeping.
		}
		sh.setStatus("Sleeping briefly before next long poll.")
	})
}

func (sh *SyncHandler) copyWorker(res chan<- copyResult, work <-chan blob.SizedRef) {
	for sb := range work {
		res <- copyResult{sb, sh.copyBlob(sb)}
	}
}

type statusFunc func() string

func (sf statusFunc) String() string { return sf() }

type status string

func (s status) String() string { return string(s) }

func (sh *SyncHandler) copyBlob(sb blob.SizedRef) error {
	key := sb.Ref.String()
	set := func(s fmt.Stringer) {
		sh.setBlobStatus(key, s)
	}
	defer set(nil)

	errorf := func(s string, args ...interface{}) error {
		// TODO: increment error stats
		pargs := []interface{}{sh.fromqName, sb.Ref}
		pargs = append(pargs, args...)
		err := fmt.Errorf("replication error for queue %q, blob %s: "+s, pargs...)
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
	err = sh.fromq.RemoveBlobs([]blob.Ref{sb.Ref})
	if err != nil {
		return errorf("source queue delete: %v", err)
	}
	return nil
}

func every(interval time.Duration, f func()) {
	for {
		t1 := time.Now()
		f()
		sleepUntil := t1.Add(interval)
		if sleep := sleepUntil.Sub(time.Now()); sleep > 0 {
			time.Sleep(sleep)
		}
	}
}
