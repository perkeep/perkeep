package main

import (
	"camli/blobref"
	"flag"
	"fmt"
	"io"
	"os"
)

var flagPubKeyDir *string = flag.String("pubkey-dir", "test/pubkey-blobs",
	"Temporary development hack; directory to dig-xxxx.camli public keys.")

type fromLocalDiskBlobFetcher struct{}
var blobFetcher = &fromLocalDiskBlobFetcher{}
func (_ *fromLocalDiskBlobFetcher) Fetch(b blobref.BlobRef) (io.ReadCloser, os.Error) {
	publicKeyFile := fmt.Sprintf("%s/%s.camli", *flagPubKeyDir, b.String())
	f, err := os.Open(publicKeyFile, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	return f, nil
}
