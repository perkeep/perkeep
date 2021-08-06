/*
Copyright 2013 The Perkeep Authors.

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
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go4.org/syncutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/localdisk"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/cmdmain"
)

type syncCmd struct {
	src       string
	dest      string
	third     string
	srcKeyID  string // GPG public key ID of the source server, if supported.
	destKeyID string // GPG public key ID of the destination server, if supported.

	loop           bool
	all            bool
	removeSrc      bool
	wipe           bool
	insecureTLS    bool
	oneIsDisk      bool // Whether one of src or dest is a local disk.
	concurrency    int  // max blobs to be copying at once
	dumpConfigFlag bool
}

func init() {
	cmdmain.RegisterMode("sync", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(syncCmd)
		flags.StringVar(&cmd.src, "src", "", "Source blobserver. "+serverFlagHelp)
		flags.StringVar(&cmd.dest, "dest", "", "Destination blobserver (same format as src), or 'stdout' to just enumerate the --src blobs to stdout.")
		flags.StringVar(&cmd.third, "thirdleg", "", "Copy blobs present in source but missing from destination to this 'third leg' blob store, instead of the destination. (same format as src)")

		flags.BoolVar(&cmd.loop, "loop", false, "Create an associate a new permanode for the uploaded file or directory.")
		flags.BoolVar(&cmd.wipe, "wipe", false, "If dest is an index, drop it and repopulate it from scratch. NOOP for now.")
		flags.BoolVar(&cmd.all, "all", false, "Discover all sync destinations configured on the source server and run them.")
		flags.BoolVar(&cmd.dumpConfigFlag, "dump-config", false, "Discover all sync destinations configured on the source server and list them, but do nothing else.")
		flags.BoolVar(&cmd.removeSrc, "removesrc", false, "Remove each blob from the source after syncing to the destination; for queue processing.")
		// TODO(mpl): maybe move this flag up to the client pkg as an AddFlag, as it can be used by all commands.
		if debug, _ := strconv.ParseBool(os.Getenv("CAMLI_DEBUG")); debug {
			flags.BoolVar(&cmd.insecureTLS, "insecure", false, "If set, when using TLS, the server's certificates verification is disabled, and they are not checked against the trustedCerts in the client configuration either.")
		}
		flags.IntVar(&cmd.concurrency, "j", 10, "max number of blobs to be copying at once")

		return cmd
	})
}

func (c *syncCmd) Describe() string {
	return "(Re)synchronize blobs from a source to a destination."
}

func (c *syncCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: pk [globalopts] sync [syncopts] \n")
}

func (c *syncCmd) Examples() []string {
	return []string{
		"--all",
		"--src http://localhost:3179/bs/ --dest http://localhost:3179/index-mem/",
	}
}

func (c *syncCmd) RunCommand(args []string) error {
	if c.loop && !c.removeSrc {
		return cmdmain.UsageError("Can't use --loop without --removesrc")
	}
	if c.dumpConfigFlag {
		err := c.dumpConfig()
		if err != nil {
			return fmt.Errorf("dumb-config failed: %v", err)
		}
		return nil
	}
	if c.all {
		err := c.syncAll()
		if err != nil {
			return fmt.Errorf("sync all failed: %v", err)
		}
		return nil
	}

	ss, err := c.storageFromParam("src", c.src)
	if err != nil {
		return err
	}
	ds, err := c.storageFromParam("dest", c.dest)
	if err != nil {
		return err
	}
	ts, err := c.storageFromParam("thirdleg", c.third)
	if err != nil {
		return err
	}

	differentKeyIDs := fmt.Sprintf("WARNING: the source server GPG key ID (%v) and the destination's (%v) differ. All blobs will be synced, but because the indexer at the other side is indexing claims by a different user, you may not see what you expect in that server's web UI, etc.", c.srcKeyID, c.destKeyID)

	if c.dest != "stdout" && !c.oneIsDisk && c.srcKeyID != c.destKeyID { // both blank is ok.
		// Warn at the top (and hope the user sees it and can abort if it was a mistake):
		fmt.Fprintln(cmdmain.Stderr, differentKeyIDs)
		// Warn also at the end (in case the user missed the first one)
		defer fmt.Fprintln(cmdmain.Stderr, differentKeyIDs)
	}

	passNum := 0
	for {
		passNum++
		stats, err := c.doPass(ss, ds, ts)
		cmdmain.Logf("sync stats - pass: %d, blobs: %d, bytes %d\n", passNum, stats.BlobsCopied, stats.BytesCopied)
		if err != nil {
			return fmt.Errorf("sync failed: %v", err)
		}
		if !c.loop {
			break
		}
	}
	return nil
}

// A storageType is one of "src", "dest", or "thirdleg". These match the flag names.
type storageType string

const (
	storageSource storageType = "src"
	storageDest   storageType = "dest"
	storageThird  storageType = "thirdleg"
)

// which is one of "src", "dest", or "thirdleg"
func (c *syncCmd) storageFromParam(which storageType, val string) (blobserver.Storage, error) {
	var httpClient *http.Client

	if val == "" {
		switch which {
		case storageThird:
			return nil, nil
		case storageSource:
			discl := c.discoClient()
			src, err := discl.BlobRoot()
			if err != nil {
				return nil, fmt.Errorf("failed to discover source server's blob path: %w", err)
			}
			val = src
			httpClient = discl.HTTPClient()
		}
		if val == "" {
			return nil, cmdmain.UsageError("No --" + string(which) + " flag value specified")
		}
	}
	if which == storageDest && val == "stdout" {
		return nil, nil
	}
	if looksLikePath(val) {
		disk, err := localdisk.New(val)
		if err != nil {
			return nil, fmt.Errorf("interpreted --%v=%q as a local disk path, but got error: %w", which, val, err)
		}
		c.oneIsDisk = true
		return disk, nil
	}
	cl, err := client.New(client.OptionServer(val), client.OptionInsecure(c.insecureTLS))
	if err != nil {
		return nil, fmt.Errorf("creating client for %q: %v", val, err)
	}
	if httpClient != nil {
		cl.SetHTTPClient(httpClient)
	}
	if err := cl.SetupAuth(); err != nil {
		return nil, fmt.Errorf("could not setup auth for connecting to %v: %v", val, err)
	}
	cl.Verbose = *cmdmain.FlagVerbose
	cl.Logger = log.New(cmdmain.Stderr, "", log.LstdFlags)
	serverKeyID, err := cl.ServerKeyID()
	if err != nil && err != client.ErrNoSigning {
		fmt.Fprintf(cmdmain.Stderr, "Failed to discover keyId for server %v: %v", val, err)
	} else {
		if which == storageSource {
			c.srcKeyID = serverKeyID
		} else if which == storageDest {
			c.destKeyID = serverKeyID
		}
	}
	return cl, nil
}

func looksLikePath(v string) bool {
	prefix := func(s string) bool { return strings.HasPrefix(filepath.ToSlash(v), s) }
	return prefix("./") || prefix("/") || prefix("../") || filepath.VolumeName(v) != ""
}

type SyncStats struct {
	BlobsCopied int
	BytesCopied int64
	ErrorCount  int
}

func (c *syncCmd) dumpConfig() error {
	if c.loop {
		return cmdmain.UsageError("--dump-config can't be used with --loop")
	}
	if c.third != "" {
		return cmdmain.UsageError("--dump-config can't be used with --thirdleg")
	}
	if c.dest != "" {
		return cmdmain.UsageError("--dump-config can't be used with --dest")
	}
	dc := c.discoClient()
	dc.Verbose = *cmdmain.FlagVerbose
	dc.Logger = log.New(cmdmain.Stderr, "", log.LstdFlags)
	syncHandlers, err := dc.SyncHandlers()
	if err != nil {
		return fmt.Errorf("sync handlers discovery failed: %v", err)
	}
	for _, sh := range syncHandlers {
		fmt.Printf("%v -> %v\n", sh.From, sh.To)
	}
	return nil
}

func (c *syncCmd) syncAll() error {
	if c.loop {
		return cmdmain.UsageError("--all can't be used with --loop")
	}
	if c.third != "" {
		return cmdmain.UsageError("--all can't be used with --thirdleg")
	}
	if c.dest != "" {
		return cmdmain.UsageError("--all can't be used with --dest")
	}

	dc := c.discoClient()
	syncHandlers, err := dc.SyncHandlers()
	if err != nil {
		return fmt.Errorf("sync handlers discovery failed: %v", err)
	}
	cmdmain.Logf("To be synced:\n")
	for _, sh := range syncHandlers {
		cmdmain.Logf("%v -> %v", sh.From, sh.To)
	}
	for _, sh := range syncHandlers {
		from, err := client.New(client.OptionServer(sh.From), client.OptionInsecure(c.insecureTLS))
		if err != nil {
			return fmt.Errorf("creating source client from %q: %v", sh.From, err)
		}
		from.Verbose = *cmdmain.FlagVerbose
		from.Logger = log.New(cmdmain.Stderr, "", log.LstdFlags)
		if err := from.SetupAuth(); err != nil {
			return fmt.Errorf("could not setup auth for connecting to %v: %v", sh.From, err)
		}

		to, err := client.New(client.OptionServer(sh.To), client.OptionInsecure(c.insecureTLS))
		if err != nil {
			return fmt.Errorf("creating destination client to %q: %v", sh.To, err)
		}
		to.Verbose = *cmdmain.FlagVerbose
		to.Logger = log.New(cmdmain.Stderr, "", log.LstdFlags)
		if err := to.SetupAuth(); err != nil {
			return fmt.Errorf("could not setup auth for connecting to %v: %v", sh.To, err)
		}
		cmdmain.Logf("Now syncing: %v -> %v", sh.From, sh.To)
		stats, err := c.doPass(from, to, nil)
		cmdmain.Logf("sync stats, blobs: %d, bytes %d\n", stats.BlobsCopied, stats.BytesCopied)
		if err != nil {
			return err
		}
	}
	return nil
}

// discoClient returns a client initialized with a server
// based from --src or from the configuration file if --src
// is blank. The returned client can then be used to discover
// the blobRoot and syncHandlers.
func (c *syncCmd) discoClient() *client.Client {
	cl := newClient(c.src, client.OptionInsecure(c.insecureTLS))
	cl.Verbose = *cmdmain.FlagVerbose
	cl.Logger = log.New(cmdmain.Stderr, "", log.LstdFlags)
	return cl
}

func enumerateAllBlobs(ctx context.Context, s blobserver.Storage, destc chan<- blob.SizedRef) error {
	// Use *client.Client's support for enumerating all blobs if
	// possible, since it could probably do a better job knowing
	// HTTP boundaries and such.
	if c, ok := s.(*client.Client); ok {
		return c.SimpleEnumerateBlobs(ctx, destc)
	}

	defer close(destc)
	return blobserver.EnumerateAll(ctx, s, func(sb blob.SizedRef) error {
		select {
		case destc <- sb:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	})
}

// src: non-nil source
// dest: non-nil destination
// thirdLeg: optional third-leg client. if not nil, anything on src
//     but not on dest will instead be copied to thirdLeg, instead of
//     directly to dest. (sneakernet mode, copying to a portable drive
//     and transporting thirdLeg to dest)
func (c *syncCmd) doPass(src, dest, thirdLeg blobserver.Storage) (stats SyncStats, retErr error) {
	var statsMu sync.Mutex // guards stats return value

	srcBlobs := make(chan blob.SizedRef, 100)
	destBlobs := make(chan blob.SizedRef, 100)
	srcErr := make(chan error, 1)
	destErr := make(chan error, 1)

	ctx := context.TODO()
	enumCtx, cancel := context.WithCancel(ctx) // used for all (2 or 3) enumerates
	defer cancel()
	enumerate := func(errc chan<- error, sto blobserver.Storage, blobc chan<- blob.SizedRef) {
		err := enumerateAllBlobs(enumCtx, sto, blobc)
		if err != nil {
			cancel()
		}
		errc <- err
	}

	go enumerate(srcErr, src, srcBlobs)
	checkSourceError := func() {
		if err := <-srcErr; err != nil && err != context.Canceled {
			retErr = fmt.Errorf("enumerate error from source: %w", err)
		}
	}

	if c.dest == "stdout" {
		for sb := range srcBlobs {
			fmt.Fprintf(cmdmain.Stdout, "%s %d\n", sb.Ref, sb.Size)
		}
		checkSourceError()
		return
	}

	if c.wipe {
		// TODO(mpl): dest is a client. make it send a "wipe" request?
		// upon reception its server then wipes itself if it is a wiper.
		log.Fatal("Index wiping not yet supported.")
	}

	go enumerate(destErr, dest, destBlobs)
	checkDestError := func() {
		if err := <-destErr; err != nil && err != context.Canceled {
			retErr = fmt.Errorf("enumerate error from destination: %w", err)
		}
	}

	destNotHaveBlobs := make(chan blob.SizedRef)

	readSrcBlobs := srcBlobs
	if *cmdmain.FlagVerbose {
		readSrcBlobs = loggingBlobRefChannel(srcBlobs)
	}

	mismatches := []blob.Ref{}

	logErrorf := func(format string, args ...interface{}) {
		log.Printf(format, args...)
		statsMu.Lock()
		stats.ErrorCount++
		statsMu.Unlock()
	}

	onMismatch := func(br blob.Ref) {
		// TODO(bradfitz): check both sides and repair, carefully.  For now, fail.
		logErrorf("WARNING: blobref %v has differing sizes on source and dest", br)
		mismatches = append(mismatches, br)
	}

	go blobserver.ListMissingDestinationBlobs(destNotHaveBlobs, onMismatch, readSrcBlobs, destBlobs)

	// Handle three-legged mode if tc is provided.
	checkThirdError := func() {} // default nop
	syncBlobs := destNotHaveBlobs
	firstHopDest := dest
	if thirdLeg != nil {
		thirdBlobs := make(chan blob.SizedRef, 100)
		thirdErr := make(chan error, 1)
		go enumerate(thirdErr, thirdLeg, thirdBlobs)
		checkThirdError = func() {
			if err := <-thirdErr; err != nil && err != context.Canceled {
				retErr = fmt.Errorf("enumerate error from third leg: %w", err)
			}
		}
		thirdNeedBlobs := make(chan blob.SizedRef)
		go blobserver.ListMissingDestinationBlobs(thirdNeedBlobs, onMismatch, destNotHaveBlobs, thirdBlobs)
		syncBlobs = thirdNeedBlobs
		firstHopDest = thirdLeg
	}

	var gate = syncutil.NewGate(c.concurrency)
	var wg sync.WaitGroup

	for sb := range syncBlobs {
		sb := sb
		gate.Start()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer gate.Done()
			fmt.Fprintf(cmdmain.Stdout, "Destination needs blob: %s\n", sb)
			blobReader, size, err := src.Fetch(ctxbg, sb.Ref)

			if err != nil {
				logErrorf("Error fetching %s: %v", sb.Ref, err)
				return
			}
			if size != sb.Size {
				logErrorf("Source blobserver's enumerate size of %d for blob %s doesn't match its Get size of %d",
					sb.Size, sb.Ref, size)
				return
			}

			_, err = blobserver.Receive(ctxbg, firstHopDest, sb.Ref, blobReader)
			if err != nil {
				logErrorf("Upload of %s to destination blobserver failed: %v", sb.Ref, err)
				return
			}
			statsMu.Lock()
			stats.BlobsCopied++
			stats.BytesCopied += int64(size)
			statsMu.Unlock()

			if c.removeSrc {
				if err := src.RemoveBlobs(ctxbg, []blob.Ref{sb.Ref}); err != nil {
					logErrorf("Failed to delete %s from source: %v", sb.Ref, err)
				}
			}
		}()
	}
	wg.Wait()

	checkSourceError()
	checkDestError()
	checkThirdError()
	if retErr == nil && stats.ErrorCount > 0 {
		retErr = fmt.Errorf("%d errors during sync", stats.ErrorCount)
	}
	return stats, retErr
}

func loggingBlobRefChannel(ch <-chan blob.SizedRef) chan blob.SizedRef {
	ch2 := make(chan blob.SizedRef)
	go func() {
		defer close(ch2)
		var last time.Time
		var nblob, nbyte int64
		for v := range ch {
			ch2 <- v
			nblob++
			nbyte += int64(v.Size)
			now := time.Now()
			if last.IsZero() || now.After(last.Add(1*time.Second)) {
				last = now
				log.Printf("At source blob %v (%d blobs, %d bytes)", v.Ref, nblob, nbyte)
			}
		}
		log.Printf("Total blobs: %d, %d bytes", nblob, nbyte)
	}()
	return ch2
}
