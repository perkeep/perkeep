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

package blobserver

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	"perkeep.org/pkg/blob"
)

// HashEstimate is an estimate of how many blobs of one hash type a
// storage has, and their total size.
type HashEstimate struct {
	Hash  string `json:"hash,omitempty"` // such as "sha224"; omitted in maps keyed by it
	Blobs int64  `json:"blobs"`          // number of blobs
	Bytes int64  `json:"bytes"`          // their total size
	// Exact is whether Blobs and Bytes are exact, because the sample
	// had all the storage's blobs of this type.
	Exact bool `json:"exact,omitempty"`
	// BlobsErr and BytesErr are the standard errors of Blobs and Bytes,
	// if they're estimates: they're within about twice that of the
	// truth, 95% of the time.
	BlobsErr int64 `json:"blobsErr,omitempty"`
	BytesErr int64 `json:"bytesErr,omitempty"`
}

// CensusID returns the ID of the health check whose details, a
// []HashEstimate, estimate how many blobs of each hash type the storage
// at prefix has, and their size.
func CensusID(prefix string) string {
	return "storage.census[" + strings.Trim(prefix, "/") + "]"
}

// AddErrs returns the standard error of the sum of two independent
// estimates with the standard errors a and b.
func AddErrs(a, b int64) int64 {
	return int64(math.Hypot(float64(a), float64(b)))
}

// errSampleFull stops the enumeration in EstimateByHashType.
var errSampleFull = errors.New("sentinel error: sample full")

// EstimateByHashType estimates how many blobs of each hash type (see
// blob.HashFuncs) src has, and their total size, from the first sample
// blobs of each type in its enumeration. As digests are uniformly
// distributed, the last blob's digest says what fraction of the type's
// blobs the sample is. If src has fewer than sample blobs of a type, the
// estimate is exact. It omits types src has no blobs of.
//
// With a sample of n blobs, the relative standard error of the number of
// blobs is about 1/sqrt(n), and that of their size about
// sqrt((1+c²)/n), where c is the sizes' coefficient of variation (their
// standard deviation over their mean), which the estimates' BlobsErr and
// BytesErr are computed from.
func EstimateByHashType(ctx context.Context, src BlobEnumerator, sample int) ([]HashEstimate, error) {
	var ests []HashEstimate
	for _, hash := range blob.HashFuncs() {
		prefix := hash + "-"
		var hs HashSample
		var last string
		err := EnumerateAllFrom(ctx, src, prefix, func(sb blob.SizedRef) error {
			ref := sb.Ref.String()
			if !strings.HasPrefix(ref, prefix) {
				return errSampleFull
			}
			hs.Add(sb.Size)
			last = ref
			if hs.Blobs == int64(sample) {
				return errSampleFull
			}
			return nil
		})
		if err != nil && !errors.Is(err, errSampleFull) {
			return nil, err
		}
		switch {
		case hs.Blobs == 0:
		case hs.Blobs < int64(sample):
			ests = append(ests, HashEstimate{Hash: hash, Blobs: hs.Blobs, Bytes: hs.Bytes, Exact: true})
		default:
			ests = append(ests, hs.Estimate(hash, DigestFraction(strings.TrimPrefix(last, prefix))))
		}
	}
	return ests, nil
}

// HashSample is a sample of a storage's blobs of one hash type, from
// which its Estimate method estimates them all.
type HashSample struct {
	Blobs int64 // number of blobs
	Bytes int64 // their total size

	sumSq float64 // of the sizes
}

// Add adds a blob of the given size to the sample.
func (s *HashSample) Add(size uint32) {
	s.Blobs++
	s.Bytes += int64(size)
	s.sumSq += float64(size) * float64(size)
}

// Estimate estimates the storage's blobs of type hash, if the sample is
// those whose digests are in the fraction frac of the space of digests
// (see DigestFraction), with the standard errors described at
// EstimateByHashType. If frac isn't positive, it estimates only the
// sample's blobs.
func (s HashSample) Estimate(hash string, frac float64) HashEstimate {
	est := HashEstimate{Hash: hash, Blobs: s.Blobs, Bytes: s.Bytes}
	if s.Blobs == 0 {
		return est
	}
	if frac > 0 {
		est.Blobs = int64(float64(s.Blobs) / frac)
		est.Bytes = int64(float64(s.Bytes) / frac)
	}
	n := float64(s.Blobs)
	mean := float64(s.Bytes) / n
	var cv2 float64 // the sizes' squared coefficient of variation
	if mean > 0 {
		cv2 = max(0, s.sumSq/n-mean*mean) / (mean * mean)
	}
	est.BlobsErr = int64(float64(est.Blobs) / math.Sqrt(n))
	est.BytesErr = int64(float64(est.Bytes) * math.Sqrt((1+cv2)/n))
	return est
}

// DigestFraction returns how far through the space of digests the hex
// digest (without its hash type's name) is, from 0 to 1, counting the
// digests it's a prefix of as passed, from its first 13 hex digits (52
// bits, which a float64 represents exactly). For example, the digests
// up to those starting with "7f" are DigestFraction("7f"), or 0.5, of
// them all.
func DigestFraction(hex string) float64 {
	const digits = 13
	if len(hex) > digits {
		hex = hex[:digits]
	}
	v, err := strconv.ParseUint(hex, 16, 64)
	if err != nil || hex == "" {
		return 0
	}
	return float64(v+1) / float64(uint64(1)<<(4*len(hex)))
}

// BlobsWithErr formats e.Blobs with its standard error, such as
// "≈6.73M ±0.21M", or exactly, such as "190402", if e.Exact.
func (e HashEstimate) BlobsWithErr() string {
	if e.Exact || e.Blobs < 1000 {
		return e.BlobsString()
	}
	return "≈" + e.BlobsString() + " ±" + inUnitOf(e.BlobsString(), e.Blobs, e.BlobsErr)
}

// BytesWithErr formats e.Bytes with its standard error, such as
// "≈331 ±40 GiB", or exactly, such as "2.00 GiB", if e.Exact.
func (e HashEstimate) BytesWithErr() string {
	s := e.BytesString()
	if e.Exact {
		return s
	}
	num, unit, ok := strings.Cut(s, " ")
	if !ok {
		return "≈" + s
	}
	return "≈" + num + " ±" + inUnitOf(num, e.Bytes, e.BytesErr) + " " + unit
}

// inUnitOf formats err in the same unit, and to the same number of
// decimals, as formatted, which is the formatting of v.
func inUnitOf(formatted string, v, err int64) string {
	num := strings.TrimRight(formatted, "KMGT")
	suffix := formatted[len(num):]
	f, perr := strconv.ParseFloat(num, 64)
	if perr != nil || f == 0 {
		return strconv.FormatInt(err, 10)
	}
	decimals := 0
	if i := strings.IndexByte(num, '.'); i >= 0 {
		decimals = len(num) - i - 1
	}
	return strconv.FormatFloat(float64(err)*f/float64(v), 'f', decimals, 64) + suffix
}

// BlobsString formats e.Blobs: exactly if e.Exact, and otherwise to three
// significant digits, such as "6.73M".
func (e HashEstimate) BlobsString() string {
	n := e.Blobs
	if e.Exact || n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	for _, u := range []struct {
		div  float64
		unit string
	}{{1e9, "G"}, {1e6, "M"}, {1e3, "K"}} {
		if v := float64(n) / u.div; v >= 1 {
			return threeDigits(v) + u.unit
		}
	}
	return strconv.FormatInt(n, 10)
}

// BytesString formats e.Bytes in binary units, such as "331 GiB".
func (e HashEstimate) BytesString() string {
	n := e.Bytes
	for _, u := range []struct {
		div  float64
		unit string
	}{{1 << 40, "TiB"}, {1 << 30, "GiB"}, {1 << 20, "MiB"}, {1 << 10, "KiB"}} {
		if v := float64(n) / u.div; v >= 1 {
			return threeDigits(v) + " " + u.unit
		}
	}
	return strconv.FormatInt(n, 10) + " B"
}

// threeDigits formats v, which is at least 1, to three significant
// digits.
func threeDigits(v float64) string {
	switch {
	case v >= 100:
		return strconv.FormatFloat(v, 'f', 0, 64)
	case v >= 10:
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}
