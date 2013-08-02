/*
Copyright 2013 The Camlistore Authors.

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
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/localdisk"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/cmdmain"
)

type syncCmd struct {
	src   string
	dest  string
	third string

	loop      bool
	verbose   bool
	all       bool
	removeSrc bool

	logger *log.Logger
}

func init() {
	cmdmain.RegisterCommand("sync", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(syncCmd)
		flags.StringVar(&cmd.src, "src", "", "Source blobserver is either a URL prefix (with optional path), a host[:port], a path (starting with /, ./, or ../), or blank to use the Camlistore client config's default host.")
		flags.StringVar(&cmd.dest, "dest", "", "Destination blobserver (same format as src), or 'stdout' to just enumerate the --src blobs to stdout.")
		flags.StringVar(&cmd.third, "thirdleg", "", "Copy blobs present in source but missing from destination to this 'third leg' blob store, instead of the destination. (same format as src)")

		flags.BoolVar(&cmd.loop, "loop", false, "Create an associate a new permanode for the uploaded file or directory.")
		flags.BoolVar(&cmd.verbose, "verbose", false, "Be verbose.")
		flags.BoolVar(&cmd.all, "all", false, "Discover all sync destinations configured on the source server and run them.")
		flags.BoolVar(&cmd.removeSrc, "removesrc", false, "Remove each blob from the source after syncing to the destination; for queue processing.")

		return cmd
	})
}

func (c *syncCmd) Describe() string {
	return "Synchronize blobs from a source to a destination."
}

func (c *syncCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camtool [globalopts] sync [syncopts] \n")
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
	if c.verbose {
		c.logger = log.New(os.Stderr, "", 0) // else nil
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

	passNum := 0
	for {
		passNum++
		stats, err := c.doPass(ss, ds, ts)
		if c.verbose {
			log.Printf("sync stats - pass: %d, blobs: %d, bytes %d\n", passNum, stats.BlobsCopied, stats.BytesCopied)
		}
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
	if val == "" {
		switch which {
		case storageThird:
			return nil, nil
		case storageSource:
			discl := c.discoClient()
			discl.SetLogger(c.logger)
			src, err := discl.BlobRoot()
			if err != nil {
				return nil, fmt.Errorf("Failed to discover source server's blob path: %v", err)
			}
			val = src
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
			return nil, fmt.Errorf("Interpreted --%v=%q as a local disk path, but got error: %v", val, err)
		}
		return disk, nil
	}
	cl := client.New(val)
	// TODO(mpl): probably needs the transport setup for trusted certs here.
	cl.SetupAuth()
	cl.SetLogger(c.logger)
	return noHub{cl}, nil
}

func looksLikePath(v string) bool {
	prefix := func(s string) bool { return strings.HasPrefix(v, s) }
	return prefix("./") || prefix("/") || prefix("../")
}

type SyncStats struct {
	BlobsCopied int
	BytesCopied int64
	ErrorCount  int
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
	dc.SetLogger(c.logger)
	syncHandlers, err := dc.SyncHandlers()
	if err != nil {
		return fmt.Errorf("sync handlers discovery failed: %v", err)
	}
	if c.verbose {
		log.Printf("To be synced:\n")
		for _, sh := range syncHandlers {
			log.Printf("%v -> %v", sh.From, sh.To)
		}
	}
	for _, sh := range syncHandlers {
		from := client.New(sh.From)
		from.SetLogger(c.logger)
		from.SetupAuth()
		to := client.New(sh.To)
		to.SetLogger(c.logger)
		to.SetupAuth()
		if c.verbose {
			log.Printf("Now syncing: %v -> %v", sh.From, sh.To)
		}
		stats, err := c.doPass(noHub{from}, noHub{to}, nil)
		if c.verbose {
			log.Printf("sync stats, blobs: %d, bytes %d\n", stats.BlobsCopied, stats.BytesCopied)
		}
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
	var cl *client.Client
	if c.src == "" {
		cl = client.NewOrFail()
	} else {
		cl = client.New(c.src)
	}
	cl.SetupAuth()
	return cl
}

func enumerateAllBlobs(s blobserver.Storage, destc chan<- blobref.SizedBlobRef) error {
	// Use *client.Client's support for enumerating all blobs if
	// possible, since it could probably do a better job knowing
	// HTTP boundaries and such.
	if nh, ok := s.(noHub); ok {
		return nh.Client.SimpleEnumerateBlobs(destc)
	}

	defer close(destc)
	return blobserver.EnumerateAll(s, func(sb blobref.SizedBlobRef) error {
		destc <- sb
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
	srcBlobs := make(chan blobref.SizedBlobRef, 100)
	destBlobs := make(chan blobref.SizedBlobRef, 100)
	srcErr := make(chan error, 1)
	destErr := make(chan error, 1)

	go func() {
		srcErr <- enumerateAllBlobs(src, srcBlobs)
	}()
	checkSourceError := func() {
		if err := <-srcErr; err != nil {
			retErr = fmt.Errorf("Enumerate error from source: %v", err)
		}
	}

	if c.dest == "stdout" {
		for sb := range srcBlobs {
			fmt.Printf("%s %d\n", sb.BlobRef, sb.Size)
		}
		checkSourceError()
		return
	}

	go func() {
		destErr <- enumerateAllBlobs(dest, destBlobs)
	}()
	checkDestError := func() {
		if err := <-destErr; err != nil {
			retErr = errors.New(fmt.Sprintf("Enumerate error from destination: %v", err))
		}
	}

	destNotHaveBlobs := make(chan blobref.SizedBlobRef)
	sizeMismatch := make(chan *blobref.BlobRef)
	readSrcBlobs := srcBlobs
	if c.verbose {
		readSrcBlobs = loggingBlobRefChannel(srcBlobs)
	}
	mismatches := []*blobref.BlobRef{}
	go client.ListMissingDestinationBlobs(destNotHaveBlobs, sizeMismatch, readSrcBlobs, destBlobs)

	// Handle three-legged mode if tc is provided.
	checkThirdError := func() {} // default nop
	syncBlobs := destNotHaveBlobs
	firstHopDest := dest
	if thirdLeg != nil {
		thirdBlobs := make(chan blobref.SizedBlobRef, 100)
		thirdErr := make(chan error, 1)
		go func() {
			thirdErr <- enumerateAllBlobs(thirdLeg, thirdBlobs)
		}()
		checkThirdError = func() {
			if err := <-thirdErr; err != nil {
				retErr = fmt.Errorf("Enumerate error from third leg: %v", err)
			}
		}
		thirdNeedBlobs := make(chan blobref.SizedBlobRef)
		go client.ListMissingDestinationBlobs(thirdNeedBlobs, sizeMismatch, destNotHaveBlobs, thirdBlobs)
		syncBlobs = thirdNeedBlobs
		firstHopDest = thirdLeg
	}
For:
	for {
		select {
		case br := <-sizeMismatch:
			// TODO(bradfitz): check both sides and repair, carefully.  For now, fail.
			log.Printf("WARNING: blobref %v has differing sizes on source and dest", br)
			stats.ErrorCount++
			mismatches = append(mismatches, br)
		case sb, ok := <-syncBlobs:
			if !ok {
				break For
			}
			fmt.Printf("Destination needs blob: %s\n", sb)

			blobReader, size, err := src.FetchStreaming(sb.BlobRef)
			if err != nil {
				stats.ErrorCount++
				log.Printf("Error fetching %s: %v", sb.BlobRef, err)
				continue
			}
			if size != sb.Size {
				stats.ErrorCount++
				log.Printf("Source blobserver's enumerate size of %d for blob %s doesn't match its Get size of %d",
					sb.Size, sb.BlobRef, size)
				continue
			}

			if _, err := firstHopDest.ReceiveBlob(sb.BlobRef, blobReader); err != nil {
				stats.ErrorCount++
				log.Printf("Upload of %s to destination blobserver failed: %v", sb.BlobRef, err)
				continue
			}
			stats.BlobsCopied++
			stats.BytesCopied += size

			if c.removeSrc {
				if err = src.RemoveBlobs([]*blobref.BlobRef{sb.BlobRef}); err != nil {
					stats.ErrorCount++
					log.Printf("Failed to delete %s from source: %v", sb.BlobRef, err)
				}
			}
		}
	}

	checkSourceError()
	checkDestError()
	checkThirdError()
	if retErr == nil && stats.ErrorCount > 0 {
		retErr = fmt.Errorf("%d errors during sync", stats.ErrorCount)
	}
	return stats, retErr
}

func loggingBlobRefChannel(ch <-chan blobref.SizedBlobRef) chan blobref.SizedBlobRef {
	ch2 := make(chan blobref.SizedBlobRef)
	go func() {
		defer close(ch2)
		var last time.Time
		var nblob, nbyte int64
		for v := range ch {
			ch2 <- v
			nblob++
			nbyte += v.Size
			now := time.Now()
			if last.IsZero() || now.After(last.Add(1*time.Second)) {
				last = now
				log.Printf("At source blob %v (%d blobs, %d bytes)", v.BlobRef, nblob, nbyte)
			}
		}
		log.Printf("Total blobs: %d, %d bytes", nblob, nbyte)
	}()
	return ch2
}

type noHub struct {
	*client.Client
}

func (noHub) GetBlobHub() blobserver.BlobHub {
	panic("not needed, nor implemented")
}
