package main

import "syscall"

func init() {
	cleanSysStat = func(si interface{}) interface{} {
		st, ok := si.(*syscall.Win32FileAttributeData)
		if !ok {
			return si
		}

		st.LastAccessTime = syscall.Filetime{}
		return st
	}
}
