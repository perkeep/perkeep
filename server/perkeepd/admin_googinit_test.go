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

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"go4.org/oauthutil"
	"golang.org/x/oauth2/google"

	"perkeep.org/pkg/blobserver/google/drive"
)

const (
	adminGoogClientID     = "fake-client-id"
	adminGoogClientSecret = "fake-client-secret"
	adminGoogAuthCode     = "fake-auth-code"
	adminGoogRefreshToken = "fake-refresh-token"
)

type adminGoogRoundTripper func(*http.Request) (*http.Response, error)

func (f adminGoogRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// Replace only the client, not google.Endpoint: the command must keep using the
// original OAuth URLs. These tests cannot run in parallel because DefaultClient
// is global. No request can escape this transport to the network.
func adminGoogHTTP(t *testing.T, f adminGoogRoundTripper) {
	t.Helper()
	client, endpoint := http.DefaultClient, google.Endpoint
	t.Cleanup(func() {
		http.DefaultClient = client
		if google.Endpoint != endpoint {
			t.Error("googinit changed google.Endpoint")
		}
	})
	http.DefaultClient = &http.Client{Transport: adminGoogRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.String() != endpoint.TokenURL {
			t.Errorf("unexpected OAuth request: %s %s", r.Method, r.URL)
			return nil, fmt.Errorf("unexpected OAuth request: %s %s", r.Method, r.URL)
		}
		return f(r)
	})}
}

func adminGoogCheckExchange(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Errorf("exchange Content-Type = %q; want application/x-www-form-urlencoded", got)
	}
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	want := url.Values{
		"client_id":     {adminGoogClientID},
		"client_secret": {adminGoogClientSecret},
		"code":          {adminGoogAuthCode},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {oauthutil.TitleBarRedirectURL},
	}
	if !reflect.DeepEqual(r.PostForm, want) {
		t.Errorf("exchange form = %v; want %v", r.PostForm, want)
	}
}

func adminGoogResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}
}

func adminGoogTokenResponse(r *http.Request) *http.Response {
	return adminGoogResponse(r, http.StatusOK,
		`{"access_token":"fake-access-token","token_type":"Bearer","refresh_token":"fake-refresh-token"}`)
}

func adminGoogInput() io.Reader {
	// All three lines arrive in a single reader. Constructing a fresh Scanner
	// for each prompt would consume buffered input and lose later lines.
	return strings.NewReader(" \t" + adminGoogClientID + " \t\r\n" +
		"\t " + adminGoogClientSecret + " \t\r\n" +
		" \t" + adminGoogAuthCode + " \t\r\n")
}

func adminGoogAuthQuery(scope string) url.Values {
	return url.Values{
		"access_type":   {"offline"},
		"client_id":     {adminGoogClientID},
		"prompt":        {"consent"}, // oauth2.ApprovalForce
		"redirect_uri":  {oauthutil.TitleBarRedirectURL},
		"response_type": {"code"},
		"scope":         {scope},
	}
}

func adminGoogOutput(scope string) string {
	return "Please provide the client id and client secret \n" +
		"(You can find these at https://console.developers.google.com/apis/credentials)\n" +
		"Client ID:Client Secret:Get auth code from:\n\n" +
		google.Endpoint.AuthURL + "?" + adminGoogAuthQuery(scope).Encode() + "\n\n" +
		"Enter auth code:\nYour Google auth object:\n\n" +
		`{"client_id":"fake-client-id","client_secret":"fake-client-secret","refresh_token":"fake-refresh-token"}` + "\n" +
		"\n\nFor server-config.json, your 'googlecloudstorage' value (update with your bucket name and path):\n\n" +
		"fake-client-id:fake-client-secret:fake-refresh-token:bucketName[/optional/dir]\n"
}

func TestRunAdminGoogInitOAuth(t *testing.T) {
	for _, tt := range []struct {
		name        string
		storageType string
		scope       string
		input       io.Reader
	}{
		{"cloud", "cloud", storage.ScopeReadWrite, adminGoogInput()},
		{"drive", "drive", drive.Scope, adminGoogInput()},
		{"cloud-no-final-newline", "cloud", storage.ScopeReadWrite,
			strings.NewReader(adminGoogClientID + "\n" + adminGoogClientSecret + "\n" + adminGoogAuthCode)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			adminGoogHTTP(t, func(r *http.Request) (*http.Response, error) {
				calls++
				adminGoogCheckExchange(t, r)
				return adminGoogTokenResponse(r), nil
			})
			var stdout, stderr bytes.Buffer
			if got := runAdmin([]string{"googinit", "--type=" + tt.storageType}, tt.input, &stdout, &stderr); got != 0 {
				t.Fatalf("status = %d; want 0; stderr: %s", got, &stderr)
			}
			if calls != 1 {
				t.Errorf("token requests = %d; want 1", calls)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q; want empty", &stderr)
			}
			// Preserve the original prompts, JSON encoding, whitespace, and
			// googlecloudstorage example even when initializing Drive.
			if got, want := stdout.String(), adminGoogOutput(tt.scope); got != want {
				t.Errorf("stdout = %q; want %q", got, want)
			}
			_, authPart, ok := strings.Cut(stdout.String(), "Get auth code from:\n\n")
			if !ok {
				t.Fatal("missing authorization URL")
			}
			authLine, _, _ := strings.Cut(authPart, "\n")
			authURL, err := url.Parse(authLine)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := authURL.Query(), adminGoogAuthQuery(tt.scope); !reflect.DeepEqual(got, want) {
				t.Errorf("authorization query = %v; want %v", got, want)
			}
			_, authJSON, ok := strings.Cut(stdout.String(), "Your Google auth object:\n\n")
			if !ok {
				t.Fatal("missing auth JSON")
			}
			var auth map[string]string
			if err := json.NewDecoder(strings.NewReader(authJSON)).Decode(&auth); err != nil {
				t.Fatal(err)
			}
			wantAuth := map[string]string{
				"client_id":     adminGoogClientID,
				"client_secret": adminGoogClientSecret,
				"refresh_token": adminGoogRefreshToken,
			}
			if !reflect.DeepEqual(auth, wantAuth) {
				t.Errorf("auth JSON = %v; want %v", auth, wantAuth)
			}
		})
	}
}

func adminGoogCheckError(t *testing.T, code int, stdout, stderr string, detail string) {
	t.Helper()
	if code != 1 {
		t.Errorf("status = %d; want 1; stderr: %s", code, stderr)
	}
	if !strings.HasPrefix(stderr, "perkeepd admin googinit: ") || !strings.Contains(stderr, detail) {
		t.Errorf("stderr = %q; want googinit error prefix and %q", stderr, detail)
	}
	if strings.Contains(stdout, "Your Google auth object:") ||
		strings.Contains(stdout, "'googlecloudstorage'") ||
		strings.Contains(stdout, adminGoogRefreshToken) {
		t.Errorf("failed initialization printed credential configuration: %q", stdout)
	}
}

func TestRunAdminGoogInitExchangeErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		err    error
		detail string
	}{
		{"rejected-code", http.StatusBadRequest, `{"error":"invalid_grant"}`, nil, "invalid_grant"},
		{"invalid-json", http.StatusOK, `{`, nil, "cannot parse json"},
		{"transport", 0, "", errors.New("fake exchange failure"), "fake exchange failure"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			adminGoogHTTP(t, func(r *http.Request) (*http.Response, error) {
				calls++
				adminGoogCheckExchange(t, r)
				if tt.err != nil {
					return nil, tt.err
				}
				return adminGoogResponse(r, tt.status, tt.body), nil
			})
			var stdout, stderr bytes.Buffer
			code := runAdmin([]string{"googinit", "--type=cloud"}, adminGoogInput(), &stdout, &stderr)
			adminGoogCheckError(t, code, stdout.String(), stderr.String(), tt.detail)
			if calls != 1 {
				t.Errorf("token requests = %d; want 1", calls)
			}
			if !strings.Contains(stderr.String(), "could not acquire token: could not exchange auth code for a token:") {
				t.Errorf("stderr = %q; want original exchange error context", &stderr)
			}
		})
	}
}

type adminGoogReadFunc func([]byte) (int, error)

func (f adminGoogReadFunc) Read(p []byte) (int, error) { return f(p) }

func TestRunAdminGoogInitInputErrors(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input string
	}{
		{"client-id", ""},
		{"client-secret", adminGoogClientID + "\n"},
		{"auth-code", adminGoogClientID + "\n" + adminGoogClientSecret + "\n"},
	} {
		for _, readError := range []bool{false, true} {
			name := tt.name + "/EOF"
			if readError {
				name = tt.name + "/read-error"
			}
			t.Run(name, func(t *testing.T) {
				adminGoogHTTP(t, func(r *http.Request) (*http.Response, error) {
					t.Error("input failure attempted token exchange")
					return nil, errors.New("unexpected token exchange")
				})
				var input io.Reader = strings.NewReader(tt.input)
				detail := "EOF"
				if readError {
					detail = "fake input failure"
					input = io.MultiReader(input, adminGoogReadFunc(func([]byte) (int, error) {
						return 0, errors.New(detail)
					}))
				}
				var stdout, stderr bytes.Buffer
				code := runAdminGoogInit([]string{"--type=cloud"}, input, &stdout, &stderr)
				adminGoogCheckError(t, code, stdout.String(), stderr.String(), detail)
			})
		}
	}
}

func TestRunAdminGoogInitPartialInputErrors(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input string
	}{
		{"client-id", "partial-id"},
		{"client-secret", adminGoogClientID + "\npartial-secret"},
		{"auth-code", adminGoogClientID + "\n" + adminGoogClientSecret + "\npartial-code"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			adminGoogHTTP(t, func(r *http.Request) (*http.Response, error) {
				t.Error("partial input with a read error attempted token exchange")
				return nil, errors.New("unexpected token exchange")
			})
			// Scanner can return a final partial token with Scan() == true
			// while Err() holds a non-EOF error. Do not accept that token.
			input := io.MultiReader(strings.NewReader(tt.input), adminGoogReadFunc(func([]byte) (int, error) {
				return 0, errors.New("fake partial input failure")
			}))
			var stdout, stderr bytes.Buffer
			code := runAdminGoogInit([]string{"--type=cloud"}, input, &stdout, &stderr)
			adminGoogCheckError(t, code, stdout.String(), stderr.String(), "fake partial input failure")
		})
	}
}

type adminGoogFailWriter struct {
	remaining int
}

func (w *adminGoogFailWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		n := w.remaining
		w.remaining = 0
		return n, errors.New("fake output failure")
	}
	w.remaining -= len(p)
	return len(p), nil
}

func TestRunAdminGoogInitOutputErrors(t *testing.T) {
	output := adminGoogOutput(storage.ScopeReadWrite)
	for _, tt := range []struct {
		name     string
		limit    int
		requests int
	}{
		{"first-prompt", 0, 0},
		{"authorization-url", strings.Index(output, "Get auth code from:") + 10, 0},
		{"auth-json", strings.Index(output, `{"client_id":`) + 10, 1},
		{"config-heading", strings.Index(output, "\n\nFor server-config.json") + 10, 1},
		{"config-value", len(output) - 5, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			adminGoogHTTP(t, func(r *http.Request) (*http.Response, error) {
				calls++
				adminGoogCheckExchange(t, r)
				return adminGoogTokenResponse(r), nil
			})
			var stderr bytes.Buffer
			stdout := &adminGoogFailWriter{remaining: tt.limit}
			code := runAdminGoogInit([]string{"--type=cloud"}, adminGoogInput(), stdout, &stderr)
			if code != 1 || !strings.HasPrefix(stderr.String(), "perkeepd admin googinit: ") ||
				!strings.Contains(stderr.String(), "fake output failure") {
				t.Errorf("status = %d, stderr = %q; want status 1 and output error", code, &stderr)
			}
			if calls != tt.requests {
				t.Errorf("token requests = %d; want %d", calls, tt.requests)
			}
		})
	}
}

func TestRunAdminGoogInitArgumentsDoNotReadInput(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		code int
	}{
		{"missing-type", nil, 2},
		{"bad-type", []string{"--type=invalid"}, 2},
		{"missing-type-value", []string{"--type"}, 2},
		{"unknown-flag", []string{"--unknown"}, 2},
		{"extra-argument", []string{"--type=cloud", "extra"}, 2},
		{"help", []string{"--help"}, 0},
		{"help-with-type", []string{"--type=cloud", "--help"}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			adminGoogHTTP(t, func(r *http.Request) (*http.Response, error) {
				t.Error("help or invalid arguments attempted token exchange")
				return nil, errors.New("unexpected token exchange")
			})
			input := adminGoogReadFunc(func([]byte) (int, error) {
				t.Error("help or invalid arguments read stdin")
				return 0, errors.New("unexpected input read")
			})
			var stdout, stderr bytes.Buffer
			if got := runAdminGoogInit(tt.args, input, &stdout, &stderr); got != tt.code {
				t.Errorf("status = %d; want %d; stderr: %s", got, tt.code, &stderr)
			}
			if stdout.Len() != 0 || stderr.Len() == 0 {
				t.Errorf("stdout = %q, stderr = %q; want only help or argument error on stderr", &stdout, &stderr)
			}
		})
	}
}
