//+build darwin

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
		st.Atimespec = syscall.Timespec{}
		return st
	}
}
