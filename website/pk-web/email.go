/*
Copyright 2013 The Perkeep Authors

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
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/datastore"
	"github.com/mailgun/mailgun-go"

	"perkeep.org/internal/osutil"
)

var (
	emailNow       = flag.String("email_now", "", "[debug] if non-empty, this commit hash is emailed immediately, without starting the webserver.")
	mailgunCfgFile = flag.String("mailgun_config", "", "[optional] Mailgun JSON configuration for sending emails on new commits.")
	emailsTo       = flag.String("email_dest", "", "[optional] The email address for new commit emails.")
)

type mailgunCfg struct {
	Domain       string `json:"domain"`
	APIKey       string `json:"apiKey"`
	PublicAPIKey string `json:"publicAPIKey"`
}

// mailgun is for sending the camweb startup e-mail, and the commits e-mails. No
// e-mails are sent if it is nil. It is set in sendStartingEmail, and it is nil
// if mailgunCfgFile is not set.
var mailGun mailgun.Mailgun

func mailgunCfgFromGCS() (*mailgunCfg, error) {
	var cfg mailgunCfg
	data, err := fromGCS(*mailgunCfgFile)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("could not JSON decode website's mailgun config: %v", err)
	}
	return &cfg, nil
}

func startEmailCommitLoop(errc chan<- error) {
	if *emailsTo == "" {
		return
	}
	if *emailNow != "" {
		dir, err := osutil.GoPackagePath(prodDomain)
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
	if mailGun == nil {
		return nil
	}

	var body []byte
	if err := emailOnTimeout("git show", 2*time.Minute, func() error {
		cmd := execGit(dir, "show", nil, "show", hash)
		body, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("error running git show: %v\n%s", err, body)
		}
		return nil
	}); err != nil {
		return err
	}
	if !bytes.Contains(body, diffMarker) {
		// Boring merge commit. Don't email.
		return nil
	}

	var out []byte
	if err := emailOnTimeout("git show_pretty", 2*time.Minute, func() error {
		cmd := execGit(dir, "show_pretty", nil, "show", "--pretty=oneline", hash)
		out, err = cmd.Output()
		if err != nil {
			return fmt.Errorf("error running git show_pretty: %v\n%s", err, out)
		}
		return nil
	}); err != nil {
		return err
	}
	subj := out[41:] // remove hash and space
	if i := bytes.IndexByte(subj, '\n'); i != -1 {
		subj = subj[:i]
	}
	if len(subj) > 80 {
		subj = subj[:80]
	}

	contents := fmt.Sprintf(`

https://github.com/perkeep/perkeep/commit/%s

%s`, hash, body)

	m := mailGun.NewMessage(
		"noreply@perkeep.org",
		string(subj),
		contents,
		*emailsTo,
	)
	m.SetReplyTo("camlistore-commits@googlegroups.com")
	if _, _, err := mailGun.Send(m); err != nil {
		return fmt.Errorf("failed to send e-mail: %v", err)
	}
	return nil
}

var latestHash struct {
	sync.Mutex
	s string // hash of the most recent perkeep revision
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

	dir := pkSrcDir()

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

// emailOnTimeout runs fn in a goroutine. If fn is not done after d,
// a message about fnName is logged, and an e-mail about it is sent.
func emailOnTimeout(fnName string, d time.Duration, fn func() error) error {
	c := make(chan error, 1)
	go func() {
		c <- fn()
	}()
	select {
	case <-time.After(d):
		log.Printf("timeout for %s, sending e-mail about it", fnName)
		m := mailGun.NewMessage(
			"noreply@perkeep.org",
			"timeout for docker on pk-web",
			"Because "+fnName+" is stuck.",
			"mathieu.lonjaret@gmail.com",
		)
		if _, _, err := mailGun.Send(m); err != nil {
			return fmt.Errorf("failed to send docker restart e-mail: %v", err)
		}
		return nil
	case err := <-c:
		return err
	}
}

// execGit runs the git command with gitArgs. All the other arguments are only
// relevant if *gitContainer, in which case we run in a docker container.
func execGit(workdir string, containerName string, mounts map[string]string, gitArgs ...string) *exec.Cmd {
	var cmd *exec.Cmd
	if *gitContainer {
		removeContainer(containerName)
		args := []string{
			"run",
			"--rm",
			"--name=" + containerName,
		}
		for host, container := range mounts {
			args = append(args, "-v", host+":"+container+":ro")
		}
		args = append(args, []string{
			"-v", workdir + ":" + workdir,
			"--workdir=" + workdir,
			"camlistore/git",
			"git"}...)
		args = append(args, gitArgs...)
		cmd = exec.Command("docker", args...)
	} else {
		cmd = exec.Command("git", gitArgs...)
		cmd.Dir = workdir
	}
	return cmd
}

// GitCommit is a datastore entity to track which commits we've
// already emailed about.
type GitCommit struct {
	Emailed bool
}

func pollCommits(dir string) {
	if err := emailOnTimeout("git pull_origin", 5*time.Minute, func() error {
		cmd := execGit(dir, "pull_origin", nil, "pull", "origin")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("error running git pull origin master in %s: %v\n%s", dir, err, out)
		}
		return nil
	}); err != nil {
		log.Printf("%v", err)
		return
	}
	log.Printf("Ran git pull.")
	// TODO: see if .git/refs/remotes/origin/master
	// changed. (quicker than running recentCommits each time)

	hashes, err := recentCommits(dir)
	if err != nil {
		log.Print(err)
		return
	}
	if len(hashes) == 0 {
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
			key := datastore.NameKey("git_commit", commit, nil)
			var gc GitCommit
			if err := dsClient.Get(ctx, key, &gc); err == nil && gc.Emailed {
				log.Printf("Already emailed about commit %v; skipping", commit)
				knownCommit[commit] = true
				continue
			}
		}
		if err := emailCommit(dir, commit); err != nil {
			log.Printf("Error with commit e-mail: %v", err)
			continue
		}
		log.Printf("Emailed commit %s", commit)
		knownCommit[commit] = true
		if dsClient != nil {
			ctx := context.Background()
			key := datastore.NameKey("git_commit", commit, nil)
			_, err := dsClient.Put(ctx, key, &GitCommit{Emailed: true})
			log.Printf("datastore put of git_commit(%v): %v", commit, err)
		}
	}
}

func recentCommits(dir string) (hashes []string, err error) {
	var out []byte
	if err := emailOnTimeout("git log_origin_master", 2*time.Minute, func() error {
		cmd := execGit(dir, "log_origin_master", nil, "log", "--since=1 month ago", "--pretty=oneline", "origin/master")
		out, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("error running git log in %s: %v\n%s", dir, err, out)
		}
		return nil
	}); err != nil {
		return nil, err
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
