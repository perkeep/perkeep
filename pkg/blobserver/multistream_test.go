/*
Copyright 2014 The Perkeep Authors

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

package blobserver_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/test"
)

type staticStreamer []*blob.Blob

func (s staticStreamer) StreamBlobs(ctx context.Context, dest chan<- blobserver.BlobAndToken, contToken string) error {
	defer close(dest)
	var pos int
	if contToken != "" {
		var err error
		pos, err = strconv.Atoi(contToken)
		if err != nil || pos < 0 || pos >= len(s) {
			return errors.New("invalid token")
		}
		s = s[pos:]
	}
	for len(s) > 0 {
		select {
		case dest <- blobserver.BlobAndToken{Blob: s[0], Token: strconv.Itoa(pos)}:
			pos++
			s = s[1:]
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func TestStaticStreamer(t *testing.T) {
	var blobs []*blob.Blob
	var want []blob.SizedRef
	for i := range 5 {
		tb := &test.Blob{Contents: strconv.Itoa(i)}
		b := tb.Blob()
		blobs = append(blobs, b)
		want = append(want, b.SizedRef())
	}
	bs := staticStreamer(blobs)
	storagetest.TestStreamer(t, bs, storagetest.WantSizedRefs(want))
}

func TestMultiStreamer(t *testing.T) {
	var streamers []blobserver.BlobStreamer
	var want []blob.SizedRef
	n := 0

	for range 3 {
		var blobs []*blob.Blob
		for range 3 {
			n++
			tb := &test.Blob{Contents: strconv.Itoa(n)}
			b := tb.Blob()
			want = append(want, b.SizedRef()) // overall
			blobs = append(blobs, b)          // this sub-streamer
		}
		streamers = append(streamers, staticStreamer(blobs))
	}
	storagetest.TestStreamer(t, blobserver.NewMultiBlobStreamer(streamers...), storagetest.WantSizedRefs(want))
}
