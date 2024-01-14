/*
Copyright 2014 The Perkeep Authors

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

package azure

import (
	"context"
	"flag"
	"log"
	"strings"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/storagetest"
)

var (
	account   = flag.String("azure-account", "", "AWS access Key ID")
	secret    = flag.String("azure-key", "", "AWS access secret")
	container = flag.String("azure-container", "", "Container name to use for testing. If empty, testing is skipped. If non-empty, it must begin with 'camlistore-' and end in '-test' and have zero items in it.")
)

func TestAzureStorage(t *testing.T) {
	ctx := context.Background()
	if *container == "" || *account == "" || *secret == "" {
		t.Skip("Skipping test because at least one of -azure-key, -azure-secret, or -azure-container flags has not been provided.")
	}
	if !strings.HasPrefix(*container, "camlistore-") || !strings.HasSuffix(*container, "-test") {
		t.Fatalf("bogus container name %q; must begin with 'camlistore-' and end in '-test'", *container)
	}
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		sto, err := newFromConfig(nil, jsonconfig.Obj{
			"azure_account":    *account,
			"azure_access_key": *secret,
			"container":        *container,
		})
		if err != nil {
			t.Fatalf("newFromConfig error: %v", err)
		}
		if !testing.Short() {
			log.Printf("Warning: this test does many serial operations. Without the go test -short flag, this test will be very slow.")
		}
		clearContainer := func() {
			var all []blob.Ref
			blobserver.EnumerateAll(ctx, sto, func(sb blob.SizedRef) error {
				t.Logf("Deleting: %v", sb.Ref)
				all = append(all, sb.Ref)
				return nil
			})
			if err := sto.RemoveBlobs(ctx, all); err != nil {
				t.Fatalf("Error removing blobs during cleanup: %v", err)
			}
		}
		clearContainer()
		t.Cleanup(clearContainer)
		return sto
	})
}
