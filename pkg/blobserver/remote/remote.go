/*
Copyright 2011 The Perkeep Authors

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

/*
Package remote registers the "remote" blobserver storage type, storing
and fetching blobs from a remote Perkeep server over HTTPS.

Example low-level config:

     "/peer/": {
         "handler": "storage-remote",
         "handlerArgs": {
             "url": "https://some-other-server/base",
             "auth": "userpass:user:pass",
             "skipStartupCheck": false
          }
     },

The "handlerArgs" may also contain an optional "trustedCert" option to
trust a self-signed TLS certificate. The value is the 20 byte hex prefix
of the SHA-256 of the cert, as printed by the perkeepd server
on start-up.
*/
package remote // import "perkeep.org/pkg/blobserver/remote"

import (
	"context"
	"io"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/client"
)

// remoteStorage is a blobserver.Storage proxy for a remote camlistore
// blobserver.
type remoteStorage struct {
	client *client.Client
}

var (
	_ blobserver.Storage = (*remoteStorage)(nil)
	_ io.Closer          = (*remoteStorage)(nil)
)

// NewFromClient returns a new Storage implementation using the
// provided Perkeep client.
func NewFromClient(c *client.Client) blobserver.Storage {
	return &remoteStorage{client: c}
}

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err error) {
	url := config.RequiredString("url")
	auth := config.RequiredString("auth")
	skipStartupCheck := config.OptionalBool("skipStartupCheck", false)
	trustedCert := config.OptionalString("trustedCert", "")
	if err := config.Validate(); err != nil {
		return nil, err
	}

	client, err := client.New(
		client.OptionServer(url),
		client.OptionTrustedCert(trustedCert),
	)
	if err != nil {
		return nil, err
	}
	if err = client.SetupAuthFromString(auth); err != nil {
		return nil, err
	}
	sto := &remoteStorage{
		client: client,
	}
	if !skipStartupCheck {
		// Do a quick dummy operation to check that our credentials are
		// correct.
		// TODO(bradfitz,mpl): skip this operation smartly if it turns out this is annoying/slow for whatever reason.
		c := make(chan blob.SizedRef, 1)
		err = sto.EnumerateBlobs(context.TODO(), c, "", 1)
		if err != nil {
			return nil, err
		}
	}
	return sto, nil
}

func (sto *remoteStorage) Close() error {
	return sto.client.Close()
}

func (sto *remoteStorage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	return sto.client.RemoveBlobs(ctx, blobs)
}

func (sto *remoteStorage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	// TODO: cache the stat response's uploadUrl to save a future
	// stat later?  otherwise clients will just Stat + Upload, but
	// Upload will also Stat.  should be smart and make sure we
	// avoid ReceiveBlob's Stat whenever it would be redundant.
	return sto.client.StatBlobs(ctx, blobs, fn)
}

func (sto *remoteStorage) ReceiveBlob(ctx context.Context, blob blob.Ref, source io.Reader) (outsb blob.SizedRef, outerr error) {
	h := &client.UploadHandle{
		BlobRef:  blob,
		Size:     0, // size isn't known; 0 is fine, but TODO: ask source if it knows its size
		Contents: source,
	}
	pr, err := sto.client.Upload(ctx, h)
	if err != nil {
		outerr = err
		return
	}
	return pr.SizedBlobRef(), nil
}

func (sto *remoteStorage) Fetch(ctx context.Context, b blob.Ref) (file io.ReadCloser, size uint32, err error) {
	return sto.client.Fetch(ctx, b)
}

func (sto *remoteStorage) MaxEnumerate() int { return 1000 }

func (sto *remoteStorage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	return sto.client.EnumerateBlobsOpts(ctx, dest, client.EnumerateOpts{
		After: after,
		Limit: limit,
	})
}

func init() {
	blobserver.RegisterStorageConstructor("remote", blobserver.StorageConstructor(newFromConfig))
}
