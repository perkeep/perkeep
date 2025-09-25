//go:build !windows && !darwin

package robustio

import "os"

func rename(oldname, newname string) error {
	return os.Rename(oldname, newname)
}

func remove(path string) error {
	return os.Remove(path)
}
