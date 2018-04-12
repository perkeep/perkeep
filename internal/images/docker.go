/*
Copyright 2018 The Perkeep Authors

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

package images // import "perkeep.org/internal/images"

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// TODO(mpl): refactor somewhere with pkg/test/dockertest

const thumbnailImage = "gcr.io/perkeep-containers/thumbnail"

func setUpThumbnailContainer() error {
	if !haveDocker() {
		return errors.New("'docker' command not found")
	}
	if ok, err := haveImage(thumbnailImage); !ok || err != nil {
		if err != nil {
			return fmt.Errorf("error running docker to check for %s: %v", thumbnailImage, err)
		}
		log.Printf("pulling docker image %s ...", thumbnailImage)
		if err := pull(thumbnailImage); err != nil {
			return fmt.Errorf("error pulling %s: %v", thumbnailImage, err)
		}
	}
	return nil
}

// haveDocker returns whether the "docker" command was found.
func haveDocker() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

func haveImage(name string) (ok bool, err error) {
	out, err := exec.Command("docker", "images", "--no-trunc").Output()
	if err != nil {
		return
	}
	return bytes.Contains(out, []byte(name)), nil
}

// Pull retrieves the docker image with 'docker pull'.
func pull(image string) error {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("docker", "pull", image)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String()
	// TODO(mpl): if it turns out docker respects conventions and the
	// "Authentication is required" message does come from stderr, then quit
	// checking stdout.
	if err != nil || stderr.Len() != 0 || strings.Contains(out, "Authentication is required") {
		return fmt.Errorf("docker pull failed: stdout: %s, stderr: %s, err: %v", out, stderr.String(), err)
	}
	return nil
}
