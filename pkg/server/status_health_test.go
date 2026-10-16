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

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/health"

	"golang.org/x/net/xsrftoken"
)

func TestStatusHealthActions(t *testing.T) {
	mon := health.New()
	defer mon.Close()
	mon.Register("test.check[x]", "a test check").SetRunner(func(context.Context) error { return nil }, health.ScheduleOff)
	sh := &StatusHandler{prefix: "/status/", health: mon}

	var page strings.Builder
	sh.writeHealthHTML(&page, mon.Status())
	for _, want := range []string{"test.check[x]", "value='run'", "<option value='off' selected>off (default)</option>"} {
		if !strings.Contains(page.String(), want) {
			t.Errorf("health HTML doesn't contain %q:\n%s", want, page.String())
		}
	}

	post := func(form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/status/health", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		sh.serveHealthAction(rec, req)
		return rec
	}
	token := xsrftoken.Generate(auth.Token(), "user", healthActionXSRF)

	if rec := post(url.Values{"token": {"bogus"}, "id": {"test.check[x]"}, "action": {"schedule"}, "schedule": {"daily"}}); rec.Code != http.StatusForbidden {
		t.Errorf("POST with a bad token = %d; want %d", rec.Code, http.StatusForbidden)
	}
	rec := post(url.Values{"token": {token}, "id": {"test.check[x]"}, "action": {"schedule"}, "schedule": {"daily"}})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/status/#health" {
		t.Errorf("POST to schedule = %d to %q; want a redirect to /status/#health", rec.Code, rec.Header().Get("Location"))
	}
	if got := mon.Status().Checks[0].Schedule; got != health.ScheduleDaily {
		t.Errorf("schedule = %q; want daily", got)
	}
	if rec := post(url.Values{"token": {token}, "id": {"test.nope[x]"}, "action": {"run"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("POST to run a check that doesn't exist = %d; want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHealthHTMLStates(t *testing.T) {
	mon := health.New()
	defer mon.Close()
	mon.Register("test.ok[x]", "x").OK()
	mon.Register("test.warn[x]", "x").Warn(errors.New("hmm"))
	mon.Register("test.fail[x]", "x").Fatal(errors.New("broken"))
	mon.Register("test.running[x]", "x").Start()
	mon.Register("test.off[x]", "x").SetRunner(func(context.Context) error { return nil }, health.ScheduleOff)
	var buf strings.Builder
	(&StatusHandler{prefix: "/status/", health: mon}).writeHealthHTML(&buf, mon.Status())
	page := buf.String()
	for _, want := range []string{">ok</td>", ">⚠️ warning</td>", ">❌ failed (fatal)</td>", ">⏳ running</td>", ">unchecked</td>"} {
		if !strings.Contains(page, want) {
			t.Errorf("health HTML doesn't contain %q:\n%s", want, page)
		}
	}
}

func TestFormatETA(t *testing.T) {
	for _, tt := range []struct {
		d    time.Duration
		want string
	}{
		{0, "-0m:00s"},
		{5 * time.Second, "-0m:05s"},
		{12*time.Minute + 5*time.Second, "-12m:05s"},
		{59*time.Minute + 59*time.Second + 600*time.Millisecond, "-1h:00m"},
		{time.Hour + 53*time.Minute + 10*time.Second, "-1h:53m"},
		{26*time.Hour + 5*time.Minute, "-26h:05m"},
	} {
		if got := formatETA(tt.d); got != tt.want {
			t.Errorf("formatETA(%v) = %q; want %q", tt.d, got, tt.want)
		}
	}
}

func TestHealthControls(t *testing.T) {
	pass := time.Date(2026, 10, 15, 21, 14, 31, 0, time.UTC)
	for _, tt := range []struct {
		name    string
		c       health.CheckStatus
		want    []string
		notWant []string
	}{
		{"plain", health.CheckStatus{Runnable: true}, []string{"value='run'", ">Run now<"}, []string{">Start<", "value='resume'", ">Cancel<"}},
		{"sharded", health.CheckStatus{Runnable: true, Sharded: true}, []string{">Start<"}, []string{"value='resume'", "Run now"}},
		{"sharded_unfinished", health.CheckStatus{Runnable: true, Sharded: true, PassSince: pass, Paused: true}, []string{">Start<", "value='resume'", "started at 2026-10-15T21:14:31Z"}, []string{"Run now"}},
		{"running", health.CheckStatus{Runnable: true, Sharded: true, PassSince: pass, State: health.StateRunning}, []string{">Cancel<"}, []string{">Start<", "value='resume'"}},
	} {
		var buf strings.Builder
		writeHealthControls(&buf, tt.c, "token")
		for _, w := range tt.want {
			if !strings.Contains(buf.String(), w) {
				t.Errorf("%s: controls don't contain %q:\n%s", tt.name, w, buf.String())
			}
		}
		for _, w := range tt.notWant {
			if strings.Contains(buf.String(), w) {
				t.Errorf("%s: controls contain %q:\n%s", tt.name, w, buf.String())
			}
		}
	}
}

func TestStateHTML(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	running := health.CheckStatus{
		State:        health.StateRunning,
		Started:      now.Add(-10 * time.Minute),
		Done:         200,
		Total:        768,
		ProgressHint: "shards: <all of them>",
	}
	// 200 shards in 10 minutes leaves 568 shards, or 28m24s.
	if got, want := stateHTML(running, now), "⏳ running, <span title='shards: &lt;all of them&gt; (200 / 768)'>26.0%</span><br><span style='color: #666'>-28m:24s</span>"; got != want {
		t.Errorf("running:\n got %q\nwant %q", got, want)
	}
	done := running
	done.State, done.Done = health.StateOK, 768
	if got, want := stateHTML(done, now), "ok"; got != want {
		t.Errorf("done: got %q; want %q", got, want)
	}
	paused := running
	paused.State, paused.Sharded, paused.Paused, paused.Done, paused.ProgressHint = health.StateCanceled, true, true, 60, ""
	if got, want := stateHTML(paused, now), "canceled<br><small>pass paused at <span title='(60 / 768)'>7.8%</span></small>"; got != want {
		t.Errorf("paused:\n got %q\nwant %q", got, want)
	}
	if got, want := stateHTML(health.CheckStatus{State: health.StateRunning, Done: 42}, now), "⏳ running, <span title=''>42</span>"; got != want {
		t.Errorf("no total: got %q; want %q", got, want)
	}
}

func TestWriteStorageHTML(t *testing.T) {
	var buf strings.Builder
	writeStorageHTML(&buf, map[string]storageStatus{
		"/bs/": {Type: "blobpacked", Primary: true, ByHash: map[string]blobserver.HashEstimate{
			"sha224": {Blobs: 190_402, Bytes: 2 << 30, Exact: true},
			"sha1":   {Blobs: 6_730_000, Bytes: 331 << 30, BlobsErr: 210_000, BytesErr: 40 << 30},
		}},
		"/cache/": {Type: "filesystem"},
	})
	page := buf.String()
	for _, want := range []string{
		"<tr><td rowspan='2'>/bs/</td><td rowspan='2'>blobpacked (primary)</td><td rowspan='2' align='right'>≈6.92M ±0.21M</td><td rowspan='2' align='right'>≈333 ±40 GiB</td>" +
			"<td>sha1</td><td align='right'>≈6.73M ±0.21M</td><td align='right'>≈331 ±40 GiB</td></tr>" +
			"<tr><td>sha224</td><td align='right'>190402</td><td align='right'>2.00 GiB</td></tr>",
		"<tr><td rowspan='1'>/cache/</td><td rowspan='1'>filesystem</td><td rowspan='1' align='right'></td><td rowspan='1' align='right'></td><td></td><td></td><td></td></tr>",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("storage HTML doesn't contain %q:\n%s", want, page)
		}
	}
}

func TestStorageStatusJSON(t *testing.T) {
	js, err := json.Marshal(storageStatus{Type: "s3", ByHash: map[string]blobserver.HashEstimate{
		"sha224": {Blobs: 3, Bytes: 30, Exact: true},
		"sha1":   {Blobs: 6_730_000, Bytes: 331 << 30, BlobsErr: 210_000, BytesErr: 40 << 30},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"s3","byHash":{"sha1":{"blobs":6730000,"bytes":355408543744,"blobsErr":210000,"bytesErr":42949672960},"sha224":{"blobs":3,"bytes":30,"exact":true}}}`
	if string(js) != want {
		t.Errorf("JSON =\n%s\nwant\n%s", js, want)
	}
}
