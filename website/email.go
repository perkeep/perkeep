/*
Copyright 2013 Google Inc.

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
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/cloud/datastore"

	"camlistore.org/pkg/osutil"
)

var (
	emailNow   = flag.String("email_now", "", "[debug] if non-empty, this commit hash is emailed immediately, without starting the webserver.")
	smtpServer = flag.String("smtp_server", "127.0.0.1:25", "[optional] SMTP server for sending emails on new commits.")
	emailsTo   = flag.String("email_dest", "", "[optional] The email address for new commit emails.")
)

func startEmailCommitLoop(errc chan<- error) {
	if *emailsTo == "" {
		return
	}
	if *emailNow != "" {
		dir, err := osutil.GoPackagePath("camlistore.org")
		if err != nil {
			log.Fatal(err)
		}
		if err := emailCommit(dir, *emailNow); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}
	go func() {
		errc <- commitEmailLoop()
	}()
}

// tokenc holds tokens for the /mailnow handler.
// Hitting /mailnow (unauthenticated) forces a 'git fetch origin
// master'.  Because it's unauthenticated, we don't want to allow
// attackers to force us to hit git. The /mailnow handler tries to
// take a token from tokenc.
var tokenc = make(chan bool, 3)

var fetchc = make(chan bool, 1)

var knownCommit = map[string]bool{} // commit -> true

var diffMarker = []byte("diff --git a/")

func emailCommit(dir, hash string) (err error) {
	cmd := execGit(dir, "show", hash)
	body, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Error runnning git show: %v\n%s", err, body)
	}
	if !bytes.Contains(body, diffMarker) {
		// Boring merge commit. Don't email.
		return nil
	}

	cmd = execGit(dir, "show", "--pretty=oneline", hash)
	out, err := cmd.Output()
	if err != nil {
		return
	}
	subj := out[41:] // remove hash and space
	if i := bytes.IndexByte(subj, '\n'); i != -1 {
		subj = subj[:i]
	}
	if len(subj) > 80 {
		subj = subj[:80]
	}

	cl, err := smtp.Dial(*smtpServer)
	if err != nil {
		return
	}
	defer cl.Quit()
	if err = cl.Mail("noreply@camlistore.org"); err != nil {
		return
	}
	if err = cl.Rcpt(*emailsTo); err != nil {
		return
	}
	wc, err := cl.Data()
	if err != nil {
		return
	}
	_, err = fmt.Fprintf(wc, `From: noreply@camlistore.org (Camlistore Commit)
To: %s
Subject: %s
Reply-To: camlistore@googlegroups.com

https://camlistore.googlesource.com/camlistore/+/%s

%s`, *emailsTo, subj, hash, body)
	if err != nil {
		return
	}
	return wc.Close()
}

var latestHash struct {
	sync.Mutex
	s string // hash of the most recent camlistore revision
}

// dsClient is our datastore client to track which commits we've
// emailed about. It's only non-nil in production.
var dsClient *datastore.Client

func commitEmailLoop() error {
	http.HandleFunc("/mailnow", mailNowHandler)

	var err error
	dsClient, err = datastore.NewClient(context.Background(), "camlistore-website")
	log.Printf("datastore = %v, %v", dsClient, err)

	go func() {
		for {
			select {
			case tokenc <- true:
			default:
			}
			time.Sleep(15 * time.Second)
		}
	}()

	dir := camSrcDir()

	http.HandleFunc("/latesthash", latestHashHandler)
	http.HandleFunc("/debug/email", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ds = %v, %v", dsClient, err)
	})

	for {
		pollCommits(dir)

		// Poll every minute or whenever we're forced with the
		// /mailnow handler.
		select {
		case <-time.After(1 * time.Minute):
		case <-fetchc:
			log.Printf("Polling git due to explicit trigger.")
		}
	}
}

func execGit(dir string, gitArgs ...string) *exec.Cmd {
	var cmd *exec.Cmd
	if *gitContainer {
		args := append([]string{
			"run",
			"--rm",
			"-v", dir + ":" + dir,
			"--workdir=" + dir,
			"camlistore/git",
			"git",
		}, gitArgs...)
		cmd = exec.Command("docker", args...)
	} else {
		cmd = exec.Command("git", gitArgs...)
		cmd.Dir = dir
	}
	return cmd
}

// GitCommit is a datastore entity to track which commits we've
// already emailed about.
type GitCommit struct {
	Emailed bool
}

func pollCommits(dir string) {
	cmd := execGit(dir, "fetch", "origin")
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Error running git fetch origin master in %s: %v\n%s", dir, err, out)
		return
	}
	log.Printf("Ran git fetch.")
	// TODO: see if .git/refs/remotes/origin/master
	// changed. (quicker than running recentCommits each time)

	hashes, err := recentCommits(dir)
	if err != nil {
		log.Print(err)
		return
	}
	latestHash.Lock()
	latestHash.s = hashes[0]
	latestHash.Unlock()
	for _, commit := range hashes {
		if knownCommit[commit] {
			continue
		}
		if dsClient != nil {
			ctx := context.Background()
			key := datastore.NewKey(ctx, "git_commit", commit, 0, nil)
			var gc GitCommit
			if err := dsClient.Get(ctx, key, &gc); err == nil && gc.Emailed {
				log.Printf("Already emailed about commit %v; skipping", commit)
				knownCommit[commit] = true
				continue
			}
		}
		if err := emailCommit(dir, commit); err == nil {
			log.Printf("Emailed commit %s", commit)
			knownCommit[commit] = true
			if dsClient != nil {
				ctx := context.Background()
				key := datastore.NewKey(ctx, "git_commit", commit, 0, nil)
				_, err := dsClient.Put(ctx, key, &GitCommit{Emailed: true})
				log.Printf("datastore put of git_commit(%v): %v", commit, err)
			}
		}
	}
}

func recentCommits(dir string) (hashes []string, err error) {
	cmd := execGit(dir, "log", "--since=1 month ago", "--pretty=oneline", "origin/master")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Error running git log in %s: %v\n%s", dir, err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		v := strings.SplitN(line, " ", 2)
		if len(v) > 1 {
			hashes = append(hashes, v[0])
		}
	}
	return
}

func mailNowHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case <-tokenc:
		log.Printf("/mailnow got a token")
	default:
		// Too many requests. Ignore.
		log.Printf("Ignoring /mailnow request; too soon.")
		return
	}
	select {
	case fetchc <- true:
		log.Printf("/mailnow triggered a git fetch")
	default:
	}
}

func latestHashHandler(w http.ResponseWriter, r *http.Request) {
	latestHash.Lock()
	defer latestHash.Unlock()
	fmt.Fprint(w, latestHash.s)
}
