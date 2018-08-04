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

// Command build_syno builds and packages Perkeep for Synology appliances.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

var (
	// TODO(mpl): include more arches.
	flagArch       = flag.String("arch", "x64", "Synology architecture to build for. Possible values are limited to x64 or 6281 for now.")
	flagDsm        = flag.String("dsm", "6.2", "DSM version to build for.")
	flagPerkeepRev = flag.String("pkrev", "8b537a66307cf41a659786f1a898c77b46303601", "git revision of Perkeep to package")
	flagNoCache    = flag.Bool("nocache", false, "build docker image with --no-cache")
)

var pwd string

func main() {
	flag.Parse()
	if *flagArch != "x64" && *flagArch != "6281" {
		log.Fatalf("unsupported architecture: %v", *flagArch)
	}

	newCwd := filepath.Dir(flag.Arg(0))
	if err := os.Chdir(newCwd); err != nil {
		log.Fatalf("error changing dir to %v: %v", newCwd, err)
	}
	var err error
	pwd, err = os.Getwd()
	if err != nil {
		log.Fatalf("error getting current directory: %v", err)
	}

	gobin := "/go/bin"
	goarch := "amd64"
	// TODO(mpl): figure out the correspondance between all the other arches and the
	// values for the go vars.
	if *flagArch == "6281" {
		gobin = "/go/bin/linux_arm"
		goarch = "arm"
	}

	// we start by building only the first stage of the Dockerfile, even though
	// we're eventually going to build all the stages. We do that in order to tag the
	// first stage (with its relevant arch in the tag), and therefore so it does not
	// get removed by 'docker rmi'.

	// docker build --target pkbuild -t perkeep/synology-pkbuild-x64 --build-arg arch=x64 --build-arg dsm=6.2 --build-arg gobin=/go/bin --build-arg goarch=amd64 --build-arg perkeep_version=8b537a66307cf41a659786f1a898c77b46303601 .
	buildImage(gobin, goarch, true)

	// docker build -t perkeep/synology-x64 --build-arg arch=x64 --build-arg dsm=6.2 --build-arg gobin=/go/bin --build-arg goarch=amd64 --build-arg perkeep_version=8b537a66307cf41a659786f1a898c77b46303601 .
	buildImage(gobin, goarch, false)

	if err := os.MkdirAll(filepath.Join(pwd+"/out"), 0755); err != nil {
		log.Fatalf("Error creating out dir: %v", err)
	}

	// the actual building step (./pkgscripts/PkgCreate.py) can't be in the
	// Dockerfile since it's doing higher privilege stuff like chroot and mounts.
	// Which is why we run it as the last step, and in privileged mode.

	// TODO(mpl): we might be able to "just" run a furtherly modified version of
	// perkeep/SynoBuildConf/install because at the end of the day, we've already built the
	// binaries, and all we need to do is package them in an .spk. But that would require
	// further understanding and rewriting of the various pieces involved, and I don't think
	// it's worth spending time on it for now.
	// Although if we manage that, another win is that then we can skip EnvDeploy,
	// which means avoiding a heavy download.

	// For runPkgCreate, you need to fetch the GPG keys at
	// https://drive.google.com/drive/folders/1P95Lk1U6nA6kaVaxKRPi4Dv2Het-CWlY?usp=sharing
	// and add them to $HOME/keys/perkeep-synology , which is then mounted to the container.
	runPkgCreate()
}

func buildImage(gobin, goarch string, firstStageOnly bool) {
	args := []string{"build"}
	if *flagNoCache {
		args = append(args, "--no-cache")
	}
	if firstStageOnly {
		args = append(args, "--target", "pkbuild",
			"-t", "perkeep/synology-pkbuild-"+*flagArch)
	} else {
		args = append(args, "-t", "perkeep/synology-"+*flagArch)
	}
	args = append(args,
		"--build-arg", "arch="+*flagArch,
		"--build-arg", "dsm="+*flagDsm,
		"--build-arg", "gobin="+gobin,
		"--build-arg", "goarch="+goarch,
		"--build-arg", "perkeep_version="+*flagPerkeepRev,
		".")
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if firstStageOnly {
			log.Fatalf("Error building first stage of synology image: %v, %s", err, out)
		}
		log.Fatalf("Error building synology image: %v, %s", err, out)
	}
	fmt.Println(string(out))
}

// runPkgCreate runs the actual build/install step provided in the DSM toolkit: ./pkgscripts/PkgCreate.py
// It can't be run during the build stage (in the Dockerfile), because it does
// privileged operations (at least chroot) so it is run as a privileged container.
// It requires the GPG keys at
// https://drive.google.com/drive/folders/1P95Lk1U6nA6kaVaxKRPi4Dv2Het-CWlY?usp=sharing
func runPkgCreate() {
	// To test the equivalent from the shell:
	// docker run --rm -i -t --privileged -v $PWD/out:/toolkit/result_spk -v $HOME/keys/perkeep-synology:/toolkit/build_env/ds.6281-6.2/root/.gnupg perkeep/synology ./pkgscripts/PkgCreate.py -v 6.2 -p x64 -x0 -c --print-log --build-opt="-J" perkeep
	build_env := "ds." + *flagArch + "-" + *flagDsm
	// TODO(mpl): test discrete --cap-add flags when I have a docker that supports them.
	cmd := exec.Command("docker", "run", "--rm", "--privileged",
		"-v", filepath.Join(pwd+"/out")+":/toolkit/result_spk",
		"-v", filepath.Join(os.Getenv("HOME"), "keys", "perkeep-synology")+":/toolkit/build_env/"+build_env+"/root/.gnupg",
		"perkeep/synology-"+*flagArch,
		"./pkgscripts/PkgCreate.py", "-p", *flagArch, "-v", *flagDsm, "-x0", "-c", "--print-log", `--build-opt="-J"`, "perkeep")
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("Error building synology package: %v, %s", err, out)
	}
	fmt.Println(string(out))
}
