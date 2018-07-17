// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

/*

reactGen is a go generate generator that helps to automate the process of
writing GopherJS React web applications.

For more information see https://github.com/myitcv/react/wiki

*/
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"myitcv.io/gogenerate"
)

const (
	reactGenCmd = "reactGen"

	jsPkg = "github.com/gopherjs/gopherjs/js"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix(reactGenCmd + ": ")

	flag.Usage = usage
	flag.Parse()

	wd, err := os.Getwd()
	if err != nil {
		fatalf("unable to get working directory: %v", err)
	}

	if fInit.val != nil {
		mainInit(wd)
	} else {
		mainGen(wd)
	}
}

func mainInit(wd string) {
	doinit(wd, *fInit.val)
}

func mainGen(wd string) {
	gogenerate.DefaultLogLevel(fGoGenLog, gogenerate.LogFatal)

	envFile, ok := os.LookupEnv(gogenerate.GOFILE)
	if !ok {
		fatalf("env not correct; missing %v", gogenerate.GOFILE)
	}

	dirFiles, err := gogenerate.FilesContainingCmd(wd, reactGenCmd)
	if err != nil {
		fatalf("could not determine if we are the first file: %v", err)
	}

	if dirFiles == nil {
		fatalf("cannot find any files containing the %v directive", reactGenCmd)
	}

	if dirFiles[envFile] != 1 {
		fatalf("expected a single occurrence of %v directive in %v. Got: %v", reactGenCmd, envFile, dirFiles)
	}

	license, err := gogenerate.CommentLicenseHeader(fLicenseFile)
	if err != nil {
		fatalf("could not comment license file: %v", err)
	}

	// if we get here, we know we are the first file...

	dogen(wd, license)
}

func fatalf(format string, args ...interface{}) {
	panic(fmt.Errorf(format, args...))
}

func infof(format string, args ...interface{}) {
	if *fGoGenLog == string(gogenerate.LogInfo) {
		log.Printf(format, args...)
	}
}
