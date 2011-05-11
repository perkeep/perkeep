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
)

const queueSyncInterval = seconds(5)
const maxErrors = 20

var _ = log.Printf

type SyncHandler struct {
	fromName, fromqName, toName string
	from, fromq, to             blobserver.Storage

	lk           sync.Mutex
	lastStatus   string
	recentErrors []timestampedError
}

type timestampedError struct {
	t   *time.Time
	err os.Error
}

func createSyncHandler(fromName, toName string, from, to blobserver.Storage) (*SyncHandler, os.Error) {
	h := &SyncHandler{
		from:       from,
		to:         to,
		fromName:   fromName,
		toName:     toName,
		lastStatus: "not started",
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
	fmt.Fprintf(rw, "sync handler, from %s to %s<p>status: %s", sh.fromName, sh.toName,
		html.EscapeString(sh.status()))

	sh.lk.Lock()
	defer sh.lk.Unlock()
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
	sh.lastStatus = s
}

func (sh *SyncHandler) status() string {
	sh.lk.Lock()
	defer sh.lk.Unlock()
	return sh.lastStatus
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

func (sh *SyncHandler) syncQueueLoop() {
	every(queueSyncInterval, func() {
		sh.setStatus("Long-polling enumerate on queue %q, waiting for new blobs.", sh.fromqName)

		ch := make(chan blobref.SizedBlobRef)
		errch := make(chan os.Error, 1)
		go func() {
			log.Printf("pre-enumerate, for %d seconds", int(queueSyncInterval.Seconds()))
			errch <- sh.fromq.EnumerateBlobs(ch, "", 100, int(queueSyncInterval.Seconds()))
			log.Printf("post-enumerate")
		}()
		for sb := range ch {
			log.Printf("sync in queue %q: got blob: %s", sh.fromqName, sb)

			// TODO: have a pool of copiers, not just a
			// single thread here.  Mostly simple, but
			// having a good status will make it more
			// complicated.

			error := func(s string, args ...interface{}) {
				// TODO: increment error stats
				pargs := []interface{}{sh.fromqName, sb.BlobRef}
				pargs = append(pargs, args...)
				sh.addErrorToLog(fmt.Errorf("replication error for queue %q, blob %s: "+s, pargs...))
			}

			sh.setStatus("Syncing blob %s (size %d)", sb.BlobRef, sb.Size)
			blobReader, fromSize, err := sh.from.FetchStreaming(sb.BlobRef)
			if err != nil {
				error("source fetch: %v", err)
				continue
			}
			if fromSize != sb.Size {
				error("source fetch size mismatch: get=%d, enumerate=%d", fromSize, sb.Size)
				continue
			}
			newsb, err := sh.to.ReceiveBlob(sb.BlobRef, blobReader)
			if err != nil {
				error("dest write: %v", err)
				continue
			}
			if newsb.Size != sb.Size {
				error("write size mismatch: source_read=%d but dest_write=%d", sb.Size, newsb.Size)
				continue
			}
			err = sh.fromq.Remove([]*blobref.BlobRef{sb.BlobRef})
			if err != nil {
				error("source queue delete: %v", err)
			}
			error("replicated %s size %d", sb.BlobRef, sb.Size)
		}
		if err := <-errch; err != nil {
			sh.addErrorToLog(fmt.Errorf("replication error for queue %q, enumerate from source: %v", err))
			return
		}

		sh.setStatus("Sleeping briefly before next long poll.")
	})
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
