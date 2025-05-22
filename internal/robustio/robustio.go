package robustio

// Implementations taken from https://github.com/golang/go/blob/master/src/cmd/go/internal/robustio to help with flakiness of file operations on windows

func Rename(oldname, newname string) error {
	return rename(oldname, newname)
}

func Remove(path string) error {
	return remove(path)
}
