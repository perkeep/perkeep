// The gencontrib binary generates an HTML list of git repository contributers.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

var contribStr = `<h1>Contributors</h1>

<p>Camlistore contributors include:</p>

<ul>
{{range .}}<li>{{if .URL}}<a href="{{.URL}}">{{index .Names 0}}</a>{{else}}{{index .Names 0}}{{end}}</li>
{{end}}
</ul>

<p>Want to help?  See <a href="/docs/contributing">contributing</a>.</p>
`

var urlsFile = flag.String("urls", "", "email → url map file")

func addURLs(idx map[string]*author) {
	if *urlsFile == "" {
		return
	}

	f, err := os.Open(*urlsFile)
	if err != nil {
		log.Fatal("couldn't open urls file:", *urlsFile)
	}

	dec := json.NewDecoder(f)
	var mapping map[string]interface{}
	err = dec.Decode(&mapping)
	if err != nil {
		log.Fatal("couldn't parse urls file:", err)
	}

	for email, url := range mapping {
		a := idx[email]
		if a != nil {
			a.URL = url.(string)
		} else {
			log.Printf("email %v is not a commiter", email)
		}
	}
}

type author struct {
	Names   []string
	Emails  []string
	Commits int
	URL     string
}

func (a *author) add(src *author) {
	if src == nil {
		return
	}
	a.Emails = append(a.Emails, src.Emails...)
	a.Names = append(a.Names, src.Names...)
	a.Commits += src.Commits
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

func shortlog() io.Reader {
	gitlog := exec.Command("git", "log")
	gitlogOut, err := gitlog.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	gitlog.Start()
	if err != nil {
		log.Fatal("couldn't run git log:", err)
	}

	shortlog := exec.Command("git", "shortlog", "-sen")
	shortlog.Stdin = gitlogOut
	shortlogOut, err := shortlog.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	shortlog.Start()
	if err != nil {
		log.Fatal("couldn't run git shortlog:", err)
	}

	return shortlogOut
}

func main() {
	flag.Parse()
	contribHtml, err := template.New("contrib").Parse(contribStr)
	if err != nil {
		log.Fatal("couldn't parse template")
	}

	byName := make(map[string]*author)
	byEmail := make(map[string]*author)
	authorMap := make(map[*author]bool)

	sl := shortlog()

	scn := bufio.NewScanner(sl)
	for scn.Scan() {
		name, email, commits, err := parseLine(scn.Text())
		if err != nil {
			log.Fatalf("couldn't parse line \"%v\": %v", scn.Text(), err)
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
		log.Fatal(err)
	}

	addURLs(byEmail)

	authors := Authors{}
	for a, _ := range authorMap {
		authors = append(authors, a)
	}

	sort.Sort(authors)

	if err := contribHtml.Execute(os.Stdout, authors); err != nil {
		log.Fatalf("executing template: %v", err)
	}
}
