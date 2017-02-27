/*
Copyright 2016 The Camlistore Authors

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

// Command monthly builds the tarballs and zip archives for all the monthly
// released Camlistore downloads. That is: source zip, linux and darwin tarballs,
// and windows zip. These files are then uploaded to the dedicated repository, as
// well as a file with their checksum, for each of them. Finally, the template page
// to serve these downloads with camweb is generated.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/osutil"

	"cloud.google.com/go/storage"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

var (
	flagRev       = flag.String("rev", "", "Camlistore revision to build (tag or commit hash). For development purposes, you can instead specify the path to a local Camlistore source tree from which to build, with the form \"WIP:/path/to/dir\".")
	flagDate      = flag.String("date", "", "The release date to use in the file names to be uploaded, in the YYYYMMDD format. Defaults to today's date.")
	flagUpload    = flag.Bool("upload", true, "Upload all the generated tarballs and zip archives.")
	flagSkipGen   = flag.Bool("skipgen", false, "Do not recreate the release tarballs, and directly use the ones found in camlistore.org/misc/docker/release. Use -upload=false and -skipgen=true to only generate the monthly release page.")
	flagStatsFrom = flag.String("stats_from", "", "Also generate commit statistics on the release page, starting from the given commit, and ending at the one given as -rev.")
	// TODO(mpl): make sanity run the tests too, once they're more reliable.
	flagSanity = flag.Bool("sanity", true, "Verify 'go run make.go' succeeds when building the source tarball. Abort everything if not.")
)

var (
	camDir      string
	releaseDate time.Time
)

const (
	titleDateFormat = "2006-01-02"
	fileDateFormat  = "20060102"
	project         = "camlistore-website"
	bucket          = "camlistore-release"
)

func isWIP() bool {
	return strings.HasPrefix(*flagRev, "WIP")
}

func rev() string {
	if isWIP() {
		return "WORKINPROGRESS"
	}
	return (*flagRev)[0:10]
}

// genDownloads creates all the zips and tarballs, and uploads them.
func genDownloads() error {
	dockDotGo := filepath.Join(camDir, "misc", "docker", "dock.go")
	releaseDir := filepath.Join(camDir, "misc", "docker", "release")
	var wg sync.WaitGroup
	if !*flagSkipGen {
		// Gen the source zip:
		args := []string{
			"run",
			dockDotGo,
			"-build_image=false",
			"-zip_source=true",
			"-rev=" + *flagRev,
			"-sanity=" + fmt.Sprintf("%t", *flagSanity),
		}
		cmd := exec.Command("go", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		upload(filepath.Join(releaseDir, "camlistore-src.zip"))
	}()

	// gen the binaries tarballs:
	for _, platform := range []string{"linux", "darwin", "windows"} {
		if !*flagSkipGen {
			args := []string{
				"run",
				dockDotGo,
				"-build_image=false",
				"-build_release=true",
				"-rev=" + *flagRev,
				"-os=" + platform,
			}
			cmd := exec.Command("go", args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return err
			}
		}
		wg.Add(1)
		go func(osType string) {
			defer wg.Done()
			filename := "camlistore-" + osType + ".tar.gz"
			if osType == "windows" {
				filename = strings.Replace(filename, ".tar.gz", ".zip", 1)
			}
			upload(filepath.Join(releaseDir, filename))
		}(platform)
	}
	wg.Wait()
	return nil
}

func upload(srcPath string) {
	if !*flagUpload {
		return
	}
	destName := strings.Replace(filepath.Base(srcPath), "camlistore", "camlistore-"+releaseDate.Format(fileDateFormat), 1)
	versionedTarball := "monthly/" + destName

	log.Printf("Uploading %s/%s ...", bucket, versionedTarball)

	ts, err := tokenSource(bucket)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	stoClient, err := storage.NewClient(ctx, option.WithTokenSource(ts), option.WithHTTPClient(oauth2.NewClient(ctx, ts)))
	if err != nil {
		log.Fatal(err)
	}
	w := stoClient.Bucket(bucket).Object(versionedTarball).NewWriter(ctx)
	w.ACL = publicACL(project)
	w.CacheControl = "no-cache" // TODO: remove for non-tip releases? set expirations?
	contentType := "application/x-gtar"
	if strings.HasSuffix(versionedTarball, ".zip") {
		contentType = "application/zip"
	}
	w.ContentType = contentType
	csw := sha256.New()
	mw := io.MultiWriter(w, csw)

	src, err := os.Open(srcPath)
	if err != nil {
		log.Fatal(err)
	}
	defer src.Close()

	if _, err := io.Copy(mw, src); err != nil {
		log.Fatalf("io.Copy: %v", err)
	}
	if err := w.Close(); err != nil {
		log.Fatalf("closing GCS storage writer: %v", err)
	}
	log.Printf("Uploaded monthly tarball to %s", versionedTarball)

	// And upload the corresponding checksum
	checkSumFile := versionedTarball + ".sha256"
	sum := fmt.Sprintf("%x", csw.Sum(nil))
	w = stoClient.Bucket(bucket).Object(checkSumFile).NewWriter(ctx)
	w.ACL = publicACL(project)
	w.CacheControl = "no-cache" // TODO: remove for non-tip releases? set expirations?
	w.ContentType = "text/plain"
	if _, err := io.Copy(w, strings.NewReader(sum)); err != nil {
		log.Fatalf("error uploading checksum %v: %v", checkSumFile, err)
	}
	if err := w.Close(); err != nil {
		log.Fatalf("closing GCS storage writer: %v", err)
	}
	log.Printf("Uploaded monthly tarball checksum to %s", checkSumFile)
}

type DownloadData struct {
	Filename string
	Platform string
	Checksum string
}

type ReleaseData struct {
	Date         string
	Download     []DownloadData
	CamliVersion string
	GoVersion    string
	Stats        *stats
	ReleaseNotes map[string][]string
}

// Note: the space trimming in the range loop is important. Since all of our
// html still goes through a markdown engine, newlines in between items would make
// markdown wrap the items in <p></p>, which breaks the page's style.
var monthlyTemplate = `
<h1>Monthly Release: {{.Date}}</h1>

<p>
Camlistore version <a href='https://github.com/camlistore/camlistore/commit/{{.CamliVersion}}'>{{.CamliVersion}}</a> built with Go {{.GoVersion}}.
</p>

<h2>Downloads</h2>

<center>
{{- range $d := .Download}}
<a class="downloadBox" href="/dl/monthly/{{$d.Filename}}">
<div class="platform">{{$d.Platform}}</div>
<div>
	<span class="filename">{{$d.Filename}}</span>
</div>
<div class="checksum">SHA256: {{$d.Checksum}}</div>
</a>
{{- end}}
</center>

{{if .Stats}}
<h2>Release Stats</h2>

<p>
{{.Stats.TotalCommitters}} total committers over {{.Stats.Commits}} commits since <a href='https://github.com/camlistore/camlistore/commit/{{.Stats.FromRev}}'>{{.Stats.FromRev}}</a> including {{.Stats.NamesList}}.
</p>

<p>Thank you!</p>
{{end}}

{{if .ReleaseNotes}}
<h2>Release Notes</h2>

<p>
<ul>
{{- range $pkg, $changes := .ReleaseNotes}}

<li>
{{$pkg}}:
<ul>
{{- range $change := $changes}}
<li>{{$change}}</li>
{{- end}}
</ul>
</li>

{{- end}}
</ul>
</p>
{{end}}
`

// TODO(mpl): keep goVersion automatically in sync with version in
// misc/docker/go. Or guess it from somewhere else.

const goVersion = "1.8"

// listDownloads lists all the files found in the monthly repo, and from them,
// builds the data that we'll feed to the template to generate the monthly
// downloads camweb page.
func listDownloads() (*ReleaseData, error) {
	ts, err := tokenSource(bucket)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	stoClient, err := storage.NewClient(ctx, option.WithTokenSource(ts), option.WithHTTPClient(oauth2.NewClient(ctx, ts)))
	if err != nil {
		return nil, err
	}

	platformBySuffix := map[string]string{
		"src.zip":       "Source",
		"linux.tar.gz":  "Linux",
		"darwin.tar.gz": "Darwin",
		"windows.zip":   "Windows",
	}
	getPlatform := func(name string) string {
		for suffix, platform := range platformBySuffix {
			if strings.HasSuffix(name, suffix) {
				return platform
			}
		}
		return ""
	}
	getChecksum := func(name string) (string, error) {
		r, err := stoClient.Bucket(bucket).Object(name).NewReader(ctx)
		if err != nil {
			return "", err
		}
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, r); err != nil {
			return "", err
		}
		return buf.String(), nil
	}
	var date time.Time
	checkDate := func(objDate time.Time) error {
		if date.IsZero() {
			date = objDate
			return nil
		}
		d := date.Sub(objDate)
		if d < 0 {
			d = -d
		}
		if d < 24*time.Hour {
			return nil
		}
		return fmt.Errorf("objects in monthly have not been uploaded or updated the same day")
	}

	var (
		downloadData []DownloadData
		nameToSum    = make(map[string]string)
	)
	fileDate := releaseDate.Format(fileDateFormat)
	log.Printf("Now looking for monthly/camlistore-%s-* files in bucket", fileDate)
	objIt := stoClient.Bucket(bucket).Objects(ctx, &storage.Query{Prefix: "monthly/"})
	for {
		attrs, err := objIt.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error listing objects in \"monthly\": %v", err)
		}
		if !strings.Contains(attrs.Name, fileDate) {
			continue
		}
		if err := checkDate(attrs.Updated); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(attrs.Name, ".sha256") {
			continue
		}
		sum, err := getChecksum(attrs.Name)
		if err != nil {
			return nil, err
		}
		nameToSum[strings.TrimSuffix(attrs.Name, ".sha256")] = sum
	}
	objIt = stoClient.Bucket(bucket).Objects(ctx, &storage.Query{Prefix: "monthly/"})
	for {
		attrs, err := objIt.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error listing objects in \"monthly\": %v", err)
		}
		if !strings.Contains(attrs.Name, fileDate) {
			continue
		}
		if strings.HasSuffix(attrs.Name, ".sha256") {
			continue
		}
		sum, ok := nameToSum[attrs.Name]
		if !ok {
			return nil, fmt.Errorf("%v has no checksum file!", attrs.Name)
		}
		downloadData = append(downloadData, DownloadData{
			Filename: filepath.Base(attrs.Name),
			Platform: getPlatform(attrs.Name),
			Checksum: sum,
		})
	}

	return &ReleaseData{
		Date:         releaseDate.Format(titleDateFormat),
		Download:     downloadData,
		CamliVersion: rev(),
		GoVersion:    goVersion,
	}, nil
}

func genMonthlyPage(releaseData *ReleaseData) error {
	tpl, err := template.New("monthly").Parse(monthlyTemplate)
	if err != nil {
		return fmt.Errorf("could not parse template: %v", err)
	}

	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "monthly", releaseData); err != nil {
		return fmt.Errorf("could not execute template: %v", err)
	}

	monthlyDocDir := filepath.Join(camDir, filepath.FromSlash("doc/release"))
	if err := os.MkdirAll(monthlyDocDir, 0755); err != nil {
		return err
	}
	monthlyDocPage := filepath.Join(monthlyDocDir, "monthly.html")
	if err := ioutil.WriteFile(monthlyDocPage, buf.Bytes(), 0700); err != nil {
		return fmt.Errorf("could not write template to file %v: %v", monthlyDocPage, err)
	}
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "%s [-rev camlistore_revision | -rev WIP:/path/to/camli/source]\n", os.Args[0])
	flag.PrintDefaults()
	os.Exit(1)
}

func checkFlags() {
	if flag.NArg() != 0 {
		usage()
	}
	if *flagRev == "" {
		fmt.Fprintf(os.Stderr, "Usage error: --rev is required.\n")
		usage()
	}
	releaseDate = time.Now()
	if *flagDate != "" {
		var err error
		releaseDate, err = time.Parse(fileDateFormat, *flagDate)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Incorrect date format: %v", err)
			usage()
		}
	}
}

type stats struct {
	FromRev         string
	TotalCommitters int
	Commits         int
	NamesList       string
}

// returns commiters names mapped by e-mail, uniqued first by e-mail, then by name.
// When uniquing, higher count of commits wins.
func committers() (map[string]string, error) {
	cmd := exec.Command("git", "shortlog", "-n", "-e", "-s", *flagStatsFrom+".."+rev())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%v; %v", err, string(out))
	}
	rxp := regexp.MustCompile(`^\s+(\d+)\s+(.*)<(.*)>.*$`)
	sc := bufio.NewScanner(bytes.NewReader(out))
	// maps email to name
	committers := make(map[string]string)
	// remember the count, to keep the committer that has the most count, when same name.
	commitCountByEmail := make(map[string]int)
	for sc.Scan() {
		m := rxp.FindStringSubmatch(sc.Text())
		if len(m) != 4 {
			return nil, fmt.Errorf("commit line regexp didn't match properly")
		}
		count, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("couldn't convert %q as a number of commits: %v", m[1], err)
		}
		name := strings.TrimSpace(m[2])
		email := m[3]
		// uniq by e-mail. first one encountered wins as it has more commits.
		if _, ok := committers[email]; !ok {
			committers[email] = name
		}
		commitCountByEmail[email] += count
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// uniq by name
	committerByName := make(map[string]string)
	for email, name := range committers {
		firstEmail, ok := committerByName[name]
		if !ok {
			committerByName[name] = email
			continue
		}
		c1, _ := commitCountByEmail[firstEmail]
		c2, _ := commitCountByEmail[email]
		if c1 < c2 {
			delete(committers, firstEmail)
		} else {
			delete(committers, email)
		}
	}
	return committers, nil
}

func countCommits() (int, error) {
	cmd := exec.Command("git", "log", "--format=oneline", *flagStatsFrom+".."+rev())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("%v; %v", err, string(out))
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	var sum int
	for sc.Scan() {
		sum++
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return sum, nil
}

func genCommitStats() (*stats, error) {
	committers, err := committers()
	if err != nil {
		return nil, fmt.Errorf("Could not count number of committers: %v", err)
	}
	commits, err := countCommits()
	if err != nil {
		return nil, fmt.Errorf("Could not count number of commits: %v", err)
	}
	var names []string
	for _, v := range committers {
		names = append(names, v)
	}
	sort.Strings(names)
	return &stats{
		TotalCommitters: len(committers),
		Commits:         commits,
		FromRev:         *flagStatsFrom,
		NamesList:       strings.Join(names, ", "),
	}, nil
}

func genReleaseNotes() (map[string][]string, error) {
	cmd := exec.Command("git", "log", "--format=oneline", "--no-merges", *flagStatsFrom+".."+rev())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%v; %v", err, string(out))
	}

	// We define the "context" of a commit message as the very first
	// part of the message, before the first colon.
	startsWithContext := regexp.MustCompile(`^(.+?):\s+(.*)$`)
	// Any of the keys in webUIContext, when encountered as context of
	// a commit message, means the commit is about the web UI. So we group
	// them all together under the "camlistored/ui" context.
	webUIContext := map[string]bool{
		"server/camlistored/ui": true,
		"ui":     true,
		"web ui": true,
		"webui":  true,
	}
	var noContext []string
	commitByContext := make(map[string][]string)
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		hashStripped := strings.SplitN(sc.Text(), " ", 2)[1]
		if strings.Contains(hashStripped, "CLA") {
			continue
		}
		m := startsWithContext.FindStringSubmatch(hashStripped)
		if len(m) != 3 {
			noContext = append(noContext, hashStripped)
			continue
		}
		change := m[2]
		commitContext := strings.ToLower(m[1])
		// remove "pkg/" prefix to group together e.g. "pkg/search:" and "search:"
		commitContext = strings.TrimPrefix(commitContext, "pkg/")
		// same thing for command-line tools
		commitContext = strings.TrimPrefix(commitContext, "cmd/")
		// group together all web UI stuff
		if _, ok := webUIContext[commitContext]; ok {
			commitContext = "camlistored/ui"
		}
		if commitContext == "server/camlistored" {
			commitContext = "camlistored"
		}
		var changes []string
		oldChanges, ok := commitByContext[commitContext]
		if !ok {
			changes = []string{change}
		} else {
			changes = append(oldChanges, change)
		}
		commitByContext[commitContext] = changes
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	commitByContext["zz_nocontext"] = noContext
	// TODO(mpl): remove keys with only one entry maybe?
	return commitByContext, nil
}

func main() {
	flag.Usage = usage
	flag.Parse()
	checkFlags()

	var err error
	camDir, err = osutil.GoPackagePath("camlistore.org")
	if err != nil {
		log.Fatalf("Error looking up camlistore.org dir: %v", err)
	}

	if err := genDownloads(); err != nil {
		log.Fatal(err)
	}

	releaseData, err := listDownloads()
	if err != nil {
		if *flagSkipGen {
			// Most likely we're failing because we can't reach the
			// bucket (working offline), annd we're working on this
			// program and testing things out, so make this error
			// non-critical so we can still generate the release notes
			// and stats.
			log.Print(err)
			releaseData = &ReleaseData{}
		} else {
			log.Fatal(err)
		}
	}

	if *flagStatsFrom != "" && !isWIP() {
		commitStats, err := genCommitStats()
		if err != nil {
			log.Fatal(err)
		}
		releaseData.Stats = commitStats

		notes, err := genReleaseNotes()
		if err != nil {
			log.Fatal(err)
		}
		releaseData.ReleaseNotes = notes
	}

	if err := genMonthlyPage(releaseData); err != nil {
		log.Fatal(err)
	}
}

// TODO(mpl): refactor in a common place so that dock.go and this program here can use the helpers below.

func homedir() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
	}
	return os.Getenv("HOME")
}

// ProjectTokenSource returns an OAuth2 TokenSource for the given Google Project ID.
func ProjectTokenSource(proj string, scopes ...string) (oauth2.TokenSource, error) {
	// TODO(bradfitz): try different strategies too, like
	// three-legged flow if the service account doesn't exist, and
	// then cache the token file on disk somewhere. Or maybe that should be an
	// option, for environments without stdin/stdout available to the user.
	// We'll figure it out as needed.
	fileName := filepath.Join(homedir(), "keys", proj+".key.json")
	jsonConf, err := ioutil.ReadFile(fileName)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Missing JSON key configuration. Download the Service Account JSON key from https://console.developers.google.com/project/%s/apiui/credential and place it at %s", proj, fileName)
		}
		return nil, err
	}
	conf, err := google.JWTConfigFromJSON(jsonConf, scopes...)
	if err != nil {
		return nil, fmt.Errorf("reading JSON config from %s: %v", fileName, err)
	}
	return conf.TokenSource(oauth2.NoContext), nil
}

var bucketProject = map[string]string{
	"camlistore-release": "camlistore-website",
}

func tokenSource(bucket string) (oauth2.TokenSource, error) {
	proj, ok := bucketProject[bucket]
	if !ok {
		return nil, fmt.Errorf("unknown project for bucket %q", bucket)
	}
	return ProjectTokenSource(proj, storage.ScopeReadWrite)
}

func publicACL(proj string) []storage.ACLRule {
	return []storage.ACLRule{
		// If you don't give the owners access, the web UI seems to
		// have a bug and doesn't have access to see that it's public, so
		// won't render the "Shared Publicly" link. So we do that, even
		// though it's dumb and unnecessary otherwise:
		{Entity: storage.ACLEntity("project-owners-" + proj), Role: storage.RoleOwner},
		{Entity: storage.AllUsers, Role: storage.RoleReader},
	}
}
