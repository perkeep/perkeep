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

package search

import (
	"context"
	"errors"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/types/camtypes"
	"tailscale.com/util/set"
)

// digestIndex is implemented by indexes that index the digests of files'
// contents, such as *index.Index.
type digestIndex interface {
	GetFileDigests(ctx context.Context, fileRef blob.Ref) ([]string, error)
	AppendWholeRefsOfDigest(ctx context.Context, dst []blob.Ref, d string) ([]blob.Ref, error)
}

// filesOfDigest returns the files in the corpus whose contents have
// digest d, as constraintBlobCandidates does.
func (s *search) filesOfDigest(ctx context.Context, d string) (set.Set[blob.Ref], bool) {
	di, ok := s.h.index.(digestIndex)
	if !ok {
		return nil, false
	}
	wholeRefs, err := di.AppendWholeRefsOfDigest(ctx, nil, d)
	if err != nil {
		log.Printf("search: finding the contents with digest %s: %v", d, err)
		return nil, false
	}
	var files []blob.Ref
	for _, wr := range wholeRefs {
		files = s.h.corpus.AppendFilesOfWholeRef(files, wr)
	}
	return set.Of(files...), true
}

// fileHasDigest reports whether the contents of the file br, with info
// fi, have digest d.
func (s *search) fileHasDigest(ctx context.Context, br blob.Ref, fi camtypes.FileInfo, d string) (bool, error) {
	if !fi.WholeRef.Valid() {
		return false, nil
	}
	if fi.WholeRef.String() == d {
		return true, nil
	}
	if hexSum, isCRC := strings.CutPrefix(d, "crc32-"); isCRC && s.h.corpus != nil {
		want, err := strconv.ParseUint(hexSum, 16, 32)
		if err != nil {
			return false, nil
		}
		sum, ok := s.h.corpus.FileCRC32(br)
		return ok && sum == uint32(want), nil
	}
	di, ok := s.h.index.(digestIndex)
	if !ok {
		return false, nil
	}
	digests, err := di.GetFileDigests(ctx, br)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return slices.Contains(digests, d), nil
}
