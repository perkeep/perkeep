/*
 * Copyright 2014, Google Inc.
 * All rights reserved.
 *
 * Redistribution and use in source and binary forms, with or without
 * modification, are permitted provided that the following conditions are
 * met:
 *
 *     * Redistributions of source code must retain the above copyright
 * notice, this list of conditions and the following disclaimer.
 *     * Redistributions in binary form must reproduce the above
 * copyright notice, this list of conditions and the following disclaimer
 * in the documentation and/or other materials provided with the
 * distribution.
 *     * Neither the name of Google Inc. nor the names of its
 * contributors may be used to endorse or promote products derived from
 * this software without specific prior written permission.
 *
 * THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
 * "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
 * LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
 * A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
 * OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
 * SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
 * LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
 * DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
 * THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
 * (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
 * OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
 *
 */

package grpc

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/context"
	"golang.org/x/net/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
)

// Invoke sends a non-streaming RPC request on the wire and returns
// after a response is received.
//
// Invoke is generally only called by generated code.
func Invoke(ctx context.Context, method string, args, reply interface{}, cc *ClientConn, opts ...CallOption) error {
	return cc.invoke(ctx, method, args, reply, opts...)
}

func (cc *ClientConn) invoke(ctx context.Context, method string, args, reply interface{}, opts ...CallOption) (reterr error) {
	c := defaultCallInfo
	if mc, ok := cc.getMethodConfig(method); ok {
		c.failFast = !mc.WaitForReady
		if mc.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, mc.Timeout)
			defer cancel()
		}
	}
	for _, o := range opts {
		if err := o.before(&c); err != nil {
			return toRPCErr(err)
		}
	}
	defer func() {
		for _, o := range opts {
			o.after(&c)
		}
	}()
	if EnableTracing {
		c.traceInfo.tr = trace.New("grpc.Sent."+methodFamily(method), method)
		defer c.traceInfo.tr.Finish()
		c.traceInfo.firstLine.client = true
		if deadline, ok := ctx.Deadline(); ok {
			c.traceInfo.firstLine.deadline = deadline.Sub(time.Now())
		}
		c.traceInfo.tr.LazyLog(&c.traceInfo.firstLine, false)
		// TODO(dsymonds): Arrange for c.traceInfo.firstLine.remoteAddr to be set.
		defer func() {
			if reterr != nil {
				c.traceInfo.tr.LazyLog(&fmtStringer{"%v", []interface{}{reterr}}, true)
				c.traceInfo.tr.SetError()
			}
		}()
	}
	sh := cc.opts.statsHandler
	if sh != nil {
		ctx = sh.TagRPC(ctx, &stats.RPCTagInfo{FullMethodName: method})
		begin := &stats.Begin{
			Client:    true,
			BeginTime: time.Now(),
			FailFast:  c.failFast,
		}
		sh.HandleRPC(ctx, begin)
	}
	if sh != nil {
		defer func() {
			end := &stats.End{
				Client:  true,
				EndTime: time.Now(),
				Error:   reterr,
			}
			sh.HandleRPC(ctx, end)
		}()
	}
	// TODO(bradfitz): non-failfast retries & proper error mapping.
	// Previously:
	// Retry a non-failfast RPC when
	// i) there is a connection error; or
	// ii) the server started to drain before this RPC was initiated.
	//if _, ok := err.(transport.ConnectionError); ok || err == transport.ErrStreamDrain {
	//if c.failFast {
	//return toRPCErr(err)
	//}
	// ...
	//return toRPCErr(err)
	//..
	//if err == errConnClosing || err == errConnUnavailable {
	//			if c.failFast {
	//				return Errorf(codes.Unavailable, "%v", err)
	//			}
	//			continue
	//}
	// All the other errors are treated as Internal errors.
	//		return Errorf(codes.Internal, "%v", err)

	var (
		cbuf        *bytes.Buffer
		statsOut    *stats.OutPayload
		compressAlg string
	)
	if cc.opts.cp != nil {
		compressAlg = cc.opts.cp.Type()
		cbuf = new(bytes.Buffer)
	}
	if c.traceInfo.tr != nil {
		c.traceInfo.tr.LazyLog(&payload{sent: true, msg: args}, true)
	}
	if cc.opts.statsHandler != nil {
		statsOut = &stats.OutPayload{
			Client: true,
		}
	}
	outBuf, err := encode(cc.opts.codec, args, cc.opts.cp, cbuf, statsOut)
	if err != nil {
		return Errorf(codes.Internal, "grpc: %v", err)
	}

	urlStr := cc.target + method
	req, err := http.NewRequest("POST", urlStr, bytes.NewReader(outBuf))
	if err != nil {
		return Errorf(codes.Internal, "grpc: %v", err)
	}
	hdr := req.Header
	hdr.Set("Te", "trailers")
	hdr.Set("Content-Type", "application/grpc") // WITHOUT +proto, or Google GFE 404s
	if compressAlg != "" {
		hdr.Set("Grpc-Encoding", compressAlg)
	}
	if cc.opts.userAgent != "" {
		// TODO(bradfitz): append our version? what'd it do before?
		hdr.Set("User-Agent", cc.opts.userAgent)
	}
	if md, ok := metadata.FromContext(ctx); ok {
		for k, vv := range md {
			k = http.CanonicalHeaderKey(k)
			for _, v := range vv {
				hdr.Add(k, v)
			}
		}
	}
	for _, rpcCred := range cc.opts.perRPCCreds {
		metadata, err := rpcCred.GetRequestMetadata(ctx, urlStr)
		if err != nil {
			return err
		}
		for k, v := range metadata {
			hdr.Add(k, v)
		}
	}
	if dl, ok := ctx.Deadline(); ok {
		timeout := dl.Sub(time.Now())
		hdr.Set("Grpc-Timeout", encodeTimeout(timeout))
	}

	res, err := cc.hc.Do(req)
	if err != nil {
		// TODO(bradfitz): error mapping; see TODOs above
		// For now:
		return Errorf(codes.Internal, "grpc: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		return Errorf(codes.Internal, "grpc: unexpected status code %v", res.Status)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/grpc") {
		return Errorf(codes.Internal, "grpc: unexpected content-type %q", ct)
	}
	maxMsgSize := 10 << 20 // TODO(bradfitz): set this

	var inPayload *stats.InPayload
	if cc.opts.statsHandler != nil {
		inPayload = &stats.InPayload{
			Client: true,
		}
	}

	p := &parser{r: res.Body}
	err = recvNew(p,
		cc.opts.codec,
		compressAlg,
		cc.opts.dc,
		reply,
		maxMsgSize,
		inPayload)

	immediateEOF := err == io.EOF

	if !immediateEOF {
		if err != nil {
			// TODO: error mapping; see TODOs above.
			return Errorf(codes.Internal, "grpc: %v", err)
		}

		// Check for only 1 message. We should hit an EOF immediately.
		// TODO(bradfitz): I believe. Looks like streaming RPCs go via another path.
		if _, err := res.Body.Read(p.header[:1]); err != io.EOF {
			return Errorf(codes.Internal, "grpc: malformed response with extra data after first message")
		}
	}

	// Now that we've seen res.Body return EOF,
	// the Trailers are valid.

	// Capture that and return it if that copt is set.
	statusStrs := res.Trailer["Grpc-Status"]
	if len(statusStrs) == 0 {
		return Errorf(codes.Internal, "grpc: malformed response from server; lacks grpc-status")
	}
	if len(statusStrs) > 1 {
		return Errorf(codes.Internal, "grpc: malformed response from server; multiple grpc-status values")
	}
	statusStr := statusStrs[0]
	statusCode, err := strconv.ParseUint(statusStr, 10, 32)
	if err != nil {
		return Errorf(codes.Internal, "grpc: malformed grpc-status from server")
	}
	if statusCode != 0 {
		msg := res.Trailer.Get("Grpc-Message")
		if msg == "" {
			msg = "no grpc-message from server"
		}
		return Errorf(codes.Code(statusCode), "grpc: %v", msg)
	}
	if immediateEOF {
		return Errorf(codes.Internal, "gprc: unexpected empty response from server with grpc-status 0")
	}

	if statsOut != nil {
		statsOut.SentTime = time.Now() // TODO(bradfitz): set this earlier probably
		cc.opts.statsHandler.HandleRPC(ctx, statsOut)
	}

	if c.traceInfo.tr != nil {
		c.traceInfo.tr.LazyLog(&payload{sent: false, msg: reply}, true)
	}
	return nil
}

const maxTimeoutValue int64 = 100000000 - 1

// div does integer division and round-up the result. Note that this is
// equivalent to (d+r-1)/r but has less chance to overflow.
func div(d, r time.Duration) int64 {
	if m := d % r; m > 0 {
		return int64(d/r + 1)
	}
	return int64(d / r)
}

// TODO(zhaoq): It is the simplistic and not bandwidth efficient. Improve it.
func encodeTimeout(t time.Duration) string {
	if t <= 0 {
		return "0n"
	}
	if d := div(t, time.Nanosecond); d <= maxTimeoutValue {
		return strconv.FormatInt(d, 10) + "n"
	}
	if d := div(t, time.Microsecond); d <= maxTimeoutValue {
		return strconv.FormatInt(d, 10) + "u"
	}
	if d := div(t, time.Millisecond); d <= maxTimeoutValue {
		return strconv.FormatInt(d, 10) + "m"
	}
	if d := div(t, time.Second); d <= maxTimeoutValue {
		return strconv.FormatInt(d, 10) + "S"
	}
	if d := div(t, time.Minute); d <= maxTimeoutValue {
		return strconv.FormatInt(d, 10) + "M"
	}
	// Note that maxTimeoutValue * time.Hour > MaxInt64.
	return strconv.FormatInt(div(t, time.Hour), 10) + "H"
}
