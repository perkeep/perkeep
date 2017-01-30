/*
 *
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
	"context"
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/stats"
)

var (
	// ErrClientConnClosing indicates that the operation is illegal because
	// the ClientConn is closing.
	ErrClientConnClosing = errors.New("grpc: the client connection is closing")
	// ErrClientConnTimeout indicates that the ClientConn cannot establish the
	// underlying connections within the specified timeout.
	// DEPRECATED: Please use context.DeadlineExceeded instead. This error will be
	// removed in Q1 2017.
	ErrClientConnTimeout = errors.New("grpc: timed out when dialing")

	// errNoTransportSecurity indicates that there is no transport security
	// being set for ClientConn. Users should either set one or explicitly
	// call WithInsecure DialOption to disable security.
	errNoTransportSecurity = errors.New("grpc: no transport security set (use grpc.WithInsecure() explicitly or set credentials)")
	// errTransportCredentialsMissing indicates that users want to transmit security
	// information (e.g., oauth2 token) which requires secure connection on an insecure
	// connection.
	errTransportCredentialsMissing = errors.New("grpc: the credentials require transport level security (use grpc.WithTransportCredentials() to set)")
	// errCredentialsConflict indicates that grpc.WithTransportCredentials()
	// and grpc.WithInsecure() are both called for a connection.
	errCredentialsConflict = errors.New("grpc: transport credentials are set for an insecure connection (grpc.WithTransportCredentials() and grpc.WithInsecure() are both called)")
)

type clientOptions struct {
	codec Codec
	cp    Compressor
	dc    Decompressor

	// All may be zero:
	perRPCCreds    []credentials.PerRPCCredentials
	userAgent      string
	statsHandler   stats.Handler
	transportCreds credentials.TransportCredentials // only checked for non-nil for now
	insecure       bool                             // not TLS
}

// DialOption is a client option.
//
// Despite its name, it does not necessarily have anything to do with
// dialing.
//
// TODO: rename this.
type DialOption func(*clientOptions)

// WithCodec returns a DialOption which sets a codec for message marshaling and unmarshaling.
func WithCodec(c Codec) DialOption {
	return func(o *clientOptions) {
		o.codec = c
	}
}

// WithCompressor returns a DialOption which sets a CompressorGenerator for generating message
// compressor.
func WithCompressor(cp Compressor) DialOption {
	return func(o *clientOptions) {
		o.cp = cp
	}
}

// WithDecompressor returns a DialOption which sets a DecompressorGenerator for generating
// message decompressor.
func WithDecompressor(dc Decompressor) DialOption {
	return func(o *clientOptions) {
		o.dc = dc
	}
}

// WithPerRPCCredentials returns an option which sets
// credentials which will place auth state on each outbound RPC.
func WithPerRPCCredentials(creds credentials.PerRPCCredentials) DialOption {
	return func(o *clientOptions) {
		o.perRPCCreds = append(o.perRPCCreds, creds)
	}
}

// WithStatsHandler returns a DialOption that specifies the stats handler
// for all the RPCs and underlying network connections in this ClientConn.
func WithStatsHandler(h stats.Handler) DialOption {
	return func(o *clientOptions) {
		o.statsHandler = h
	}
}

// WithUserAgent returns a DialOption that specifies a user agent string for all the RPCs.
func WithUserAgent(s string) DialOption {
	return func(o *clientOptions) {
		o.userAgent = s
	}
}

// NewClient returns a new gRPC client for the provided target server.
// If the provided HTTP client is nil, http.DefaultClient is used.
// The target should be a URL scheme and authority, without a path.
// For example, "https://api.example.com" for TLS or "http://10.0.5.3:5000"
// for unencrypted HTTP/2.
//
// The returned type is named "ClientConn" for legacy reasons. It does
// not necessarily represent one actual connection. (It might be zero
// or multiple.)
func NewClient(hc *http.Client, target string, opts ...DialOption) (*ClientConn, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	if target == "" {
		return nil, errors.New("NewClientConn: missing required target parameter")
	}
	cc := &ClientConn{
		hc:     hc,
		target: target,
	}
	for _, opt := range opts {
		opt(&cc.opts)
	}
	// Set defaults.
	if cc.opts.codec == nil {
		cc.opts.codec = protoCodec{}
	}
	return cc, nil
}

// ConnectivityState indicates the state of a client connection.
type ConnectivityState int

const (
	// Idle indicates the ClientConn is idle.
	Idle ConnectivityState = iota
	// Connecting indicates the ClienConn is connecting.
	Connecting
	// Ready indicates the ClientConn is ready for work.
	Ready
	// TransientFailure indicates the ClientConn has seen a failure but expects to recover.
	TransientFailure
	// Shutdown indicates the ClientConn has started shutting down.
	Shutdown
)

func (s ConnectivityState) String() string {
	switch s {
	case Idle:
		return "IDLE"
	case Connecting:
		return "CONNECTING"
	case Ready:
		return "READY"
	case TransientFailure:
		return "TRANSIENT_FAILURE"
	case Shutdown:
		return "SHUTDOWN"
	default:
		panic(fmt.Sprintf("unknown connectivity state: %d", s))
	}
}

// ClientConn is a gRPC client.
//
// Despite its name, it is not necessarily a single
// connection. Depending on its underlying transport, it could be
// using zero or multiple TCP or other connections, and changing over
// time.
type ClientConn struct {
	target string // server URL prefix (scheme + authority, optional port), without path ("https://api.example.com"); use http:// for h2c
	opts   clientOptions
	hc     *http.Client

	sc *ServiceConfig // TODO(bradfitz): support; may be nil for now
}

func (cc *ClientConn) getMethodConfig(method string) (m MethodConfig, ok bool) {
	if cc.sc == nil {
		return
	}
	m, ok = cc.sc.Methods[method]
	return
}

// Close tears down the ClientConn and all underlying connections.
func (cc *ClientConn) Close() error {
	// TODO(bradfitz): something? maybe just close some cancel
	// chan that we then merge into all http Request's context
	// with some new context.Context impl? And then do some
	// http.Transport.CloseIdleConnections? But first research
	// what callers of this actually expect. It's unclear.
	return nil
}

// WithTransportCredentials is controls whether to use TLS or not for connections.
//
// Deprecated: this is only respected in a minimal form to let
// existing code in the wild work. Uew NewClient instead.
func WithTransportCredentials(creds credentials.TransportCredentials) DialOption {
	return func(o *clientOptions) {
		o.transportCreds = creds
	}
}

// WithInsecure returns a DialOption which disables transport security for this ClientConn.
// WithInsecure is mutually exclusive with use of WithTransportCredentials or https
// endpoints.
func WithInsecure() DialOption {
	return func(o *clientOptions) { o.insecure = true }
}

// DialContext is the old way to create a gRPC client.
//
// Deprecated: use NewClient instead.
func DialContext(ctx context.Context, target string, opts ...DialOption) (*ClientConn, error) {
	var o clientOptions
	for _, opt := range opts {
		opt(&o)
	}
	if (o.transportCreds != nil) == o.insecure {
		return nil, fmt.Errorf("only one of TransportCredentials or Insecure may be used")
	}
	if o.transportCreds != nil {
		if o.transportCreds.Info().SecurityProtocol == "tls" {
			target = "https://" + target
			// TODO(bradfitz): care about the rest? use the interface?
			// Not today. Prefer to delete the interace.
		} else {
			return nil, fmt.Errorf("unsupported TransportCredentials %+v", o.transportCreds.Info())
		}
	}
	if o.insecure {
		panic("TODO: implement insecure http2.Transport dialing")
	}
	return NewClient(nil, target, opts...)
}

// Dial is the old way to create a gRPC client.
//
// Deprecated: use NewClient instead. This only exists to let existing code
// in the wild work.
func Dial(target string, opts ...DialOption) (*ClientConn, error) {
	return DialContext(context.Background(), target, opts...)
}
