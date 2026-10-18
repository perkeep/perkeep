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

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/serverinit"

	// Storage types that a server's config may use.
	_ "perkeep.org/pkg/blobserver/blobpacked"
	_ "perkeep.org/pkg/blobserver/diskpacked"
	_ "perkeep.org/pkg/blobserver/google/cloudstorage"
	// Previously registered by googinit's scope import.
	_ "perkeep.org/pkg/blobserver/google/drive"
	_ "perkeep.org/pkg/blobserver/localdisk"
	_ "perkeep.org/pkg/blobserver/s3"
	_ "perkeep.org/pkg/sorted/kvfile"
	_ "perkeep.org/pkg/sorted/leveldb"
	_ "perkeep.org/pkg/sorted/mysql"
	_ "perkeep.org/pkg/sorted/postgres"
	_ "perkeep.org/pkg/sorted/sqlite"
)

type copyStorageCmd struct {
	srcConfig string
	dstConfig string
	dstPrefix string
	dryRun    bool
	limit     int
	workers   int
}

func init() {
	cmdmain.RegisterMode("copy-storage", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(copyStorageCmd)
		flags.StringVar(&cmd.srcConfig, "src-config", "", "Server config file that defines the source storage. Defaults to the server's config file.")
		flags.StringVar(&cmd.dstConfig, "dst-config", "", "Server config file that defines the destination storage. Required.")
		flags.StringVar(&cmd.dstPrefix, "dst-prefix", "", "Prefix of the destination storage in -dst-config. Defaults to the source prefix.")
		flags.BoolVar(&cmd.dryRun, "n", false, "Only report how many blobs are missing from the destination; don't copy them.")
		flags.IntVar(&cmd.limit, "limit", 0, "If positive, only consider the first `N` blobs in the source, such as to sample them with -n.")
		flags.IntVar(&cmd.workers, "workers", 8, "Number of blobs to copy at once.")
		return cmd
	})
}

func (c *copyStorageCmd) Demote() bool { return true }

func (c *copyStorageCmd) Describe() string {
	return "Copy blobs between storage defined by server configs, such as to convert a storage to another layout."
}

func (c *copyStorageCmd) Usage() {
	fmt.Fprint(os.Stderr, `Usage: pk copy-storage [opts] -dst-config=FILE PREFIX

Copies each blob in the storage at PREFIX, as the source server config
defines it, to the storage at the same prefix (or -dst-prefix) as the
destination server config defines it, if it's not already there. It
lists only the source, and stats each of its blobs in the destination,
so its cost depends on the size of the source. It verifies each blob's
digest before writing it. The server shouldn't be running, as both may
use the same local files.

For example, to convert a cloud mirror that has blobs in both of its
layouts (see packRelatedExcludeTypes in the server config docs) to
loose blobs at the root of its bucket, copy it from the current config
to one that sets "packRelatedExcludeTypes": ["s3"]:

    pk copy-storage -dst-config=new-config.json /sto-s3/

With -n and -limit, it checks a sample of the source's blobs first.
Running it again with just -n reports whether anything is still missing.
Removing the copied blobs from the old layout is up to you.
`)
}

func (c *copyStorageCmd) RunCommand(args []string) error {
	if len(args) != 1 {
		return cmdmain.UsageError("want one PREFIX argument")
	}
	srcPrefix := args[0]
	if c.dstConfig == "" {
		return cmdmain.UsageError("-dst-config is required")
	}
	if c.srcConfig == "" {
		c.srcConfig = osutil.UserServerConfigPath()
	}
	if c.dstPrefix == "" {
		c.dstPrefix = srcPrefix
	}
	if c.workers < 1 {
		return cmdmain.UsageError("-workers must be at least 1")
	}
	if c.srcConfig == c.dstConfig && c.dstPrefix == srcPrefix {
		return cmdmain.UsageError("the source and destination are the same storage")
	}

	src, closeSrc, err := openConfigStorage(c.srcConfig, srcPrefix)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	defer closeSrc.Close()
	dst, closeDst, err := openConfigStorage(c.dstConfig, c.dstPrefix)
	if err != nil {
		return fmt.Errorf("destination: %w", err)
	}
	defer closeDst.Close()

	res, err := copyMissingBlobs(context.Background(), src, dst, copyOpts{
		workers: c.workers,
		dryRun:  c.dryRun,
		limit:   c.limit,
		log:     cmdmain.Stderr,
	})
	fmt.Fprintf(cmdmain.Stdout, "%d source blobs; %d (%d bytes) missing from the destination", res.seen, res.missing, res.missingBytes)
	if !c.dryRun {
		fmt.Fprintf(cmdmain.Stdout, "; copied %d (%d bytes); %d failed", res.copied, res.copiedBytes, res.failed)
	}
	fmt.Fprintln(cmdmain.Stdout)
	if err != nil {
		return err
	}
	if res.failed > 0 {
		return fmt.Errorf("%d blobs failed to copy", res.failed)
	}
	return nil
}

func openConfigStorage(file, prefix string) (blobserver.Storage, io.Closer, error) {
	conf, err := serverinit.LoadFile(file)
	if err != nil {
		return nil, nil, err
	}
	return conf.OpenStorage(prefix)
}

type copyResult struct {
	seen                  int64 // blobs in the source
	missing, missingBytes int64
	copied, copiedBytes   int64
	failed                int64
}

// statBatchSize is how many source blobs copyMissingBlobs stats in the
// destination at once.
const statBatchSize = 100

type copyOpts struct {
	workers int       // blobs to copy at once
	dryRun  bool      // only count the missing blobs
	limit   int       // if positive, only consider this many source blobs
	log     io.Writer // for progress and failures
}

// errLimit stops the enumeration of the source at copyOpts.limit.
var errLimit = errors.New("reached the limit")

// copyMissingBlobs copies each blob in src that's not in dst, verifying
// each one's digest before writing it.
//
// It lists only src, and stats its blobs in dst, so its cost depends on
// the size of src, not dst.
func copyMissingBlobs(ctx context.Context, src, dst blobserver.Storage, opts copyOpts) (copyResult, error) {
	workers, dryRun, logw := opts.workers, opts.dryRun, opts.log
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var res copyResult
	var copied, copiedBytes, failed atomic.Int64

	work := make(chan blob.SizedRef)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for sb := range work {
				if err := copyBlob(ctx, src, dst, sb); err != nil {
					failed.Add(1)
					fmt.Fprintf(logw, "%v: %v\n", sb.Ref, err)
					continue
				}
				copied.Add(1)
				copiedBytes.Add(int64(sb.Size))
			}
		})
	}

	// flush stats the blobs in batch in dst, and copies those missing.
	var batch []blob.SizedRef
	var statErr error
	flush := func() error {
		refs := make([]blob.Ref, len(batch))
		for i, sb := range batch {
			refs[i] = sb.Ref
		}
		var mu sync.Mutex
		have := make(map[blob.Ref]uint32)
		if err := dst.StatBlobs(ctx, refs, func(sb blob.SizedRef) error {
			mu.Lock()
			defer mu.Unlock()
			have[sb.Ref] = sb.Size
			return nil
		}); err != nil {
			statErr = fmt.Errorf("statting destination: %w", err)
			return statErr
		}
		for _, sb := range batch {
			if size, ok := have[sb.Ref]; ok {
				if size != sb.Size {
					fmt.Fprintf(logw, "%v: %d bytes in the source but %d in the destination; not copying\n", sb.Ref, sb.Size, size)
				}
				continue
			}
			res.missing++
			res.missingBytes += int64(sb.Size)
			if dryRun {
				continue
			}
			select {
			case work <- sb:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		batch = batch[:0]
		return nil
	}

	lastProgress := time.Now()
	err := blobserver.EnumerateAll(ctx, src, func(sb blob.SizedRef) error {
		if opts.limit > 0 && res.seen == int64(opts.limit) {
			return errLimit
		}
		res.seen++
		batch = append(batch, sb)
		if len(batch) < statBatchSize {
			return nil
		}
		if err := flush(); err != nil {
			return err
		}
		if time.Since(lastProgress) >= 10*time.Second {
			lastProgress = time.Now()
			fmt.Fprintf(logw, "%d source blobs checked, %d missing; %d copied (%d bytes), %d failed\n",
				res.seen, res.missing, copied.Load(), copiedBytes.Load(), failed.Load())
		}
		return nil
	})
	if errors.Is(err, errLimit) {
		err = nil
	}
	if err == nil && len(batch) > 0 {
		err = flush()
	}
	close(work)
	wg.Wait()
	res.copied, res.copiedBytes, res.failed = copied.Load(), copiedBytes.Load(), failed.Load()
	switch {
	case statErr != nil:
		return res, statErr
	case err != nil:
		return res, fmt.Errorf("enumerating source: %w", err)
	}
	return res, nil
}

// errDigestMismatch is returned by copyBlob for a source blob whose
// contents don't match its digest.
var errDigestMismatch = errors.New("contents in the source don't match the digest; not copying")

func copyBlob(ctx context.Context, src blob.Fetcher, dst blobserver.BlobReceiver, sb blob.SizedRef) error {
	rc, _, err := src.Fetch(ctx, sb.Ref)
	if err != nil {
		return fmt.Errorf("fetching: %w", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, blobserver.MaxBlobSize+1))
	if err != nil {
		return fmt.Errorf("fetching: %w", err)
	}
	h := sb.Ref.Hash()
	if h == nil {
		return fmt.Errorf("unsupported digest type")
	}
	h.Write(data)
	if !sb.Ref.HashMatches(h) {
		return errDigestMismatch
	}
	if _, err := blobserver.Receive(ctx, dst, sb.Ref, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("writing: %w", err)
	}
	return nil
}
