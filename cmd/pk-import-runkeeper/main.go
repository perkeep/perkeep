/*
Copyright 2026 The Perkeep Authors.

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

// The pk-import-runkeeper command uploads a Runkeeper export and asks the
// server's shared archive importer to process it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/types/camtypes"
)

var errUsage = errors.New("invalid command line")

type importCmd struct {
	server  string
	fileRef string
	inspect bool
	detach  bool
	json    bool
	stdout  io.Writer
	stderr  io.Writer
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintf(os.Stderr, "pk-import-runkeeper: %v\n", err)
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	c := &importCmd{stdout: stdout, stderr: stderr}
	flags := flag.NewFlagSet("pk-import-runkeeper", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&c.server, "server", "", "Server URL, host[:port], or config alias; blank uses the configured default.")
	flags.StringVar(&c.fileRef, "fileref", "", "Use an already uploaded file-schema blobref instead of a local file.")
	flags.BoolVar(&c.inspect, "inspect", false, "Upload and inspect identity without creating an account or starting import.")
	flags.BoolVar(&c.detach, "detach", false, "Start the import without waiting for completion.")
	flags.BoolVar(&c.json, "json", false, "Print the inspection or final import result as JSON.")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: pk-import-runkeeper [options] <archive.zip>")
		fmt.Fprintln(stderr, "       pk-import-runkeeper [options] -fileref=<file-schema-ref>")
		fmt.Fprintln(stderr, "\nUpload and import a Runkeeper account export using the server's archive service.")
		fmt.Fprintln(stderr, "\nOptions:")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	return c.run(ctx, flags.Args())
}

func (c *importCmd) run(ctx context.Context, args []string) error {
	want := 1
	if c.fileRef != "" {
		want = 0
	}
	if len(args) != want {
		return fmt.Errorf("%w: requires a local archive or -fileref; see -help", errUsage)
	}
	if c.inspect && c.detach {
		return fmt.Errorf("%w: -inspect and -detach cannot be combined", errUsage)
	}
	var ref blob.Ref
	if c.fileRef != "" {
		var ok bool
		ref, ok = blob.Parse(c.fileRef)
		if !ok {
			return fmt.Errorf("%w: invalid -fileref", errUsage)
		}
	}
	cl, err := newClient(c.server)
	if err != nil {
		return err
	}
	defer cl.Close()
	// Discover the importer before spending time hashing/uploading a large file.
	if _, err := cl.ImporterRoot(); err != nil {
		return err
	}
	if c.fileRef == "" {
		name := args[0]
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return errors.New("archive must be a regular file")
		}
		fmt.Fprintf(c.stderr, "Hashing and uploading %s (%d bytes)...\n", name, fi.Size())
		ref, err = cl.UploadFile(ctx, filepath.Base(name), f, &client.FileUploadOptions{FileInfo: fi})
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(c.stderr, "Archive file: %s\n", ref)
	var result *camtypes.ArchiveImportResult
	if c.inspect {
		result, err = cl.InspectArchive(ctx, "runkeeper", ref)
	} else {
		result, err = cl.ImportArchive(ctx, "runkeeper", ref)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(c.stderr, "Account: %s (%s)\n", result.Identity.UserName, result.Identity.Email)
	if result.AccountRef.Valid() {
		fmt.Fprintf(c.stderr, "Account permanode: %s\n", result.AccountRef)
	}
	if c.inspect || c.detach {
		if c.json {
			return json.NewEncoder(c.stdout).Encode(result)
		}
		if c.inspect {
			fmt.Fprintln(c.stdout, "Inspection succeeded; no import started.")
		} else {
			fmt.Fprintln(c.stdout, "Import started; the server will continue after this command exits.")
		}
		return nil
	}
	status, err := waitArchiveImport(ctx, cl, "runkeeper", result, c.stderr)
	if err != nil {
		if ctx.Err() != nil {
			fmt.Fprintf(c.stderr, "Stopped waiting. The server may still be importing; retry with -fileref=%s.\n", ref)
		}
		return err
	}
	if c.json {
		return json.NewEncoder(c.stdout).Encode(status)
	}
	fmt.Fprintf(c.stdout, "Import complete: %d activities, %d unchanged, %d warnings.\n",
		status.Progress.ItemsDone, status.Progress.ItemsUnchanged, status.Progress.WarningCount)
	return nil
}

// newClient uses the same server aliases and authentication as the other
// Perkeep clients, without linking command or server implementations.
func newClient(server string) (*client.Client, error) {
	var opts []client.ClientOption
	if server != "" {
		opts = append(opts, client.OptionServer(server))
	}
	cl, err := client.New(opts...)
	if err != nil {
		return nil, err
	}
	if server != "" {
		if err := cl.SetupAuth(); err != nil {
			cl.Close()
			return nil, err
		}
	}
	return cl, nil
}

type archiveStatusClient interface {
	ArchiveImportStatus(context.Context, string, blob.Ref) (*camtypes.ArchiveImportStatus, error)
}

func waitArchiveImport(ctx context.Context, cl archiveStatusClient, provider string, result *camtypes.ArchiveImportResult, out io.Writer) (*camtypes.ArchiveImportStatus, error) {
	if !result.AccountRef.Valid() || result.Status == nil {
		return nil, errors.New("server returned no archive run status")
	}
	status := result.Status
	lastProgress := ""
	warningsShown := 0
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if status.FileRef != result.Status.FileRef || !status.Started.Equal(result.Status.Started) {
			return nil, errors.New("archive run was replaced by another import; check account status")
		}
		p := status.Progress
		line := fmt.Sprintf("%d/%d activities (%d unchanged)", p.ItemsDone, p.ItemsTotal, p.ItemsUnchanged)
		if line != lastProgress {
			fmt.Fprintln(out, line)
			lastProgress = line
		}
		for warningsShown < len(p.Warnings) {
			fmt.Fprintf(out, "Warning: %s\n", p.Warnings[warningsShown])
			warningsShown++
		}
		switch status.State {
		case "complete":
			return status, nil
		case "error", "interrupted":
			return nil, fmt.Errorf("archive import %s: %s", status.State, status.Error)
		case "running":
		default:
			return nil, fmt.Errorf("unknown archive import state %q", status.State)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
		var err error
		status, err = cl.ArchiveImportStatus(ctx, provider, result.AccountRef)
		if err != nil {
			return nil, err
		}
	}
}
