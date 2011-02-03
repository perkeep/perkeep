package main

import (
	"camli/blobref"
	"os"
)

type BlobServer interface {
	blobref.Fetcher

	// Remove 0 or more blobs from provided partition, which should be empty
	// for the default partition.  Removal of non-existent items isn't an error.
	// Returns failure if any items existed but failed to be deleted.
	Remove(partition string, blobs []*blobref.BlobRef) os.Error
}
