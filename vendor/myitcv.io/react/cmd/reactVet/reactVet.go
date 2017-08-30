// reactVet is a vet program used to check the correctness of myitcv.io/react based packages.
//
//For more information see https://github.com/myitcv/react/wiki/reactVet
//
package main

import (
	"flag"
	"fmt"
	"go/build"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kisielk/gotool"
)

func main() {
	flag.Parse()

	wd, err := os.Getwd()
	if err != nil {
		fatalf("could not get the working directory")
	}

	specs := gotool.ImportPaths(flag.Args())

	emsgs := vet(wd, specs)

	for _, msg := range emsgs {
		fmt.Fprintf(os.Stderr, "%v\n", msg)
	}

	if len(emsgs) > 0 {
		os.Exit(1)
	}
}

func vet(wd string, specs []string) []vetErr {

	var vetErrs []vetErr

	for _, spec := range specs {

		bpkg, err := build.Import(spec, wd, 0)
		if err != nil {
			fatalf("unable to import %v relative to %v: %v", spec, wd, err)
		}

		rv := newReactVetter(bpkg, wd)
		rv.vetPackages()

		vetErrs = append(vetErrs, rv.errlist...)
	}

	for i := range vetErrs {
		rel, err := filepath.Rel(wd, vetErrs[i].pos.Filename)
		if err != nil {
			fatalf("relative path error, %v", err)
		}

		vetErrs[i].pos.Filename = rel
	}

	sort.Slice(vetErrs, func(i, j int) bool {

		l, r := vetErrs[i].pos, vetErrs[j].pos

		if v := strings.Compare(l.Filename, r.Filename); v != 0 {
			return v < 0
		}

		if l.Line != r.Line {
			return l.Line < r.Line
		}

		if l.Column != r.Column {
			return l.Column < r.Column
		}

		return vetErrs[i].msg < vetErrs[j].msg
	})

	return vetErrs
}

type vetErr struct {
	pos token.Position
	msg string
}

func (r vetErr) String() string {
	return fmt.Sprintf("%v:%v:%v: %v", r.pos.Filename, r.pos.Line, r.pos.Column, r.msg)
}

func fatalf(format string, args ...interface{}) {
	panic(fmt.Errorf(format, args...))
}
