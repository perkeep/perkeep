package fstestutil

import (
	"errors"

	"camlistore.org/third_party/github.com/artyom/mtab"
)

// Inventory of mount information parsing packages out there:
//
// https://github.com/cratonica/gomounts
//
// Does it "right" by using getmntent(3), but that needs CGo which
// prevents cross-compiling easily.
//
// https://github.com/antage/mntent
//
// Does not handle escaping at all.
//
// https://github.com/deniswernert/go-fstab
//
// Does not handle escaping at all. Has trivial bugs like
// https://github.com/deniswernert/go-fstab/issues/1
//
// http://godoc.org/github.com/docker/docker/pkg/mount
//
// Does not handle escaping at all. Part of an overly large source
// tree.
//
// https://github.com/artyom/mtab
//
// Does not split options. Otherwise seems to work.

func findMount(mnt string) (*mtab.Entry, error) {
	mounts, err := mtab.Entries("/proc/mounts")
	if err != nil {
		return nil, err
	}
	for _, m := range mounts {
		if m.Dir == mnt {
			return &m, nil
		}
	}
	return nil, errors.New("mount not found")
}

func getMountInfo(mnt string) (*MountInfo, error) {
	m, err := findMount(mnt)
	if err != nil {
		return nil, err
	}
	i := &MountInfo{
		FSName: m.Fsname,
		Type:   m.Type,
	}
	return i, nil
}
