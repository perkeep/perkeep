/*
Copyright 2016 The Camlistore Authors.

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
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"

	"go4.org/wkfs"
)

const githubSSHKeyGCS = "/gcs/camlistore-website-resource/id_github_camlistorebot_push"

var githubSSHKey string // Also used to detect whether we do the syncs to github

func githubSSHConfig(filename string) string {
	return `
Host github.com
  User git
  IdentityFile ` + filename + `
  IdentitiesOnly yes
`
}

func initGithubSyncing() error {
	if !inProd {
		return nil
	}
	keyData, err := wkfs.ReadFile(githubSSHKeyGCS)
	if err != nil {
		log.Print("Not syncing to github, because no ssh key found.")
		return nil
	}
	u, err := user.Current()
	if err != nil {
		return fmt.Errorf("can't look up user: %v", err)
	}
	dir, err := ioutil.TempDir("", "ssh")
	if err != nil {
		return fmt.Errorf("failed to create temp dir for github SSH key: %v", err)
	}
	keyFile := filepath.Join(dir, "id_github_camlistorebot_push")
	if err := ioutil.WriteFile(keyFile, keyData, 0600); err != nil {
		return fmt.Errorf("failed to create temp github SSH key: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(u.HomeDir, ".ssh"), 0700); err != nil {
		return fmt.Errorf("failed to create ssh config dir %v: %v", filepath.Join(u.HomeDir, ".ssh"), err)
	}
	if err := ioutil.WriteFile(
		filepath.Join(u.HomeDir, ".ssh", "id_github_camlistorebot_push"),
		[]byte(githubSSHConfig(keyFile)),
		0600); err != nil {
		return fmt.Errorf("failed to create github SSH config: %v", err)
	}
	githubSSHKey = keyFile
	return nil
}

// githubHEAD returns the hash of the HEAD commit on the github repo.
// The gerritHEAD argument is used as an optimization in the request to github:
// if it is found as the HEAD commit, the request is not counted in our
// non-authenticated requests quota.
func githubHEAD(gerritHEAD string) (string, error) {
	const (
		headAPI  = "https://api.github.com/repos/camlistore/camlistore/commits/HEAD"
		mimeType = "application/vnd.github.VERSION.sha"
	)
	req, err := http.NewRequest("GET", headAPI, nil)
	if err != nil {
		return "", err
	}
	req.Header.Add("If-None-Match", `"`+gerritHEAD+`"`)
	req.Header.Add("Accept", mimeType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return gerritHEAD, nil
	}
	ghCommit, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(ghCommit), nil
}

func syncToGithub(dir, gerritHEAD string) error {
	gh, err := githubHEAD(gerritHEAD)
	if err != nil {
		return fmt.Errorf("error looking up the github HEAD commit: %v", err)
	}
	if gh == gerritHEAD {
		return nil
	}
	cmd := execGit(dir, "push", "git@github.com:camlistore/camlistore.git", "master:master")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error running git push to github: %v\n%s", err, out)
	}
	return nil
}
