/*
Copyright 2014 The Camlistore Authors

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

// Package test provides common functionality for importer tests.
package test // import "camlistore.org/pkg/importer/test"

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/test"
)

// FindChildRefs returns the children of an importer.Object
func FindChildRefs(parent *importer.Object) ([]blob.Ref, error) {
	childRefs := []blob.Ref{}
	var err error
	parent.ForeachAttr(func(key, value string) {
		if strings.HasPrefix(key, "camliPath:") {
			if br, ok := blob.Parse(value); ok {
				childRefs = append(childRefs, br)
				return
			}
			if err == nil {
				err = fmt.Errorf("invalid blobRef for %s attribute of %v: %q", key, parent, value)
			}
		}
	})
	return childRefs, err
}

func setupClient(w *test.World) (*client.Client, error) {
	// Do the silly env vars dance to avoid the "non-hermetic use of host config panic".
	if err := os.Setenv("CAMLI_KEYID", w.ClientIdentity()); err != nil {
		return nil, err
	}
	if err := os.Setenv("CAMLI_SECRET_RING", w.SecretRingFile()); err != nil {
		return nil, err
	}
	osutil.AddSecretRingFlag()
	cl := client.New(w.ServerBaseURL())
	return cl, nil
}

// GetRequiredChildPathObj returns the child object at path or an error if none exists.
func GetRequiredChildPathObj(parent *importer.Object, path string) (*importer.Object, error) {
	return parent.ChildPathObjectOrFunc(path, func() (*importer.Object, error) {
		return nil, fmt.Errorf("Unable to locate child path %s of node %v", path, parent.PermanodeRef())
	})
}

// ImporterTest sets up the environment for an importer integration test.
func ImporterTest(t *testing.T, importerName string, transport http.RoundTripper, fn func(*importer.RunContext)) {
	const importerPrefix = "/importer/"

	w := test.GetWorld(t)
	defer w.Stop()
	baseURL := w.ServerBaseURL()

	// TODO(mpl): add a utility in integration package to provide a client that
	// just works with World.
	cl, err := setupClient(w)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := cl.Signer()
	if err != nil {
		t.Fatal(err)
	}
	clientId := map[string]string{
		importerName: "fakeStaticClientId",
	}
	clientSecret := map[string]string{
		importerName: "fakeStaticClientSecret",
	}

	httpClient := &http.Client{
		Transport: transport,
	}

	hc := importer.HostConfig{
		BaseURL:      baseURL,
		Prefix:       importerPrefix,
		Target:       cl,
		BlobSource:   cl,
		Signer:       signer,
		Search:       cl,
		ClientId:     clientId,
		ClientSecret: clientSecret,
		HTTPClient:   httpClient,
	}

	host, err := importer.NewHost(hc)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := importer.CreateAccount(host, importerName)
	if err != nil {
		t.Fatal(err)
	}
	fn(rc)

}
