// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// See https://code.google.com/p/go/source/browse/CONTRIBUTORS
// Licensed under the same terms as Go itself:
// https://code.google.com/p/go/source/browse/LICENSE

package http2

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/third_party/github.com/bradfitz/http2/hpack"
)

const (
	prefaceTimeout        = 5 * time.Second
	firstSettingsTimeout  = 2 * time.Second // should be in-flight with preface anyway
	handlerChunkWriteSize = 4 << 10
	defaultMaxStreams     = 250
)

var (
	errClientDisconnected = errors.New("client disconnected")
	errClosedBody         = errors.New("body closed by handler")
	errStreamBroken       = errors.New("http2: stream broken")
)

var responseWriterStatePool = sync.Pool{
	New: func() interface{} {
		rws := &responseWriterState{}
		rws.bw = bufio.NewWriterSize(chunkWriter{rws}, handlerChunkWriteSize)
		return rws
	},
}

// Test hooks.
var (
	testHookOnConn        func()
	testHookGetServerConn func(*serverConn)
)

// TODO: finish GOAWAY support. Consider each incoming frame type and
// whether it should be ignored during a shutdown race.

// TODO: (edge case?) if peer sends a SETTINGS frame with e.g. a
// SETTINGS_MAX_FRAME_SIZE that's lower than what we had before,
// before we ACK it we have to make sure all currently-active streams
// know about that and don't have existing too-large frames in flight?
// Perhaps the settings processing should just wait for new frame to
// be in-flight and then the frame scheduler in the serve goroutine
// will be responsible for splitting things.

// TODO: send PING frames to idle clients and disconnect them if no
// reply

// TODO: don't keep the writeFrames goroutine active. turn it off when no frames
// are enqueued.

// TODO: for bonus points: turn off the serve goroutine also when
// idle, so an idle conn only has the readFrames goroutine
// active. (which could also be optimized probably to pin less memory
// in crypto/tls). This would involve tracking when the serve
// goroutine is active (atomic int32 read/CAS probably?) and starting
// it up when frames arrive, and shutting it down when all handlers
// exit. the occasional PING packets could use time.AfterFunc to call
// sc.wakeStartServeLoop() (which is a no-op if already running) and
// then queue the PING write as normal. The serve loop would then exit
// in most cases (if no Handlers running) and not be woken up again
// until the PING packet returns.

// Server is an HTTP/2 server.
type Server struct {
	// MaxHandlers limits the number of http.Handler ServeHTTP goroutines
	// which may run at a time over all connections.
	// Negative or zero no limit.
	// TODO: implement
	MaxHandlers int

	// MaxConcurrentStreams optionally specifies the number of
	// concurrent streams that each client may have open at a
	// time. This is unrelated to the number of http.Handler goroutines
	// which may be active globally, which is MaxHandlers.
	// If zero, MaxConcurrentStreams defaults to at least 100, per
	// the HTTP/2 spec's recommendations.
	MaxConcurrentStreams uint32

	// MaxReadFrameSize optionally specifies the largest frame
	// this server is willing to read. A valid value is between
	// 16k and 16M, inclusive. If zero or otherwise invalid, a
	// default value is used.
	MaxReadFrameSize uint32
}

func (s *Server) maxReadFrameSize() uint32 {
	if v := s.MaxReadFrameSize; v >= minMaxFrameSize && v <= maxFrameSize {
		return v
	}
	return defaultMaxReadFrameSize
}

func (s *Server) maxConcurrentStreams() uint32 {
	if v := s.MaxConcurrentStreams; v > 0 {
		return v
	}
	return defaultMaxStreams
}

// ConfigureServer adds HTTP/2 support to a net/http Server.
//
// The configuration conf may be nil.
//
// ConfigureServer must be called before s begins serving.
func ConfigureServer(s *http.Server, conf *Server) {
	if conf == nil {
		conf = new(Server)
	}
	if s.TLSConfig == nil {
		s.TLSConfig = new(tls.Config)
	}
	haveNPN := false
	for _, p := range s.TLSConfig.NextProtos {
		if p == npnProto {
			haveNPN = true
			break
		}
	}
	if !haveNPN {
		s.TLSConfig.NextProtos = append(s.TLSConfig.NextProtos, npnProto)
	}

	if s.TLSNextProto == nil {
		s.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
	}
	s.TLSNextProto[npnProto] = func(hs *http.Server, c *tls.Conn, h http.Handler) {
		if testHookOnConn != nil {
			testHookOnConn()
		}
		conf.handleConn(hs, c, h)
	}
}

func (srv *Server) handleConn(hs *http.Server, c net.Conn, h http.Handler) {
	sc := &serverConn{
		srv:               srv,
		hs:                hs,
		conn:              c,
		bw:                newBufferedWriter(c),
		handler:           h,
		streams:           make(map[uint32]*stream),
		readFrameCh:       make(chan frameAndGate),
		readFrameErrCh:    make(chan error, 1), // must be buffered for 1
		wantWriteFrameCh:  make(chan frameWriteMsg, 8),
		wroteFrameCh:      make(chan struct{}, 1), // TODO: consider 0. will deadlock currently in sendFrameWrite in sentReset case
		flow:              newFlow(initialWindowSize),
		doneServing:       make(chan struct{}),
		advMaxStreams:     srv.maxConcurrentStreams(),
		maxWriteFrameSize: initialMaxFrameSize,
		initialWindowSize: initialWindowSize,
		headerTableSize:   initialHeaderTableSize,
		serveG:            newGoroutineLock(),
		pushEnabled:       true,
	}
	sc.hpackEncoder = hpack.NewEncoder(&sc.headerWriteBuf)
	sc.hpackDecoder = hpack.NewDecoder(initialHeaderTableSize, sc.onNewHeaderField)

	fr := NewFramer(sc.bw, c)
	fr.SetMaxReadFrameSize(srv.maxReadFrameSize())
	sc.framer = fr

	if hook := testHookGetServerConn; hook != nil {
		hook(sc)
	}
	sc.serve()
}

// frameAndGates coordinates the readFrames and serve
// goroutines. Because the Framer interface only permits the most
// recently-read Frame from being accessed, the readFrames goroutine
// blocks until it has a frame, passes it to serve, and then waits for
// serve to be done with it before reading the next one.
type frameAndGate struct {
	f Frame
	g gate
}

type serverConn struct {
	// Immutable:
	srv              *Server
	hs               *http.Server
	conn             net.Conn
	bw               *bufferedWriter // writing to conn
	handler          http.Handler
	framer           *Framer
	hpackDecoder     *hpack.Decoder
	doneServing      chan struct{}     // closed when serverConn.serve ends
	readFrameCh      chan frameAndGate // written by serverConn.readFrames
	readFrameErrCh   chan error
	wantWriteFrameCh chan frameWriteMsg // from handlers -> serve
	wroteFrameCh     chan struct{}      // from writeFrames -> serve, tickles more frame writes
	testHookCh       chan func()        // code to run on the serve loop

	serveG goroutineLock // used to verify funcs are on serve()
	writeG goroutineLock // used to verify things running on writeLoop
	flow   *flow         // connection-wide (not stream-specific) flow control

	// Everything following is owned by the serve loop; use serveG.check():
	pushEnabled           bool
	sawFirstSettings      bool // got the initial SETTINGS frame after the preface
	needToSendSettingsAck bool
	clientMaxStreams      uint32 // SETTINGS_MAX_CONCURRENT_STREAMS from client (our PUSH_PROMISE limit)
	advMaxStreams         uint32 // our SETTINGS_MAX_CONCURRENT_STREAMS advertised the client
	curOpenStreams        uint32 // client's number of open streams
	maxStreamID           uint32 // max ever seen
	streams               map[uint32]*stream
	maxWriteFrameSize     uint32
	initialWindowSize     int32
	headerTableSize       uint32
	maxHeaderListSize     uint32            // zero means unknown (default)
	canonHeader           map[string]string // http2-lower-case -> Go-Canonical-Case
	req                   requestParam      // non-zero while reading request headers
	writingFrame          bool              // started write goroutine but haven't heard back on wroteFrameCh
	needsFrameFlush       bool              // last frame write wasn't a flush
	writeQueue            []frameWriteMsg   // TODO: proper scheduler, not a queue
	inGoAway              bool              // we've started to or sent GOAWAY
	needToSendGoAway      bool              // we need to schedule a GOAWAY frame write
	goAwayCode            ErrCode
	shutdownTimerCh       <-chan time.Time // nil until used
	shutdownTimer         *time.Timer      // nil until used

	// Owned by the writeFrames goroutine; use writeG.check():
	headerWriteBuf bytes.Buffer
	hpackEncoder   *hpack.Encoder
}

// requestParam is the state of the next request, initialized over
// potentially several frames HEADERS + zero or more CONTINUATION
// frames.
type requestParam struct {
	// stream is non-nil if we're reading (HEADER or CONTINUATION)
	// frames for a request (but not DATA).
	stream            *stream
	header            http.Header
	method, path      string
	scheme, authority string
	sawRegularHeader  bool // saw a non-pseudo header already
	invalidHeader     bool // an invalid header was seen
}

// stream represents a stream. This is the minimal metadata needed by
// the serve goroutine. Most of the actual stream state is owned by
// the http.Handler's goroutine in the responseWriter. Because the
// responseWriter's responseWriterState is recycled at the end of a
// handler, this struct intentionally has no pointer to the
// *responseWriter{,State} itself, as the Handler ending nils out the
// responseWriter's state field.
type stream struct {
	// immutable:
	id   uint32
	conn *serverConn
	flow *flow       // limits writing from Handler to client
	body *pipe       // non-nil if expecting DATA frames
	cw   closeWaiter // closed wait stream transitions to closed state

	// owned by serverConn's serve loop:
	state         streamState
	bodyBytes     int64 // body bytes seen so far
	declBodyBytes int64 // or -1 if undeclared
	sentReset     bool  // only true once detached from streams map
	gotReset      bool  // only true once detacted from streams map
}

func (sc *serverConn) state(streamID uint32) streamState {
	sc.serveG.check()
	// http://http2.github.io/http2-spec/#rfc.section.5.1
	if st, ok := sc.streams[streamID]; ok {
		return st.state
	}
	// "The first use of a new stream identifier implicitly closes all
	// streams in the "idle" state that might have been initiated by
	// that peer with a lower-valued stream identifier. For example, if
	// a client sends a HEADERS frame on stream 7 without ever sending a
	// frame on stream 5, then stream 5 transitions to the "closed"
	// state when the first frame for stream 7 is sent or received."
	if streamID <= sc.maxStreamID {
		return stateClosed
	}
	return stateIdle
}

func (sc *serverConn) vlogf(format string, args ...interface{}) {
	if VerboseLogs {
		sc.logf(format, args...)
	}
}

func (sc *serverConn) logf(format string, args ...interface{}) {
	if lg := sc.hs.ErrorLog; lg != nil {
		lg.Printf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

func (sc *serverConn) condlogf(err error, format string, args ...interface{}) {
	if err == nil {
		return
	}
	str := err.Error()
	if err == io.EOF || strings.Contains(str, "use of closed network connection") {
		// Boring, expected errors.
		sc.vlogf(format, args...)
	} else {
		sc.logf(format, args...)
	}
}

func (sc *serverConn) onNewHeaderField(f hpack.HeaderField) {
	sc.serveG.check()
	switch {
	case !validHeader(f.Name):
		sc.req.invalidHeader = true
	case strings.HasPrefix(f.Name, ":"):
		if sc.req.sawRegularHeader {
			sc.logf("pseudo-header after regular header")
			sc.req.invalidHeader = true
			return
		}
		var dst *string
		switch f.Name {
		case ":method":
			dst = &sc.req.method
		case ":path":
			dst = &sc.req.path
		case ":scheme":
			dst = &sc.req.scheme
		case ":authority":
			dst = &sc.req.authority
		default:
			// 8.1.2.1 Pseudo-Header Fields
			// "Endpoints MUST treat a request or response
			// that contains undefined or invalid
			// pseudo-header fields as malformed (Section
			// 8.1.2.6)."
			sc.logf("invalid pseudo-header %q", f.Name)
			sc.req.invalidHeader = true
			return
		}
		if *dst != "" {
			sc.logf("duplicate pseudo-header %q sent", f.Name)
			sc.req.invalidHeader = true
			return
		}
		*dst = f.Value
	case f.Name == "cookie":
		sc.req.sawRegularHeader = true
		if s, ok := sc.req.header["Cookie"]; ok && len(s) == 1 {
			s[0] = s[0] + "; " + f.Value
		} else {
			sc.req.header.Add("Cookie", f.Value)
		}
	default:
		sc.req.sawRegularHeader = true
		sc.req.header.Add(sc.canonicalHeader(f.Name), f.Value)
	}
}

func (sc *serverConn) canonicalHeader(v string) string {
	sc.serveG.check()
	cv, ok := commonCanonHeader[v]
	if ok {
		return cv
	}
	cv, ok = sc.canonHeader[v]
	if ok {
		return cv
	}
	if sc.canonHeader == nil {
		sc.canonHeader = make(map[string]string)
	}
	cv = http.CanonicalHeaderKey(v)
	sc.canonHeader[v] = cv
	return cv
}

// readFrames is the loop that reads incoming frames.
// It's run on its own goroutine.
func (sc *serverConn) readFrames() {
	g := make(gate, 1)
	for {
		f, err := sc.framer.ReadFrame()
		if err != nil {
			sc.readFrameErrCh <- err
			close(sc.readFrameCh)
			return
		}
		sc.readFrameCh <- frameAndGate{f, g}
		g.Wait()
	}
}

// writeFrameAsync runs in its own goroutine and writes a single frame
// and then reports when it's done.
// At most one goroutine can be running writeFrameAsync at a time per
// serverConn.
func (sc *serverConn) writeFrameAsync(wm frameWriteMsg) {
	sc.writeG = newGoroutineLock()
	var streamID uint32
	if wm.stream != nil {
		streamID = wm.stream.id
	}
	err := wm.write(sc, streamID, wm.v)
	if ch := wm.done; ch != nil {
		select {
		case ch <- err:
		default:
			panic(fmt.Sprintf("unbuffered done channel passed in for type %T", wm.v))
		}
	}
	sc.wroteFrameCh <- struct{}{} // tickle frame selection scheduler
}

func (sc *serverConn) flushFrameWriter(uint32, interface{}) error {
	sc.writeG.check()
	return sc.bw.Flush() // may block on the network
}

func (sc *serverConn) closeAllStreamsOnConnClose() {
	sc.serveG.check()
	for _, st := range sc.streams {
		sc.closeStream(st, errClientDisconnected)
	}
}

func (sc *serverConn) stopShutdownTimer() {
	sc.serveG.check()
	if t := sc.shutdownTimer; t != nil {
		t.Stop()
	}
}

func (sc *serverConn) serve() {
	sc.serveG.check()
	defer sc.conn.Close()
	defer sc.closeAllStreamsOnConnClose()
	defer sc.stopShutdownTimer()
	defer close(sc.doneServing) // unblocks handlers trying to send

	sc.vlogf("HTTP/2 connection from %v on %p", sc.conn.RemoteAddr(), sc.hs)

	sc.writeFrame(frameWriteMsg{write: (*serverConn).sendInitialSettings})

	if err := sc.readPreface(); err != nil {
		sc.condlogf(err, "error reading preface from client %v: %v", sc.conn.RemoteAddr(), err)
		return
	}

	go sc.readFrames() // closed by defer sc.conn.Close above

	settingsTimer := time.NewTimer(firstSettingsTimeout)
	for {
		select {
		case wm := <-sc.wantWriteFrameCh:
			sc.writeFrame(wm)
		case <-sc.wroteFrameCh:
			sc.writingFrame = false
			sc.scheduleFrameWrite()
		case fg, ok := <-sc.readFrameCh:
			if !ok {
				sc.readFrameCh = nil
			}
			if !sc.processFrameFromReader(fg, ok) {
				return
			}
			if settingsTimer.C != nil {
				settingsTimer.Stop()
				settingsTimer.C = nil
			}
		case <-settingsTimer.C:
			sc.logf("timeout waiting for SETTINGS frames from %v", sc.conn.RemoteAddr())
			return
		case <-sc.shutdownTimerCh:
			sc.vlogf("GOAWAY close timer fired; closing conn from %v", sc.conn.RemoteAddr())
			return
		case fn := <-sc.testHookCh:
			fn()
		}
	}
}

func (sc *serverConn) sendInitialSettings(uint32, interface{}) error {
	sc.writeG.check()
	return sc.framer.WriteSettings(
		Setting{SettingMaxFrameSize, sc.srv.maxReadFrameSize()},
		Setting{SettingMaxConcurrentStreams, sc.advMaxStreams},
		/* TODO: more actual settings */
	)
}

// readPreface reads the ClientPreface greeting from the peer
// or returns an error on timeout or an invalid greeting.
func (sc *serverConn) readPreface() error {
	errc := make(chan error, 1)
	go func() {
		// Read the client preface
		buf := make([]byte, len(ClientPreface))
		// TODO: timeout reading from the client
		if _, err := io.ReadFull(sc.conn, buf); err != nil {
			errc <- err
		} else if !bytes.Equal(buf, clientPreface) {
			errc <- fmt.Errorf("bogus greeting %q", buf)
		} else {
			errc <- nil
		}
	}()
	timer := time.NewTimer(5 * time.Second) // TODO: configurable on *Server?
	defer timer.Stop()
	select {
	case <-timer.C:
		return errors.New("timeout waiting for client preface")
	case err := <-errc:
		if err == nil {
			sc.vlogf("client %v said hello", sc.conn.RemoteAddr())
		}
		return err
	}
}

// writeData writes the data described in req to stream.id.
//
// The provided ch is used to avoid allocating new channels for each
// write operation.  It's expected that the caller reuses req and ch
// over time.
func (sc *serverConn) writeData(stream *stream, data *dataWriteParams, ch chan error) error {
	sc.serveG.checkNotOn() // otherwise could deadlock in sc.writeFrame

	// TODO: wait for flow control tokens. instead of writing a
	// frame directly, add a new "write data" channel to the serve
	// loop and modify the frame scheduler there to write chunks
	// of req as tokens allow. Don't necessarily write it all at
	// once in one frame.
	sc.writeFrameFromHandler(frameWriteMsg{
		write:     (*serverConn).writeDataFrame,
		cost:      uint32(len(data.p)),
		stream:    stream,
		endStream: data.end,
		v:         data,
		done:      ch,
	})
	select {
	case err := <-ch:
		return err
	case <-sc.doneServing:
		return errClientDisconnected
	}
}

// writeFrameFromHandler sends wm to sc.wantWriteFrameCh, but aborts
// if the connection has gone away.
//
// This must not be run from the serve goroutine itself, else it might
// deadlock writing to sc.wantWriteFrameCh (which is only mildly
// buffered and is read by serve itself). If you're on the serve
// goroutine, call writeFrame instead.
func (sc *serverConn) writeFrameFromHandler(wm frameWriteMsg) {
	sc.serveG.checkNotOn() // NOT
	select {
	case sc.wantWriteFrameCh <- wm:
	case <-sc.doneServing:
		// Client has closed their connection to the server.
	}
}

// writeFrame either sends wm to the writeFrames goroutine, or
// enqueues it for the future (with no pushback; the serve goroutine
// never blocks!), for sending when the currently-being-written frame
// is done writing.
//
// If you're not on the serve goroutine, use writeFrame instead.
func (sc *serverConn) writeFrame(wm frameWriteMsg) {
	sc.serveG.check()
	// Fast path for common case:
	if !sc.writingFrame {
		sc.sendFrameWrite(wm)
		return
	}
	sc.writeQueue = append(sc.writeQueue, wm) // TODO: proper scheduler
}

// sendFrameWrite sends a frame to the writeFrames goroutine.
// Only one frame can be in-flight at a time.
// sendFrameWrite also updates stream state right before the frame is
// sent to be written.
func (sc *serverConn) sendFrameWrite(wm frameWriteMsg) {
	sc.serveG.check()
	if sc.writingFrame {
		panic("invariant")
	}

	st := wm.stream
	if st != nil {
		switch st.state {
		case stateHalfClosedLocal:
			panic("internal error: attempt to send frame on half-closed-local stream")
		case stateClosed:
			if st.sentReset || st.gotReset {
				// Skip this frame. But fake the frame write to reschedule:
				sc.wroteFrameCh <- struct{}{}
				return
			}
			panic("internal error: attempt to send a frame on a closed stream")
		}
	}

	sc.writingFrame = true
	sc.needsFrameFlush = true
	if wm.endStream {
		if st == nil {
			panic("nil stream with endStream set")
		}
		switch st.state {
		case stateOpen:
			st.state = stateHalfClosedLocal
		case stateHalfClosedRemote:
			sc.closeStream(st, nil)
		}
	}
	go sc.writeFrameAsync(wm)
}

// scheduleFrameWrite tickles the frame writing scheduler.
//
// If a frame is already being written, nothing happens. This will be called again
// when the frame is done being written.
//
// If a frame isn't being written we need to send one, the best frame
// to send is selected, preferring first things that aren't
// stream-specific (e.g. ACKing settings), and then finding the
// highest priority stream.
//
// If a frame isn't being written and there's nothing else to send, we
// flush the write buffer.
func (sc *serverConn) scheduleFrameWrite() {
	sc.serveG.check()
	if sc.writingFrame {
		return
	}
	if sc.needToSendGoAway {
		sc.needToSendGoAway = false
		sc.sendFrameWrite(frameWriteMsg{
			write: (*serverConn).writeGoAwayFrame,
			v: &goAwayParams{
				maxStreamID: sc.maxStreamID,
				code:        sc.goAwayCode,
			},
		})
		return
	}
	if len(sc.writeQueue) == 0 && sc.needsFrameFlush {
		sc.sendFrameWrite(frameWriteMsg{write: (*serverConn).flushFrameWriter})
		sc.needsFrameFlush = false // after sendFrameWrite, since it sets this true
		return
	}
	if sc.inGoAway {
		// No more frames after we've sent GOAWAY.
		return
	}
	if sc.needToSendSettingsAck {
		sc.needToSendSettingsAck = false
		sc.sendFrameWrite(frameWriteMsg{write: (*serverConn).writeSettingsAck})
		return
	}
	if len(sc.writeQueue) == 0 {
		return
	}

	// TODO:
	// -- prioritize all non-DATA frames first. they're not flow controlled anyway and
	//    they're generally more important.
	// -- for all DATA frames that are enqueued (and we should enqueue []byte instead of FRAMES),
	//    go over each (in priority order, as determined by the whole priority tree chaos),
	//    and decide which we have tokens for, and how many tokens.

	// Writing on stream X requires that we have tokens on the
	// stream 0 (the conn-as-a-whole stream) as well as stream X.

	// So: find the highest priority stream X, then see: do we
	// have tokens for X? Let's say we have N_X tokens. Then we should
	// write MIN(N_X, TOKENS(conn-wide-tokens)).
	//
	// Any tokens left over? Repeat. Well, not really... the
	// repeat will happen via the next call to
	// scheduleFrameWrite. So keep a HEAP (priqueue) of which
	// streams to write to.

	// TODO: proper scheduler
	wm := sc.writeQueue[0]
	// shift it all down. kinda lame. will be removed later anyway.
	copy(sc.writeQueue, sc.writeQueue[1:])
	sc.writeQueue = sc.writeQueue[:len(sc.writeQueue)-1]

	// TODO: if wm is a data frame, make sure it's not too big
	// (because a SETTINGS frame changed our max frame size while
	// a stream was open and writing) and cut it up into smaller
	// bits.
	sc.sendFrameWrite(wm)
}

func (sc *serverConn) goAway(code ErrCode) {
	sc.serveG.check()
	if sc.inGoAway {
		return
	}
	if code != ErrCodeNo {
		sc.shutDownIn(250 * time.Millisecond)
	} else {
		// TODO: configurable
		sc.shutDownIn(1 * time.Second)
	}
	sc.inGoAway = true
	sc.needToSendGoAway = true
	sc.goAwayCode = code
	sc.scheduleFrameWrite()
}

func (sc *serverConn) shutDownIn(d time.Duration) {
	sc.serveG.check()
	sc.shutdownTimer = time.NewTimer(d)
	sc.shutdownTimerCh = sc.shutdownTimer.C
}

func (sc *serverConn) writeGoAwayFrame(_ uint32, v interface{}) error {
	sc.writeG.check()
	p := v.(*goAwayParams)
	err := sc.framer.WriteGoAway(p.maxStreamID, p.code, nil)
	if p.code != 0 {
		sc.bw.Flush() // ignore error: we're hanging up on them anyway
		time.Sleep(50 * time.Millisecond)
		sc.conn.Close()
	}
	return err
}

func (sc *serverConn) resetStream(se StreamError) {
	sc.serveG.check()
	st, ok := sc.streams[se.StreamID]
	if !ok {
		panic("internal package error; resetStream called on non-existent stream")
	}
	sc.writeFrame(frameWriteMsg{
		write: (*serverConn).writeRSTStreamFrame,
		v:     &se,
	})
	st.sentReset = true
	sc.closeStream(st, se)
}

func (sc *serverConn) writeRSTStreamFrame(streamID uint32, v interface{}) error {
	sc.writeG.check()
	se := v.(*StreamError)
	return sc.framer.WriteRSTStream(se.StreamID, se.Code)
}

func (sc *serverConn) curHeaderStreamID() uint32 {
	sc.serveG.check()
	st := sc.req.stream
	if st == nil {
		return 0
	}
	return st.id
}

// processFrameFromReader processes the serve loop's read from readFrameCh from the
// frame-reading goroutine.
// processFrameFromReader returns whether the connection should be kept open.
func (sc *serverConn) processFrameFromReader(fg frameAndGate, fgValid bool) bool {
	sc.serveG.check()
	var clientGone bool
	var err error
	if !fgValid {
		err = <-sc.readFrameErrCh
		if err == ErrFrameTooLarge {
			sc.goAway(ErrCodeFrameSize)
			return true // goAway will close the loop
		}
		clientGone = err == io.EOF || strings.Contains(err.Error(), "use of closed network connection")
		if clientGone {
			// TODO: could we also get into this state if
			// the peer does a half close
			// (e.g. CloseWrite) because they're done
			// sending frames but they're still wanting
			// our open replies?  Investigate.
			return false
		}
	}

	if fgValid {
		f := fg.f
		sc.vlogf("got %v: %#v", f.Header(), f)
		err = sc.processFrame(f)
		fg.g.Done() // unblock the readFrames goroutine
		if err == nil {
			return true
		}
	}

	switch ev := err.(type) {
	case StreamError:
		sc.resetStream(ev)
		return true
	case goAwayFlowError:
		sc.goAway(ErrCodeFlowControl)
		return true
	case ConnectionError:
		sc.logf("%v: %v", sc.conn.RemoteAddr(), ev)
		sc.goAway(ErrCode(ev))
		return true // goAway will handle shutdown
	default:
		if !fgValid {
			sc.logf("disconnecting; error reading frame from client %s: %v", sc.conn.RemoteAddr(), err)
		} else {
			sc.logf("disconnection due to other error: %v", err)
		}
	}
	return false
}

func (sc *serverConn) processFrame(f Frame) error {
	sc.serveG.check()

	// First frame received must be SETTINGS.
	if !sc.sawFirstSettings {
		if _, ok := f.(*SettingsFrame); !ok {
			return ConnectionError(ErrCodeProtocol)
		}
		sc.sawFirstSettings = true
	}

	if s := sc.curHeaderStreamID(); s != 0 {
		if cf, ok := f.(*ContinuationFrame); !ok {
			return ConnectionError(ErrCodeProtocol)
		} else if cf.Header().StreamID != s {
			return ConnectionError(ErrCodeProtocol)
		}
	}

	switch f := f.(type) {
	case *SettingsFrame:
		return sc.processSettings(f)
	case *HeadersFrame:
		return sc.processHeaders(f)
	case *ContinuationFrame:
		return sc.processContinuation(f)
	case *WindowUpdateFrame:
		return sc.processWindowUpdate(f)
	case *PingFrame:
		return sc.processPing(f)
	case *DataFrame:
		return sc.processData(f)
	case *RSTStreamFrame:
		return sc.processResetStream(f)
	default:
		log.Printf("Ignoring frame: %v", f.Header())
		return nil
	}
}

func (sc *serverConn) processPing(f *PingFrame) error {
	sc.serveG.check()
	if f.Flags.Has(FlagSettingsAck) {
		// 6.7 PING: " An endpoint MUST NOT respond to PING frames
		// containing this flag."
		return nil
	}
	if f.StreamID != 0 {
		// "PING frames are not associated with any individual
		// stream. If a PING frame is received with a stream
		// identifier field value other than 0x0, the recipient MUST
		// respond with a connection error (Section 5.4.1) of type
		// PROTOCOL_ERROR."
		return ConnectionError(ErrCodeProtocol)
	}
	sc.writeFrame(frameWriteMsg{
		write: (*serverConn).writePingAck,
		v:     f,
	})
	return nil
}

func (sc *serverConn) writePingAck(_ uint32, v interface{}) error {
	sc.writeG.check()
	pf := v.(*PingFrame) // contains the data we need to write back
	return sc.framer.WritePing(true, pf.Data)
}

func (sc *serverConn) processWindowUpdate(f *WindowUpdateFrame) error {
	sc.serveG.check()
	switch {
	case f.StreamID != 0: // stream-level flow control
		st := sc.streams[f.StreamID]
		if st == nil {
			// "WINDOW_UPDATE can be sent by a peer that has sent a
			// frame bearing the END_STREAM flag. This means that a
			// receiver could receive a WINDOW_UPDATE frame on a "half
			// closed (remote)" or "closed" stream. A receiver MUST
			// NOT treat this as an error, see Section 5.1."
			return nil
		}
		if !st.flow.add(int32(f.Increment)) {
			return StreamError{f.StreamID, ErrCodeFlowControl}
		}
	default: // connection-level flow control
		if !sc.flow.add(int32(f.Increment)) {
			return goAwayFlowError{}
		}
	}
	return nil
}

func (sc *serverConn) processResetStream(f *RSTStreamFrame) error {
	sc.serveG.check()
	if sc.state(f.StreamID) == stateIdle {
		// 6.4 "RST_STREAM frames MUST NOT be sent for a
		// stream in the "idle" state. If a RST_STREAM frame
		// identifying an idle stream is received, the
		// recipient MUST treat this as a connection error
		// (Section 5.4.1) of type PROTOCOL_ERROR.
		return ConnectionError(ErrCodeProtocol)
	}
	st, ok := sc.streams[f.StreamID]
	if ok {
		st.gotReset = true
		sc.closeStream(st, StreamError{f.StreamID, f.ErrCode})
	}
	return nil
}

func (sc *serverConn) closeStream(st *stream, err error) {
	sc.serveG.check()
	if st.state == stateIdle || st.state == stateClosed {
		panic("invariant")
	}
	st.state = stateClosed
	sc.curOpenStreams--
	delete(sc.streams, st.id)
	st.flow.close()
	if p := st.body; p != nil {
		p.Close(err)
	}
	st.cw.Close() // signals Handler's CloseNotifier goroutine (if any) to send
}

func (sc *serverConn) processSettings(f *SettingsFrame) error {
	sc.serveG.check()
	if f.IsAck() {
		// TODO: do we need to do anything?
		return nil
	}
	if err := f.ForeachSetting(sc.processSetting); err != nil {
		return err
	}
	sc.needToSendSettingsAck = true
	sc.scheduleFrameWrite()
	return nil
}

func (sc *serverConn) writeSettingsAck(uint32, interface{}) error {
	return sc.framer.WriteSettingsAck()
}

func (sc *serverConn) processSetting(s Setting) error {
	sc.serveG.check()
	if err := s.Valid(); err != nil {
		return err
	}
	sc.vlogf("processing setting %v", s)
	switch s.ID {
	case SettingHeaderTableSize:
		sc.headerTableSize = s.Val
		sc.hpackEncoder.SetMaxDynamicTableSize(s.Val)
	case SettingEnablePush:
		sc.pushEnabled = s.Val != 0
	case SettingMaxConcurrentStreams:
		sc.clientMaxStreams = s.Val
	case SettingInitialWindowSize:
		return sc.processSettingInitialWindowSize(s.Val)
	case SettingMaxFrameSize:
		sc.maxWriteFrameSize = s.Val
	case SettingMaxHeaderListSize:
		sc.maxHeaderListSize = s.Val
	default:
		// Unknown setting: "An endpoint that receives a SETTINGS
		// frame with any unknown or unsupported identifier MUST
		// ignore that setting."
	}
	return nil
}

func (sc *serverConn) processSettingInitialWindowSize(val uint32) error {
	sc.serveG.check()
	// Note: val already validated to be within range by
	// processSetting's Valid call.

	// "A SETTINGS frame can alter the initial flow control window
	// size for all current streams. When the value of
	// SETTINGS_INITIAL_WINDOW_SIZE changes, a receiver MUST
	// adjust the size of all stream flow control windows that it
	// maintains by the difference between the new value and the
	// old value."
	old := sc.initialWindowSize
	sc.initialWindowSize = int32(val)
	growth := sc.initialWindowSize - old // may be negative
	for _, st := range sc.streams {
		if !st.flow.add(growth) {
			// 6.9.2 Initial Flow Control Window Size
			// "An endpoint MUST treat a change to
			// SETTINGS_INITIAL_WINDOW_SIZE that causes any flow
			// control window to exceed the maximum size as a
			// connection error (Section 5.4.1) of type
			// FLOW_CONTROL_ERROR."
			return ConnectionError(ErrCodeFlowControl)
		}
	}
	return nil
}

func (sc *serverConn) processData(f *DataFrame) error {
	sc.serveG.check()
	// "If a DATA frame is received whose stream is not in "open"
	// or "half closed (local)" state, the recipient MUST respond
	// with a stream error (Section 5.4.2) of type STREAM_CLOSED."
	id := f.Header().StreamID
	st, ok := sc.streams[id]
	if !ok || (st.state != stateOpen && st.state != stateHalfClosedLocal) {
		return StreamError{id, ErrCodeStreamClosed}
	}
	if st.body == nil {
		// Not expecting data.
		// TODO: which error code?
		return StreamError{id, ErrCodeStreamClosed}
	}
	data := f.Data()

	// Sender sending more than they'd declared?
	if st.declBodyBytes != -1 && st.bodyBytes+int64(len(data)) > st.declBodyBytes {
		st.body.Close(fmt.Errorf("sender tried to send more than declared Content-Length of %d bytes", st.declBodyBytes))
		return StreamError{id, ErrCodeStreamClosed}
	}
	if len(data) > 0 {
		// TODO: verify they're allowed to write with the flow control
		// window we'd advertised to them.
		// TODO: verify n from Write
		if _, err := st.body.Write(data); err != nil {
			return StreamError{id, ErrCodeStreamClosed}
		}
		st.bodyBytes += int64(len(data))
	}
	if f.StreamEnded() {
		if st.declBodyBytes != -1 && st.declBodyBytes != st.bodyBytes {
			st.body.Close(fmt.Errorf("request declared a Content-Length of %d but only wrote %d bytes",
				st.declBodyBytes, st.bodyBytes))
		} else {
			st.body.Close(io.EOF)
		}
		switch st.state {
		case stateOpen:
			st.state = stateHalfClosedRemote
		case stateHalfClosedLocal:
			st.state = stateClosed
		}
	}
	return nil
}

func (sc *serverConn) processHeaders(f *HeadersFrame) error {
	sc.serveG.check()
	id := f.Header().StreamID
	if sc.inGoAway {
		// Ignore.
		return nil
	}
	// http://http2.github.io/http2-spec/#rfc.section.5.1.1
	if id%2 != 1 || id <= sc.maxStreamID || sc.req.stream != nil {
		// Streams initiated by a client MUST use odd-numbered
		// stream identifiers. [...] The identifier of a newly
		// established stream MUST be numerically greater than all
		// streams that the initiating endpoint has opened or
		// reserved. [...]  An endpoint that receives an unexpected
		// stream identifier MUST respond with a connection error
		// (Section 5.4.1) of type PROTOCOL_ERROR.
		return ConnectionError(ErrCodeProtocol)
	}
	if id > sc.maxStreamID {
		sc.maxStreamID = id
	}
	st := &stream{
		conn:  sc,
		id:    id,
		state: stateOpen,
		flow:  newFlow(sc.initialWindowSize),
	}
	st.cw.Init() // make Cond use its Mutex, without heap-promoting them separately
	if f.StreamEnded() {
		st.state = stateHalfClosedRemote
	}
	sc.streams[id] = st
	sc.curOpenStreams++
	sc.req = requestParam{
		stream: st,
		header: make(http.Header),
	}
	return sc.processHeaderBlockFragment(st, f.HeaderBlockFragment(), f.HeadersEnded())
}

func (sc *serverConn) processContinuation(f *ContinuationFrame) error {
	sc.serveG.check()
	st := sc.streams[f.Header().StreamID]
	if st == nil || sc.curHeaderStreamID() != st.id {
		return ConnectionError(ErrCodeProtocol)
	}
	return sc.processHeaderBlockFragment(st, f.HeaderBlockFragment(), f.HeadersEnded())
}

func (sc *serverConn) processHeaderBlockFragment(st *stream, frag []byte, end bool) error {
	sc.serveG.check()
	if _, err := sc.hpackDecoder.Write(frag); err != nil {
		// TODO: convert to stream error I assume?
		return err
	}
	if !end {
		return nil
	}
	if err := sc.hpackDecoder.Close(); err != nil {
		// TODO: convert to stream error I assume?
		return err
	}
	defer sc.resetPendingRequest()
	if sc.curOpenStreams > sc.advMaxStreams {
		// Too many open streams.
		// TODO: which error code here? Using ErrCodeProtocol for now.
		// https://github.com/http2/http2-spec/issues/649
		return StreamError{st.id, ErrCodeProtocol}
	}

	rw, req, err := sc.newWriterAndRequest()
	if err != nil {
		return err
	}
	st.body = req.Body.(*requestBody).pipe // may be nil
	st.declBodyBytes = req.ContentLength
	go sc.runHandler(rw, req)
	return nil
}

// resetPendingRequest zeros out all state related to a HEADERS frame
// and its zero or more CONTINUATION frames sent to start a new
// request.
func (sc *serverConn) resetPendingRequest() {
	sc.serveG.check()
	sc.req = requestParam{}
}

func (sc *serverConn) newWriterAndRequest() (*responseWriter, *http.Request, error) {
	sc.serveG.check()
	rp := &sc.req
	if rp.invalidHeader || rp.method == "" || rp.path == "" ||
		(rp.scheme != "https" && rp.scheme != "http") {
		// See 8.1.2.6 Malformed Requests and Responses:
		//
		// Malformed requests or responses that are detected
		// MUST be treated as a stream error (Section 5.4.2)
		// of type PROTOCOL_ERROR."
		//
		// 8.1.2.3 Request Pseudo-Header Fields
		// "All HTTP/2 requests MUST include exactly one valid
		// value for the :method, :scheme, and :path
		// pseudo-header fields"
		return nil, nil, StreamError{rp.stream.id, ErrCodeProtocol}
	}
	var tlsState *tls.ConnectionState // make this non-nil if https
	if rp.scheme == "https" {
		tlsState = &tls.ConnectionState{}
		if tc, ok := sc.conn.(*tls.Conn); ok {
			*tlsState = tc.ConnectionState()
			if tlsState.Version < tls.VersionTLS12 {
				// 9.2 Use of TLS Features
				// An implementation of HTTP/2 over TLS MUST use TLS
				// 1.2 or higher with the restrictions on feature set
				// and cipher suite described in this section. Due to
				// implementation limitations, it might not be
				// possible to fail TLS negotiation. An endpoint MUST
				// immediately terminate an HTTP/2 connection that
				// does not meet the TLS requirements described in
				// this section with a connection error (Section
				// 5.4.1) of type INADEQUATE_SECURITY.
				return nil, nil, ConnectionError(ErrCodeInadequateSecurity)
			}
			// TODO: verify cipher suites. (9.2.1, 9.2.2)
		}
	}
	authority := rp.authority
	if authority == "" {
		authority = rp.header.Get("Host")
	}
	needsContinue := rp.header.Get("Expect") == "100-continue"
	if needsContinue {
		rp.header.Del("Expect")
	}
	bodyOpen := rp.stream.state == stateOpen
	body := &requestBody{
		stream:        rp.stream,
		needsContinue: needsContinue,
	}
	url, err := url.ParseRequestURI(rp.path)
	if err != nil {
		// TODO: find the right error code?
		return nil, nil, StreamError{rp.stream.id, ErrCodeProtocol}
	}
	req := &http.Request{
		Method:     rp.method,
		URL:        url,
		RemoteAddr: sc.conn.RemoteAddr().String(),
		Header:     rp.header,
		RequestURI: rp.path,
		Proto:      "HTTP/2.0",
		ProtoMajor: 2,
		ProtoMinor: 0,
		TLS:        tlsState,
		Host:       authority,
		Body:       body,
	}
	if bodyOpen {
		body.pipe = &pipe{
			b: buffer{buf: make([]byte, 65536)}, // TODO: share/remove
		}
		body.pipe.c.L = &body.pipe.m

		if vv, ok := rp.header["Content-Length"]; ok {
			req.ContentLength, _ = strconv.ParseInt(vv[0], 10, 64)
		} else {
			req.ContentLength = -1
		}
	}

	rws := responseWriterStatePool.Get().(*responseWriterState)
	bwSave := rws.bw
	*rws = responseWriterState{} // zero all the fields
	rws.bw = bwSave
	rws.bw.Reset(chunkWriter{rws})
	rws.stream = rp.stream
	rws.req = req
	rws.body = body
	rws.frameWriteCh = make(chan error, 1)

	rw := &responseWriter{rws: rws}
	return rw, req, nil
}

// Run on its own goroutine.
func (sc *serverConn) runHandler(rw *responseWriter, req *http.Request) {
	defer rw.handlerDone()
	// TODO: catch panics like net/http.Server
	sc.handler.ServeHTTP(rw, req)
}

type frameWriteMsg struct {
	// write runs on the writeFrames goroutine.
	write func(sc *serverConn, streamID uint32, v interface{}) error

	v         interface{} // passed to write
	cost      uint32      // number of flow control bytes required
	stream    *stream     // used for prioritization
	endStream bool        // streamID is being closed locally

	// done, if non-nil, must be a buffered channel with space for
	// 1 message and is sent the return value from write (or an
	// earlier error) when the frame has been written.
	done chan error
}

// headerWriteReq is a request to write an HTTP response header from a server Handler.
type headerWriteReq struct {
	stream      *stream
	httpResCode int
	h           http.Header // may be nil
	endStream   bool

	contentType   string
	contentLength string
}

// called from handler goroutines.
// h may be nil.
func (sc *serverConn) writeHeaders(req headerWriteReq, tempCh chan error) {
	sc.serveG.checkNotOn() // NOT on
	var errc chan error
	if req.h != nil {
		// If there's a header map (which we don't own), so we have to block on
		// waiting for this frame to be written, so an http.Flush mid-handler
		// writes out the correct value of keys, before a handler later potentially
		// mutates it.
		errc = tempCh
	}
	sc.writeFrameFromHandler(frameWriteMsg{
		write:     (*serverConn).writeHeadersFrame,
		v:         req,
		stream:    req.stream,
		done:      errc,
		endStream: req.endStream,
	})
	if errc != nil {
		select {
		case <-errc:
			// Ignore. Just for synchronization.
			// Any error will be handled in the writing goroutine.
		case <-sc.doneServing:
			// Client has closed the connection.
		}
	}
}

func (sc *serverConn) writeHeadersFrame(streamID uint32, v interface{}) error {
	sc.writeG.check()
	req := v.(headerWriteReq)

	sc.headerWriteBuf.Reset()
	sc.hpackEncoder.WriteField(hpack.HeaderField{Name: ":status", Value: httpCodeString(req.httpResCode)})
	for k, vv := range req.h {
		k = lowerHeader(k)
		for _, v := range vv {
			// TODO: more of "8.1.2.2 Connection-Specific Header Fields"
			if k == "transfer-encoding" && v != "trailers" {
				continue
			}
			sc.hpackEncoder.WriteField(hpack.HeaderField{Name: k, Value: v})
		}
	}
	if req.contentType != "" {
		sc.hpackEncoder.WriteField(hpack.HeaderField{Name: "content-type", Value: req.contentType})
	}
	if req.contentLength != "" {
		sc.hpackEncoder.WriteField(hpack.HeaderField{Name: "content-length", Value: req.contentLength})
	}

	headerBlock := sc.headerWriteBuf.Bytes()
	if len(headerBlock) > int(sc.maxWriteFrameSize) {
		// we'll need continuation ones.
		panic("TODO")
	}
	return sc.framer.WriteHeaders(HeadersFrameParam{
		StreamID:      req.stream.id,
		BlockFragment: headerBlock,
		EndStream:     req.endStream,
		EndHeaders:    true, // no continuation yet
	})
}

// called from handler goroutines.
func (sc *serverConn) write100ContinueHeaders(st *stream) {
	sc.serveG.checkNotOn() // NOT
	sc.writeFrameFromHandler(frameWriteMsg{
		write:  (*serverConn).write100ContinueHeadersFrame,
		stream: st,
	})
}

func (sc *serverConn) write100ContinueHeadersFrame(streamID uint32, _ interface{}) error {
	sc.writeG.check()
	sc.headerWriteBuf.Reset()
	sc.hpackEncoder.WriteField(hpack.HeaderField{Name: ":status", Value: "100"})
	return sc.framer.WriteHeaders(HeadersFrameParam{
		StreamID:      streamID,
		BlockFragment: sc.headerWriteBuf.Bytes(),
		EndStream:     false,
		EndHeaders:    true,
	})
}

func (sc *serverConn) writeDataFrame(streamID uint32, v interface{}) error {
	sc.writeG.check()
	req := v.(*dataWriteParams)
	return sc.framer.WriteData(streamID, req.end, req.p)
}

type windowUpdateReq struct {
	n uint32
}

// called from handler goroutines
func (sc *serverConn) sendWindowUpdate(st *stream, n int) {
	sc.serveG.checkNotOn() // NOT
	if st == nil {
		panic("no stream")
	}
	const maxUint32 = 2147483647
	for n >= maxUint32 {
		sc.writeFrameFromHandler(frameWriteMsg{
			write:  (*serverConn).sendWindowUpdateInLoop,
			v:      windowUpdateReq{maxUint32},
			stream: st,
		})
		n -= maxUint32
	}
	if n > 0 {
		sc.writeFrameFromHandler(frameWriteMsg{
			write:  (*serverConn).sendWindowUpdateInLoop,
			v:      windowUpdateReq{uint32(n)},
			stream: st,
		})
	}
}

func (sc *serverConn) sendWindowUpdateInLoop(streamID uint32, v interface{}) error {
	sc.writeG.check()
	wu := v.(windowUpdateReq)
	if err := sc.framer.WriteWindowUpdate(0, wu.n); err != nil {
		return err
	}
	if err := sc.framer.WriteWindowUpdate(streamID, wu.n); err != nil {
		return err
	}
	return nil
}

type requestBody struct {
	stream        *stream
	closed        bool
	pipe          *pipe // non-nil if we have a HTTP entity message body
	needsContinue bool  // need to send a 100-continue
}

func (b *requestBody) Close() error {
	if b.pipe != nil {
		b.pipe.Close(errClosedBody)
	}
	b.closed = true
	return nil
}

func (b *requestBody) Read(p []byte) (n int, err error) {
	if b.needsContinue {
		b.needsContinue = false
		b.stream.conn.write100ContinueHeaders(b.stream)
	}
	if b.pipe == nil {
		return 0, io.EOF
	}
	n, err = b.pipe.Read(p)
	if n > 0 {
		b.stream.conn.sendWindowUpdate(b.stream, n)
	}
	return
}

// responseWriter is the http.ResponseWriter implementation.  It's
// intentionally small (1 pointer wide) to minimize garbage.  The
// responseWriterState pointer inside is zeroed at the end of a
// request (in handlerDone) and calls on the responseWriter thereafter
// simply crash (caller's mistake), but the much larger responseWriterState
// and buffers are reused between multiple requests.
type responseWriter struct {
	rws *responseWriterState
}

// Optional http.ResponseWriter interfaces implemented.
var (
	_ http.CloseNotifier = (*responseWriter)(nil)
	_ http.Flusher       = (*responseWriter)(nil)
	_ stringWriter       = (*responseWriter)(nil)
)

type responseWriterState struct {
	// immutable within a request:
	stream *stream
	req    *http.Request
	body   *requestBody // to close at end of request, if DATA frames didn't

	// TODO: adjust buffer writing sizes based on server config, frame size updates from peer, etc
	bw *bufio.Writer // writing to a chunkWriter{this *responseWriterState}

	// mutated by http.Handler goroutine:
	handlerHeader http.Header // nil until called
	snapHeader    http.Header // snapshot of handlerHeader at WriteHeader time
	status        int         // status code passed to WriteHeader
	wroteHeader   bool        // WriteHeader called (explicitly or implicitly). Not necessarily sent to user yet.
	sentHeader    bool        // have we sent the header frame?
	handlerDone   bool        // handler has finished
	curWrite      dataWriteParams
	frameWriteCh  chan error // re-used whenever we need to block on a frame being written

	closeNotifierMu sync.Mutex // guards closeNotifierCh
	closeNotifierCh chan bool  // nil until first used
}

func (rws *responseWriterState) writeData(p []byte, end bool) error {
	rws.curWrite.p = p
	rws.curWrite.end = end
	return rws.stream.conn.writeData(rws.stream, &rws.curWrite, rws.frameWriteCh)
}

type chunkWriter struct{ rws *responseWriterState }

func (cw chunkWriter) Write(p []byte) (n int, err error) { return cw.rws.writeChunk(p) }

// writeChunk writes chunks from the bufio.Writer. But because
// bufio.Writer may bypass its chunking, sometimes p may be
// arbitrarily large.
//
// writeChunk is also responsible (on the first chunk) for sending the
// HEADER response.
func (rws *responseWriterState) writeChunk(p []byte) (n int, err error) {
	if !rws.wroteHeader {
		rws.writeHeader(200)
	}
	if !rws.sentHeader {
		rws.sentHeader = true
		var ctype, clen string // implicit ones, if we can calculate it
		if rws.handlerDone && rws.snapHeader.Get("Content-Length") == "" {
			clen = strconv.Itoa(len(p))
		}
		if rws.snapHeader.Get("Content-Type") == "" {
			ctype = http.DetectContentType(p)
		}
		endStream := rws.handlerDone && len(p) == 0
		rws.stream.conn.writeHeaders(headerWriteReq{
			stream:        rws.stream,
			httpResCode:   rws.status,
			h:             rws.snapHeader,
			endStream:     endStream,
			contentType:   ctype,
			contentLength: clen,
		}, rws.frameWriteCh)
		if endStream {
			return
		}
	}
	if len(p) == 0 {
		if rws.handlerDone {
			err = rws.writeData(nil, true)
		}
		return
	}
	for len(p) > 0 {
		chunk := p
		if len(chunk) > handlerChunkWriteSize {
			chunk = chunk[:handlerChunkWriteSize]
		}
		allowedSize := rws.stream.flow.wait(int32(len(chunk)))
		if allowedSize == 0 {
			return n, errStreamBroken
		}
		chunk = chunk[:allowedSize]
		p = p[len(chunk):]
		isFinal := rws.handlerDone && len(p) == 0
		err = rws.writeData(chunk, isFinal)
		if err != nil {
			break
		}
		n += len(chunk)
	}
	return
}

func (w *responseWriter) Flush() {
	rws := w.rws
	if rws == nil {
		panic("Header called after Handler finished")
	}
	if rws.bw.Buffered() > 0 {
		if err := rws.bw.Flush(); err != nil {
			// Ignore the error. The frame writer already knows.
			return
		}
	} else {
		// The bufio.Writer won't call chunkWriter.Write
		// (writeChunk with zero bytes, so we have to do it
		// ourselves to force the HTTP response header and/or
		// final DATA frame (with END_STREAM) to be sent.
		rws.writeChunk(nil)
	}
}

func (w *responseWriter) CloseNotify() <-chan bool {
	rws := w.rws
	if rws == nil {
		panic("CloseNotify called after Handler finished")
	}
	rws.closeNotifierMu.Lock()
	ch := rws.closeNotifierCh
	if ch == nil {
		ch = make(chan bool, 1)
		rws.closeNotifierCh = ch
		go func() {
			rws.stream.cw.Wait() // wait for close
			ch <- true
		}()
	}
	rws.closeNotifierMu.Unlock()
	return ch
}

func (w *responseWriter) Header() http.Header {
	rws := w.rws
	if rws == nil {
		panic("Header called after Handler finished")
	}
	if rws.handlerHeader == nil {
		rws.handlerHeader = make(http.Header)
	}
	return rws.handlerHeader
}

func (w *responseWriter) WriteHeader(code int) {
	rws := w.rws
	if rws == nil {
		panic("WriteHeader called after Handler finished")
	}
	rws.writeHeader(code)
}

func (rws *responseWriterState) writeHeader(code int) {
	if !rws.wroteHeader {
		rws.wroteHeader = true
		rws.status = code
		if len(rws.handlerHeader) > 0 {
			rws.snapHeader = cloneHeader(rws.handlerHeader)
		}
	}
}

func cloneHeader(h http.Header) http.Header {
	h2 := make(http.Header, len(h))
	for k, vv := range h {
		vv2 := make([]string, len(vv))
		copy(vv2, vv)
		h2[k] = vv2
	}
	return h2
}

// The Life Of A Write is like this:
//
// TODO: copy/adapt the similar comment from Go's http server.go
func (w *responseWriter) Write(p []byte) (n int, err error) {
	return w.write(len(p), p, "")
}

func (w *responseWriter) WriteString(s string) (n int, err error) {
	return w.write(len(s), nil, s)
}

// either dataB or dataS is non-zero.
func (w *responseWriter) write(lenData int, dataB []byte, dataS string) (n int, err error) {
	rws := w.rws
	if rws == nil {
		panic("Write called after Handler finished")
	}
	if !rws.wroteHeader {
		w.WriteHeader(200)
	}
	if dataB != nil {
		return rws.bw.Write(dataB)
	} else {
		return rws.bw.WriteString(dataS)
	}
}

func (w *responseWriter) handlerDone() {
	rws := w.rws
	if rws == nil {
		panic("handlerDone called twice")
	}
	rws.handlerDone = true
	w.Flush()
	w.rws = nil
	responseWriterStatePool.Put(rws)
}
