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

// Hacks for running camput as a child process on Android.

package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
)

var detectOnce sync.Once
var onAndroidCache bool

func dirExists(f string) bool {
	fi, err := os.Stat(f)
	return err == nil && fi.IsDir()
}

func initOnAndroid() {
	// Good enough heuristic. Suggestions welcome.
	onAndroidCache = dirExists("/data/data") && dirExists("/system/etc")
}

func onAndroid() bool {
	detectOnce.Do(initOnAndroid)
	return onAndroidCache
}

var pingRx = regexp.MustCompile(`\((.+?)\)`)

func androidLookupHost(host string) string {
	// Android has no "dig" or "host" tool, so use "ping -c 1". Ghetto.
	// $ ping -c 1 google.com
	// PING google.com (74.125.224.64) 56(84) bytes of data.
	c := make(chan string, 1)
	go func() {
		cmd := exec.Command("/system/bin/ping", "-c", "1", host)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			panic(err)
		}
		defer stdout.Close()
		if err := cmd.Start(); err != nil {
			log.Printf("Error resolving %q with ping: %v", host, err)
			c <- host
			return
		}
		defer func() {
			if p := cmd.Process; p != nil {
				p.Kill()
			}
		}()
		br := bufio.NewReader(stdout)
		line, err := br.ReadString('\n')
		if err != nil {
			c <- host
			return
		}
		if m := pingRx.FindStringSubmatch(line); m != nil {
			c <- m[1]
			return
		}
		c <- host
	}()
	return <-c
}

func dialFunc() func(network, addr string) (net.Conn, error) {
	return func(network, addr string) (net.Conn, error) {
		if onAndroid() {
			// Temporary laziness hack, avoiding doing a
			// cross-compiled Android cgo build.
			// Without cgo, package net uses
			// /etc/resolv.conf (not available on
			// Android).  We really want a cgo binary to
			// use Android's DNS cache, but it's kinda
			// hard/impossible to cross-compile for now.
			host, port, err := net.SplitHostPort(addr)
			if err == nil {
				return net.Dial(network, net.JoinHostPort(androidLookupHost(host), port))
			} else {
				log.Printf("couldn't split %q", addr)
			}
		}
		return net.Dial(network, addr)
	}
}

func tlsClientConfig() *tls.Config {
	const certDir = "/system/etc/security/cacerts"
	if fi, err := os.Stat(certDir); err != nil || !fi.IsDir() {
		return nil
	}
	pool := x509.NewCertPool()
	cfg := &tls.Config{RootCAs: pool}

	f, err := os.Open(certDir)
	if err != nil {
		return nil
	}
	defer f.Close()
	names, _ := f.Readdirnames(-1)
	for _, name := range names {
		if pem, err := ioutil.ReadFile(filepath.Join(certDir, name)); err == nil {
			pool.AppendCertsFromPEM(pem)
		}
	}
	return cfg
}
