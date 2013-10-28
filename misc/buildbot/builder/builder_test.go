package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSendReport(t *testing.T) {
	wants := []struct {
		host       string
		authHeader bool
		path       string
	}{
		// TODO(wathiede): add https tests if needed.
		{
			host:       "http://HOST",
			authHeader: false,
			path:       reportPrefix,
		},
		{
			host:       "http://user:pass@HOST",
			authHeader: true,
			path:       reportPrefix,
		},
		{
			host:       "http://user:pass@HOST/",
			authHeader: true,
			path:       "/",
		},
		{
			host:       "http://user:pass@HOST/other",
			authHeader: true,
			path:       "/other",
		},
	}

	reqNum := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { reqNum++ }()
		if reqNum > len(wants) {
			t.Fatal("Only expected", len(wants), "requests, got", reqNum)
		}
		want := wants[reqNum]

		gotAuthHeader := r.Header.Get("Authorization") != ""
		if want.authHeader != gotAuthHeader {
			if gotAuthHeader {
				t.Error("Got unexpected Authorization header")
			} else {
				t.Error("Authorization header missing")
			}
		}

		if r.URL.Path != want.path {
			t.Error("Got path", r.URL.Path, "want", want.path)
		}
	}))
	defer ts.Close()

	testU, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	var hosts []string
	for _, want := range wants {
		u, err := url.Parse(want.host)
		if err != nil {
			t.Fatal(err)
		}
		u.Host = testU.Host
		hosts = append(hosts, u.String())
	}
	// override --masterhosts.
	*masterHosts = strings.Join(hosts, ",")
	currentBiSuite = &biTestSuite{}

	sendReport()

	if reqNum != len(wants) {
		t.Error("Expected", len(wants), "requests, only got", reqNum)
	}
}

func TestMasterHostsReader(t *testing.T) {
	datum := []struct {
		body string
		good bool
		num  int
	}{
		{
			body: "http://host1",
			good: true,
			num:  1,
		},
		{
			body: "http://host1\n",
			good: true,
			num:  1,
		},
		{
			body: "# Hello\nhttp://host1\n",
			good: false,
			num:  0,
		},
		{
			body: "http://host1\nhttp://host2\n",
			good: true,
			num:  2,
		},
	}

	for i, d := range datum {
		hosts, err := masterHostsReader(strings.NewReader(d.body))
		if d.good && err != nil {
			t.Error(i, "Unexpected parse failure:", err)
		}
		if !d.good && err == nil {
			t.Error(i, "Expected parse failure, but succeeded")
		}

		if len(hosts) != d.num {
			t.Error(i, "Expected", d.num, "hosts, got", len(hosts), hosts)
		}
	}
}
