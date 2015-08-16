package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/cloud/logging"
)

type logRecord struct {
	http.ResponseWriter

	time                time.Time
	ip, method, rawpath string
	responseBytes       int64
	responseStatus      int
	userAgent, referer  string
	proto               string // "HTTP/1.1"
}

type logHandler struct {
	handler http.Handler
	log     WebLogger

	ch chan *logRecord
}

type WebLogger interface {
	// LogEvent is called serially from the same goroutine.
	LogEvent(*logRecord)
}

func NewApacheLogger(dir string, writeStdout bool) WebLogger {
	return &apacheLogger{
		dir:    dir,
		stdout: writeStdout,
	}
}

type apacheLogger struct {
	dir    string // or "" to not log
	stdout bool

	// stateful parts:
	lastFileName string
	logFile      *os.File
}

func (al *apacheLogger) LogEvent(lr *logRecord) {
	// [10/Oct/2000:13:55:36 -0700]
	dateString := fmt.Sprintf("%02d/%s/%04d:%02d:%02d:%02d -0000",
		lr.time.Day(),
		monthAbbr[lr.time.Month()-1],
		lr.time.Year(),
		lr.time.Hour(), lr.time.Minute(), lr.time.Second())

	if al.dir != "" {
		fileName := fmt.Sprintf("%s/%04d-%02d-%02d%s%02d.log", al.dir,
			lr.time.Year(), lr.time.Month(), lr.time.Day(), "h", lr.time.Hour())
		if fileName > al.lastFileName {
			if al.logFile != nil {
				al.logFile.Close()
			}
			var err error
			al.logFile, err = os.OpenFile(fileName, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
			if err != nil {
				log.Printf("Error opening %q: %v", fileName, err)
				return
			}
			al.lastFileName = fileName
		}
	}

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
	if al.stdout {
		os.Stdout.WriteString(logLine)
	}
	if al.logFile != nil {
		al.logFile.WriteString(logLine)
	}
}

func NewLoggingHandler(h http.Handler, wl WebLogger) http.Handler {
	lh := &logHandler{
		ch:      make(chan *logRecord, 1000),
		handler: h,
		log:     wl,
	}
	go func() {
		for {
			lh.log.LogEvent(<-lh.ch)
		}
	}()
	return lh
}

func (h *logHandler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	// Strip port number from address
	// TODO(bradfitz): IPv6 stuff
	addr := r.RemoteAddr
	if colon := strings.LastIndex(addr, ":"); colon != -1 {
		addr = addr[:colon]
	}

	lr := &logRecord{
		time:           time.Now().UTC(),
		ip:             addr,
		method:         r.Method,
		rawpath:        r.URL.RequestURI(),
		userAgent:      r.UserAgent(),
		referer:        r.Referer(),
		responseStatus: http.StatusOK,
		proto:          r.Proto,
		ResponseWriter: rw,
	}
	h.handler.ServeHTTP(lr, r)
	h.ch <- lr
}

var monthAbbr = [12]string{
	"Jan", "Feb", "Mar", "Apr", "May", "Jun",
	"Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
}

func (lr *logRecord) Write(p []byte) (int, error) {
	written, err := lr.ResponseWriter.Write(p)
	lr.responseBytes += int64(written)
	return written, err
}

func (lr *logRecord) WriteHeader(status int) {
	lr.responseStatus = status
	lr.ResponseWriter.WriteHeader(status)
}

type gceLogger struct {
	c *logging.Client
}

func (lg gceLogger) LogEvent(lr *logRecord) {
	lg.c.Log(logging.Entry{
		Time: lr.time,
		Payload: map[string]interface{}{
			"ip":            lr.ip,
			"path":          lr.rawpath,
			"method":        lr.method,
			"responseBytes": lr.responseBytes,
			"status":        lr.responseStatus,
			"userAgent":     lr.userAgent,
			"referer":       lr.referer,
			"proto":         lr.proto,
		},
	})
}
