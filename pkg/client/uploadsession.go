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

	flushMu sync.Mutex // serializes Flush calls

	mu sync.Mutex
	// accepted are the blobs the server accepted, and hasn't reported
	// durable.
	accepted map[blob.Ref]uint32
	// early are blobs a Flush reported durable before their Upload call
	// noted them accepted.
	early map[blob.Ref]bool
	// durable are blobs found durable outside of a Flush, for the next
	// Flush to return.
	durable []blob.SizedRef
	// ack are the blobs the last successful Flush returned, for the next
	// to acknowledge.
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
		c:        c,
		id:       opened.Session,
		accepted: make(map[blob.Ref]uint32),
		early:    make(map[blob.Ref]bool),
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
	c.statsMutex.Lock()
	c.stats.UploadRequests.Blobs++
	c.stats.UploadRequests.Bytes += int64(h.Size)
	c.statsMutex.Unlock()

	ures, err := c.postUpload(ctx, h, s.id)
	if err != nil {
		return err
	}
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
	switch {
	case received:
		c.haveCache.NoteBlobExists(h.BlobRef, h.Size)
		s.durable = append(s.durable, blob.SizedRef{Ref: h.BlobRef, Size: h.Size})
	case s.early[h.BlobRef]:
		delete(s.early, h.BlobRef) // a Flush already returned it
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
	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	s.mu.Lock()
	before := make(map[blob.Ref]bool, len(s.accepted))
	for br := range s.accepted {
		before[br] = true
	}
	form := url.Values{}
	for _, br := range s.ack {
		form.Add("ack", br.String())
	}
	s.mu.Unlock()

	st, err := s.postFlush(ctx, form)
	be := &blobserver.BlobsError{Op: "upload session flush"}
	if errors.Is(err, uploadsession.ErrUnknownSession) {
		// Everything not yet reported durable is lost.
		s.mu.Lock()
		defer s.mu.Unlock()
		for br := range s.accepted {
			be.Add(br, err)
		}
		clear(s.accepted)
		clear(s.early)
		s.ack = nil
		durable := s.durable
		s.durable = nil
		return durable, be.OrNil()
	}
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	durable := append(s.durable, st.Durable...)
	s.durable = nil
	for _, sb := range st.Durable {
		if _, ok := s.accepted[sb.Ref]; ok {
			delete(s.accepted, sb.Ref)
		} else {
			// Its Upload hasn't noted it accepted yet.
			s.early[sb.Ref] = true
		}
		s.c.haveCache.NoteBlobExists(sb.Ref, sb.Size)
	}
	for _, fb := range st.Failed {
		be.Add(fb.BlobRef, fmt.Errorf("client: server failed to commit blob: %s", fb.Error))
		delete(s.accepted, fb.BlobRef)
	}
	// The server reports every blob added before the flush as durable or
	// failed; one it reported neither is lost.
	for br := range before {
		if _, ok := s.accepted[br]; ok {
			be.Add(br, errLostBlob)
			delete(s.accepted, br)
		}
	}
	s.ack = s.ack[:0]
	for _, sb := range st.Durable {
		s.ack = append(s.ack, sb.Ref)
	}
	return durable, be.OrNil()
}

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
