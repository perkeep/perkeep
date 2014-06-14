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

// This file adds the "server" subcommand to devcam, to run camlistored.

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
	sqlite   bool

	slow     bool
	throttle int
	latency  int

	fullIndexSync bool

	fullClosure bool
	mini        bool
	publish     bool // whether to start the publish handlers
	hello       bool // whether to build and start the hello demo app

	openBrowser      bool
	flickrAPIKey     string
	foursquareAPIKey string
	picasaAPIKey     string
	twitterAPIKey    string
	extraArgs        string // passed to camlistored
	// end of flag vars

	listen string // address + port to listen on
	root   string // the temp dir where blobs are stored
	env    *Env
}

func init() {
	cmdmain.RegisterCommand("server", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := &serverCmd{
			env: NewCopyEnv(),
		}
		flags.BoolVar(&cmd.all, "all", false, "Listen on all interfaces.")
		flags.StringVar(&cmd.hostname, "hostname", "", "Hostname to advertise, defaults to the hostname reported by the kernel.")
		flags.StringVar(&cmd.port, "port", "3179", "Port to listen on.")
		flags.BoolVar(&cmd.tls, "tls", false, "Use TLS.")
		flags.BoolVar(&cmd.wipe, "wipe", false, "Wipe the blobs on disk and the indexer.")
		flags.BoolVar(&cmd.debug, "debug", false, "Enable http debugging.")
		flags.BoolVar(&cmd.publish, "publish", true, "Enable publish handlers")
		flags.BoolVar(&cmd.hello, "hello", false, "Enable hello (demo) app")
		flags.BoolVar(&cmd.mini, "mini", false, "Enable minimal mode, where all optional features are disabled. (Currently just publishing)")

		flags.BoolVar(&cmd.mongo, "mongo", false, "Use mongodb as the indexer. Excludes -mysql, -postgres, -sqlite.")
		flags.BoolVar(&cmd.mysql, "mysql", false, "Use mysql as the indexer. Excludes -mongo, -postgres, -sqlite.")
		flags.BoolVar(&cmd.postgres, "postgres", false, "Use postgres as the indexer. Excludes -mongo, -mysql, -sqlite.")
		flags.BoolVar(&cmd.sqlite, "sqlite", false, "Use sqlite as the indexer. Excludes -mongo, -mysql, -postgres.")

		flags.BoolVar(&cmd.slow, "slow", false, "Add artificial latency.")
		flags.IntVar(&cmd.throttle, "throttle", 150, "If -slow, this is the rate in kBps, to which we should throttle.")
		flags.IntVar(&cmd.latency, "latency", 90, "If -slow, this is the added latency, in ms.")

		flags.BoolVar(&cmd.fullIndexSync, "fullindexsync", false, "Perform full sync to indexer on startup.")

		flags.BoolVar(&cmd.fullClosure, "fullclosure", false, "Use the ondisk closure library.")

		flags.BoolVar(&cmd.openBrowser, "openbrowser", false, "Open the start page on startup.")
		flags.StringVar(&cmd.flickrAPIKey, "flickrapikey", "", "The key and secret to use with the Flickr importer. Formatted as '<key>:<secret>'.")
		flags.StringVar(&cmd.foursquareAPIKey, "foursquareapikey", "", "The key and secret to use with the Foursquare importer. Formatted as '<clientID>:<clientSecret>'.")
		flags.StringVar(&cmd.picasaAPIKey, "picasakey", "", "The username and password to use with the Picasa importer. Formatted as '<username>:<password>'.")
		flags.StringVar(&cmd.twitterAPIKey, "twitterapikey", "", "The key and secret to use with the Twitter importer. Formatted as '<APIkey>:<APIsecret>'.")
		flags.StringVar(&cmd.root, "root", "", "A directory to store data in. Defaults to a location in the OS temp directory.")
		flags.StringVar(&cmd.extraArgs, "extraargs", "",
			"List of comma separated options that will be passed to camlistored")
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
	for _, v := range []bool{c.mongo, c.mysql, c.postgres, c.sqlite} {
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

func (c *serverCmd) setRoot() error {
	if c.root == "" {
		user := osutil.Username()
		if user == "" {
			return errors.New("Could not get username from environment")
		}
		c.root = filepath.Join(os.TempDir(), "camliroot-"+user, "port"+c.port)
	}
	log.Printf("Temp dir root is %v", c.root)
	if c.wipe {
		log.Printf("Wiping %v", c.root)
		if err := os.RemoveAll(c.root); err != nil {
			return fmt.Errorf("Could not wipe %v: %v", c.root, err)
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
	c.env.SetCamdevVars(false)
	setenv := func(k, v string) {
		c.env.Set(k, v)
	}
	if c.slow {
		setenv("DEV_THROTTLE_KBPS", fmt.Sprintf("%d", c.throttle))
		setenv("DEV_THROTTLE_LATENCY_MS", fmt.Sprintf("%d", c.latency))
	}
	if c.debug {
		setenv("CAMLI_HTTP_DEBUG", "1")
	}
	user := osutil.Username()
	if user == "" {
		return errors.New("Could not get username from environment")
	}
	setenv("CAMLI_FULL_INDEX_SYNC_ON_START", "false")
	if c.fullIndexSync {
		setenv("CAMLI_FULL_INDEX_SYNC_ON_START", "true")
	}
	setenv("CAMLI_DBNAME", "devcamli"+user)
	setenv("CAMLI_MYSQL_ENABLED", "false")
	setenv("CAMLI_MONGO_ENABLED", "false")
	setenv("CAMLI_POSTGRES_ENABLED", "false")
	setenv("CAMLI_SQLITE_ENABLED", "false")
	setenv("CAMLI_KVINDEX_ENABLED", "false")

	setenv("CAMLI_PUBLISH_ENABLED", strconv.FormatBool(c.publish))
	setenv("CAMLI_HELLO_ENABLED", strconv.FormatBool(c.hello))
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
	case c.sqlite:
		setenv("CAMLI_SQLITE_ENABLED", "true")
		setenv("CAMLI_INDEXER_PATH", "/index-sqlite/")
		if c.root == "" {
			panic("no root set")
		}
		setenv("CAMLI_DBNAME", filepath.Join(c.root, "sqliteindex.db"))
	default:
		setenv("CAMLI_KVINDEX_ENABLED", "true")
		setenv("CAMLI_INDEXER_PATH", "/index-kv/")
		if c.root == "" {
			panic("no root set")
		}
		setenv("CAMLI_DBNAME", filepath.Join(c.root, "kvindex.db"))
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

	setenv("CAMLI_DEV_CAMLI_ROOT", camliSrcRoot)
	setenv("CAMLI_AUTH", "devauth:pass3179")
	fullSuffix := func(name string) string {
		return filepath.Join(c.root, name)
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
	if c.flickrAPIKey != "" {
		setenv("CAMLI_FLICKR_ENABLED", "true")
		setenv("CAMLI_FLICKR_API_KEY", c.flickrAPIKey)
	}
	if c.foursquareAPIKey != "" {
		setenv("CAMLI_FOURSQUARE_ENABLED", "true")
		setenv("CAMLI_FOURSQUARE_API_KEY", c.foursquareAPIKey)
	}
	if c.picasaAPIKey != "" {
		setenv("CAMLI_PICASA_ENABLED", "true")
		setenv("CAMLI_PICASA_API_KEY", c.picasaAPIKey)
	}
	if c.twitterAPIKey != "" {
		setenv("CAMLI_TWITTER_ENABLED", "true")
		setenv("CAMLI_TWITTER_API_KEY", c.twitterAPIKey)
	}
	setenv("CAMLI_CONFIG_DIR", "config")
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
			"-dbname="+c.env.m["CAMLI_DBNAME"])
	case c.mysql:
		args = append(args,
			"-user=root",
			"-password=root",
			"-host=localhost",
			"-dbname="+c.env.m["CAMLI_DBNAME"])
	case c.sqlite:
		args = append(args,
			"-dbtype=sqlite",
			"-dbname="+c.env.m["CAMLI_DBNAME"])
	case c.mongo:
		args = append(args,
			"-dbtype=mongo",
			"-host=localhost",
			"-dbname="+c.env.m["CAMLI_DBNAME"])
	default:
		return nil
	}
	if c.wipe {
		args = append(args, "-wipe")
	} else {
		args = append(args, "-ignoreexists")
	}
	binPath := filepath.Join("bin", "camtool")
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
		templateDir := "dev-server-template"
		if _, err := os.Stat(templateDir); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		blobsDir := filepath.Join(c.root, "sha1")
		if err := cpDir(templateDir, blobsDir, nil); err != nil {
			return fmt.Errorf("Could not cp template blobs: %v", err)
		}
	}
	return nil
}

func (c *serverCmd) setFullClosure() error {
	if c.fullClosure {
		oldsvn := filepath.Join(c.root, filepath.FromSlash("tmp/closure-lib/.svn"))
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
		c.env.Set("CAMLI_DEV_CLOSURE_DIR", "third_party/closure/lib/closure")
	}
	return nil
}

func (c *serverCmd) RunCommand(args []string) error {
	if c.mini {
		c.publish = false
		c.hello = false
	}
	err := c.checkFlags(args)
	if err != nil {
		return cmdmain.UsageError(fmt.Sprint(err))
	}
	if !*noBuild {
		withSqlite = c.sqlite
		targets := []string{
			filepath.Join("server", "camlistored"),
			filepath.Join("cmd", "camtool"),
		}
		if c.hello {
			targets = append(targets, filepath.Join("app", "hello"))
		}
		for _, name := range targets {
			err := build(name)
			if err != nil {
				return fmt.Errorf("Could not build %v: %v", name, err)
			}
		}
	}
	if err := c.setRoot(); err != nil {
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

	log.Printf("Starting dev server on %v/ui/ with password \"pass3179\"\n",
		c.env.m["CAMLI_BASEURL"])

	camliBin := filepath.Join("bin", "camlistored")
	cmdArgs := []string{
		"-configfile=" + filepath.Join(camliSrcRoot, "config", "dev-server-config.json"),
		"-listen=" + c.listen,
		"-openbrowser=" + strconv.FormatBool(c.openBrowser),
	}
	if c.extraArgs != "" {
		cmdArgs = append(cmdArgs, strings.Split(c.extraArgs, ",")...)
	}
	return runExec(camliBin, cmdArgs, c.env)
}
