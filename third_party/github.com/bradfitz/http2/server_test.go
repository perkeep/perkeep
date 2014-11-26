// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// See https://code.google.com/p/go/source/browse/CONTRIBUTORS
// Licensed under the same terms as Go itself:
// https://code.google.com/p/go/source/browse/LICENSE

package http2

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"camlistore.org/third_party/github.com/bradfitz/http2/hpack"
)

type serverTester struct {
	cc        net.Conn // client conn
	t         *testing.T
	ts        *httptest.Server
	fr        *Framer
	logBuf    *bytes.Buffer
	sc        *serverConn
	logFilter []string // substrings to filter out
}

func newServerTester(t *testing.T, handler http.HandlerFunc) *serverTester {
	logBuf := new(bytes.Buffer)
	ts := httptest.NewUnstartedServer(handler)
	ConfigureServer(ts.Config, &Server{})

	st := &serverTester{
		t:      t,
		ts:     ts,
		logBuf: logBuf,
	}

	ts.TLS = ts.Config.TLSConfig // the httptest.Server has its own copy of this TLS config
	ts.Config.ErrorLog = log.New(io.MultiWriter(twriter{t: t, st: st}, logBuf), "", log.LstdFlags)
	ts.StartTLS()

	if VerboseLogs {
		t.Logf("Running test server at: %s", ts.URL)
	}
	var (
		mu sync.Mutex
		sc *serverConn
	)
	testHookGetServerConn = func(v *serverConn) {
		mu.Lock()
		defer mu.Unlock()
		sc = v
		sc.testHookCh = make(chan func())
	}
	cc, err := tls.Dial("tcp", ts.Listener.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{NextProtoTLS},
	})
	if err != nil {
		t.Fatal(err)
	}
	log.SetOutput(twriter{t: t, st: st})

	st.cc = cc
	st.fr = NewFramer(cc, cc)

	mu.Lock()
	st.sc = sc
	mu.Unlock() // unnecessary, but looks weird without.
	return st
}

func (st *serverTester) addLogFilter(phrase string) {
	st.logFilter = append(st.logFilter, phrase)
}

func (st *serverTester) stream(id uint32) *stream {
	ch := make(chan *stream, 1)
	st.sc.testHookCh <- func() {
		ch <- st.sc.streams[id]
	}
	return <-ch
}

func (st *serverTester) streamState(id uint32) streamState {
	ch := make(chan streamState, 1)
	st.sc.testHookCh <- func() {
		ch <- st.sc.state(id)
	}
	return <-ch
}

func (st *serverTester) Close() {
	st.ts.Close()
	st.cc.Close()
	log.SetOutput(os.Stderr)
}

// greet initiates the client's HTTP/2 connection into a state where
// frames may be sent.
func (st *serverTester) greet() {
	st.writePreface()
	st.writeInitialSettings()
	st.wantSettings()
	st.writeSettingsAck()
	st.wantSettingsAck()
}

func (st *serverTester) writePreface() {
	n, err := st.cc.Write(clientPreface)
	if err != nil {
		st.t.Fatalf("Error writing client preface: %v", err)
	}
	if n != len(clientPreface) {
		st.t.Fatalf("Writing client preface, wrote %d bytes; want %d", n, len(clientPreface))
	}
}

func (st *serverTester) writeInitialSettings() {
	if err := st.fr.WriteSettings(); err != nil {
		st.t.Fatalf("Error writing initial SETTINGS frame from client to server: %v", err)
	}
}

func (st *serverTester) writeSettingsAck() {
	if err := st.fr.WriteSettingsAck(); err != nil {
		st.t.Fatalf("Error writing ACK of server's SETTINGS: %v", err)
	}
}

func (st *serverTester) writeHeaders(p HeadersFrameParam) {
	if err := st.fr.WriteHeaders(p); err != nil {
		st.t.Fatalf("Error writing HEADERS: %v", err)
	}
}

// bodylessReq1 writes a HEADERS frames with StreamID 1 and EndStream and EndHeaders set.
func (st *serverTester) bodylessReq1(headers ...string) {
	st.writeHeaders(HeadersFrameParam{
		StreamID:      1, // clients send odd numbers
		BlockFragment: encodeHeader(st.t, headers...),
		EndStream:     true,
		EndHeaders:    true,
	})
}

func (st *serverTester) writeData(streamID uint32, endStream bool, data []byte) {
	if err := st.fr.WriteData(streamID, endStream, data); err != nil {
		st.t.Fatalf("Error writing DATA: %v", err)
	}
}

func (st *serverTester) readFrame() (Frame, error) {
	frc := make(chan Frame, 1)
	errc := make(chan error, 1)
	go func() {
		fr, err := st.fr.ReadFrame()
		if err != nil {
			errc <- err
		} else {
			frc <- fr
		}
	}()
	t := time.NewTimer(2 * time.Second)
	defer t.Stop()
	select {
	case f := <-frc:
		return f, nil
	case err := <-errc:
		return nil, err
	case <-t.C:
		return nil, errors.New("timeout waiting for frame")
	}
}

func (st *serverTester) wantHeaders() *HeadersFrame {
	f, err := st.readFrame()
	if err != nil {
		st.t.Fatalf("Error while expecting a HEADERS frame: %v", err)
	}
	hf, ok := f.(*HeadersFrame)
	if !ok {
		st.t.Fatalf("got a %T; want *HeadersFrame", f)
	}
	return hf
}

func (st *serverTester) wantData() *DataFrame {
	f, err := st.readFrame()
	if err != nil {
		st.t.Fatalf("Error while expecting a DATA frame: %v", err)
	}
	df, ok := f.(*DataFrame)
	if !ok {
		st.t.Fatalf("got a %T; want *DataFrame", f)
	}
	return df
}

func (st *serverTester) wantSettings() *SettingsFrame {
	f, err := st.readFrame()
	if err != nil {
		st.t.Fatalf("Error while expecting a SETTINGS frame: %v", err)
	}
	sf, ok := f.(*SettingsFrame)
	if !ok {
		st.t.Fatalf("got a %T; want *SettingsFrame", f)
	}
	return sf
}

func (st *serverTester) wantPing() *PingFrame {
	f, err := st.readFrame()
	if err != nil {
		st.t.Fatalf("Error while expecting a PING frame: %v", err)
	}
	pf, ok := f.(*PingFrame)
	if !ok {
		st.t.Fatalf("got a %T; want *PingFrame", f)
	}
	return pf
}

func (st *serverTester) wantGoAway() *GoAwayFrame {
	f, err := st.readFrame()
	if err != nil {
		st.t.Fatalf("Error while expecting a GOAWAY frame: %v", err)
	}
	gf, ok := f.(*GoAwayFrame)
	if !ok {
		st.t.Fatalf("got a %T; want *GoAwayFrame", f)
	}
	return gf
}

func (st *serverTester) wantRSTStream(streamID uint32, errCode ErrCode) {
	f, err := st.readFrame()
	if err != nil {
		st.t.Fatalf("Error while expecting an RSTStream frame: %v", err)
	}
	rs, ok := f.(*RSTStreamFrame)
	if !ok {
		st.t.Fatalf("got a %T; want *RSTStreamFrame", f)
	}
	if rs.FrameHeader.StreamID != streamID {
		st.t.Fatalf("RSTStream StreamID = %d; want %d", rs.FrameHeader.StreamID, streamID)
	}
	if rs.ErrCode != errCode {
		st.t.Fatalf("RSTStream ErrCode = %d (%s); want %d (%s)", rs.ErrCode, rs.ErrCode, errCode, errCode)
	}
}

func (st *serverTester) wantWindowUpdate(streamID, incr uint32) {
	f, err := st.readFrame()
	if err != nil {
		st.t.Fatalf("Error while expecting an RSTStream frame: %v", err)
	}
	wu, ok := f.(*WindowUpdateFrame)
	if !ok {
		st.t.Fatalf("got a %T; want *WindowUpdateFrame", f)
	}
	if wu.FrameHeader.StreamID != streamID {
		st.t.Fatalf("WindowUpdate StreamID = %d; want %d", wu.FrameHeader.StreamID, streamID)
	}
	if wu.Increment != incr {
		st.t.Fatalf("WindowUpdate increment = %d; want %d", wu.Increment, incr)
	}
}

func (st *serverTester) wantSettingsAck() {
	f, err := st.readFrame()
	if err != nil {
		st.t.Fatal(err)
	}
	sf, ok := f.(*SettingsFrame)
	if !ok {
		st.t.Fatalf("Wanting a settings ACK, received a %T", f)
	}
	if !sf.Header().Flags.Has(FlagSettingsAck) {
		st.t.Fatal("Settings Frame didn't have ACK set")
	}

}

func TestServer(t *testing.T) {
	gotReq := make(chan bool, 1)
	st := newServerTester(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Foo", "Bar")
		gotReq <- true
	})
	defer st.Close()

	covers("3.5", `
		The server connection preface consists of a potentially empty
		SETTINGS frame ([SETTINGS]) that MUST be the first frame the
		server sends in the HTTP/2 connection.
	`)

	st.writePreface()
	st.writeInitialSettings()
	st.wantSettings()
	st.writeSettingsAck()
	st.wantSettingsAck()

	st.writeHeaders(HeadersFrameParam{
		StreamID:      1, // clients send odd numbers
		BlockFragment: encodeHeader(t),
		EndStream:     true, // no DATA frames
		EndHeaders:    true,
	})

	select {
	case <-gotReq:
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for request")
	}
}

func TestServer_Request_Get(t *testing.T) {
	testServerRequest(t, func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1, // clients send odd numbers
			BlockFragment: encodeHeader(t, "foo-bar", "some-value"),
			EndStream:     true, // no DATA frames
			EndHeaders:    true,
		})
	}, func(r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Method = %q; want GET", r.Method)
		}
		if r.URL.Path != "/" {
			t.Errorf("URL.Path = %q; want /", r.URL.Path)
		}
		if r.ContentLength != 0 {
			t.Errorf("ContentLength = %v; want 0", r.ContentLength)
		}
		if r.Close {
			t.Error("Close = true; want false")
		}
		if !strings.Contains(r.RemoteAddr, ":") {
			t.Errorf("RemoteAddr = %q; want something with a colon", r.RemoteAddr)
		}
		if r.Proto != "HTTP/2.0" || r.ProtoMajor != 2 || r.ProtoMinor != 0 {
			t.Errorf("Proto = %q Major=%v,Minor=%v; want HTTP/2.0", r.Proto, r.ProtoMajor, r.ProtoMinor)
		}
		wantHeader := http.Header{
			"Foo-Bar": []string{"some-value"},
		}
		if !reflect.DeepEqual(r.Header, wantHeader) {
			t.Errorf("Header = %#v; want %#v", r.Header, wantHeader)
		}
		if n, err := r.Body.Read([]byte(" ")); err != io.EOF || n != 0 {
			t.Errorf("Read = %d, %v; want 0, EOF", n, err)
		}
	})
}

func TestServer_Request_Get_PathSlashes(t *testing.T) {
	testServerRequest(t, func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1, // clients send odd numbers
			BlockFragment: encodeHeader(t, ":path", "/%2f/"),
			EndStream:     true, // no DATA frames
			EndHeaders:    true,
		})
	}, func(r *http.Request) {
		if r.RequestURI != "/%2f/" {
			t.Errorf("RequestURI = %q; want /%2f/", r.RequestURI)
		}
		if r.URL.Path != "///" {
			t.Errorf("URL.Path = %q; want ///", r.URL.Path)
		}
	})
}

// TODO: add a test with EndStream=true on the HEADERS but setting a
// Content-Length anyway.  Should we just omit it and force it to
// zero?

func TestServer_Request_Post_NoContentLength_EndStream(t *testing.T) {
	testServerRequest(t, func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1, // clients send odd numbers
			BlockFragment: encodeHeader(t, ":method", "POST"),
			EndStream:     true,
			EndHeaders:    true,
		})
	}, func(r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q; want POST", r.Method)
		}
		if r.ContentLength != 0 {
			t.Errorf("ContentLength = %v; want 0", r.ContentLength)
		}
		if n, err := r.Body.Read([]byte(" ")); err != io.EOF || n != 0 {
			t.Errorf("Read = %d, %v; want 0, EOF", n, err)
		}
	})
}

func TestServer_Request_Post_Body_ImmediateEOF(t *testing.T) {
	testBodyContents(t, -1, "", func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1, // clients send odd numbers
			BlockFragment: encodeHeader(t, ":method", "POST"),
			EndStream:     false, // to say DATA frames are coming
			EndHeaders:    true,
		})
		st.writeData(1, true, nil) // just kidding. empty body.
	})
}

func TestServer_Request_Post_Body_OneData(t *testing.T) {
	const content = "Some content"
	testBodyContents(t, -1, content, func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1, // clients send odd numbers
			BlockFragment: encodeHeader(t, ":method", "POST"),
			EndStream:     false, // to say DATA frames are coming
			EndHeaders:    true,
		})
		st.writeData(1, true, []byte(content))
	})
}

func TestServer_Request_Post_Body_TwoData(t *testing.T) {
	const content = "Some content"
	testBodyContents(t, -1, content, func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1, // clients send odd numbers
			BlockFragment: encodeHeader(t, ":method", "POST"),
			EndStream:     false, // to say DATA frames are coming
			EndHeaders:    true,
		})
		st.writeData(1, false, []byte(content[:5]))
		st.writeData(1, true, []byte(content[5:]))
	})
}

func TestServer_Request_Post_Body_ContentLength_Correct(t *testing.T) {
	const content = "Some content"
	testBodyContents(t, int64(len(content)), content, func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID: 1, // clients send odd numbers
			BlockFragment: encodeHeader(t,
				":method", "POST",
				"content-length", strconv.Itoa(len(content)),
			),
			EndStream:  false, // to say DATA frames are coming
			EndHeaders: true,
		})
		st.writeData(1, true, []byte(content))
	})
}

func TestServer_Request_Post_Body_ContentLength_TooLarge(t *testing.T) {
	testBodyContentsFail(t, 3, "request declared a Content-Length of 3 but only wrote 2 bytes",
		func(st *serverTester) {
			st.writeHeaders(HeadersFrameParam{
				StreamID: 1, // clients send odd numbers
				BlockFragment: encodeHeader(t,
					":method", "POST",
					"content-length", "3",
				),
				EndStream:  false, // to say DATA frames are coming
				EndHeaders: true,
			})
			st.writeData(1, true, []byte("12"))
		})
}

func TestServer_Request_Post_Body_ContentLength_TooSmall(t *testing.T) {
	testBodyContentsFail(t, 4, "sender tried to send more than declared Content-Length of 4 bytes",
		func(st *serverTester) {
			st.writeHeaders(HeadersFrameParam{
				StreamID: 1, // clients send odd numbers
				BlockFragment: encodeHeader(t,
					":method", "POST",
					"content-length", "4",
				),
				EndStream:  false, // to say DATA frames are coming
				EndHeaders: true,
			})
			st.writeData(1, true, []byte("12345"))
		})
}

func testBodyContents(t *testing.T, wantContentLength int64, wantBody string, write func(st *serverTester)) {
	testServerRequest(t, write, func(r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q; want POST", r.Method)
		}
		if r.ContentLength != wantContentLength {
			t.Errorf("ContentLength = %v; want %d", r.ContentLength, wantContentLength)
		}
		all, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(all) != wantBody {
			t.Errorf("Read = %q; want %q", all, wantBody)
		}
		if err := r.Body.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
}

func testBodyContentsFail(t *testing.T, wantContentLength int64, wantReadError string, write func(st *serverTester)) {
	testServerRequest(t, write, func(r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q; want POST", r.Method)
		}
		if r.ContentLength != wantContentLength {
			t.Errorf("ContentLength = %v; want %d", r.ContentLength, wantContentLength)
		}
		all, err := ioutil.ReadAll(r.Body)
		if err == nil {
			t.Fatalf("expected an error (%q) reading from the body. Successfully read %q instead.",
				wantReadError, all)
		}
		if !strings.Contains(err.Error(), wantReadError) {
			t.Fatalf("Body.Read = %v; want substring %q", err, wantReadError)
		}
		if err := r.Body.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
}

// Using a Host header, instead of :authority
func TestServer_Request_Get_Host(t *testing.T) {
	const host = "example.com"
	testServerRequest(t, func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1, // clients send odd numbers
			BlockFragment: encodeHeader(t, "host", host),
			EndStream:     true,
			EndHeaders:    true,
		})
	}, func(r *http.Request) {
		if r.Host != host {
			t.Errorf("Host = %q; want %q", r.Host, host)
		}
	})
}

// Using an :authority pseudo-header, instead of Host
func TestServer_Request_Get_Authority(t *testing.T) {
	const host = "example.com"
	testServerRequest(t, func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1, // clients send odd numbers
			BlockFragment: encodeHeader(t, ":authority", host),
			EndStream:     true,
			EndHeaders:    true,
		})
	}, func(r *http.Request) {
		if r.Host != host {
			t.Errorf("Host = %q; want %q", r.Host, host)
		}
	})
}

func TestServer_Request_WithContinuation(t *testing.T) {
	wantHeader := http.Header{
		"Foo-One":   []string{"value-one"},
		"Foo-Two":   []string{"value-two"},
		"Foo-Three": []string{"value-three"},
	}
	testServerRequest(t, func(st *serverTester) {
		fullHeaders := encodeHeader(t,
			"foo-one", "value-one",
			"foo-two", "value-two",
			"foo-three", "value-three",
		)
		remain := fullHeaders
		chunks := 0
		for len(remain) > 0 {
			const maxChunkSize = 5
			chunk := remain
			if len(chunk) > maxChunkSize {
				chunk = chunk[:maxChunkSize]
			}
			remain = remain[len(chunk):]

			if chunks == 0 {
				st.writeHeaders(HeadersFrameParam{
					StreamID:      1, // clients send odd numbers
					BlockFragment: chunk,
					EndStream:     true,  // no DATA frames
					EndHeaders:    false, // we'll have continuation frames
				})
			} else {
				err := st.fr.WriteContinuation(1, len(remain) == 0, chunk)
				if err != nil {
					t.Fatal(err)
				}
			}
			chunks++
		}
		if chunks < 2 {
			t.Fatal("too few chunks")
		}
	}, func(r *http.Request) {
		if !reflect.DeepEqual(r.Header, wantHeader) {
			t.Errorf("Header = %#v; want %#v", r.Header, wantHeader)
		}
	})
}

// Concatenated cookie headers. ("8.1.2.5 Compressing the Cookie Header Field")
func TestServer_Request_CookieConcat(t *testing.T) {
	const host = "example.com"
	testServerRequest(t, func(st *serverTester) {
		st.bodylessReq1(
			":authority", host,
			"cookie", "a=b",
			"cookie", "c=d",
			"cookie", "e=f",
		)
	}, func(r *http.Request) {
		const want = "a=b; c=d; e=f"
		if got := r.Header.Get("Cookie"); got != want {
			t.Errorf("Cookie = %q; want %q", got, want)
		}
	})
}

func TestServer_Request_Reject_CapitalHeader(t *testing.T) {
	testRejectRequest(t, func(st *serverTester) { st.bodylessReq1("UPPER", "v") })
}

func TestServer_Request_Reject_Pseudo_Missing_method(t *testing.T) {
	testRejectRequest(t, func(st *serverTester) { st.bodylessReq1(":method", "") })
}

func TestServer_Request_Reject_Pseudo_ExactlyOne(t *testing.T) {
	// 8.1.2.3 Request Pseudo-Header Fields
	// "All HTTP/2 requests MUST include exactly one valid value" ...
	testRejectRequest(t, func(st *serverTester) {
		st.addLogFilter("duplicate pseudo-header")
		st.bodylessReq1(":method", "GET", ":method", "POST")
	})
}

func TestServer_Request_Reject_Pseudo_AfterRegular(t *testing.T) {
	// 8.1.2.3 Request Pseudo-Header Fields
	// "All pseudo-header fields MUST appear in the header block
	// before regular header fields. Any request or response that
	// contains a pseudo-header field that appears in a header
	// block after a regular header field MUST be treated as
	// malformed (Section 8.1.2.6)."
	testRejectRequest(t, func(st *serverTester) {
		st.addLogFilter("pseudo-header after regular header")
		var buf bytes.Buffer
		enc := hpack.NewEncoder(&buf)
		enc.WriteField(hpack.HeaderField{Name: ":method", Value: "GET"})
		enc.WriteField(hpack.HeaderField{Name: "regular", Value: "foobar"})
		enc.WriteField(hpack.HeaderField{Name: ":path", Value: "/"})
		enc.WriteField(hpack.HeaderField{Name: ":scheme", Value: "https"})
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1, // clients send odd numbers
			BlockFragment: buf.Bytes(),
			EndStream:     true,
			EndHeaders:    true,
		})
	})
}

func TestServer_Request_Reject_Pseudo_Missing_path(t *testing.T) {
	testRejectRequest(t, func(st *serverTester) { st.bodylessReq1(":path", "") })
}

func TestServer_Request_Reject_Pseudo_Missing_scheme(t *testing.T) {
	testRejectRequest(t, func(st *serverTester) { st.bodylessReq1(":scheme", "") })
}

func TestServer_Request_Reject_Pseudo_scheme_invalid(t *testing.T) {
	testRejectRequest(t, func(st *serverTester) { st.bodylessReq1(":scheme", "bogus") })
}

func TestServer_Request_Reject_Pseudo_Unknown(t *testing.T) {
	testRejectRequest(t, func(st *serverTester) {
		st.addLogFilter(`invalid pseudo-header ":unknown_thing"`)
		st.bodylessReq1(":unknown_thing", "")
	})
}

func testRejectRequest(t *testing.T, send func(*serverTester)) {
	st := newServerTester(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server request made it to handler; should've been rejected")
	})
	defer st.Close()

	st.greet()
	send(st)
	st.wantRSTStream(1, ErrCodeProtocol)
}

func TestServer_Ping(t *testing.T) {
	st := newServerTester(t, nil)
	defer st.Close()
	st.greet()

	// Server should ignore this one, since it has ACK set.
	ackPingData := [8]byte{1, 2, 4, 8, 16, 32, 64, 128}
	if err := st.fr.WritePing(true, ackPingData); err != nil {
		t.Fatal(err)
	}

	// But the server should reply to this one, since ACK is false.
	pingData := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	if err := st.fr.WritePing(false, pingData); err != nil {
		t.Fatal(err)
	}

	pf := st.wantPing()
	if !pf.Flags.Has(FlagPingAck) {
		t.Error("response ping doesn't have ACK set")
	}
	if pf.Data != pingData {
		t.Errorf("response ping has data %q; want %q", pf.Data, pingData)
	}
}

func TestServer_RejectsLargeFrames(t *testing.T) {
	st := newServerTester(t, nil)
	defer st.Close()
	st.greet()

	// Write too large of a frame (too large by one byte)
	// We ignore the return value because it's expected that the server
	// will only read the first 9 bytes (the headre) and then disconnect.
	st.fr.WriteRawFrame(0xff, 0, 0, make([]byte, defaultMaxReadFrameSize+1))

	gf := st.wantGoAway()
	if gf.ErrCode != ErrCodeFrameSize {
		t.Errorf("GOAWAY err = %v; want %v", gf.ErrCode, ErrCodeFrameSize)
	}
}

func TestServer_Handler_Sends_WindowUpdate(t *testing.T) {
	puppet := newHandlerPuppet()
	st := newServerTester(t, func(w http.ResponseWriter, r *http.Request) {
		puppet.act(w, r)
	})
	defer st.Close()
	defer puppet.done()

	st.greet()

	st.writeHeaders(HeadersFrameParam{
		StreamID:      1, // clients send odd numbers
		BlockFragment: encodeHeader(t, ":method", "POST"),
		EndStream:     false, // data coming
		EndHeaders:    true,
	})
	st.writeData(1, true, []byte("abcdef"))
	puppet.do(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 3)
		_, err := io.ReadFull(r.Body, buf)
		if err != nil {
			t.Error(err)
			return
		}
		if string(buf) != "abc" {
			t.Errorf("read %q; want abc", buf)
		}
	})
	st.wantWindowUpdate(0, 3)
	st.wantWindowUpdate(1, 3)
	puppet.do(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 3)
		_, err := io.ReadFull(r.Body, buf)
		if err != nil {
			t.Error(err)
			return
		}
		if string(buf) != "def" {
			t.Errorf("read %q; want abc", buf)
		}
	})
	st.wantWindowUpdate(0, 3)
	st.wantWindowUpdate(1, 3)
}

func TestServer_Send_GoAway_After_Bogus_WindowUpdate(t *testing.T) {
	st := newServerTester(t, nil)
	defer st.Close()
	st.greet()
	if err := st.fr.WriteWindowUpdate(0, 1<<31-1); err != nil {
		t.Fatal(err)
	}
	gf := st.wantGoAway()
	if gf.ErrCode != ErrCodeFlowControl {
		t.Errorf("GOAWAY err = %v; want %v", gf.ErrCode, ErrCodeFlowControl)
	}
	if gf.LastStreamID != 0 {
		t.Errorf("GOAWAY last stream ID = %v; want %v", gf.LastStreamID, 0)
	}
}

func TestServer_Send_RstStream_After_Bogus_WindowUpdate(t *testing.T) {
	inHandler := make(chan bool)
	blockHandler := make(chan bool)
	st := newServerTester(t, func(w http.ResponseWriter, r *http.Request) {
		inHandler <- true
		<-blockHandler
	})
	defer st.Close()
	defer close(blockHandler)
	st.greet()
	st.writeHeaders(HeadersFrameParam{
		StreamID:      1,
		BlockFragment: encodeHeader(st.t, ":method", "POST"),
		EndStream:     false, // keep it open
		EndHeaders:    true,
	})
	<-inHandler
	// Send a bogus window update:
	if err := st.fr.WriteWindowUpdate(1, 1<<31-1); err != nil {
		t.Fatal(err)
	}
	st.wantRSTStream(1, ErrCodeFlowControl)
}

// testServerPostUnblock sends a hanging POST with unsent data to handler,
// then runs fn once in the handler, and verifies that the error returned from
// handler is acceptable. It fails if takes over 5 seconds for handler to exit.
func testServerPostUnblock(t *testing.T,
	handler func(http.ResponseWriter, *http.Request) error,
	fn func(*serverTester),
	checkErr func(error),
	otherHeaders ...string) {
	inHandler := make(chan bool)
	errc := make(chan error, 1)
	st := newServerTester(t, func(w http.ResponseWriter, r *http.Request) {
		inHandler <- true
		errc <- handler(w, r)
	})
	st.greet()
	st.writeHeaders(HeadersFrameParam{
		StreamID:      1,
		BlockFragment: encodeHeader(st.t, append([]string{":method", "POST"}, otherHeaders...)...),
		EndStream:     false, // keep it open
		EndHeaders:    true,
	})
	<-inHandler
	fn(st)
	select {
	case err := <-errc:
		if checkErr != nil {
			checkErr(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Handler to return")
	}
	st.Close()
}

func TestServer_RSTStream_Unblocks_Read(t *testing.T) {
	testServerPostUnblock(t,
		func(w http.ResponseWriter, r *http.Request) (err error) {
			_, err = r.Body.Read(make([]byte, 1))
			return
		},
		func(st *serverTester) {
			if err := st.fr.WriteRSTStream(1, ErrCodeCancel); err != nil {
				t.Fatal(err)
			}
		},
		func(err error) {
			if err == nil {
				t.Error("unexpected nil error from Request.Body.Read")
			}
		},
	)
}

func TestServer_DeadConn_Unblocks_Read(t *testing.T) {
	testServerPostUnblock(t,
		func(w http.ResponseWriter, r *http.Request) (err error) {
			_, err = r.Body.Read(make([]byte, 1))
			return
		},
		func(st *serverTester) { st.cc.Close() },
		func(err error) {
			if err == nil {
				t.Error("unexpected nil error from Request.Body.Read")
			}
		},
	)
}

var blockUntilClosed = func(w http.ResponseWriter, r *http.Request) error {
	<-w.(http.CloseNotifier).CloseNotify()
	return nil
}

func TestServer_CloseNotify_After_RSTStream(t *testing.T) {
	testServerPostUnblock(t, blockUntilClosed, func(st *serverTester) {
		if err := st.fr.WriteRSTStream(1, ErrCodeCancel); err != nil {
			t.Fatal(err)
		}
	}, nil)
}

func TestServer_CloseNotify_After_ConnClose(t *testing.T) {
	testServerPostUnblock(t, blockUntilClosed, func(st *serverTester) { st.cc.Close() }, nil)
}

// that CloseNotify unblocks after a stream error due to the client's
// problem that's unrelated to them explicitly canceling it (which is
// TestServer_CloseNotify_After_RSTStream above)
func TestServer_CloseNotify_After_StreamError(t *testing.T) {
	testServerPostUnblock(t, blockUntilClosed, func(st *serverTester) {
		// data longer than declared Content-Length => stream error
		st.writeData(1, true, []byte("1234"))
	}, nil, "content-length", "3")
}

func TestServer_StateTransitions(t *testing.T) {
	var st *serverTester
	inHandler := make(chan bool)
	writeData := make(chan bool)
	leaveHandler := make(chan bool)
	st = newServerTester(t, func(w http.ResponseWriter, r *http.Request) {
		inHandler <- true
		if st.stream(1) == nil {
			t.Errorf("nil stream 1 in handler")
		}
		if got, want := st.streamState(1), stateOpen; got != want {
			t.Errorf("in handler, state is %v; want %v", got, want)
		}
		writeData <- true
		if n, err := r.Body.Read(make([]byte, 1)); n != 0 || err != io.EOF {
			t.Errorf("body read = %d, %v; want 0, EOF", n, err)
		}
		if got, want := st.streamState(1), stateHalfClosedRemote; got != want {
			t.Errorf("in handler, state is %v; want %v", got, want)
		}

		<-leaveHandler
	})
	st.greet()
	if st.stream(1) != nil {
		t.Fatal("stream 1 should be empty")
	}
	if got := st.streamState(1); got != stateIdle {
		t.Fatalf("stream 1 should be idle; got %v", got)
	}

	st.writeHeaders(HeadersFrameParam{
		StreamID:      1,
		BlockFragment: encodeHeader(st.t, ":method", "POST"),
		EndStream:     false, // keep it open
		EndHeaders:    true,
	})
	<-inHandler
	<-writeData
	st.writeData(1, true, nil)

	leaveHandler <- true
	hf := st.wantHeaders()
	if !hf.StreamEnded() {
		t.Fatal("expected END_STREAM flag")
	}

	if got, want := st.streamState(1), stateClosed; got != want {
		t.Errorf("at end, state is %v; want %v", got, want)
	}
	if st.stream(1) != nil {
		t.Fatal("at end, stream 1 should be gone")
	}
}

// test HEADERS w/o EndHeaders + another HEADERS (should get rejected)
func TestServer_Rejects_HeadersNoEnd_Then_Headers(t *testing.T) {
	testServerRejects(t, func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1,
			BlockFragment: encodeHeader(st.t),
			EndStream:     true,
			EndHeaders:    false,
		})
		st.writeHeaders(HeadersFrameParam{ // Not a continuation.
			StreamID:      3, // different stream.
			BlockFragment: encodeHeader(st.t),
			EndStream:     true,
			EndHeaders:    true,
		})
	})
}

// test HEADERS w/o EndHeaders + PING (should get rejected)
func TestServer_Rejects_HeadersNoEnd_Then_Ping(t *testing.T) {
	testServerRejects(t, func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1,
			BlockFragment: encodeHeader(st.t),
			EndStream:     true,
			EndHeaders:    false,
		})
		if err := st.fr.WritePing(false, [8]byte{}); err != nil {
			t.Fatal(err)
		}
	})
}

// test HEADERS w/ EndHeaders + a continuation HEADERS (should get rejected)
func TestServer_Rejects_HeadersEnd_Then_Continuation(t *testing.T) {
	testServerRejects(t, func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1,
			BlockFragment: encodeHeader(st.t),
			EndStream:     true,
			EndHeaders:    true,
		})
		st.wantHeaders()
		if err := st.fr.WriteContinuation(1, true, encodeHeaderNoImplicit(t, "foo", "bar")); err != nil {
			t.Fatal(err)
		}
	})
}

// test HEADERS w/o EndHeaders + a continuation HEADERS on wrong stream ID
func TestServer_Rejects_HeadersNoEnd_Then_ContinuationWrongStream(t *testing.T) {
	testServerRejects(t, func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1,
			BlockFragment: encodeHeader(st.t),
			EndStream:     true,
			EndHeaders:    false,
		})
		if err := st.fr.WriteContinuation(3, true, encodeHeaderNoImplicit(t, "foo", "bar")); err != nil {
			t.Fatal(err)
		}
	})
}

// No HEADERS on stream 0.
func TestServer_Rejects_Headers0(t *testing.T) {
	testServerRejects(t, func(st *serverTester) {
		st.fr.AllowIllegalWrites = true
		st.writeHeaders(HeadersFrameParam{
			StreamID:      0,
			BlockFragment: encodeHeader(st.t),
			EndStream:     true,
			EndHeaders:    true,
		})
	})
}

// No CONTINUATION on stream 0.
func TestServer_Rejects_Continuation0(t *testing.T) {
	testServerRejects(t, func(st *serverTester) {
		st.fr.AllowIllegalWrites = true
		if err := st.fr.WriteContinuation(0, true, encodeHeader(t)); err != nil {
			t.Fatal(err)
		}
	})
}

// testServerRejects tests that the server hangs up with a GOAWAY
// frame and a server close after the client does something
// deserving a CONNECTION_ERROR.
func testServerRejects(t *testing.T, writeReq func(*serverTester)) {
	st := newServerTester(t, func(w http.ResponseWriter, r *http.Request) {})
	st.addLogFilter("connection error: PROTOCOL_ERROR")
	defer st.Close()
	st.greet()
	writeReq(st)

	st.wantGoAway()
	errc := make(chan error, 1)
	go func() {
		fr, err := st.fr.ReadFrame()
		if err == nil {
			err = fmt.Errorf("got frame of type %T", fr)
		}
		errc <- err
	}()
	select {
	case err := <-errc:
		if err != io.EOF {
			t.Errorf("ReadFrame = %v; want io.EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for disconnect")
	}
}

// testServerRequest sets up an idle HTTP/2 connection and lets you
// write a single request with writeReq, and then verify that the
// *http.Request is built correctly in checkReq.
func testServerRequest(t *testing.T, writeReq func(*serverTester), checkReq func(*http.Request)) {
	gotReq := make(chan bool, 1)
	st := newServerTester(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			t.Fatal("nil Body")
		}
		checkReq(r)
		gotReq <- true
	})
	defer st.Close()

	st.greet()
	writeReq(st)

	select {
	case <-gotReq:
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for request")
	}
}

func getSlash(st *serverTester) { st.bodylessReq1() }

func TestServer_Response_NoData(t *testing.T) {
	testServerResponse(t, func(w http.ResponseWriter, r *http.Request) error {
		// Nothing.
		return nil
	}, func(st *serverTester) {
		getSlash(st)
		hf := st.wantHeaders()
		if !hf.StreamEnded() {
			t.Fatal("want END_STREAM flag")
		}
		if !hf.HeadersEnded() {
			t.Fatal("want END_HEADERS flag")
		}
	})
}

func TestServer_Response_NoData_Header_FooBar(t *testing.T) {
	testServerResponse(t, func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("Foo-Bar", "some-value")
		return nil
	}, func(st *serverTester) {
		getSlash(st)
		hf := st.wantHeaders()
		if !hf.StreamEnded() {
			t.Fatal("want END_STREAM flag")
		}
		if !hf.HeadersEnded() {
			t.Fatal("want END_HEADERS flag")
		}
		goth := decodeHeader(t, hf.HeaderBlockFragment())
		wanth := [][2]string{
			{":status", "200"},
			{"foo-bar", "some-value"},
			{"content-type", "text/plain; charset=utf-8"},
			{"content-length", "0"},
		}
		if !reflect.DeepEqual(goth, wanth) {
			t.Errorf("Got headers %v; want %v", goth, wanth)
		}
	})
}

func TestServer_Response_Data_Sniff_DoesntOverride(t *testing.T) {
	const msg = "<html>this is HTML."
	testServerResponse(t, func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("Content-Type", "foo/bar")
		io.WriteString(w, msg)
		return nil
	}, func(st *serverTester) {
		getSlash(st)
		hf := st.wantHeaders()
		if hf.StreamEnded() {
			t.Fatal("don't want END_STREAM, expecting data")
		}
		if !hf.HeadersEnded() {
			t.Fatal("want END_HEADERS flag")
		}
		goth := decodeHeader(t, hf.HeaderBlockFragment())
		wanth := [][2]string{
			{":status", "200"},
			{"content-type", "foo/bar"},
			{"content-length", strconv.Itoa(len(msg))},
		}
		if !reflect.DeepEqual(goth, wanth) {
			t.Errorf("Got headers %v; want %v", goth, wanth)
		}
		df := st.wantData()
		if !df.StreamEnded() {
			t.Error("expected DATA to have END_STREAM flag")
		}
		if got := string(df.Data()); got != msg {
			t.Errorf("got DATA %q; want %q", got, msg)
		}
	})
}

func TestServer_Response_TransferEncoding_chunked(t *testing.T) {
	const msg = "hi"
	testServerResponse(t, func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("Transfer-Encoding", "chunked") // should be stripped
		io.WriteString(w, msg)
		return nil
	}, func(st *serverTester) {
		getSlash(st)
		hf := st.wantHeaders()
		goth := decodeHeader(t, hf.HeaderBlockFragment())
		wanth := [][2]string{
			{":status", "200"},
			{"content-type", "text/plain; charset=utf-8"},
			{"content-length", strconv.Itoa(len(msg))},
		}
		if !reflect.DeepEqual(goth, wanth) {
			t.Errorf("Got headers %v; want %v", goth, wanth)
		}
	})
}

// Header accessed only after the initial write.
func TestServer_Response_Data_IgnoreHeaderAfterWrite_After(t *testing.T) {
	const msg = "<html>this is HTML."
	testServerResponse(t, func(w http.ResponseWriter, r *http.Request) error {
		io.WriteString(w, msg)
		w.Header().Set("foo", "should be ignored")
		return nil
	}, func(st *serverTester) {
		getSlash(st)
		hf := st.wantHeaders()
		if hf.StreamEnded() {
			t.Fatal("unexpected END_STREAM")
		}
		if !hf.HeadersEnded() {
			t.Fatal("want END_HEADERS flag")
		}
		goth := decodeHeader(t, hf.HeaderBlockFragment())
		wanth := [][2]string{
			{":status", "200"},
			{"content-type", "text/html; charset=utf-8"},
			{"content-length", strconv.Itoa(len(msg))},
		}
		if !reflect.DeepEqual(goth, wanth) {
			t.Errorf("Got headers %v; want %v", goth, wanth)
		}
	})
}

// Header accessed before the initial write and later mutated.
func TestServer_Response_Data_IgnoreHeaderAfterWrite_Overwrite(t *testing.T) {
	const msg = "<html>this is HTML."
	testServerResponse(t, func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("foo", "proper value")
		io.WriteString(w, msg)
		w.Header().Set("foo", "should be ignored")
		return nil
	}, func(st *serverTester) {
		getSlash(st)
		hf := st.wantHeaders()
		if hf.StreamEnded() {
			t.Fatal("unexpected END_STREAM")
		}
		if !hf.HeadersEnded() {
			t.Fatal("want END_HEADERS flag")
		}
		goth := decodeHeader(t, hf.HeaderBlockFragment())
		wanth := [][2]string{
			{":status", "200"},
			{"foo", "proper value"},
			{"content-type", "text/html; charset=utf-8"},
			{"content-length", strconv.Itoa(len(msg))},
		}
		if !reflect.DeepEqual(goth, wanth) {
			t.Errorf("Got headers %v; want %v", goth, wanth)
		}
	})
}

func TestServer_Response_Data_SniffLenType(t *testing.T) {
	const msg = "<html>this is HTML."
	testServerResponse(t, func(w http.ResponseWriter, r *http.Request) error {
		io.WriteString(w, msg)
		return nil
	}, func(st *serverTester) {
		getSlash(st)
		hf := st.wantHeaders()
		if hf.StreamEnded() {
			t.Fatal("don't want END_STREAM, expecting data")
		}
		if !hf.HeadersEnded() {
			t.Fatal("want END_HEADERS flag")
		}
		goth := decodeHeader(t, hf.HeaderBlockFragment())
		wanth := [][2]string{
			{":status", "200"},
			{"content-type", "text/html; charset=utf-8"},
			{"content-length", strconv.Itoa(len(msg))},
		}
		if !reflect.DeepEqual(goth, wanth) {
			t.Errorf("Got headers %v; want %v", goth, wanth)
		}
		df := st.wantData()
		if !df.StreamEnded() {
			t.Error("expected DATA to have END_STREAM flag")
		}
		if got := string(df.Data()); got != msg {
			t.Errorf("got DATA %q; want %q", got, msg)
		}
	})
}

func TestServer_Response_Header_Flush_MidWrite(t *testing.T) {
	const msg = "<html>this is HTML"
	const msg2 = ", and this is the next chunk"
	testServerResponse(t, func(w http.ResponseWriter, r *http.Request) error {
		io.WriteString(w, msg)
		w.(http.Flusher).Flush()
		io.WriteString(w, msg2)
		return nil
	}, func(st *serverTester) {
		getSlash(st)
		hf := st.wantHeaders()
		if hf.StreamEnded() {
			t.Fatal("unexpected END_STREAM flag")
		}
		if !hf.HeadersEnded() {
			t.Fatal("want END_HEADERS flag")
		}
		goth := decodeHeader(t, hf.HeaderBlockFragment())
		wanth := [][2]string{
			{":status", "200"},
			{"content-type", "text/html; charset=utf-8"}, // sniffed
			// and no content-length
		}
		if !reflect.DeepEqual(goth, wanth) {
			t.Errorf("Got headers %v; want %v", goth, wanth)
		}
		{
			df := st.wantData()
			if df.StreamEnded() {
				t.Error("unexpected END_STREAM flag")
			}
			if got := string(df.Data()); got != msg {
				t.Errorf("got DATA %q; want %q", got, msg)
			}
		}
		{
			df := st.wantData()
			if !df.StreamEnded() {
				t.Error("wanted END_STREAM flag on last data chunk")
			}
			if got := string(df.Data()); got != msg2 {
				t.Errorf("got DATA %q; want %q", got, msg2)
			}
		}
	})
}

func TestServer_Response_LargeWrite(t *testing.T) {
	const size = 1 << 20
	testServerResponse(t, func(w http.ResponseWriter, r *http.Request) error {
		n, err := w.Write(bytes.Repeat([]byte("a"), size))
		if err != nil {
			return fmt.Errorf("Write error: %v", err)
		}
		if n != size {
			return fmt.Errorf("wrong size %d from Write", n)
		}
		return nil
	}, func(st *serverTester) {
		getSlash(st) // make the single request

		// Give the handler quota to write:
		if err := st.fr.WriteWindowUpdate(1, size); err != nil {
			t.Fatal(err)
		}

		hf := st.wantHeaders()
		if hf.StreamEnded() {
			t.Fatal("unexpected END_STREAM flag")
		}
		if !hf.HeadersEnded() {
			t.Fatal("want END_HEADERS flag")
		}
		goth := decodeHeader(t, hf.HeaderBlockFragment())
		wanth := [][2]string{
			{":status", "200"},
			{"content-type", "text/plain; charset=utf-8"}, // sniffed
			// and no content-length
		}
		if !reflect.DeepEqual(goth, wanth) {
			t.Errorf("Got headers %v; want %v", goth, wanth)
		}
		var bytes, frames int
		for {
			df := st.wantData()
			bytes += len(df.Data())
			frames++
			for _, b := range df.Data() {
				if b != 'a' {
					t.Fatal("non-'a' byte seen in DATA")
				}
			}
			if df.StreamEnded() {
				break
			}
		}
		if bytes != size {
			t.Errorf("Got %d bytes; want %d", bytes, size)
		}
		if want := 257; frames != want {
			t.Errorf("Got %d frames; want %d", frames, size)
		}
	})
}

// Test that the handler can't write more than the client allows
func TestServer_Response_LargeWrite_FlowControlled(t *testing.T) {
	const size = 1 << 20
	testServerResponse(t, func(w http.ResponseWriter, r *http.Request) error {
		w.(http.Flusher).Flush()
		n, err := w.Write(bytes.Repeat([]byte("a"), size))
		if err != nil {
			return fmt.Errorf("Write error: %v", err)
		}
		if n != size {
			return fmt.Errorf("wrong size %d from Write", n)
		}
		return nil
	}, func(st *serverTester) {
		// Set the window size to something explicit for this test.
		// It's also how much initial data we expect.
		const initWindowSize = 123
		if err := st.fr.WriteSettings(Setting{SettingInitialWindowSize, initWindowSize}); err != nil {
			t.Fatal(err)
		}
		st.wantSettingsAck()

		getSlash(st) // make the single request

		defer func() { st.fr.WriteRSTStream(1, ErrCodeCancel) }()

		hf := st.wantHeaders()
		if hf.StreamEnded() {
			t.Fatal("unexpected END_STREAM flag")
		}
		if !hf.HeadersEnded() {
			t.Fatal("want END_HEADERS flag")
		}

		df := st.wantData()
		if got := len(df.Data()); got != initWindowSize {
			t.Fatalf("Initial window size = %d but got DATA with %d bytes", initWindowSize, got)
		}

		for _, quota := range []int{1, 13, 127} {
			if err := st.fr.WriteWindowUpdate(1, uint32(quota)); err != nil {
				t.Fatal(err)
			}
			df := st.wantData()
			if int(quota) != len(df.Data()) {
				t.Fatalf("read %d bytes after giving %d quota", len(df.Data()), quota)
			}
		}

		if err := st.fr.WriteRSTStream(1, ErrCodeCancel); err != nil {
			t.Fatal(err)
		}
	})
}

func TestServer_Response_Automatic100Continue(t *testing.T) {
	const msg = "foo"
	const reply = "bar"
	testServerResponse(t, func(w http.ResponseWriter, r *http.Request) error {
		if v := r.Header.Get("Expect"); v != "" {
			t.Errorf("Expect header = %q; want empty", v)
		}
		buf := make([]byte, len(msg))
		// This read should trigger the 100-continue being sent.
		if n, err := io.ReadFull(r.Body, buf); err != nil || n != len(msg) || string(buf) != msg {
			return fmt.Errorf("ReadFull = %q, %v; want %q, nil", buf[:n], err, msg)
		}
		_, err := io.WriteString(w, reply)
		return err
	}, func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1, // clients send odd numbers
			BlockFragment: encodeHeader(st.t, ":method", "POST", "expect", "100-continue"),
			EndStream:     false,
			EndHeaders:    true,
		})
		hf := st.wantHeaders()
		if hf.StreamEnded() {
			t.Fatal("unexpected END_STREAM flag")
		}
		if !hf.HeadersEnded() {
			t.Fatal("want END_HEADERS flag")
		}
		goth := decodeHeader(t, hf.HeaderBlockFragment())
		wanth := [][2]string{
			{":status", "100"},
		}
		if !reflect.DeepEqual(goth, wanth) {
			t.Fatalf("Got headers %v; want %v", goth, wanth)
		}

		// Okay, they sent status 100, so we can send our
		// gigantic and/or sensitive "foo" payload now.
		st.writeData(1, true, []byte(msg))

		st.wantWindowUpdate(0, uint32(len(msg)))
		st.wantWindowUpdate(1, uint32(len(msg)))

		hf = st.wantHeaders()
		if hf.StreamEnded() {
			t.Fatal("expected data to follow")
		}
		if !hf.HeadersEnded() {
			t.Fatal("want END_HEADERS flag")
		}
		goth = decodeHeader(t, hf.HeaderBlockFragment())
		wanth = [][2]string{
			{":status", "200"},
			{"content-type", "text/plain; charset=utf-8"},
			{"content-length", strconv.Itoa(len(reply))},
		}
		if !reflect.DeepEqual(goth, wanth) {
			t.Errorf("Got headers %v; want %v", goth, wanth)
		}

		df := st.wantData()
		if string(df.Data()) != reply {
			t.Errorf("Client read %q; want %q", df.Data(), reply)
		}
		if !df.StreamEnded() {
			t.Errorf("expect data stream end")
		}
	})
}

func TestServer_HandlerWriteErrorOnDisconnect(t *testing.T) {
	errc := make(chan error, 1)
	testServerResponse(t, func(w http.ResponseWriter, r *http.Request) error {
		p := []byte("some data.\n")
		for {
			_, err := w.Write(p)
			if err != nil {
				errc <- err
				return nil
			}
		}
	}, func(st *serverTester) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      1,
			BlockFragment: encodeHeader(st.t),
			EndStream:     false,
			EndHeaders:    true,
		})
		hf := st.wantHeaders()
		if hf.StreamEnded() {
			t.Fatal("unexpected END_STREAM flag")
		}
		if !hf.HeadersEnded() {
			t.Fatal("want END_HEADERS flag")
		}
		// Close the connection and wait for the handler to (hopefully) notice.
		st.cc.Close()
		select {
		case <-errc:
		case <-time.After(5 * time.Second):
			t.Error("timeout")
		}
	})
}

func TestServer_Rejects_Too_Many_Streams(t *testing.T) {
	const testPath = "/some/path"

	inHandler := make(chan uint32)
	leaveHandler := make(chan bool)
	st := newServerTester(t, func(w http.ResponseWriter, r *http.Request) {
		id := w.(*responseWriter).rws.stream.id
		inHandler <- id
		if id == 1+(defaultMaxStreams+1)*2 && r.URL.Path != testPath {
			t.Errorf("decoded final path as %q; want %q", r.URL.Path, testPath)
		}
		<-leaveHandler
	})
	defer st.Close()
	st.greet()
	nextStreamID := uint32(1)
	streamID := func() uint32 {
		defer func() { nextStreamID += 2 }()
		return nextStreamID
	}
	sendReq := func(id uint32, headers ...string) {
		st.writeHeaders(HeadersFrameParam{
			StreamID:      id,
			BlockFragment: encodeHeader(st.t, headers...),
			EndStream:     true,
			EndHeaders:    true,
		})
	}
	for i := 0; i < defaultMaxStreams; i++ {
		sendReq(streamID())
		<-inHandler
	}
	defer func() {
		for i := 0; i < defaultMaxStreams; i++ {
			leaveHandler <- true
		}
	}()

	// And this one should cross the limit:
	// (It's also sent as a CONTINUATION, to verify we still track the decoder context,
	// even if we're rejecting it)
	rejectID := streamID()
	headerBlock := encodeHeader(st.t, ":path", testPath)
	frag1, frag2 := headerBlock[:3], headerBlock[3:]
	st.writeHeaders(HeadersFrameParam{
		StreamID:      rejectID,
		BlockFragment: frag1,
		EndStream:     true,
		EndHeaders:    false, // CONTINUATION coming
	})
	if err := st.fr.WriteContinuation(rejectID, true, frag2); err != nil {
		t.Fatal(err)
	}
	st.wantRSTStream(rejectID, ErrCodeProtocol)

	// But let a handler finish:
	leaveHandler <- true
	st.wantHeaders()

	// And now another stream should be able to start:
	goodID := streamID()
	sendReq(goodID, ":path", testPath)
	select {
	case got := <-inHandler:
		if got != goodID {
			t.Errorf("Got stream %d; want %d", got, goodID)
		}
	case <-time.After(3 * time.Second):
		t.Error("timeout waiting for handler")
	}
}

func decodeHeader(t *testing.T, headerBlock []byte) (pairs [][2]string) {
	d := hpack.NewDecoder(initialHeaderTableSize, func(f hpack.HeaderField) {
		pairs = append(pairs, [2]string{f.Name, f.Value})
	})
	if _, err := d.Write(headerBlock); err != nil {
		t.Fatalf("hpack decoding error: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("hpack decoding error: %v", err)
	}
	return
}

// testServerResponse sets up an idle HTTP/2 connection and lets you
// write a single request with writeReq, and then reply to it in some way with the provided handler,
// and then verify the output with the serverTester again (assuming the handler returns nil)
func testServerResponse(t *testing.T,
	handler func(http.ResponseWriter, *http.Request) error,
	client func(*serverTester),
) {
	errc := make(chan error, 1)
	st := newServerTester(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			t.Fatal("nil Body")
		}
		errc <- handler(w, r)
	})
	defer st.Close()

	donec := make(chan bool)
	go func() {
		defer close(donec)
		st.greet()
		client(st)
	}()

	select {
	case <-donec:
		return
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Error in handler: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for handler to finish")
	}
}

func TestServerWithCurl(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("skipping Docker test on Darwin; requires --net which won't work with boot2docker anyway")
	}
	requireCurl(t)
	const msg = "Hello from curl!\n"
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Foo", "Bar")
		io.WriteString(w, msg)
	}))
	ConfigureServer(ts.Config, &Server{})
	ts.TLS = ts.Config.TLSConfig // the httptest.Server has its own copy of this TLS config
	ts.StartTLS()
	defer ts.Close()

	var gotConn int32
	testHookOnConn = func() { atomic.StoreInt32(&gotConn, 1) }

	t.Logf("Running test server for curl to hit at: %s", ts.URL)
	container := curl(t, "--silent", "--http2", "--insecure", "-v", ts.URL)
	defer kill(container)
	resc := make(chan interface{}, 1)
	go func() {
		res, err := dockerLogs(container)
		if err != nil {
			resc <- err
		} else {
			resc <- res
		}
	}()
	select {
	case res := <-resc:
		if err, ok := res.(error); ok {
			t.Fatal(err)
		}
		if !strings.Contains(string(res.([]byte)), "< foo:Bar") {
			t.Errorf("didn't see foo:Bar header")
			t.Logf("Got: %s", res)
		}
		if !strings.Contains(string(res.([]byte)), msg) {
			t.Errorf("didn't see %q content", msg)
			t.Logf("Got: %s", res)
		}
	case <-time.After(3 * time.Second):
		t.Errorf("timeout waiting for curl")
	}

	if atomic.LoadInt32(&gotConn) == 0 {
		t.Error("never saw an http2 connection")
	}
}
