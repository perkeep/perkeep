package fuse

// Compilation test for DummyFuse and DummyPathFuse

import (
	"testing"
)

func TestDummy(t *testing.T) {
	fs := new(DefaultRawFuseFileSystem)
	NewMountState(fs)

	pathFs := new(DefaultPathFilesystem)

	NewPathFileSystemConnector(pathFs)
}

func TestDummyFile(t *testing.T) {
	d := new(DefaultRawFuseFile)
	var filePtr RawFuseFile = d

	d2 := new(DefaultRawFuseDir)
	var fileDir RawFuseDir = d2
	_ = fileDir
	_ = filePtr
}
