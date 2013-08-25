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

// This program runs camlistored in "dev" mode,
// to facilitate hacking on camlistore.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/osutil"
)

type serverCmd struct {
	// start of flag vars
	all      bool
	hostname string
	port     string
	tls      bool
	wipe     bool
	debug    bool

	mongo    bool
	mysql    bool
	postgres bool
	memindex bool // memory index; default is kvfile

	slow     bool
	throttle int
	latency  int

	noBuild     bool
	fullClosure bool

	openBrowser bool
	// end of flag vars

	camliSrcRoot string // the camlistore source tree
	listen       string // address + port to listen on
	camliRoot    string // the temp dir where blobs are stored
}

func init() {
	cmdmain.RegisterCommand("server", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(serverCmd)
		flags.BoolVar(&cmd.all, "all", false, "Listen on all interfaces.")
		flags.StringVar(&cmd.hostname, "hostname", "", "Hostname to advertise, defaults to the hostname reported by the kernel.")
		flags.StringVar(&cmd.port, "port", "3179", "Port to listen on.")
		flags.BoolVar(&cmd.tls, "tls", false, "Use TLS.")
		flags.BoolVar(&cmd.wipe, "wipe", false, "Wipe the blobs on disk and the indexer.")
		flags.BoolVar(&cmd.debug, "debug", false, "Enable http debugging.")

		flags.BoolVar(&cmd.mongo, "mongo", false, "Use mongodb as the indexer. Excludes -mysql, -postgres, -memindex.")
		flags.BoolVar(&cmd.mysql, "mysql", false, "Use mysql as the indexer. Excludes -mongo, -postgres, -memindex.")
		flags.BoolVar(&cmd.postgres, "postgres", false, "Use postgres as the indexer. Excludes -mongo, -mysql, -memindex.")
		flags.BoolVar(&cmd.memindex, "memindex", false, "Use memory as the indexer. Excludes -mongo, -mysql, -postgres.")

		flags.BoolVar(&cmd.slow, "slow", false, "Add artificial latency.")
		flags.IntVar(&cmd.throttle, "throttle", 150, "If -slow, this is the rate in kBps, to which we should throttle.")
		flags.IntVar(&cmd.latency, "latency", 90, "If -slow, this is the added latency, in ms.")

		flags.BoolVar(&cmd.noBuild, "nobuild", false, "Do not rebuild anything.")
		flags.BoolVar(&cmd.fullClosure, "fullclosure", false, "Use the ondisk closure library.")

		flags.BoolVar(&cmd.openBrowser, "openbrowser", false, "Open the start page on startup.")
		return cmd
	})
}

func (c *serverCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: devcam [globalopts] server [serveropts]\n")
}

func (c *serverCmd) Examples() []string {
	return []string{
		"-wipe -mysql -fullclosure",
	}
}

func (c *serverCmd) Describe() string {
	return "run the stand-alone camlistored in dev mode."
}

func (c *serverCmd) checkFlags(args []string) error {
	if len(args) != 0 {
		c.Usage()
	}
	nindex := 0
	for _, v := range []bool{c.mongo, c.mysql, c.postgres, c.memindex} {
		if v {
			nindex++
		}
	}
	if nindex > 1 {
		return fmt.Errorf("Only one index option allowed")
	}

	if _, err := strconv.ParseInt(c.port, 0, 0); err != nil {
		return fmt.Errorf("Invalid -port value: %q", c.port)
	}
	return nil
}

func (c *serverCmd) build(name string) error {
	var target string
	switch name {
	case "camlistored":
		target = filepath.Join("camlistore.org", "server", "camlistored")
	case "camtool":
		target = filepath.Join("camlistore.org", "cmd", "camtool")
	default:
		return fmt.Errorf("Could not build, invalid target: %v", name)
	}
	binPath := filepath.Join(c.camliSrcRoot, "bin", name)
	fi, err := os.Stat(binPath)
	if err != nil {
		return fmt.Errorf("Could not stat %v: %v", binPath, err)
	}
	args := []string{
		"run", "make.go",
		"--quiet",
		"--embed_static=false",
		"--sqlite=false",
		fmt.Sprintf("--if_mods_since=%d", fi.ModTime().Unix()),
		"--targets=" + target,
	}
	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Error building %v: %v", target, err)
	}
	return nil
}

func (c *serverCmd) setCamliRoot() error {
	user := os.Getenv("USER")
	if user == "" {
		return errors.New("Could not get USER env var")
	}
	c.camliRoot = filepath.Join(os.TempDir(), "camliroot-"+user, "port"+c.port)
	if c.wipe {
		log.Printf("Wiping %v", c.camliRoot)
		if err := os.RemoveAll(c.camliRoot); err != nil {
			return fmt.Errorf("Could not wipe %v: %v", c.camliRoot, err)
		}
	}
	return nil
}

func (c *serverCmd) makeSuffixdir(fullpath string) {
	if err := os.MkdirAll(fullpath, 0755); err != nil {
		log.Fatalf("Could not create %v: %v", fullpath, err)
	}
}

func (c *serverCmd) setEnvVars() error {
	if c.slow {
		setenv("DEV_THROTTLE_KBPS", fmt.Sprintf("%d", c.throttle))
		setenv("DEV_THROTTLE_LATENCY_MS", fmt.Sprintf("%d", c.latency))
	}
	if c.debug {
		setenv("CAMLI_HTTP_DEBUG", "1")
	}
	user := os.Getenv("USER")
	if user == "" {
		return errors.New("Could not get USER env var")
	}
	setenv("CAMLI_DBNAME", "devcamli"+user)
	setenv("CAMLI_MYSQL_ENABLED", "false")
	setenv("CAMLI_MONGO_ENABLED", "false")
	setenv("CAMLI_POSTGRES_ENABLED", "false")
	setenv("CAMLI_KVINDEX_ENABLED", "false")
	setenv("CAMLI_MEMINDEX_ENABLED", "false")
	switch {
	case c.mongo:
		setenv("CAMLI_MONGO_ENABLED", "true")
		setenv("CAMLI_INDEXER_PATH", "/index-mongo/")
	case c.postgres:
		setenv("CAMLI_POSTGRES_ENABLED", "true")
		setenv("CAMLI_INDEXER_PATH", "/index-postgres/")
	case c.mysql:
		setenv("CAMLI_MYSQL_ENABLED", "true")
		setenv("CAMLI_INDEXER_PATH", "/index-mysql/")
	case c.memindex:
		setenv("CAMLI_MEMINDEX_ENABLED", "true")
		setenv("CAMLI_INDEXER_PATH", "/index-mem/")
	default:
		setenv("CAMLI_KVINDEX_ENABLED", "true")
		setenv("CAMLI_INDEXER_PATH", "/index-kv/")
		if c.camliRoot == "" {
			panic("no camliRoot set")
		}
		setenv("CAMLI_KVINDEX_FILE", filepath.Join(c.camliRoot, "kvindex.db"))
	}

	base := "http://localhost:" + c.port
	c.listen = "127.0.0.1:" + c.port
	if c.all {
		c.listen = "0.0.0.0:" + c.port
		if c.hostname == "" {
			hostname, err := os.Hostname()
			if err != nil {
				return fmt.Errorf("Could not get system hostname: %v", err)
			}
			base = "http://" + hostname + ":" + c.port
		} else {
			base = "http://" + c.hostname + ":" + c.port
		}
	}
	setenv("CAMLI_TLS", "false")
	if c.tls {
		base = strings.Replace(base, "http://", "https://", 1)
		setenv("CAMLI_TLS", "true")
	}
	setenv("CAMLI_BASEURL", base)

	setenv("CAMLI_DEV_CAMLI_ROOT", c.camliSrcRoot)
	setenv("CAMLI_AUTH", "userpass:camlistore:pass"+c.port+":+localhost")
	setenv("CAMLI_ADVERTISED_PASSWORD", "pass"+c.port)
	fullSuffix := func(name string) string {
		return filepath.Join(c.camliRoot, name)
	}
	suffixes := map[string]string{
		"CAMLI_ROOT":          fullSuffix("bs"),
		"CAMLI_ROOT_SHARD1":   fullSuffix("s1"),
		"CAMLI_ROOT_SHARD2":   fullSuffix("s2"),
		"CAMLI_ROOT_REPLICA1": fullSuffix("r1"),
		"CAMLI_ROOT_REPLICA2": fullSuffix("r2"),
		"CAMLI_ROOT_REPLICA3": fullSuffix("r3"),
		"CAMLI_ROOT_CACHE":    fullSuffix("cache"),
		"CAMLI_ROOT_ENCMETA":  fullSuffix("encmeta"),
		"CAMLI_ROOT_ENCBLOB":  fullSuffix("encblob"),
	}
	for k, v := range suffixes {
		c.makeSuffixdir(v)
		setenv(k, v)
	}
	setenv("CAMLI_PORT", c.port)
	setenv("CAMLI_SECRET_RING", filepath.Join(c.camliSrcRoot,
		filepath.FromSlash("pkg/jsonsign/testdata/test-secring.gpg")))
	return nil
}

func (c *serverCmd) setupIndexer() error {
	args := []string{"dbinit"}
	switch {
	case c.postgres:
		args = append(args,
			"-dbtype=postgres",
			"-user=postgres",
			"-password=postgres",
			"-host=localhost",
			"-dbname="+os.Getenv("CAMLI_DBNAME"))
	case c.mysql:
		args = append(args,
			"-user=root",
			"-password=root",
			"-host=localhost",
			"-dbname="+os.Getenv("CAMLI_DBNAME"))
	default:
		return nil
	}
	if c.wipe {
		args = append(args, "-wipe")
	} else {
		args = append(args, "-ignoreexists")
	}
	binPath := filepath.Join(c.camliSrcRoot, "bin", "camtool")
	cmd := exec.Command(binPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Could not run camtool dbinit: %v", err)
	}
	return nil
}

func (c *serverCmd) syncTemplateBlobs() error {
	if c.wipe {
		templateDir := filepath.Join(c.camliSrcRoot, "dev-server-template")
		if _, err := os.Stat(templateDir); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		blobsDir := filepath.Join(c.camliRoot, "sha1")
		if err := cpDir(templateDir, blobsDir, nil); err != nil {
			return fmt.Errorf("Could not cp template blobs: %v", err)
		}
	}
	return nil
}

func (c *serverCmd) setFullClosure() error {
	if c.fullClosure {
		oldsvn := filepath.Join(c.camliRoot, filepath.FromSlash("tmp/closure-lib/.svn"))
		if err := os.RemoveAll(oldsvn); err != nil {
			return fmt.Errorf("Could not remove svn checkout of closure-lib %v: %v",
				oldsvn, err)
		}
		log.Println("Updating closure library...")
		args := []string{"run", "third_party/closure/updatelibrary.go", "-verbose"}
		cmd := exec.Command("go", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("Could not run updatelibrary.go: %v", err)
		}
		setenv("CAMLI_DEV_CLOSURE_DIR", "third_party/closure/lib/closure")
	}
	return nil
}

func (c *serverCmd) RunCommand(args []string) error {
	err := c.checkFlags(args)
	if err != nil {
		return cmdmain.UsageError(fmt.Sprint(err))
	}
	c.camliSrcRoot, err = osutil.GoPackagePath("camlistore.org")
	if err != nil {
		return errors.New("Package camlistore.org not found in $GOPATH (or $GOPATH not defined).")
	}
	err = os.Chdir(c.camliSrcRoot)
	if err != nil {
		return fmt.Errorf("Could not chdir to %v: %v", c.camliSrcRoot, err)
	}
	if !c.noBuild {
		for _, name := range []string{"camlistored", "camtool"} {
			err := c.build(name)
			if err != nil {
				return fmt.Errorf("Could not build %v: %v", name, err)
			}
		}
	}
	if err := c.setCamliRoot(); err != nil {
		return fmt.Errorf("Could not setup the camli root: %v", err)
	}
	if err := c.setEnvVars(); err != nil {
		return fmt.Errorf("Could not setup the env vars: %v", err)
	}
	if err := c.setupIndexer(); err != nil {
		return fmt.Errorf("Could not setup the indexer: %v", err)
	}
	if err := c.syncTemplateBlobs(); err != nil {
		return fmt.Errorf("Could not copy the template blobs: %v", err)
	}
	if err := c.setFullClosure(); err != nil {
		return fmt.Errorf("Could not setup the closure lib: %v", err)
	}

	log.Printf("Starting dev server on %v/ui/ with password \"pass%v\"\n",
		os.Getenv("CAMLI_BASEURL"), c.port)

	camliBin := filepath.Join(c.camliSrcRoot, "bin", "camlistored")
	cmdArgs := []string{
		"-configfile=" + filepath.Join(c.camliSrcRoot, "config", "dev-server-config.json"),
		"-listen=" + c.listen,
		"-openbrowser=" + strconv.FormatBool(c.openBrowser)}
	cmd := exec.Command(camliBin, cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Could not start camlistored: %v", err)
	}
	go handleSignals(cmd.Process)
	return cmd.Wait()
}
