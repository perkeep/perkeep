package gocheck

import (
	"testing"
	"flag"
	"fmt"
)

// -----------------------------------------------------------------------
// Test suite registry.

var allSuites []interface{}

// Register the given value as a test suite to be run.  Any methods starting
// with the Test prefix in the given value will be considered as a test to
// be run.
func Suite(suite interface{}) interface{} {
	allSuites = append(allSuites, suite)
	return suite
}


// -----------------------------------------------------------------------
// Public running interface.

var filterFlag = flag.String("gocheck.f", "",
	"Regular expression selecting what to run")
var verboseFlag = flag.Bool("gocheck.v", false,
	"Verbose mode")
var streamFlag = flag.Bool("gocheck.vv", false,
	"Super verbose mode (disables output caching)")


// Run all test suites registered with the Suite() function, printing
// results to stdout, and reporting any failures back to the 'testing'
// module.
func TestingT(testingT *testing.T) {
	result := RunAll(&RunConf{Filter: *filterFlag, Verbose: *verboseFlag, Stream: *streamFlag})
	println(result.String())
	if !result.Passed() {
		testingT.Fail()
	}
}

// Run all test suites registered with the Suite() function, using the
// given run configuration.
func RunAll(runConf *RunConf) *Result {
	result := Result{}
	for _, suite := range allSuites {
		result.Add(Run(suite, runConf))
	}
	return &result
}

// Run the given test suite using the provided run configuration.
func Run(suite interface{}, runConf *RunConf) *Result {
	runner := newSuiteRunner(suite, runConf)
	return runner.run()
}


// -----------------------------------------------------------------------
// Result methods.

func (r *Result) Add(other *Result) {
	r.Succeeded += other.Succeeded
	r.Skipped += other.Skipped
	r.Failed += other.Failed
	r.Panicked += other.Panicked
	r.FixturePanicked += other.FixturePanicked
	r.Missed += other.Missed
}

func (r *Result) Passed() bool {
	return (r.Failed == 0 && r.Panicked == 0 &&
		r.FixturePanicked == 0 && r.Missed == 0 &&
		r.RunError == nil)
}

func (r *Result) String() string {
	if r.RunError != nil {
		return "ERROR: " + r.RunError.String()
	}

	var value string
	if r.Failed == 0 && r.Panicked == 0 && r.FixturePanicked == 0 &&
		r.Missed == 0 {
		value = "OK: "
	} else {
		value = "OOPS: "
	}
	value += fmt.Sprintf("%d passed", r.Succeeded)
	if r.Skipped != 0 {
		value += fmt.Sprintf(", %d skipped", r.Skipped)
	}
	if r.ExpectedFailures != 0 {
		value += fmt.Sprintf(", %d expected failures", r.ExpectedFailures)
	}
	if r.Failed != 0 {
		value += fmt.Sprintf(", %d FAILED", r.Failed)
	}
	if r.Panicked != 0 {
		value += fmt.Sprintf(", %d PANICKED", r.Panicked)
	}
	if r.FixturePanicked != 0 {
		value += fmt.Sprintf(", %d FIXTURE-PANICKED", r.FixturePanicked)
	}
	if r.Missed != 0 {
		value += fmt.Sprintf(", %d MISSED", r.Missed)
	}
	return value
}
