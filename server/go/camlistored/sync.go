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

package main

import (
	"fmt"
	"html"
	"http"
	"os"
	"log"
	"strings"
	"sync"
	"time"

	"camli/blobref"
	"camli/blobserver"
	"camli/jsonconfig"
	"camli/misc"
)

const queueSyncInterval = seconds(5)
const maxErrors = 20

var _ = log.Printf

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
	recentCopyTime *time.Time
	totalCopies    int64
	totalCopyBytes int64
	totalErrors    int64
}

func init() {
	blobserver.RegisterHandlerConstructor("sync", newSyncFromConfig)
}

func newSyncFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err os.Error) {
	from := conf.RequiredString("from")
	to := conf.RequiredString("to")
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
	synch, err := createSyncHandler(from, to, fromBs, toBs)
	if err != nil {
		return
	}
	return synch, nil
}


type timestampedError struct {
	t   *time.Time
	err os.Error
}

func createSyncHandler(fromName, toName string, from, to blobserver.Storage) (*SyncHandler, os.Error) {
	h := &SyncHandler{
		copierPoolSize: 3,
		from:           from,
		to:             to,
		fromName:       fromName,
		toName:         toName,
		status:         "not started",
		blobStatus:     make(map[string]fmt.Stringer),
	}

	qc, ok := from.(blobserver.QueueCreator)
	if !ok {
		return nil, fmt.Errorf(
			"Prefix %s (type %T) does not support being efficient replication source (queueing)",
			fromName, from)
	}
	h.fromqName = strings.Replace(strings.Trim(toName, "/"), "/", "-", -1)
	var err os.Error
	h.fromq, err = qc.CreateQueue(h.fromqName)
	if err != nil {
		return nil, fmt.Errorf("Prefix %s (type %T) failed to create queue %q: %v",
			fromName, from, h.fromqName, err)
	}

	go h.syncQueueLoop()

	return h, nil
}

func (sh *SyncHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	sh.lk.Lock()
	defer sh.lk.Unlock()

	fmt.Fprintf(rw, "<h1>%s to %s Sync Status</h1><p><b>Current status: </b>%s</p>",
		sh.fromName, sh.toName, html.EscapeString(sh.status))

	fmt.Fprintf(rw, "<h2>Stats:</h2><ul>")
	fmt.Fprintf(rw, "<li>Blobs copied: %d</li>", sh.totalCopies)
	fmt.Fprintf(rw, "<li>Bytes copied: %d</li>", sh.totalCopyBytes)
	if sh.recentCopyTime != nil {
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
				html.EscapeString(te.err.String()))
		}
		fmt.Fprintf(rw, "</ul>")
	}
}

func (sh *SyncHandler) setStatus(s string, args ...interface{}) {
	s = time.UTC().Format(time.RFC3339) + ": " + fmt.Sprintf(s, args...)
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
		sh.blobStatus[blobref] = nil, false
	}
}

func (sh *SyncHandler) addErrorToLog(err os.Error) {
	log.Printf(err.String())
	sh.lk.Lock()
	defer sh.lk.Unlock()
	sh.recentErrors = append(sh.recentErrors, timestampedError{time.UTC(), err})
	if len(sh.recentErrors) > maxErrors {
		// Kinda lame, but whatever. Only for errors, rare.
		copy(sh.recentErrors[:maxErrors], sh.recentErrors[1:maxErrors+1])
		sh.recentErrors = sh.recentErrors[:maxErrors]
	}
}

type copyResult struct {
	sb  blobref.SizedBlobRef
	err os.Error
}

func (sh *SyncHandler) syncQueueLoop() {
	every(queueSyncInterval, func() {
	Enumerate:
		sh.setStatus("Idle; waiting for new blobs")

		enumch := make(chan blobref.SizedBlobRef)
		errch := make(chan os.Error, 1)
		go func() {
			errch <- sh.fromq.EnumerateBlobs(enumch, "", 1000, int(queueSyncInterval.Seconds()))
		}()

		nCopied := 0
		toCopy := 0

		workch := make(chan blobref.SizedBlobRef, 1000)
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
				sh.recentCopyTime = time.UTC()
			} else {
				sh.totalErrors++
			}
			sh.lk.Unlock()
		}

		if err := <-errch; err != nil {
			sh.addErrorToLog(fmt.Errorf("replication error for queue %q, enumerate from source: %v", err))
			return
		}
		if nCopied > 0 {
			// Don't sleep. More to do probably.
			goto Enumerate
		}
		sh.setStatus("Sleeping briefly before next long poll.")
	})
}

func (sh *SyncHandler) copyWorker(res chan<- copyResult, work <-chan blobref.SizedBlobRef) {
	for sb := range work {
		res <- copyResult{sb, sh.copyBlob(sb)}
	}
}

type statusFunc func() string

func (sf statusFunc) String() string {
	return sf()
}

type status string

func (s status) String() string {
	return string(s)
}

func (sh *SyncHandler) copyBlob(sb blobref.SizedBlobRef) os.Error {
	key := sb.BlobRef.String()
	set := func(s fmt.Stringer) {
		sh.setBlobStatus(key, s)
	}
	defer set(nil)

	error := func(s string, args ...interface{}) os.Error {
		// TODO: increment error stats
		pargs := []interface{}{sh.fromqName, sb.BlobRef}
		pargs = append(pargs, args...)
		err := fmt.Errorf("replication error for queue %q, blob %s: "+s, pargs...)
		sh.addErrorToLog(err)
		return err
	}

	set(status("sending GET to source"))
	blobReader, fromSize, err := sh.from.FetchStreaming(sb.BlobRef)
	if err != nil {
		return error("source fetch: %v", err)
	}
	if fromSize != sb.Size {
		return error("source fetch size mismatch: get=%d, enumerate=%d", fromSize, sb.Size)
	}

	bytesCopied := int64(0) // accessed without locking; minor, just for status display
	set(statusFunc(func() string {
		return fmt.Sprintf("copying: %d/%d bytes", bytesCopied, sb.Size)
	}))
	newsb, err := sh.to.ReceiveBlob(sb.BlobRef, misc.CountingReader{blobReader, &bytesCopied})
	if err != nil {
		return error("dest write: %v", err)
	}
	if newsb.Size != sb.Size {
		return error("write size mismatch: source_read=%d but dest_write=%d", sb.Size, newsb.Size)
	}
	set(status("copied; removing from queue"))
	err = sh.fromq.Remove([]*blobref.BlobRef{sb.BlobRef})
	if err != nil {
		return error("source queue delete: %v", err)
	}
	return nil
}

// TODO: move this elsewhere (timeutil?)
func every(interval nanoer, f func()) {
	nsInterval := int64(interval.Nanos())
	for {
		t1 := time.Nanoseconds()
		f()
		if sleep := (t1 + nsInterval) - time.Nanoseconds(); sleep > 0 {
			time.Sleep(sleep)
		}
	}
}

// TODO: move this time stuff elsewhere
type seconds int64
type nanos int64

func (s seconds) Nanos() nanos {
	return nanos(int64(s) * 1e9)
}

func (s seconds) Seconds() seconds {
	return s
}

type nanoer interface {
	Nanos() nanos
}
