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

package livejournal_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/internal/livejournal"
	"perkeep.org/internal/livejournal/ljtest"
)

func TestEntries(t *testing.T) {
	s := ljtest.New(t)
	c := s.Client(ljtest.Password)
	ctx := context.Background()
	if err := c.Login(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := c.SyncItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := s.Entries()
	if len(items) != len(want) {
		t.Fatalf("got %d sync items; want %d", len(items), len(want))
	}
	es, err := c.Entries(ctx, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != len(want) {
		t.Fatalf("got %d entries; want %d", len(es), len(want))
	}
	for i, e := range es {
		w := want[i].Entry
		w.SyncTime, _ = time.Parse("2006-01-02 15:04:05", want[i].SyncTime)
		if !reflect.DeepEqual(*e, w) {
			t.Errorf("entry %d:\n got %+v\nwant %+v", i+1, *e, w)
		}
	}
	if got, want := es[1].Tags(), []string{"food", "portland"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tags = %q; want %q", got, want)
	}
	if es[1].DItemID() != 2*256+12 {
		t.Errorf("DItemID = %d", es[1].DItemID())
	}
	if s.SawPassword() {
		t.Error("the password was sent")
	}
	if n := s.Logins(); n != 1 {
		t.Errorf("logged in %d times; want once, then use the session", n)
	}
}

func TestCookieAuthRejected(t *testing.T) {
	s := ljtest.New(t)
	s.RejectCookieAuth()
	c := s.Client(ljtest.Password)
	ctx := context.Background()
	items, err := c.SyncItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	es, err := c.Entries(ctx, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != len(s.Entries()) {
		t.Fatalf("got %d entries; want %d", len(es), len(s.Entries()))
	}
	if n := s.Logins(); n < 3 {
		t.Errorf("logged in %d times; want each request to log in", n)
	}
}

func TestBadPassword(t *testing.T) {
	s := ljtest.New(t)
	err := s.Client("wrong").Login(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Invalid password") {
		t.Fatalf("Login = %v; want Invalid password", err)
	}
}

func TestComments(t *testing.T) {
	s := ljtest.New(t)
	c := s.Client(ljtest.Password)
	var calls int
	cs, err := c.Comments(context.Background(), func(done, max int) { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	want := []*livejournal.Comment{
		{ID: 1, JItemID: 1, PosterID: 7, Poster: "friend", State: "A", Subject: "Hi", Body: "café “quoted”", Date: "2001-01-02T00:00:00Z"},
		{ID: 2, JItemID: 1, ParentID: 1, PosterID: 2, Poster: "brad", State: "A", Body: "reply", Date: "2001-01-03T00:00:00Z"},
		{ID: 4, JItemID: 2, State: "D"},
	}
	if !reflect.DeepEqual(cs, want) {
		for _, c := range cs {
			t.Logf("got %+v", *c)
		}
		t.Fatal("wrong comments")
	}
	if calls == 0 {
		t.Error("progress not called")
	}
}

func TestFetch(t *testing.T) {
	s := ljtest.New(t)
	j, err := s.Client(ljtest.Password).Fetch(context.Background(), t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if j.User != ljtest.User || len(j.Entries) != len(s.Entries()) || len(j.Comments) != 3 {
		t.Errorf("Fetch = user %q, %d entries, %d comments", j.User, len(j.Entries), len(j.Comments))
	}
	if n := s.Logins(); n != 1 {
		t.Errorf("logged in %d times; want once", n)
	}
}

func TestRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := ljtest.New(t)
		s.FailNext(3)
		c := s.Client(ljtest.Password)
		c.Retries = 3
		start := time.Now()
		if err := c.Login(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got, want := time.Since(start), 7*time.Second; got != want {
			t.Errorf("took %v; want %v of backoff", got, want)
		}

		// A new client, without a session, gives up after its retries.
		s.FailNext(2)
		c = s.Client(ljtest.Password)
		c.Retries = 1
		if err := c.Login(context.Background()); err == nil || !strings.Contains(err.Error(), "503") {
			t.Errorf("Login = %v; want a 503 error", err)
		}
	})
}
