package main

import (
	"syscall"
)

func init() {
	cleanSysStat = func(si any) any {
		st, ok := si.(*syscall.Stat_t)
		if !ok {
			return si
		}
		st.Atimespec = syscall.Timespec{}
		return st
	}
}
