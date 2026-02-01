/*
Copyright 2014 The Perkeep Authors.

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

// Package app provides helpers for server applications interacting
// with Perkeep.
// See also https://perkeep.org/doc/app-environment for the related
// variables.
package app // import "perkeep.org/pkg/app"

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema"
)

// Auth returns the auth mode for the app to access Perkeep, as defined by
// environment variables automatically supplied by the Perkeep server host.
func Auth() (auth.AuthMode, error) {
	return basicAuth()
}

func basicAuth() (auth.AuthMode, error) {
	authString := os.Getenv("CAMLI_AUTH")
	if authString == "" {
		return nil, errors.New("CAMLI_AUTH var not set")
	}
	userpass := strings.Split(authString, ":")
	if len(userpass) != 2 {
		return nil, fmt.Errorf("invalid auth string syntax. got %q, want \"username:password\"", authString)
	}
	return auth.NewBasicAuth(userpass[0], userpass[1]), nil
}

// APIHost returns the Perkeep server's API host URL as defined by
// environment variables automatically supplied by the Perkeep server host.
func APIHost() (string, error) {
	server := os.Getenv("CAMLI_API_HOST")
	if server == "" {
		return "", errors.New("CAMLI_API_HOST var not set")
	}
	return server, nil
}

// Client returns a Perkeep client as defined by environment variables
// automatically supplied by the Perkeep server host.
func Client() (*client.Client, error) {
	server, err := APIHost()
	if err != nil {
		return nil, err
	}
	am, err := basicAuth()
	if err != nil {
		return nil, err
	}
	return client.New(
		client.OptionNoExternalConfig(),
		client.OptionServer(server),
		client.OptionAuthMode(am),
	)
}

// ListenAddress returns the host:[port] network address, derived from the environment,
// that the application should listen on.
func ListenAddress() (string, error) {
	listenAddr := os.Getenv("CAMLI_APP_LISTEN")
	if listenAddr == "" {
		return "", errors.New("CAMLI_APP_LISTEN is undefined")
	}
	return listenAddr, nil
}

// PathPrefix returns the app's prefix on the app handler if the request was proxied
// through Perkeep, or "/" if the request went directly to the app.
func PathPrefix(r *http.Request) string {
	if prefix := httputil.PathBase(r); prefix != "" {
		return prefix
	}
	return "/"
}

// RoutePrefix returns the app's route prefix as configured by perkeepd
// via the CAMLI_APP_ROUTE_PREFIX environment variable, or "/" if not set.
func RoutePrefix() string {
	if prefix := os.Getenv("CAMLI_APP_ROUTE_PREFIX"); prefix != "" {
		return prefix
	}
	return "/"
}

// Signer provides server-side signing for apps that don't have direct access
// to the secret key ring.
type Signer struct {
	cl     *client.Client
	server string
}

// NewSigner returns a Signer that uses the given client and server URL
// for server-side signing via the /sighelper/ endpoint.
func NewSigner(cl *client.Client, server string) *Signer {
	return &Signer{cl: cl, server: server}
}

func (s *Signer) signAndUpload(ctx context.Context, bb *schema.Builder) (blob.Ref, error) {
	pubKeyRef, err := s.cl.ServerPublicKeyBlobRef()
	if err != nil {
		return blob.Ref{}, fmt.Errorf("failed to get server public key: %w", err)
	}
	bb.SetSigner(pubKeyRef)

	unsigned, err := bb.JSON()
	if err != nil {
		return blob.Ref{}, fmt.Errorf("failed to build unsigned JSON: %w", err)
	}

	form := url.Values{}
	form.Set("json", unsigned)

	signed, err := s.cl.Sign(ctx, s.server, strings.NewReader(form.Encode()))
	if err != nil {
		return blob.Ref{}, fmt.Errorf("signing failed: %w", err)
	}

	signedStr := string(signed)
	br := blob.RefFromString(signedStr)

	uh := client.NewUploadHandleFromString(signedStr)
	if _, err = s.cl.Upload(ctx, uh); err != nil {
		return blob.Ref{}, fmt.Errorf("upload failed: %w", err)
	}

	return br, nil
}

// UploadNewPermanode creates and uploads a new permanode.
func (s *Signer) UploadNewPermanode(ctx context.Context) (blob.Ref, error) {
	return s.signAndUpload(ctx, schema.NewUnsignedPermanode())
}

// SetAttribute creates and uploads a set-attribute claim.
func (s *Signer) SetAttribute(ctx context.Context, permanode blob.Ref, attr, value string) error {
	_, err := s.signAndUpload(ctx, schema.NewSetAttributeClaim(permanode, attr, value))
	return err
}

// DelAttribute creates and uploads a del-attribute claim.
func (s *Signer) DelAttribute(ctx context.Context, permanode blob.Ref, attr, value string) error {
	_, err := s.signAndUpload(ctx, schema.NewDelAttributeClaim(permanode, attr, value))
	return err
}
