package fuse_test

import (
	"testing"

	"camlistore.org/third_party/bazil.org/fuse"
	"camlistore.org/third_party/bazil.org/fuse/fs/fstestutil"
)

func TestMountOptionCommaError(t *testing.T) {
	t.Parallel()
	// this test is not tied to FSName, but needs just some option
	// with string content
	var name = "FuseTest,Marker"
	mnt, err := fstestutil.MountedT(t, fstestutil.SimpleFS{fstestutil.Dir{}},
		fuse.FSName(name),
	)
	switch {
	case err == nil:
		mnt.Close()
		t.Fatal("expected an error about commas")
	case err.Error() == `mount options cannot contain commas on OS X: "fsname"="FuseTest,Marker"`:
		// all good
	default:
		t.Fatalf("expected an error about commas, got: %v", err)
	}
}
