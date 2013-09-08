// Program envvardoc will verify all referenced environment variables in go
// source are properly documented.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
)

var (
	srcDirs = flag.String("srcDirs", "cmd,dev,pkg,server",
		"comma separated source directories")
	doc = flag.String("doc", "doc/environment-vars.txt",
		"file containing environment variable documentation")
	all      = flag.Bool("all", false, "show all environment vars found")
	prefixes = flag.String("prefixes", "CAM,DEV,AWS",
		"comma-separated list of env var prefixes we care about. Empty implies all")

	docVar         = regexp.MustCompile(`^(\w+) \(.+?\):$`)
	literalEnvVar  = regexp.MustCompile(`os.Getenv\("(\w+)"\)`)
	variableEnvVar = regexp.MustCompile(`os.Getenv\((\w+)\)`)
)

type pos struct {
	line int
	path string
}

func (p pos) String() string {
	return fmt.Sprintf("%s:%d", p.path, p.line)
}

type varMap map[string][]pos

func sortedKeys(m varMap) []string {
	keys := make([]string, 0, len(m))
	for k, _ := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type envCollector struct {
	literals   varMap
	variables  varMap
	documented map[string]struct{}
}

func newEncCollector() *envCollector {
	return &envCollector{
		literals:   varMap{},
		variables:  varMap{},
		documented: map[string]struct{}{},
	}
}

func (ec *envCollector) findEnvVars(path string, r io.Reader) error {
	scanner := bufio.NewScanner(r)
	line := 1
	for scanner.Scan() {
		l := scanner.Text()
		m := literalEnvVar.FindStringSubmatch(l)
		if len(m) == 2 {
			p := pos{line: line, path: path}
			ec.literals[m[1]] = append(ec.literals[m[1]], p)
		}

		m = variableEnvVar.FindStringSubmatch(l)
		if len(m) == 2 {
			p := pos{line: line, path: path}
			ec.variables[m[1]] = append(ec.variables[m[1]], p)
		}
		line++
	}
	return scanner.Err()
}

func (ec *envCollector) findDocVars(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		l := scanner.Text()
		m := docVar.FindStringSubmatch(l)
		if len(m) == 2 {
			ec.documented[m[1]] = struct{}{}
		}
	}
	return scanner.Err()
}

func (ec *envCollector) walk(path string, info os.FileInfo, err error) error {
	if err != nil {
		return err
	}

	if info.IsDir() || !strings.HasSuffix(path, ".go") {
		return nil
	}

	r, err := os.Open(path)
	if err != nil {
		return err
	}
	defer r.Close()
	return ec.findEnvVars(path, r)
}

func printMap(header string, m varMap) {
	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 1, ' ', 0)
	fmt.Fprintln(w, header)
	for _, k := range sortedKeys(m) {
		for _, pos := range m[k] {
			fmt.Fprintf(w, "%s\t%s\n", k, pos)
		}
	}
	w.Flush()
}

func (ec *envCollector) printAll() {
	fmt.Println("All environment variables")
	printMap("Literal\tLocation", ec.literals)
	fmt.Println()
	printMap("Variable\tLocation", ec.variables)
}

func (ec *envCollector) printUndocumented(prefixes []string) bool {
	missing := varMap{}
	for k, v := range ec.literals {
		if _, ok := ec.documented[k]; !ok {
			keep := false
			for _, p := range prefixes {
				if strings.HasPrefix(k, p) {
					keep = true
					break
				}
			}
			if keep || len(prefixes) == 0 {
				missing[k] = v
			}
		}
	}

	if len(missing) != 0 {
		printMap("Undocumented\tLocation", missing)
	} else {
		fmt.Println("All environment variables are documented")
	}
	return len(missing) != 0
}

func main() {
	flag.Parse()
	ec := newEncCollector()

	r, err := os.Open(*doc)
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()
	err = ec.findDocVars(r)
	if err != nil {
		log.Fatal(err)
	}

	for _, dn := range strings.Split(*srcDirs, ",") {
		err := filepath.Walk(dn, ec.walk)
		if err != nil {
			log.Fatal(err)
		}
	}

	if *all {
		ec.printAll()
	} else {
		if ec.printUndocumented(strings.Split(*prefixes, ",")) {
			os.Exit(1)
		}
	}
}
