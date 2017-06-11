package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/kisielk/gotool"
)

func TestImmutableVetter(t *testing.T) {

	var expected = `_testFiles/example.go:16:15: argument must be a constant string
_testFiles/example.go:20:19: argument must be a constant string
_testFiles/example.go:24:19: argument must be a constant string`

	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	specs := gotool.ImportPaths([]string{
		"myitcv.io/react/cmd/reactVet/_testFiles",
	})

	emsgs := vet(wd, specs)

	bfr := bytes.NewBuffer(nil)

	for _, msg := range emsgs {
		fmt.Fprintf(bfr, "%v\n", msg)
	}

	diff := strDiff(expected, bfr.String())
	if diff != "" {
		fmt.Println(bfr.String())
		t.Errorf("Expected no diff; got:\n%v", diff)
	}
}

func mustTmpFile(dir string, prefix string) *os.File {
	res, err := ioutil.TempFile(dir, prefix)

	if err != nil {
		panic(err)
	}

	return res
}

func strDiff(exp, act string) string {
	actFn := mustTmpFile("", "").Name()
	expFn := mustTmpFile("", "").Name()

	err := ioutil.WriteFile(actFn, []byte(act), 077)
	if err != nil {
		panic(err)
	}
	err = ioutil.WriteFile(expFn, []byte(exp), 077)
	if err != nil {
		panic(err)
	}

	cmd := exec.Command("diff", "-wu", expFn, actFn)
	res, err := cmd.CombinedOutput()
	if err != nil {
		ec := cmd.ProcessState.Sys().(syscall.WaitStatus)
		if ec.ExitStatus() != 1 {
			panic(err)
		}
	}

	return string(res)
}
