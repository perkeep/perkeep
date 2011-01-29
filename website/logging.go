package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"http"
	"time"
)

type logRecord struct {
	timeEpochNs         int64
	ip, method, rawpath string
	responseBytes       int64
	responseStatus      int
	userAgent, referer  string

	rw http.ResponseWriter
}

type logHandler struct {
	ch      chan *logRecord
	dir     string
	handler http.Handler
}

func NewLoggingHandler(handler http.Handler, dir string) http.Handler {
	h := &logHandler{
		ch:      make(chan *logRecord),
		dir:     dir,
		handler: handler,
	}
	go h.logFromChannel()
	return h
}

func (h *logHandler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	lr := &logRecord{
		timeEpochNs:    time.Nanoseconds(),
		ip:             rw.RemoteAddr(),
		method:         r.Method,
		rawpath:        r.URL.RawPath,
		userAgent:      r.UserAgent,
		referer:        r.Referer,
		responseStatus: http.StatusOK,
		rw:             rw,
	}
	h.handler.ServeHTTP(lr, r)
	h.ch <- lr
}

func (h *logHandler) logFromChannel() {
	for {
		lr := <-h.ch
		lr.rw = nil
		logLine := fmt.Sprintf("Request: %#v\n", lr)
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
