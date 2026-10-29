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

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/protocol"
	"perkeep.org/pkg/blobserver/uploadsession"
)

// errLostBlob is the error for a blob the server accepted into a session,
// but didn't report durable or failed when the session was flushed.
var errLostBlob = errors.New("client: upload session lost the blob")

// An UploadSession uploads blobs into an upload session on the server,
// which commits them to storage in groups, without each upload waiting
// for its blob to be durable. See doc/protocol/blob-upload-session.md.
// Its methods are safe for concurrent use.
type UploadSession struct {
	c  *Client
	id string

	flushing chan struct{} // a token, held by whoever is flushing

	qmu        sync.Mutex
	queue      []*queuedUpload // waiting to be sent
	sending    int             // upload requests in flight
	maxSending int             // how many upload requests may be in flight

	mu sync.Mutex
	// accepted are the blobs the server accepted, and hasn't reported
	// durable or failed.
	accepted map[blob.Ref]uint32
	// early are blobs a flush reported durable before their Upload call
	// noted them accepted.
	early map[blob.Ref]bool
	// wasDurable are the blobs flushes have reported durable. Once
	// acknowledged, the server doesn't report them again, so one
	// uploaded again (which the server accepts and skips, if it hasn't
	// had the acknowledgment yet) mustn't wait to be.
	wasDurable map[blob.Ref]bool
	// failed are the blobs that failed or were lost, and haven't been
	// uploaded again, for WaitDurable.
	failed map[blob.Ref]error
	// durable and failures are what flushes learned since the last
	// Flush, for the next to return.
	durable  []blob.SizedRef
	failures *blobserver.BlobsError
	// ack are the blobs the last successful flush request reported
	// durable, for the next to acknowledge.
	ack []blob.Ref
}

// NewUploadSession opens an upload session on the server. ctx is used
// only for the request that opens it; the session doesn't keep ctx, and
// each later method takes its own.
//
// If the server doesn't support upload sessions (its discovery doesn't
// report uploadSessions), the error matches errors.ErrUnsupported, and
// the caller should use Client.Upload instead.
func (c *Client) NewUploadSession(ctx context.Context) (*UploadSession, error) {
	if c.sto != nil {
		return nil, fmt.Errorf("client: upload sessions with local storage: %w", errors.ErrUnsupported)
	}
	if err := c.condDiscovery(); err != nil {
		return nil, err
	}
	if !c.uploadSessions {
		return nil, fmt.Errorf("client: server has no upload sessions: %w", errors.ErrUnsupported)
	}
	pfx, err := c.prefix()
	if err != nil {
		return nil, err
	}
	req := c.newRequest(ctx, "POST", pfx+"/camli/session")
	res, err := c.expect2XX(req)
	if err != nil {
		return nil, fmt.Errorf("client: opening upload session: %w", err)
	}
	var opened protocol.SessionOpenResponse
	if err := httputil.DecodeJSON(res, &opened); err != nil {
		return nil, fmt.Errorf("client: opening upload session: %w", err)
	}
	if opened.Session == "" {
		return nil, errors.New("client: server opened upload session with no ID")
	}
	return &UploadSession{
		c:          c,
		id:         opened.Session,
		flushing:   make(chan struct{}, 1),
		maxSending: httpGateSize(c.httpClient.Transport),
		accepted:   make(map[blob.Ref]uint32),
		early:      make(map[blob.Ref]bool),
		wasDurable: make(map[blob.Ref]bool),
		failed:     make(map[blob.Ref]error),
	}, nil
}

// sessionURL returns the URL of the session's op ("flush", "status") or,
// if op is empty, of the session.
func (s *UploadSession) sessionURL(op string) (string, error) {
	pfx, err := s.c.prefix()
	if err != nil {
		return "", err
	}
	u := pfx + "/camli/session/" + url.PathEscape(s.id)
	if op != "" {
		u += "/" + op
	}
	return u, nil
}

// Upload sends h's blob to the server, into the session. It returns once
// the server has accepted the blob, which doesn't mean the blob is
// durable yet: only a later Flush says that. Upload is safe to call
// concurrently.
//
// It doesn't send blobs the client's have cache has. Blobs of concurrent
// Upload calls that would wait for a free HTTP request go together, in
// one request. If ctx is done first, Upload returns, but the blob may
// still be sent.
//
// h.Vivify must be false; Upload returns an error otherwise. Vivifying
// makes the server read the file's chunks back from storage, as soon as
// it receives the file schema blob. In a session, those chunks may still
// be only in the server's buffer, not in storage.
func (s *UploadSession) Upload(ctx context.Context, h *UploadHandle) error {
	if err := h.check(); err != nil {
		return err
	}
	if h.Vivify {
		return errors.New("client: can't vivify in an upload session")
	}
	c := s.c
	if _, ok := c.haveCache.StatBlobCache(h.BlobRef); ok {
		return nil // durable already, as the have cache only has durable blobs
	}
	s.mu.Lock()
	durable := s.wasDurable[h.BlobRef]
	s.mu.Unlock()
	if durable {
		return nil
	}
	c.statsMutex.Lock()
	c.stats.UploadRequests.Blobs++
	c.stats.UploadRequests.Bytes += int64(h.Size)
	c.statsMutex.Unlock()

	u := &queuedUpload{h: h, done: make(chan error, 1)}
	s.qmu.Lock()
	s.queue = append(s.queue, u)
	s.sendLocked()
	s.qmu.Unlock()
	select {
	case err := <-u.done:
		return err
	case <-ctx.Done():
		// The blob may still be sent and accepted, and noted as such.
		return ctx.Err()
	}
}

// A queuedUpload is a blob an Upload call is waiting to send.
type queuedUpload struct {
	h    *UploadHandle
	done chan error // of the Upload's result
}

// A session's upload requests have up to sendBlobs blobs, or about
// sendBytes bytes.
const (
	sendBlobs = 256
	sendBytes = 16 << 20
)

// sendLocked sends the queued uploads, in as many requests as can be in
// flight, so that blobs queued while those are sent go together in the
// next. The caller must hold s.qmu.
func (s *UploadSession) sendLocked() {
	for s.sending < s.maxSending && len(s.queue) > 0 {
		var batch []*queuedUpload
		var size int64
		for len(s.queue) > 0 && len(batch) < sendBlobs && size < sendBytes {
			batch = append(batch, s.queue[0])
			size += int64(s.queue[0].h.Size)
			s.queue[0] = nil
			s.queue = s.queue[1:]
		}
		s.sending++
		go func() {
			s.send(batch)
			s.qmu.Lock()
			defer s.qmu.Unlock()
			s.sending--
			s.sendLocked()
		}()
	}
}

// send uploads batch in one request, telling each Upload its result.
func (s *UploadSession) send(batch []*queuedUpload) {
	hs := make([]*UploadHandle, len(batch))
	for i, u := range batch {
		hs[i] = u.h
	}
	// Not any one Upload's context: they share the request.
	ures, err := s.c.postUploads(context.Background(), hs, s.id)
	for _, u := range batch {
		if err == nil {
			u.done <- s.noteUploaded(u.h, ures)
		} else {
			u.done <- err
		}
	}
}

// noteUploaded records the result of uploading h's blob in a request to
// which the server responded ures.
func (s *UploadSession) noteUploaded(h *UploadHandle, ures *protocol.UploadResponse) error {
	c := s.c
	find := func(sbs []blob.SizedRef) (found bool, err error) {
		for _, sb := range sbs {
			if sb.Ref != h.BlobRef {
				continue
			}
			if sb.Size != h.Size {
				return false, fmt.Errorf("client: server got blob %v, but reports wrong length (%v; we sent %d)", sb.Ref, sb.Size, h.Size)
			}
			return true, nil
		}
		return false, nil
	}
	accepted, err := find(ures.Accepted)
	if err != nil {
		return err
	}
	// A server without sessions receives the blob as usual.
	received, err := find(ures.Received)
	if err != nil {
		return err
	}
	if !accepted && !received {
		if ures.ErrorText != "" {
			return fmt.Errorf("client: server didn't accept blob %v: %s", h.BlobRef, strings.TrimSpace(ures.ErrorText))
		}
		return fmt.Errorf("client: server didn't accept blob %v", h.BlobRef)
	}
	c.statsMutex.Lock()
	c.stats.Uploads.Blobs++
	c.stats.Uploads.Bytes += int64(h.Size)
	c.statsMutex.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failed, h.BlobRef)
	switch {
	case received:
		c.haveCache.NoteBlobExists(h.BlobRef, h.Size)
		s.durable = append(s.durable, blob.SizedRef{Ref: h.BlobRef, Size: h.Size})
	case s.early[h.BlobRef]:
		delete(s.early, h.BlobRef) // a Flush already returned it
	case s.wasDurable[h.BlobRef]:
		// Uploaded again, after a flush reported it durable.
	default:
		s.accepted[h.BlobRef] = h.Size
	}
	return nil
}

// Flush makes the server commit every blob the session accepted before
// Flush was called, and waits until all of those commits have finished
// (or ctx is done). The server may already have committed some of them
// on its own.
//
// It returns the blobs that became durable since the previous successful
// Flush, including any from blobs accepted during that Flush. It also
// records them in the client's have cache. Each Flush acknowledges the
// blobs the previous successful one returned, so the server stops
// reporting them.
//
// A blob that was accepted before Flush was called, but still isn't
// durable when Flush returns, is listed in a returned
// *blobserver.BlobsError. That's either because its commit failed, or
// because the server lost the session (for instance, by restarting),
// which loses all of them. Such blobs are no longer in the session; the
// caller should upload them again. Flush can return both durable blobs
// and a *blobserver.BlobsError, when only some blobs became durable.
//
// Any other error means Flush itself failed (ctx was done, or the
// request failed), and the slice is nil. Nothing is acknowledged or
// lost: the caller can call Flush again, and it will report the same
// blobs and more.
func (s *UploadSession) Flush(ctx context.Context) ([]blob.SizedRef, error) {
	if err := s.startFlushing(ctx); err != nil {
		return nil, err
	}
	defer s.doneFlushing()
	if err := s.flush(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	durable, failures := s.durable, s.failures
	s.durable, s.failures = nil, nil
	return durable, failures.OrNil()
}

// WaitDurable waits until each of refs is durable, flushing the session
// as needed (or until ctx is done). Upload must have accepted each of
// refs, and returned, before WaitDurable is called.
//
// Flushes are shared with concurrent WaitDurable calls: a call that finds
// its blobs settled by another's flush doesn't flush again. WaitDurable
// doesn't take the blobs from Flush's reports: the next Flush still
// returns them.
//
// It returns nil if all of refs are durable, and otherwise either a
// *blobserver.BlobsError listing those that failed or were lost (as
// Flush reports them; the caller should upload them again), or another
// error if a flush failed as a whole.
func (s *UploadSession) WaitDurable(ctx context.Context, refs []blob.Ref) error {
	if !s.settled(refs) {
		if err := s.startFlushing(ctx); err != nil {
			return err
		}
		defer s.doneFlushing()
		// The flush we waited for may have settled refs.
		if !s.settled(refs) {
			if err := s.flush(ctx); err != nil {
				return err
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	be := &blobserver.BlobsError{Op: "upload session wait"}
	for _, br := range refs {
		be.Add(br, s.failed[br])
	}
	return be.OrNil()
}

// settled reports whether none of refs is waiting for a flush to learn
// whether it's durable.
func (s *UploadSession) settled(refs []blob.Ref) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, br := range refs {
		if _, ok := s.accepted[br]; ok {
			return false
		}
	}
	return true
}

// startFlushing waits for any flush in progress to finish, or for ctx to
// be done. If it returns nil, the caller may flush, and must call
// doneFlushing after.
func (s *UploadSession) startFlushing(ctx context.Context) error {
	select {
	case s.flushing <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *UploadSession) doneFlushing() { <-s.flushing }

// flush flushes the session on the server, recording what it learns for
// WaitDurable and the next Flush. It returns an error only if the flush
// failed as a whole, learning nothing; a lost session is recorded as the
// loss of its blobs. The caller must hold the flushing token.
func (s *UploadSession) flush(ctx context.Context) error {
	s.mu.Lock()
	before := make(map[blob.Ref]bool, len(s.accepted))
	for br := range s.accepted {
		before[br] = true
	}
	form := url.Values{}
	sent := min(len(s.ack), maxFlushAcks)
	for _, br := range s.ack[:sent] {
		form.Add("ack", br.String())
	}
	unsent := slices.Clone(s.ack[sent:])
	s.mu.Unlock()

	st, err := s.postFlush(ctx, form)
	if err != nil && !errors.Is(err, uploadsession.ErrUnknownSession) {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures == nil {
		s.failures = &blobserver.BlobsError{Op: "upload session flush"}
	}
	fail := func(br blob.Ref, err error) {
		s.failures.Add(br, err)
		s.failed[br] = err
		delete(s.accepted, br)
	}
	if err != nil {
		// Everything not yet reported durable is lost.
		for br := range s.accepted {
			fail(br, err)
		}
		clear(s.early)
		s.ack = nil
		return nil
	}
	// The next flush acknowledges the blobs this one didn't have room
	// for, and those reported durable, once each. The server reports
	// blobs again until they're acknowledged, but they're durable only
	// once, for WaitDurable and Flush.
	ack := unsent
	acking := make(map[blob.Ref]bool, len(unsent))
	for _, br := range unsent {
		acking[br] = true
	}
	for _, sb := range st.Durable {
		if _, ok := s.accepted[sb.Ref]; ok {
			delete(s.accepted, sb.Ref)
		} else if !s.wasDurable[sb.Ref] {
			// Its Upload hasn't noted it accepted yet.
			s.early[sb.Ref] = true
		}
		if !s.wasDurable[sb.Ref] {
			s.wasDurable[sb.Ref] = true
			s.c.haveCache.NoteBlobExists(sb.Ref, sb.Size)
			s.durable = append(s.durable, sb)
		}
		if !acking[sb.Ref] {
			acking[sb.Ref] = true
			ack = append(ack, sb.Ref)
		}
	}
	for _, fb := range st.Failed {
		fail(fb.BlobRef, fmt.Errorf("client: server failed to commit blob: %s", fb.Error))
	}
	// The server reports every blob added before the flush as durable or
	// failed; one it reported neither is lost.
	for br := range before {
		if _, ok := s.accepted[br]; ok {
			fail(br, errLostBlob)
		}
	}
	s.ack = ack
	return nil
}

// maxFlushAcks is how many blobs a flush request acknowledges, at most.
// Go servers parse at most 10,000 form values by default (see GODEBUG
// urlmaxqueryparams), and reject requests with more.
var maxFlushAcks = 5000

// postFlush sends the flush request, with form's acknowledgements.
func (s *UploadSession) postFlush(ctx context.Context, form url.Values) (*protocol.SessionStatusResponse, error) {
	u, err := s.sessionURL("flush")
	if err != nil {
		return nil, err
	}
	req := s.c.newRequest(ctx, "POST", u, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := s.c.doReqGated(req)
	if err != nil {
		return nil, fmt.Errorf("client: flushing upload session: %w", err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("client: flushing upload session: %w", uploadsession.ErrUnknownSession)
	default:
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<10))
		return nil, fmt.Errorf("client: flushing upload session: status %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	st := new(protocol.SessionStatusResponse)
	if err := httputil.DecodeJSON(res, st); err != nil {
		return nil, fmt.Errorf("client: flushing upload session: %w", err)
	}
	return st, nil
}

// Close ends the session. The server still tries to commit the blobs it
// accepted, but it forgets the session at once, so no one can learn
// which commits succeeded. Close's error only says whether the server
// got the request. To learn which blobs are durable, Flush before Close.
func (s *UploadSession) Close(ctx context.Context) error {
	u, err := s.sessionURL("")
	if err != nil {
		return err
	}
	res, err := s.c.doReqGated(s.c.newRequest(ctx, "DELETE", u))
	if err != nil {
		return fmt.Errorf("client: closing upload session: %w", err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusNoContent, http.StatusOK, http.StatusNotFound: // already gone is fine
		return nil
	}
	return fmt.Errorf("client: closing upload session: status %d", res.StatusCode)
}
