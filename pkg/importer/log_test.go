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
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
)

func TestAccountLog(t *testing.T) {
	var l accountLog
	l.add("one")
	l.add("two\nlines")
	text, end := l.since(0)
	if !strings.HasPrefix(text[19:], " one\n") || !strings.HasSuffix(text, " two\nlines\n") || end != int64(len(text)) {
		t.Fatalf("since(0) = %q, %d", text, end)
	}
	if more, end2 := l.since(end); more != "" || end2 != end {
		t.Errorf("since(end) = %q, %d; want nothing new", more, end2)
	}
	l.add("three")
	if more, _ := l.since(end); !strings.HasSuffix(more, " three\n") || strings.Contains(more, "two") {
		t.Errorf("since(end) after another line = %q; want just it", more)
	}

	// Once over the limit, it drops the oldest whole lines, keeping the
	// offsets of what it keeps.
	line := strings.Repeat("x", 1000)
	for range maxAccountLog/1000 + maxAccountLog/8000 + 10 {
		l.add(line)
	}
	text, end = l.since(0)
	if len(text) > maxAccountLog+maxAccountLog/8 || l.start == 0 {
		t.Fatalf("kept %d bytes, start %d; want it trimmed", len(text), l.start)
	}
	if !strings.HasSuffix(text, line+"\n") || strings.Contains(text, "one") || text[19] != ' ' {
		t.Errorf("trimmed log doesn't start with a whole line: %q…", text[:40])
	}
	if end != l.start+int64(len(text)) {
		t.Errorf("end = %d; want %d", end, l.start+int64(len(text)))
	}
}

type retryErr struct{ at time.Time }

func (e retryErr) Error() string         { return "rate limited" }
func (e retryErr) RetryAfter() time.Time { return e.at }

func TestAccountLogPage(t *testing.T) {
	h, err := NewHost(HostConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	ia := &importerAcct{im: h.imp["dummy1"], acct: &Object{pn: blob.RefFromString("account")}}
	ia.logf("hello %d", 42)

	get := func(off string) logResponse {
		t.Helper()
		w := httptest.NewRecorder()
		ia.ServeHTTP(w, httptest.NewRequest("GET", "/importer/dummy1/acct?log="+off, nil))
		var res logResponse
		if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
			t.Fatalf("log=%s: %v", off, err)
		}
		return res
	}
	res := get("0")
	if !strings.HasSuffix(res.Text, " hello 42\n") || res.Next != int64(len(res.Text)) || res.Running {
		t.Errorf("log=0: %+v", res)
	}
	if res := get(fmt.Sprint(res.Next)); res.Text != "" {
		t.Errorf("log=next: %+v; want nothing new", res)
	}

	// A run that stopped for a rate limit is paused, not failed.
	at := time.Now().Add(time.Hour)
	ia.lastRunDone = time.Now()
	ia.lastRunErr = fmt.Errorf("wrapped: %w", retryErr{at})
	status, _, failed := ia.status()
	if failed || !strings.Contains(status, "Paused") || !strings.Contains(status, at.Local().Format("15:04")) {
		t.Errorf("rate-limited status = %q, failed %v", status, failed)
	}
	ia.lastRunErr = errors.New("broken")
	if status, _, failed := ia.status(); !failed || !strings.Contains(status, "broken") {
		t.Errorf("failed status = %q, failed %v", status, failed)
	}
}
