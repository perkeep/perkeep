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

// The updatelibrary command allows to selectively download
// from the closure library git repository (at a chosen revision)
// the resources needed by the Camlistore ui.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"camlistore.org/pkg/osutil"
)

const (
	gitRepo = "https://code.google.com/p/closure-library/"
	gitHash = "1389e13"
)

// fileList is the list of resources from the closure library that
// are required by the ui pages of Camlistore. It was generated
// from the error messages given in the javascript console.
// TODO(mpl): Better way to do that generation.
// See http://camlistore.org/issue/149
var fileList = []string{
	"AUTHORS",
	"LICENSE",
	"README",
	"closure/goog/a11y/aria/announcer.js",
	"closure/goog/a11y/aria/aria.js",
	"closure/goog/array/array.js",
	"closure/goog/asserts/asserts.js",
	"closure/goog/base.js",
	"closure/goog/css/common.css",
	"closure/goog/css/toolbar.css",
	"closure/goog/debug/debug.js",
	"closure/goog/debug/entrypointregistry.js",
	"closure/goog/debug/errorhandler.js",
	"closure/goog/debug/errorhandlerweakdep.js",
	"closure/goog/debug/error.js",
	"closure/goog/debug/logbuffer.js",
	"closure/goog/debug/logger.js",
	"closure/goog/debug/logrecord.js",
	"closure/goog/debug/tracer.js",
	"closure/goog/deps.js",
	"closure/goog/dom/a11y.js",
	"closure/goog/dom/browserfeature.js",
	"closure/goog/dom/classes.js",
	"closure/goog/dom/dom.js",
	"closure/goog/dom/tagname.js",
	"closure/goog/dom/vendor.js",
	"closure/goog/disposable/disposable.js",
	"closure/goog/disposable/idisposable.js",
	"closure/goog/events/browserevent.js",
	"closure/goog/events/browserfeature.js",
	"closure/goog/events/eventhandler.js",
	"closure/goog/events/event.js",
	"closure/goog/events/events.js",
	"closure/goog/events/eventtarget.js",
	"closure/goog/events/eventtype.js",
	"closure/goog/events/eventwrapper.js",
	"closure/goog/events/filedrophandler.js",
	"closure/goog/events/keycodes.js",
	"closure/goog/events/keyhandler.js",
	"closure/goog/events/listenable.js",
	"closure/goog/events/listener.js",
	"closure/goog/fx/transition.js",
	"closure/goog/iter/iter.js",
	"closure/goog/json/json.js",
	"closure/goog/math/box.js",
	"closure/goog/math/coordinate.js",
	"closure/goog/math/math.js",
	"closure/goog/math/rect.js",
	"closure/goog/math/size.js",
	"closure/goog/net/errorcode.js",
	"closure/goog/net/eventtype.js",
	"closure/goog/net/httpstatus.js",
	"closure/goog/net/wrapperxmlhttpfactory.js",
	"closure/goog/net/xhrio.js",
	"closure/goog/net/xmlhttpfactory.js",
	"closure/goog/net/xmlhttp.js",
	"closure/goog/object/object.js",
	"closure/goog/positioning/abstractposition.js",
	"closure/goog/positioning/anchoredposition.js",
	"closure/goog/positioning/anchoredviewportposition.js",
	"closure/goog/positioning/clientposition.js",
	"closure/goog/positioning/menuanchoredposition.js",
	"closure/goog/positioning/positioning.js",
	"closure/goog/positioning/viewportclientposition.js",
	"closure/goog/reflect/reflect.js",
	"closure/goog/string/string.js",
	"closure/goog/structs/collection.js",
	"closure/goog/structs/map.js",
	"closure/goog/structs/set.js",
	"closure/goog/structs/simplepool.js",
	"closure/goog/structs/structs.js",
	"closure/goog/style/bidi.js",
	"closure/goog/style/style.js",
	"closure/goog/timer/timer.js",
	"closure/goog/ui/button.js",
	"closure/goog/ui/buttonrenderer.js",
	"closure/goog/ui/buttonside.js",
	"closure/goog/ui/component.js",
	"closure/goog/ui/container.js",
	"closure/goog/ui/containerrenderer.js",
	"closure/goog/ui/controlcontent.js",
	"closure/goog/ui/control.js",
	"closure/goog/ui/controlrenderer.js",
	"closure/goog/ui/cssnames.js",
	"closure/goog/ui/custombuttonrenderer.js",
	"closure/goog/ui/decorate.js",
	"closure/goog/ui/idgenerator.js",
	"closure/goog/ui/menubutton.js",
	"closure/goog/ui/menubuttonrenderer.js",
	"closure/goog/ui/menuheader.js",
	"closure/goog/ui/menuheaderrenderer.js",
	"closure/goog/ui/menuitem.js",
	"closure/goog/ui/menuitemrenderer.js",
	"closure/goog/ui/menu.js",
	"closure/goog/ui/menurenderer.js",
	"closure/goog/ui/menuseparator.js",
	"closure/goog/ui/menuseparatorrenderer.js",
	"closure/goog/ui/nativebuttonrenderer.js",
	"closure/goog/ui/popupbase.js",
	"closure/goog/ui/popupmenu.js",
	"closure/goog/ui/registry.js",
	"closure/goog/ui/separator.js",
	"closure/goog/ui/textarea.js",
	"closure/goog/ui/textarearenderer.js",
	"closure/goog/ui/toolbarbutton.js",
	"closure/goog/ui/toolbarbuttonrenderer.js",
	"closure/goog/ui/toolbar.js",
	"closure/goog/ui/toolbarmenubutton.js",
	"closure/goog/ui/toolbarmenubuttonrenderer.js",
	"closure/goog/ui/toolbarrenderer.js",
	"closure/goog/ui/toolbarseparatorrenderer.js",
	"closure/goog/uri/uri.js",
	"closure/goog/uri/utils.js",
	"closure/goog/useragent/product.js",
	"closure/goog/useragent/useragent.js",
}

var (
	currentRevCmd  = newCmd("git", "rev-parse", "--short", "HEAD")
	gitFetchCmd    = newCmd("git", "fetch")
	gitResetCmd    = newCmd("git", "reset", gitHash)
	gitCloneCmd    = newCmd("git", "clone", "-n", gitRepo, ".")
	gitCheckoutCmd = newCmd("git", "checkout", "HEAD")
)

var (
	verbose       bool
	closureGitDir string // where we do the cloning/updating: camliRoot + tmp/closure-lib/
	destDir       string // install dir: camliRoot + third_party/closure/lib/
)

func init() {
	flag.BoolVar(&verbose, "verbose", false, "verbose output")
}

type command struct {
	program string
	args    []string
}

func newCmd(program string, args ...string) *command {
	return &command{program, args}
}

func (c *command) String() string {
	return fmt.Sprintf("%v %v", c.program, c.args)
}

// run runs the command and returns the output if it succeeds.
// On error, the process dies.
func (c *command) run() []byte {
	cmd := exec.Command(c.program, c.args...)
	b, err := cmd.Output()
	if err != nil {
		log.Fatalf("Could not run %v: %v", c, err)
	}
	return b
}

func resetAndCheckout() {
	gitResetCmd.run()
	args := gitCheckoutCmd.args
	args = append(args, fileList...)
	partialCheckoutCmd := newCmd(gitCheckoutCmd.program, args...)
	if verbose {
		log.Printf("%v", partialCheckoutCmd)
	}
	partialCheckoutCmd.run()
}

func update() {
	err := os.Chdir(closureGitDir)
	if err != nil {
		log.Fatalf("Could not chdir to %v: %v", closureGitDir, err)
	}
	output := strings.TrimSpace(string(currentRevCmd.run()))
	if string(output) != gitHash {
		gitFetchCmd.run()
	} else {
		if verbose {
			log.Printf("Already at rev %v, fetching not needed.", gitHash)
		}
	}
	resetAndCheckout()
}

func clone() {
	err := os.Chdir(closureGitDir)
	if err != nil {
		log.Fatalf("Could not chdir to %v: %v", closureGitDir, err)
	}
	gitCloneCmd.run()
	resetAndCheckout()
}

func cpDir(src, dst string) error {
	return filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		suffix, err := filepath.Rel(closureGitDir, path)
		if err != nil {
			return fmt.Errorf("Failed to find Rel(%q, %q): %v", closureGitDir, path, err)
		}
		base := fi.Name()
		if fi.IsDir() {
			if base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		return cpFile(path, filepath.Join(dst, suffix))
	})
}

func cpFile(src, dst string) error {
	sfi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !sfi.Mode().IsRegular() {
		return fmt.Errorf("cpFile can't deal with non-regular file %s", src)
	}

	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}

	df, err := os.Create(dst)
	if err != nil {
		return err
	}
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	n, err := io.Copy(df, sf)
	if err == nil && n != sfi.Size() {
		err = fmt.Errorf("copied wrong size for %s -> %s: copied %d; want %d", src, dst, n, sfi.Size())
	}
	cerr := df.Close()
	if err == nil {
		err = cerr
	}
	return err
}

func cpToDestDir() {
	err := os.RemoveAll(destDir)
	if err != nil {
		log.Fatalf("could not remove %v: %v", destDir, err)
	}
	err = cpDir(closureGitDir, destDir)
	if err != nil {
		log.Fatalf("could not cp %v to %v : %v", closureGitDir, destDir, err)
	}
}

// setup checks if the camlistore root can be found,
// then sets up closureGitDir and destDir, and returns whether
// we should clone or update in closureGitDir (depending on
// if a .git dir was found).
func setup() string {
	camliRootPath, err := osutil.GoPackagePath("camlistore.org")
	if err != nil {
		log.Fatal("Package camlistore.org not found in $GOPATH (or $GOPATH not defined).")
	}
	destDir = filepath.Join(camliRootPath, "third_party", "closure", "lib")
	closureGitDir = filepath.Join(camliRootPath, "tmp", "closure-lib")
	op := "update"
	_, err = os.Stat(closureGitDir)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(closureGitDir, 0755)
			if err != nil {
				log.Fatalf("Could not create %v: %v", closureGitDir, err)
			}
			op = "clone"
		} else {
			log.Fatalf("Could not stat %v: %v", closureGitDir, err)
		}
	}
	dotGitPath := filepath.Join(closureGitDir, ".git")
	_, err = os.Stat(dotGitPath)
	if err != nil {
		if os.IsNotExist(err) {
			op = "clone"
		} else {
			log.Fatalf("Could not stat %v: %v", dotGitPath, err)
		}
	}
	return op
}

func main() {
	flag.Parse()

	op := setup()
	switch op {
	case "clone":
		if verbose {
			fmt.Printf("cloning from %v at rev %v\n", gitRepo, gitHash)
		}
		clone()
	case "update":
		if verbose {
			fmt.Printf("updating to rev %v\n", gitHash)
		}
		update()
	default:
		log.Fatalf("Unsupported operation: %v", op)
	}

	cpToDestDir()
}
