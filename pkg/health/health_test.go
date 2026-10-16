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

package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"perkeep.org/pkg/sorted"
)

func TestStates(t *testing.T) {
	m := New()
	a := m.Register("test.one[a]", "check one")
	b := m.Register("test.two[b]", "check two")

	if st := m.Status(); st.State != StateOK || !st.Starting || st.ReadOnly {
		t.Fatalf("new monitor: state %v, starting %v, read-only %v; want ok, starting, writable", st.State, st.Starting, st.ReadOnly)
	}
	m.EndStartup()

	a.Start()
	a.Progress(3, 10)
	b.Start()
	st := m.Status()
	if st.State != StateOK || st.Starting {
		t.Fatalf("running: state %v, starting %v; want ok, not starting", st.State, st.Starting)
	}
	if len(st.Checks) != 2 || st.Checks[0].ID != "test.one[a]" || st.Checks[1].ID != "test.two[b]" {
		t.Fatalf("checks = %+v; want test.one[a] then test.two[b]", st.Checks)
	}
	if c := st.Checks[0]; c.State != StateRunning || c.Done != 3 || c.Total != 10 || c.Started.IsZero() {
		t.Errorf("check one = %+v; want running, 3 of 10, started", c)
	}

	a.Warn(errors.New("meh"))
	if st := m.Status(); st.State != StateWarning {
		t.Errorf("after a warning, state = %v; want warning", st.State)
	}
	b.Fail(errors.New("broken"))
	if st := m.Status(); st.State != StateFailed || st.ReadOnly {
		t.Errorf("after a failure, state %v, read-only %v; want failed, writable", st.State, st.ReadOnly)
	}
	if err := m.ReadOnlyError(); err != nil {
		t.Errorf("ReadOnlyError after a non-fatal failure = %v; want nil", err)
	}
	b.OK()
	a.OK()
	if st := m.Status(); st.State != StateOK {
		t.Errorf("after all OK, state = %v; want ok", st.State)
	}
}

func TestFatal(t *testing.T) {
	m := New()
	m.EndStartup()
	c := m.Register("storage.blobpacked.large-integrity[bs]", "packed blobs match")
	c.Start()
	c.Fatal(errors.New("12 large blobs missing from index"))

	st := m.Status()
	if !st.ReadOnly || st.State != StateFailed || !st.Checks[0].Fatal {
		t.Fatalf("after Fatal: %+v; want read-only and failed", st)
	}
	err := m.ReadOnlyError()
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("ReadOnlyError = %v; want an ErrReadOnly", err)
	}
	for _, want := range []string{"packed blobs match", "storage.blobpacked.large-integrity[bs]", "12 large blobs missing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ReadOnlyError = %q; want it to mention %q", err, want)
		}
	}

	// Rerunning the check keeps the server read-only until it reports.
	c.Start()
	if m.ReadOnlyError() == nil {
		t.Errorf("after restarting a fatal check, server is writable; want read-only until it reports")
	}
	c.Fail(errors.New("still broken, but keepGoing"))
	if err := m.ReadOnlyError(); err != nil {
		t.Errorf("after a non-fatal failure, ReadOnlyError = %v; want nil", err)
	}
	c.Fatal(errors.New("broken again"))
	c.OK()
	if err := m.ReadOnlyError(); err != nil {
		t.Errorf("after OK, ReadOnlyError = %v; want nil", err)
	}
}

func TestRecentErrors(t *testing.T) {
	m := New()
	c := m.Register("test.c[x]", "c")
	for i := range maxRecentErrors + 3 {
		c.Warn(fmt.Errorf("problem %d", i))
	}
	c.OK()
	cs := m.Status().Checks[0]
	if cs.State != StateOK || cs.Message != "" {
		t.Errorf("after OK, state %v, message %q; want ok, no message", cs.State, cs.Message)
	}
	if len(cs.RecentErrors) != maxRecentErrors {
		t.Fatalf("got %d recent errors; want %d", len(cs.RecentErrors), maxRecentErrors)
	}
	if got, want := cs.RecentErrors[0].Message, fmt.Sprintf("problem %d", maxRecentErrors+2); got != want {
		t.Errorf("most recent error = %q; want %q", got, want)
	}
	if got, want := cs.RecentErrors[maxRecentErrors-1].Message, "problem 3"; got != want {
		t.Errorf("oldest kept error = %q; want %q", got, want)
	}
}

func TestID(t *testing.T) {
	for _, tt := range []struct{ name, prefix, want string }{
		{"storage.blobpacked.large-integrity", "/bs/", "storage.blobpacked.large-integrity[bs]"},
		{"index.integrity", "/index/", "index.integrity[index]"},
		{"storage.blobpacked.large-integrity", "/sto-s3/", "storage.blobpacked.large-integrity[sto-s3]"},
	} {
		if got := ID(tt.name, tt.prefix); got != tt.want {
			t.Errorf("ID(%q, %q) = %q; want %q", tt.name, tt.prefix, got, tt.want)
		}
	}
}

// TestStatusOrder checks that checks are sorted so that those of the
// same family and prefix are next to each other.
func TestStatusOrder(t *testing.T) {
	m := New()
	want := []string{
		"index.integrity[index]",
		"storage.census[all]",
		"storage.blobpacked.large-integrity[bs]",
		"storage.census[bs]",
		"storage.census[sto-s3]",
		"storage.s3.layout[sto-s3]",
		"sync.compare[sync]",
		"sync.copy[sync]",
		"sync.validate[sync]",
		"sync.compare[sync-to-s3]",
		"sync.copy[sync-to-s3]",
		"sync.sample[sync-to-s3]",
		"sync.validate[sync-to-s3]",
	}
	for _, i := range []int{12, 3, 7, 0, 9, 5, 1, 11, 2, 8, 4, 10, 6} {
		m.Register(want[i], "x")
	}
	var got []string
	for _, c := range m.Status().Checks {
		got = append(got, c.ID)
	}
	if !slices.Equal(got, want) {
		t.Errorf("order:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRegisterTwicePanics(t *testing.T) {
	m := New()
	m.Register("test.c[x]", "c")
	defer func() {
		if recover() == nil {
			t.Error("second Register with the same ID didn't panic")
		}
	}()
	m.Register("test.c[x]", "c again")
}

// TestNilMethods calls every exported method on a nil *Monitor and a nil
// *Check, so that a method added without handling a nil receiver fails.
func TestNilMethods(t *testing.T) {
	for _, recv := range []any{(*Monitor)(nil), (*Check)(nil)} {
		v := reflect.ValueOf(recv)
		for i := range v.NumMethod() {
			meth := v.Type().Method(i)
			t.Run(fmt.Sprintf("%T.%s", recv, meth.Name), func(t *testing.T) {
				defer func() {
					if e := recover(); e != nil {
						t.Errorf("panicked: %v", e)
					}
				}()
				var args []reflect.Value
				for j := range meth.Type.NumIn() - 1 { // without the receiver
					args = append(args, nilTestArg(t, meth.Type.In(j+1)))
				}
				v.Method(i).Call(args)
			})
		}
	}
}

// nilTestArg returns a usable argument of type typ for TestNilMethods.
func nilTestArg(t *testing.T, typ reflect.Type) reflect.Value {
	switch typ {
	case reflect.TypeFor[error]():
		return reflect.ValueOf(errors.New("x"))
	case reflect.TypeFor[http.ResponseWriter]():
		return reflect.ValueOf(httptest.NewRecorder())
	case reflect.TypeFor[*http.Request]():
		return reflect.ValueOf(httptest.NewRequest("GET", "/healthz", nil))
	case reflect.TypeFor[RunFunc]():
		return reflect.ValueOf(RunFunc(func(context.Context) error { return nil }))
	case reflect.TypeFor[Schedule]():
		return reflect.ValueOf(ScheduleOff)
	case reflect.TypeFor[func(context.Context) []string]():
		return reflect.ValueOf(func(context.Context) []string { return nil })
	case reflect.TypeFor[ShardFunc]():
		return reflect.ValueOf(ShardFunc(func(context.Context, string) (string, error) { return "", nil }))
	case reflect.TypeFor[any]():
		return reflect.ValueOf([]int{1})
	case reflect.TypeFor[sorted.KeyValue]():
		return reflect.ValueOf(sorted.NewMemoryKeyValue())
	}
	switch typ.Kind() {
	case reflect.String, reflect.Int, reflect.Int64:
		return reflect.Zero(typ)
	}
	t.Fatalf("no test argument for type %v; add one to nilTestArg", typ)
	return reflect.Value{}
}

func TestNil(t *testing.T) {
	var m *Monitor
	if c := m.Register("test.c[x]", "c"); c != nil {
		t.Fatalf("Register on nil Monitor = %v; want nil", c)
	}
	if err := m.ReadOnlyError(); err != nil {
		t.Errorf("ReadOnlyError on nil Monitor = %v; want nil", err)
	}
	if st := m.Status(); st.State != StateOK || st.ReadOnly || len(st.Checks) != 0 {
		t.Errorf("Status on nil Monitor = %+v; want ok, no checks", st)
	}
}

func TestStatusJSON(t *testing.T) {
	m := New()
	m.EndStartup()
	m.Register("test.c[x]", "c").Warn(errors.New("hmm"))
	js, err := json.Marshal(m.Status())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"state":"warning"`, `"message":"hmm"`, `"id":"test.c[x]"`} {
		if !strings.Contains(string(js), want) {
			t.Errorf("JSON %s doesn't contain %s", js, want)
		}
	}
	if strings.Contains(string(js), `"started"`) {
		t.Errorf("JSON %s has a zero start time; want it omitted", js)
	}
}

func TestServeHealthz(t *testing.T) {
	m := New()
	c := m.Register("test.c[x]", "c")
	get := func(method string) (int, string) {
		t.Helper()
		rec := httptest.NewRecorder()
		m.ServeHealthz(rec, httptest.NewRequest(method, "/healthz", nil))
		return rec.Code, rec.Body.String()
	}
	check := func(wantCode int, wantBody string) {
		t.Helper()
		if code, body := get("GET"); code != wantCode || body != wantBody+"\n" {
			t.Errorf("GET /healthz = %d %q; want %d %q", code, body, wantCode, wantBody+"\n")
		}
	}

	check(http.StatusServiceUnavailable, "starting")
	m.EndStartup()
	check(http.StatusOK, "ok")
	c.Warn(errors.New("x"))
	check(http.StatusOK, "warning")
	c.Fail(errors.New("x"))
	check(http.StatusServiceUnavailable, "failed")
	c.Fatal(errors.New("x"))
	check(http.StatusServiceUnavailable, "read-only")

	if code, _ := get("HEAD"); code != http.StatusServiceUnavailable {
		t.Errorf("HEAD /healthz = %d; want %d", code, http.StatusServiceUnavailable)
	}
	if code, _ := get("POST"); code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz = %d; want %d", code, http.StatusMethodNotAllowed)
	}
}
