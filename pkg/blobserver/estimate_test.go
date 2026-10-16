/*
Copyright 2026 The Perkeep Authors

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
	"crypto/sha1"
	"fmt"
	"math"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/memory"
)

func TestEstimateByHashType(t *testing.T) {
	ctx := context.Background()
	sto := new(memory.Storage)
	// Many sha224 blobs (the default hash), each 10 bytes, and a few
	// sha1 ones.
	const nBig = 20000
	for i := range nBig {
		data := fmt.Sprintf("blob %05d", i)
		if _, err := blobserver.ReceiveString(ctx, sto, data); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		data := fmt.Sprint("sha1 blob ", i)
		br, ok := blob.Parse(fmt.Sprintf("sha1-%x", sha1.Sum([]byte(data))))
		if !ok {
			t.Fatal("can't make a sha1 ref")
		}
		if _, err := blobserver.Receive(ctx, sto, br, strings.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}

	ests, err := blobserver.EstimateByHashType(ctx, sto, 300)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]blobserver.HashEstimate{}
	for _, e := range ests {
		got[e.Hash] = e
	}
	if len(got) != 2 {
		t.Fatalf("estimates = %+v; want sha1 and sha224 only", ests)
	}
	if e := got["sha1"]; !e.Exact || e.Blobs != 3 {
		t.Errorf("sha1 = %+v; want exactly 3 blobs", e)
	}
	e := got["sha224"]
	if e.Exact {
		t.Errorf("sha224 = %+v; want an estimate", e)
	}
	// With 300 of 20000 blobs, the estimate is within about 6%; allow 4
	// standard deviations.
	if relErr := math.Abs(float64(e.Blobs)-nBig) / nBig; relErr > 0.25 {
		t.Errorf("sha224 blobs = %d; want about %d", e.Blobs, nBig)
	}
	if relErr := math.Abs(float64(e.Bytes)-nBig*10) / (nBig * 10); relErr > 0.25 {
		t.Errorf("sha224 bytes = %d; want about %d", e.Bytes, nBig*10)
	}
	// The blobs are all the same size, so both standard errors are
	// about 1/sqrt(300) of the estimates.
	for _, v := range []struct {
		name     string
		est, err int64
	}{{"blobs", e.Blobs, e.BlobsErr}, {"bytes", e.Bytes, e.BytesErr}} {
		if rel := float64(v.err) / float64(v.est); math.Abs(rel-1/math.Sqrt(300)) > 0.001 {
			t.Errorf("sha224 %s error is %.4f of the estimate; want %.4f", v.name, rel, 1/math.Sqrt(300))
		}
	}
	if e := got["sha1"]; e.BlobsErr != 0 || e.BytesErr != 0 {
		t.Errorf("exact sha1 estimate has errors: %+v", e)
	}
}

// TestEstimateSizeVariation checks that the error of the estimated size
// grows with the variation of the sizes.
func TestEstimateSizeVariation(t *testing.T) {
	ctx := context.Background()
	sto := new(memory.Storage)
	for i := range 5000 {
		// Most blobs are small, and every tenth one is 100 times bigger.
		size := 10
		if i%10 == 0 {
			size = 1000
		}
		data := fmt.Sprintf("%0*d", size, i)
		if _, err := blobserver.ReceiveString(ctx, sto, data); err != nil {
			t.Fatal(err)
		}
	}
	ests, err := blobserver.EstimateByHashType(ctx, sto, 300)
	if err != nil {
		t.Fatal(err)
	}
	e := ests[0]
	blobsRel := float64(e.BlobsErr) / float64(e.Blobs)
	bytesRel := float64(e.BytesErr) / float64(e.Bytes)
	// With sizes of 10 and 1000 in a 9:1 mix, c is about 2.7, so the
	// size's error is about sqrt(1+c²) ≈ 2.9 times the count's.
	if ratio := bytesRel / blobsRel; ratio < 2 || ratio > 4 {
		t.Errorf("bytes error is %.2f times the blobs error, relatively; want about 2.9", ratio)
	}
}

func TestEstimateWithErrStrings(t *testing.T) {
	for _, tt := range []struct {
		e                blobserver.HashEstimate
		wantBlobs, wantB string
	}{
		{blobserver.HashEstimate{Blobs: 6_730_000, Bytes: 331 << 30, BlobsErr: 210_000, BytesErr: 40 << 30}, "≈6.73M ±0.21M", "≈331 ±40 GiB"},
		{blobserver.HashEstimate{Blobs: 68_300, Bytes: 244 << 30, BlobsErr: 2_200, BytesErr: 9 << 30}, "≈68.3K ±2.2K", "≈244 ±9 GiB"},
		{blobserver.HashEstimate{Blobs: 190_402, Bytes: 2 << 30, Exact: true}, "190402", "2.00 GiB"},
	} {
		if got := tt.e.BlobsWithErr(); got != tt.wantBlobs {
			t.Errorf("BlobsWithErr(%+v) = %q; want %q", tt.e, got, tt.wantBlobs)
		}
		if got := tt.e.BytesWithErr(); got != tt.wantB {
			t.Errorf("BytesWithErr(%+v) = %q; want %q", tt.e, got, tt.wantB)
		}
	}
}
