package blobref

import (
	"fmt"
	"os"
)

type Fetcher interface {
	Fetch(*BlobRef) (file ReadSeekCloser, size int64, err os.Error)
}

func NewSimpleDirectoryFetcher(dir string) Fetcher {
	return &dirFetcher{dir, "camli"}
}

type dirFetcher struct {
	directory, extension string
}

func (df *dirFetcher) Fetch(b *BlobRef) (file ReadSeekCloser, size int64, err os.Error) {
	fileName := fmt.Sprintf("%s/%s.%s", df.directory, b.String(), df.extension)
	var stat *os.FileInfo
	stat, err = os.Stat(fileName)
	if err != nil {
		return
	}
	file, err = os.Open(fileName, os.O_RDONLY, 0)
	if err != nil {
		return
	}
	size = stat.Size
	return
}


