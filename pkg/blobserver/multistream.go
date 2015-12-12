/*
Copyright 2014 The Camlistore Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package blobserver

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/context"
)

// NewMultiBlobStreamer concatenates multiple BlobStreamers into one.
func NewMultiBlobStreamer(streamers ...BlobStreamer) BlobStreamer {
	return multiStreamer{s: streamers}
}

type multiStreamer struct {
	s []BlobStreamer
}

var msTokenPrefixRx = regexp.MustCompile(`^(\d+):`)

func (ms multiStreamer) StreamBlobs(ctx context.Context, dest chan<- BlobAndToken, contToken string) error {
	defer close(dest)
	part := 0
	if contToken != "" {
		pfx := msTokenPrefixRx.FindString(contToken)
		var err error
		part, err = strconv.Atoi(strings.TrimSuffix(pfx, ":"))
		if pfx == "" || err != nil || part >= len(ms.s) {
			return errors.New("invalid continuation token")
		}
		contToken = contToken[len(pfx):]
	}
	srcs := ms.s[part:]
	for len(srcs) > 0 {
		bs := srcs[0]
		subDest := make(chan BlobAndToken, 16)
		errc := make(chan error, 1)
		go func() {
			errc <- bs.StreamBlobs(ctx, subDest, contToken)
		}()
		partStr := strconv.Itoa(part)
		for bt := range subDest {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case dest <- BlobAndToken{Blob: bt.Blob, Token: partStr + ":" + bt.Token}:
			}
		}
		if err := <-errc; err != nil {
			return err
		}
		// Advance to the next part:
		part++
		srcs = srcs[1:]
		contToken = ""
	}
	return nil
}
