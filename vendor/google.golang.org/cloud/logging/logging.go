// Copyright 2015 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package logging contains a Google Cloud Logging client.
package logging // import "google.golang.org/cloud/logging"

import (
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"golang.org/x/net/context"
	api "google.golang.org/api/logging/v1beta3"
	"google.golang.org/cloud"
	"google.golang.org/cloud/internal/transport"
)

// Scope is the OAuth2 scope necessary to use Google Cloud Logging.
const Scope = api.CloudPlatformScope

// Level is the log level.
type Level int

const (
	// Default means no assigned severity level.
	Default Level = iota
	Debug
	Info
	Warning
	Error
	Critical
	nLevel
)

var levelName = [nLevel]string{
	Default:  "",
	Debug:    "DEBUG",
	Info:     "INFO",
	Warning:  "WARNING",
	Error:    "ERROR",
	Critical: "CRITICAL",
}

func (v Level) String() string {
	return levelName[v]
}

// Client is a Google Cloud Logging client.
// It must be constructed via NewClient.
type Client struct {
	svc     *api.Service
	logs    *api.ProjectsLogsEntriesService
	projID  string
	logName string
	writer  [nLevel]io.Writer
	logger  [nLevel]*log.Logger

	mu          sync.Mutex
	queued      []*api.LogEntry
	curFlush    *flushCall  // currently in-flight flush
	flushTimer  *time.Timer // nil before first use
	timerActive bool        // whether flushTimer is armed
	inFlight    int         // number of log entries sent to API service but not yet ACKed

	// ServiceName may be "appengine.googleapis.com",
	// "compute.googleapis.com" or "custom.googleapis.com".
	//
	// The default is unspecified is "custom.googleapis.com".
	//
	// The service name is only used by the API server to
	// determine which of the labels are used to index the logs.
	ServiceName string

	// CommonLabels are metadata labels that apply to all log
	// entries in this request, so that you don't have to repeat
	// them in each log entry's metadata.labels field. If any of
	// the log entries contains a (key, value) with the same key
	// that is in commonLabels, then the entry's (key, value)
	// overrides the one in CommonLabels.
	CommonLabels map[string]string

	// BufferLimit is the maximum number of items to keep in memory
	// before flushing. Zero means automatic. A value of 1 means to
	// flush after each log entry.
	// The default is currently 10,000.
	BufferLimit int

	// FlushAfter optionally specifies a threshold count at which buffered
	// log entries are flushed, even if the BufferInterval has not yet
	// been reached.
	// The default is currently 10.
	FlushAfter int

	// BufferInterval is the maximum amount of time that an item
	// should remain buffered in memory before being flushed to
	// the logging service.
	BufferInterval time.Duration

	// Overflow optionally specifies a function which is run
	// when the Log function overflows its configured buffer
	// limit. If nil, the log entry is dropped. The return
	// value is returned by Log.
	Overflow func(*Client, Entry) error
}

func (c *Client) flushAfter() int {
	if c.FlushAfter > 0 {
		return c.FlushAfter
	}
	return 10
}

func (c *Client) bufferInterval() time.Duration {
	if c.BufferInterval > 0 {
		return c.BufferInterval
	}
	return time.Second
}

func (c *Client) bufferLimit() int {
	if c.BufferLimit > 0 {
		return c.BufferLimit
	}
	return 10000
}

func (c *Client) serviceName() string {
	if v := c.ServiceName; v != "" {
		return v
	}
	return "custom.googleapis.com"
}

// Writer returns an io.Writer for the provided log level.
//
// Each Write call on the returned Writer generates a log entry.
//
// This Writer accessor does not allocate, so callers do not need to
// cache.
func (c *Client) Writer(v Level) io.Writer { return c.writer[v] }

// Logger returns a *log.Logger for the provided log level.
//
// A Logger for each Level is pre-allocated by NewClient with an empty
// prefix and no flags.  This Logger accessor does not allocate.
// Callers wishing to use alternate flags (such as log.Lshortfile) may
// mutate the returned Logger with SetFlags. Such mutations affect all
// callers in the program.
func (c *Client) Logger(v Level) *log.Logger { return c.logger[v] }

type levelWriter struct {
	level Level
	c     *Client
}

func (w levelWriter) Write(p []byte) (n int, err error) {
	return len(p), w.c.Log(Entry{
		Level:   w.level,
		Payload: string(p),
	})
}

// Entry is a log entry.
type Entry struct {
	// Time is the time of the entry. If the zero value, the current time is used.
	Time time.Time

	// Level is log entry's severity level.
	// The zero value means undefined.
	Level Level

	// Payload may be either a string or JSON object.
	// For JSON objects, the type must be either map[string]interface{}
	// or implement json.Marshaler and encode a JSON object (and not any other
	// JSON value).
	Payload interface{}

	// Labels optionally specifies key/value labels for the log entry.
	// Depending on the Client's ServiceName, these are indexed differently
	// by the Cloud Logging Service.
	// See https://cloud.google.com/logging/docs/logs_index
	// The Client.Log method takes ownership of this map.
	Labels map[string]string

	// TODO: de-duping id
}

func (c *Client) apiEntry(e Entry) (*api.LogEntry, error) {
	t := e.Time
	if t.IsZero() {
		t = time.Now()
	}

	ent := &api.LogEntry{
		Metadata: &api.LogEntryMetadata{
			Timestamp:   t.UTC().Format(time.RFC3339Nano),
			ServiceName: c.serviceName(),
			Severity:    e.Level.String(),
			Labels:      e.Labels,
		},
	}
	switch p := e.Payload.(type) {
	case string:
		ent.TextPayload = p
	case map[string]interface{}:
		ent.StructPayload = p
	default:
		return nil, fmt.Errorf("unhandled Log Payload type %T", p)
	}
	return ent, nil
}

// LogSync logs e synchronously without any buffering.
// This is mostly intended for debugging or critical errors.
func (c *Client) LogSync(e Entry) error {
	ent, err := c.apiEntry(e)
	if err != nil {
		return err
	}
	_, err = c.logs.Write(c.projID, c.logName, &api.WriteLogEntriesRequest{
		CommonLabels: c.CommonLabels,
		Entries:      []*api.LogEntry{ent},
	}).Do()
	return err
}

var ErrOverflow = errors.New("logging: log entry overflowed buffer limits")

// Log queues an entry to be sent to the logging service, subject to the
// Client's parameters. By default, the log will be flushed within
// a second.
// Log only returns an error if the entry is invalid, or ErrOverflow
// if the log entry overflows the buffer limit.
func (c *Client) Log(e Entry) error {
	ent, err := c.apiEntry(e)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	buffered := len(c.queued) + c.inFlight
	if buffered >= c.bufferLimit() {
		if fn := c.Overflow; fn != nil {
			return fn(c, e)
		}
		return ErrOverflow
	}
	c.queued = append(c.queued, ent)
	if len(c.queued) >= c.flushAfter() {
		c.scheduleFlushLocked(0)
		return nil
	}
	c.scheduleFlushLocked(c.bufferInterval())
	return nil
}

// c.mu must be held.
//
// d will be one of two values: either c.BufferInterval (or its
// default value) or 0.
func (c *Client) scheduleFlushLocked(d time.Duration) {
	if c.inFlight > 0 {
		// For now to keep things simple, only allow one HTTP
		// request in flight at a time.
		return
	}
	switch {
	case c.flushTimer == nil:
		// First flush.
		c.timerActive = true
		c.flushTimer = time.AfterFunc(d, c.timeoutFlush)
	case c.timerActive && d == 0:
		// Make it happen sooner.  For example, this is the
		// case of transitioning from a 1 second flush after
		// the 1st item to an immediate flush after the 10th
		// item.
		c.flushTimer.Reset(0)
	case !c.timerActive:
		c.timerActive = true
		c.flushTimer.Reset(d)
	default:
		// else timer was already active, also at d > 0,
		// so we don't touch it and let it fire as previously
		// scheduled.
	}
}

// timeoutFlush runs in its own goroutine (from time.AfterFunc) and
// flushes c.queued.
func (c *Client) timeoutFlush() {
	if err := c.Flush(); err != nil {
		// schedule another try
		// TODO: smarter back-off?
		c.mu.Lock()
		c.scheduleFlushLocked(5 * time.Second)
		c.mu.Unlock()
	}
}

// Ping reports whether the client's connection to Google Cloud
// Logging and the authentication configuration are valid.
func (c *Client) Ping() error {
	_, err := c.logs.Write(c.projID, c.logName, &api.WriteLogEntriesRequest{
		Entries: []*api.LogEntry{},
	}).Do()
	return err
}

// Flush flushes any buffered log entries.
func (c *Client) Flush() error {
	for {
		// Easy and final case: nothing to flush.
		c.mu.Lock()
		if len(c.queued) == 0 {
			c.mu.Unlock()
			return nil
		}

		if f := c.curFlush; f != nil {
			c.mu.Unlock()
			<-f.donec // wait for it
			if f.err != nil {
				return f.err
			}
		}
		c.startFlushLocked()
		c.mu.Unlock()
	}
}

// requires c.mu be held.
func (c *Client) startFlushLocked() {
	if c.curFlush != nil {
		panic("internal error: flush already in flight")
	}
	if len(c.queued) == 0 {
		panic("internal error: no items queued")
	}
	logEntries := c.queued
	c.inFlight = len(logEntries)
	c.queued = nil

	flush := &flushCall{
		donec: make(chan struct{}),
	}
	go func() {
		defer close(flush.donec)
		_, err := c.logs.Write(c.projID, c.logName, &api.WriteLogEntriesRequest{
			CommonLabels: c.CommonLabels,
			Entries:      logEntries,
		}).Do()
		flush.err = err
		log.Printf("Raw write of %d = %v", len(logEntries), err)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.inFlight = 0
		c.curFlush = nil
		if err != nil {
			c.queued = append(c.queued, logEntries...)
		}
	}()

}

const prodAddr = "https://logging.googleapis.com/"

const userAgent = "gcloud-golang-logging/20150922"

// NewClient returns a new log client, logging to the named log in the
// provided project.
//
// The exported fields on the returned client may be modified before
// the client is used for logging. Once log entries are in flight,
// the fields must not be modified.
func NewClient(ctx context.Context, projectID, logName string, opts ...cloud.ClientOption) (*Client, error) {
	httpClient, endpoint, err := transport.NewHTTPClient(ctx, append([]cloud.ClientOption{
		cloud.WithEndpoint(prodAddr),
		cloud.WithScopes(api.CloudPlatformScope),
		cloud.WithUserAgent(userAgent),
	}, opts...)...)
	if err != nil {
		return nil, err
	}
	svc, err := api.New(httpClient)
	if err != nil {
		return nil, err
	}
	svc.BasePath = endpoint
	c := &Client{
		svc:     svc,
		logs:    api.NewProjectsLogsEntriesService(svc),
		logName: logName,
		projID:  projectID,
	}
	for i := range c.writer {
		level := Level(i)
		c.writer[level] = levelWriter{level, c}
		c.logger[level] = log.New(c.writer[level], "", 0)
	}
	return c, nil
}

// flushCall is an in-flight or completed flush.
type flushCall struct {
	donec chan struct{} // closed when response is in
	err   error         // error is valid after wg is Done
}
