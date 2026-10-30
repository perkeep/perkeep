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

package importer

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A RetryAfterError is an error of a run that stopped before it was done
// because of the service's rate limit, or another reason it should
// wait, after which a run can go on where it stopped. The account's page
// shows it as a pause, not a failure.
type RetryAfterError interface {
	error
	RetryAfter() time.Time
}

// maxAccountLog is about how much of each account's log is kept in
// memory, for its page.
const maxAccountLog = 10 << 20

// accountLog is the recent log of an account's runs. Its offsets count
// all that was ever written to it, so readers can ask for what's new.
type accountLog struct {
	mu    sync.Mutex
	buf   []byte // the kept log, of whole lines
	start int64  // the offset of buf[0]
}

// add adds a line, with the time, dropping the oldest lines if the log is
// over maxAccountLog.
func (l *accountLog) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = time.Now().AppendFormat(l.buf, "2006-01-02 15:04:05 ")
	l.buf = append(l.buf, line...)
	l.buf = append(l.buf, '\n')
	// Trim only once it's an eighth over, copying what's kept, so
	// trimming is amortized and the array doesn't grow without bound.
	if len(l.buf) <= maxAccountLog+maxAccountLog/8 {
		return
	}
	cut := len(l.buf) - maxAccountLog
	if i := strings.IndexByte(string(l.buf[cut:]), '\n'); i >= 0 {
		cut += i + 1
	}
	l.buf = append([]byte(nil), l.buf[cut:]...)
	l.start += int64(cut)
}

// since returns the log after offset off (or all that's kept, if it's
// dropped some of what's after off), and the offset of its end.
func (l *accountLog) since(off int64) (text string, end int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	end = l.start + int64(len(l.buf))
	if off < l.start {
		off = l.start
	}
	if off > end {
		off = end
	}
	return string(l.buf[off-l.start:]), end
}

// logf logs a message about ia: to the server's log, prefixed by the
// account, and to the account's log, which its page shows.
func (ia *importerAcct) logf(format string, args ...any) {
	msg := strings.TrimRight(fmt.Sprintf(format, args...), "\n")
	ia.log.add(msg)
	log.Printf("%v: %s", ia, msg)
}

// Logf logs a message of the run: to the server's log, prefixed by the
// account, and to the account's log, which its page shows while it
// runs. It's a tailscale.com/types/logger.Logf, for importers to use
// rather than the log package.
func (rc *RunContext) Logf(format string, args ...any) { rc.ia.logf(format, args...) }

// logResponse is the response of an account page's ?log=offset, which
// its script polls for what's new in the account's log.
type logResponse struct {
	Text    string `json:"text"`    // the log after the offset asked for
	Next    int64  `json:"next"`    // the offset to ask for next
	Running bool   `json:"running"` // whether a run is going, so more may come
	Status  string `json:"status"`  // the account's status, as its page shows it
}

// serveLog serves the account's log after offset v, as a logResponse.
func (ia *importerAcct) serveLog(w http.ResponseWriter, v string) {
	off, err := strconv.ParseInt(v, 10, 64)
	if err != nil || off < 0 {
		http.Error(w, "bad log offset", http.StatusBadRequest)
		return
	}
	var res logResponse
	res.Text, res.Next = ia.log.since(off)
	res.Status, res.Running, _ = ia.status()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(res)
}
