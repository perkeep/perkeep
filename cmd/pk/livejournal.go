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
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"perkeep.org/internal/livejournal"
	search "perkeep.org/internal/searchtypes"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/schema"
)

type livejournalCmd struct {
	user         string
	passwordFile string
	server       string
	tz           string
	workers      int
}

func init() {
	cmdmain.RegisterMode("livejournal", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(livejournalCmd)
		flags.StringVar(&cmd.user, "user", "", "The LiveJournal user whose journal to read, and who logs in.")
		flags.StringVar(&cmd.passwordFile, "password-file", "", "A file with the user's LiveJournal password. Only a hash of it, with a one-time challenge, is sent.")
		flags.StringVar(&cmd.server, "server", livejournal.DefaultBaseURL, "The LiveJournal server's URL.")
		flags.StringVar(&cmd.tz, "tz", "Local", "The time zone of the entries' times, which LiveJournal records as the poster's local time, without a zone.")
		flags.IntVar(&cmd.workers, "j", 8, "How many permanodes to update at once.")
		return cmd
	})
}

func (c *livejournalCmd) Describe() string {
	return "Keep a permanode of each entry of a LiveJournal journal, and of each comment on them."
}

func (c *livejournalCmd) Usage() {
	fmt.Fprintf(os.Stderr, `Usage: pk [globalopts] livejournal -user=<user> -password-file=<file> [opts] stats
       pk [globalopts] livejournal -user=<user> -password-file=<file> [opts] sync

Reads all the entries of a LiveJournal user's journal, including private
ones, and all the comments on them, and keeps a permanode of each, under a
set titled "LiveJournal - <user>". Running it again updates them. The
server's LiveJournal importer keeps the same permanodes, and sync makes
(or finds) the importer's account for the user, with the set in its
import root, so the server's importer pages list it. To run imports from
there too, set up the account's password file on the server.

"stats" reports what sync would change, without changing anything.

Nothing is deleted: entries and comments that LiveJournal no longer has
keep their permanodes, and the text that a comment had before it was
deleted stays.
`)
}

func (c *livejournalCmd) Examples() []string {
	return []string{
		"-user=brad -password-file=$HOME/keys/brad.livejournal.password stats",
		"-user=brad -password-file=$HOME/keys/brad.livejournal.password sync",
	}
}

func (c *livejournalCmd) RunCommand(args []string) error {
	if len(args) != 1 || (args[0] != "stats" && args[0] != "sync") {
		return cmdmain.UsageError("want one subcommand: stats or sync")
	}
	dryRun := args[0] == "stats"
	if c.user == "" || c.passwordFile == "" {
		return cmdmain.UsageError("need -user and -password-file")
	}
	loc, err := time.LoadLocation(c.tz)
	if err != nil {
		return fmt.Errorf("bad -tz: %w", err)
	}
	pw, err := os.ReadFile(c.passwordFile)
	if err != nil {
		return err
	}
	ctx := context.Background()

	lj := livejournal.NewClient(c.server, c.user, strings.TrimRight(string(pw), "\r\n"))
	if err := lj.Login(ctx); err != nil {
		return err
	}
	j, err := lj.Fetch(ctx, log.Printf)
	if err != nil {
		return err
	}

	cl, err := client.New()
	if err != nil {
		return err
	}
	signer, err := cl.Signer()
	if err != nil {
		return fmt.Errorf("making permanodes needs a signing identity: %w", err)
	}
	if !dryRun {
		if err := cl.UploadPublicKey(ctx); err != nil {
			return err
		}
	}
	res, err := livejournal.Sync(ctx, ljClientStore{cl, signer}, j, livejournal.SyncOptions{
		Location: loc,
		Workers:  c.workers,
		DryRun:   dryRun,
		Logf:     log.Printf,
	})
	if err != nil {
		return err
	}
	verb := "Changed"
	if dryRun {
		verb = "Would change"
	}
	fmt.Fprintf(cmdmain.Stdout, "%d entries; %d comments, of which %d of no known entry were skipped; %d values weren't valid UTF-8.\n",
		len(j.Entries), len(j.Comments), res.CommentsSkipped, res.InvalidUTF8)
	fmt.Fprintf(cmdmain.Stdout, "%s %d of %d permanodes (%d new), with %d attribute changes.\n",
		verb, res.Changed, res.Permanodes, res.Created, res.AttrChanges)
	fmt.Fprintf(cmdmain.Stdout, "Journal: %s\n", res.Journal)
	if dryRun {
		return nil
	}
	// List the journal with the server's importer accounts, as if its
	// LiveJournal importer had imported it.
	tz := c.tz
	if tz == "Local" {
		tz = "" // which would mean the server's zone, not necessarily this one
	}
	acct, changed, err := livejournal.LinkImporterAccount(ctx, ljClientStore{cl, signer}, c.user, tz, res.Journal)
	if err != nil {
		return fmt.Errorf("linking the server's LiveJournal importer account: %w", err)
	}
	if changed {
		fmt.Fprintf(cmdmain.Stdout, "Importer account: %s (updated)\n", acct)
	} else {
		fmt.Fprintf(cmdmain.Stdout, "Importer account: %s\n", acct)
	}
	return nil
}

// ljClientStore is a livejournal.Store of a client's server.
type ljClientStore struct {
	c      *client.Client
	signer *schema.Signer
}

func (s ljClientStore) Signer() *schema.Signer { return s.signer }

func (s ljClientStore) Upload(ctx context.Context, signed string) error {
	_, err := s.c.Upload(ctx, client.NewUploadHandleFromString(signed))
	return err
}

func (s ljClientStore) Describe(ctx context.Context, req *search.DescribeRequest) (*search.DescribeResponse, error) {
	return s.c.Describe(ctx, req)
}

func (s ljClientStore) Query(ctx context.Context, req *search.SearchQuery) (*search.SearchResult, error) {
	return s.c.Query(ctx, req)
}
