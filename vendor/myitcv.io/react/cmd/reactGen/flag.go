package main

import (
	"flag"
	"fmt"
	"os"

	"myitcv.io/gogenerate"
)

var (
	fLicenseFile = gogenerate.LicenseFileFlag()
	fGoGenLog    = gogenerate.LogFlag()
	fCore        = flag.Bool("core", false, "indicates we are generating for a core component (only do props expansion)")
	fInit        initFlag
)

type initFlag struct {
	val *string
}

func (f *initFlag) String() string {
	return "(does not have a default value)"
}

func (f *initFlag) Set(s string) error {
	f.val = &s
	return nil
}

func init() {
	flag.Var(&fInit, "init", "create a GopherJS React application using the specified template (see below)")
}

func usage() {
	f := func(format string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, format, args...)
	}

	l := func(args ...interface{}) {
		fmt.Fprintln(os.Stderr, args...)
	}

	l("Usage:")
	f("\t%v [-init <template>]\n", os.Args[0])
	f("\t%v [-gglog <log_level>] [-licenseFile <filepath>] [-core]\n", os.Args[0])
	l()

	flag.PrintDefaults()

	l()
	l("The flag -init only understands a single value for now: minimal. This is a minimal")
	l("Gopher React application.")
	l()
	l("When -init is not specified, it is assumed that reactGen is being called indirectly")
	l("via go generate. The options for -gglog and -licenseFile would therefore be set in")
	l("via the //go:generate directives. See https://blog.golang.org/generate for more details.")
}
