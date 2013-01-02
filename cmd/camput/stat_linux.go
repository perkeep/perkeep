//+build linux

// TODO: move this to somewhere generic in osutil; use it for all
// posix-y operation systems?  Or rather, don't clean bad fields, but
// provide a portable way to extract all good fields.

package main

import (
	"syscall"
)

func init() {
	cleanSysStat = func(si interface{}) interface{} {
		st, ok := si.(*syscall.Stat_t)
		if !ok {
			return si
		}
		st.Atim = syscall.Timespec{}
		return st
	}
}
