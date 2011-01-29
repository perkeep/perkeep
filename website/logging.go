package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"http"
	"strings"
	"time"
)

type logRecord struct {
	time                *time.Time
	ip, method, rawpath string
	responseBytes       int64
	responseStatus      int
	userAgent, referer  string
	proto               string // "HTTP/1.1"

	rw http.ResponseWriter
}

type logHandler struct {
	ch      chan *logRecord
	dir     string
	handler http.Handler
}

func NewLoggingHandler(handler http.Handler, dir string) http.Handler {
	h := &logHandler{
		ch:      make(chan *logRecord, 1000),
		dir:     dir,
		handler: handler,
	}
	go h.logFromChannel()
	return h
}

func (h *logHandler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	// Strip port number from address
	addr := rw.RemoteAddr()
	if colon := strings.LastIndex(addr, ":"); colon != -1 {
		addr = addr[:colon]
	}

	lr := &logRecord{
		time:           time.UTC(),
		ip:             addr,
		method:         r.Method,
		rawpath:        r.URL.RawPath,
		userAgent:      r.UserAgent,
		referer:        r.Referer,
		responseStatus: http.StatusOK,
		proto:          r.Proto,
		rw:             rw,
	}
	h.handler.ServeHTTP(lr, r)
	h.ch <- lr
}

var monthAbbr = [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun",
	"Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

func (h *logHandler) logFromChannel() {
	for {
		lr := <-h.ch
		lr.rw = nil

		// [10/Oct/2000:13:55:36 -0700]
		dateString := fmt.Sprintf("%02d/%s/%04d:%02d:%02d:%02d -0000",
			lr.time.Day,
			monthAbbr[lr.time.Month-1],
			lr.time.Year,
			lr.time.Hour, lr.time.Minute, lr.time.Second)

		// Combined Log Format
		// http://httpd.apache.org/docs/1.3/logs.html#combined
		logLine := fmt.Sprintf("%s - - [%s] %q %d %d %q %q\n",
			lr.ip,
			dateString,
			lr.method+" "+lr.rawpath+" "+lr.proto,
			lr.responseStatus,
			lr.responseBytes,
			lr.referer,
			lr.userAgent,
		)
		if h.dir == "-" {
			os.Stdout.WriteString(logLine)
		}
	}
}

func (lr *logRecord) Write(p []byte) (int, os.Error) {
	written, err := lr.rw.Write(p)
	lr.responseBytes += int64(written)
	return written, err
}

func (lr *logRecord) WriteHeader(status int) {
	lr.responseStatus = status
	lr.rw.WriteHeader(status)
}

// Boring proxies:  (seems like I should be able to use embedding somehow...)

func (lr *logRecord) RemoteAddr() string {
	return lr.rw.RemoteAddr()
}

func (lr *logRecord) UsingTLS() bool {
	return lr.rw.UsingTLS()
}

func (lr *logRecord) SetHeader(k, v string) {
	lr.rw.SetHeader(k, v)
}

func (lr *logRecord) Flush() {
	lr.rw.Flush()
}

func (lr *logRecord) Hijack() (io.ReadWriteCloser, *bufio.ReadWriter, os.Error) {
	return lr.rw.Hijack()
}
