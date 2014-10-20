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

package pinboard

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/schema/nodeattr"
	"camlistore.org/pkg/test"
)

func verifyUsername(t *testing.T, apiToken string, expected string) {
	extracted := extractUsername(apiToken)
	if extracted != expected {
		t.Errorf("Testing %q: user name is %q when we want %q", apiToken, extracted, expected)
	}
}

func TestExtractUsername(t *testing.T) {
	verifyUsername(t, "gina:foo", "gina")
	verifyUsername(t, "", "")
}

func findChildRefs(parent *importer.Object) ([]blob.Ref, error) {
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

func getRequiredChildPathObj(parent *importer.Object, path string) (*importer.Object, error) {
	return parent.ChildPathObjectOrFunc(path, func() (*importer.Object, error) {
		return nil, fmt.Errorf("Unable to locate child path %s of node %v", path, parent.PermanodeRef())
	})
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
	// This permanode is not needed in itself, but that takes care of uploading
	// behind the scenes the public key to the blob server. A bit gross, but
	// it's just for a test anyway.
	if _, err := cl.UploadNewPermanode(); err != nil {
		return nil, err
	}
	return cl, nil
}

// Verify that a batch import of 3 posts works
func TestIntegrationRun(t *testing.T) {
	const importerPrefix = "/importer/"
	const authToken = "gina:foo"
	const attrKey = "key"
	const attrValue = "value"

	w := test.GetWorld(t)
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
		"pinboard": "fakeStaticClientId",
	}
	clientSecret := map[string]string{
		"pinboard": "fakeStaticClientSecret",
	}

	responder := httputil.FileResponder("testdata/batchresponse.json")
	transport, err := httputil.NewRegexpFakeTransport([]*httputil.Matcher{
		&httputil.Matcher{`^https\://api\.pinboard\.in/v1/posts/all\?auth_token=gina:foo&format=json&results=10000&todt=\d\d\d\d.*`, responder},
	})
	if err != nil {
		t.Fatal(err)
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
	rc, err := importer.CreateAccount(host, "pinboard")
	if err != nil {
		t.Fatal(err)
	}
	err = rc.AccountNode().SetAttrs(attrAuthToken, authToken)
	if err != nil {
		t.Fatal(err)
	}

	testee := imp{}
	if err := testee.Run(rc); err != nil {
		t.Fatal(err)
	}

	postsNode, err := getRequiredChildPathObj(rc.RootNode(), "posts")
	if err != nil {
		t.Fatal(err)
	}

	childRefs, err := findChildRefs(postsNode)
	if err != nil {
		t.Fatal(err)
	}

	expectedPosts := map[string]string{
		`https://wiki.archlinux.org/index.php/xorg#Display_size_and_DPI`:                   "Xorg - ArchWiki",
		`http://www.harihareswara.net/sumana/2014/08/17/0`:                                 "One Way Confidence Will Look",
		`http://www.wikiart.org/en/marcus-larson/fishing-near-the-fjord-by-moonlight-1862`: "Fishing Near The Fjord By Moonlight - Marcus Larson - WikiArt.org",
	}

	if len(childRefs) != len(expectedPosts) {
		t.Fatalf("After import, found %d child refs, want %d: %v", len(childRefs), len(expectedPosts), childRefs)
	}

	for _, ref := range childRefs {
		childNode, err := host.ObjectFromRef(ref)
		if err != nil {
			t.Fatal(err)
		}
		foundURL := childNode.Attr(nodeattr.URL)
		expectedTitle, ok := expectedPosts[foundURL]
		if !ok {
			t.Fatalf("Found unexpected child node %v with url %q", childNode, foundURL)
		}
		foundTitle := childNode.Attr(nodeattr.Title)
		if foundTitle != expectedTitle {
			t.Fatalf("Found unexpected child node %v with title %q when we want %q", childNode, foundTitle, expectedTitle)
		}
		delete(expectedPosts, foundURL)
	}
	if len(expectedPosts) != 0 {
		t.Fatalf("The following entries were expected but not found: %#v", expectedPosts)
	}
}
