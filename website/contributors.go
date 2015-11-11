package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"camlistore.org/pkg/osutil"
)

var urlsMap = map[string]author{
	"brad@danga.com":             {URL: "http://bradfitz.com/", Role: "founder, lead"},
	"bslatkin@gmail.com":         {URL: "http://www.onebigfluke.com/", Role: "co-founder"},
	"mathieu.lonjaret@gmail.com": {URL: "https://granivo.re/", Role: "has touched almost everything"},
	"zboogs@gmail.com":           {URL: "http://www.aaronboodman.com/", Role: "web interface lead"},
	"adg@golang.org":             {URL: "http://nf.id.au/"},
	"dustin@spy.net":             {URL: "http://dustin.sallings.org/"},
	"dan@erat.org":               {URL: "http://www.erat.org/"},
	"martine@danga.com":          {URL: "http://neugierig.org/"},
	"agl@golang.org":             {URL: "http://www.imperialviolet.org/"},
	"lsimon@commoner.com":        {Role: "original publishing UI"},
	"s@0x65.net":                 {URL: "https://0x65.net/"},
}

type author struct {
	Names   []string
	Emails  []string
	Commits int
	Role    string
	URL     string
}

// add merges src's fields into a's.
func (a *author) add(src *author) {
	if src == nil {
		return
	}
	a.Emails = append(a.Emails, src.Emails...)
	a.Names = append(a.Names, src.Names...)
	a.Commits += src.Commits
	if src.Role != "" {
		a.Role = src.Role
	}
	if src.URL != "" {
		a.URL = src.URL
	}
}

type Authors []*author

func (s Authors) Len() int           { return len(s) }
func (s Authors) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s Authors) Less(i, j int) bool { return s[i].Commits > s[j].Commits }

func parseLine(l string) (name, email string, commits int, err error) {
	t := strings.Split(strings.TrimSpace(l), "	")
	if len(t) < 2 {
		err = fmt.Errorf("line too short")
		return
	}
	i := strings.LastIndex(t[1], " ")
	email = strings.Trim(t[1][i+1:], "<>")
	name = t[1][:i]
	commits, err = strconv.Atoi(t[0])
	return
}

func gitShortlog() *exec.Cmd {
	if !*gitContainer {
		return exec.Command("/bin/bash", "-c", "git log | git shortlog -sen")
	}
	args := []string{"run", "--rm"}
	if inProd {
		args = append(args,
			"-v", "/var/camweb:/var/camweb",
			"--workdir="+prodSrcDir,
		)
	} else {
		hostRoot, err := osutil.GoPackagePath("camlistore.org")
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Using bind root of %q", hostRoot)
		args = append(args,
			"-v", hostRoot+":"+prodSrcDir,
			"--workdir="+prodSrcDir,
		)
	}
	args = append(args, "camlistore/git", "/bin/bash", "-c", "git log | git shortlog -sen")
	cmd := exec.Command("docker", args...)
	cmd.Stderr = os.Stderr
	return cmd
}

func genContribPage() ([]byte, error) {
	contribHTML := readTemplate("contrib.html")

	// The same committers could've authored commits with different emails/usersnames.
	// We index the authors by name and by email to try and merge graphs connected by
	// the "same-email" and "same-name" relation into one entity.
	byName := make(map[string]*author)
	byEmail := make(map[string]*author)
	authorMap := make(map[*author]bool)

	shortlog := gitShortlog()
	shortlogOut, err := shortlog.StdoutPipe()
	if err != nil {
		return nil, err
	}
	err = shortlog.Start()
	if err != nil {
		return nil, fmt.Errorf("couldn't run git shortlog: %v", err)
	}

	scn := bufio.NewScanner(shortlogOut)
	for scn.Scan() {
		name, email, commits, err := parseLine(scn.Text())
		if err != nil {
			return nil, err
		}

		a := &author{
			Emails:  []string{email},
			Names:   []string{name},
			Commits: commits,
		}

		a.add(byName[name])
		a.add(byEmail[email])
		for _, n := range a.Names {
			delete(authorMap, byName[n])
			byName[n] = a
		}
		for _, e := range a.Emails {
			delete(authorMap, byEmail[e])
			byEmail[e] = a
		}
		authorMap[a] = true
	}
	if scn.Err() != nil {
		return nil, scn.Err()
	}
	err = shortlog.Wait()
	if err != nil {
		return nil, fmt.Errorf("git shortlog failed: %v", err)
	}

	// Add URLs and roles
	for email, m := range urlsMap {
		a := byEmail[email]
		if a != nil {
			a.add(&m)
		}
	}

	authors := Authors{}
	for a, _ := range authorMap {
		authors = append(authors, a)
	}

	sort.Sort(authors)

	b := &bytes.Buffer{}
	err = contribHTML.Execute(b, authors)
	return b.Bytes(), err
}

// contribHandler returns a handler that serves the generated contributors page,
// or the static file handler if it couldn't run git for any reason.
func contribHandler() http.HandlerFunc {
	c, err := genContribPage()
	if err != nil {
		log.Printf("Couldn't generate contributors page: %v", err)
		log.Printf("Using static contributors page")
		return mainHandler
	}

	title := ""
	if m := h1TitlePattern.FindSubmatch(c); len(m) > 1 {
		title = string(m[1])
	}
	return func(rw http.ResponseWriter, req *http.Request) {
		servePage(rw, title, "", c)
	}
}
