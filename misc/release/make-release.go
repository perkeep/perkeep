/*
Copyright 2018 The Perkeep Authors

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

// Command make-release builds the tarballs and zip archives for the Perkeep
// release downloads. That is: source zip, linux and darwin tarballs,
// and windows zip. These files are then uploaded to the dedicated repository, as
// well as a file with their checksum, for each of them. Finally, the template page
// to serve these downloads with pk-web is generated.
package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"perkeep.org/internal/osutil"

	"cloud.google.com/go/storage"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

var (
	flagRev         = flag.String("rev", "", "Perkeep revision to build (tag or commit hash). For development purposes, you can instead specify the path to a local Perkeep source tree from which to build, with the form \"WIP:/path/to/dir\".")
	flagVersion     = flag.String("version", "", "The name of the release, (e.g. 0.10.1, or 20180512) used as part of the name for the file downloads. Defaults to today's date in YYYYMMDD format.")
	flagArchiveType = flag.String("kind", "all", `The kind of archive to build for the release. Possible values are: "darwin", "linux", "windows", "src" (zip of all the source code), or "all" (for all the previous values).`)
	flagUpload      = flag.Bool("upload", true, "Upload all the generated tarballs and zip archives.")
	flagSkipGen     = flag.Bool("skipgen", false, "Do not recreate the release tarballs, and directly use the ones found in perkeep.org/misc/release. Use -upload=false and -skipgen=true to only generate the release page.")
	flagStatsFrom   = flag.String("stats_from", "", "Also generate commit statistics on the release page, starting from the given commit, and ending at the one given as -rev.")
)

var (
	srcRoot    string
	releaseDir string
	workDir    string
	goVersion  string
	pkVersion  string
)

const (
	goDockerImage      = "perkeep/go"
	goCmd              = "/usr/local/go/bin/go"
	genBinariesProgram = "/usr/local/bin/build-binaries.go"
	zipSourceProgram   = "/usr/local/bin/zip-source.go"
	titleDateFormat    = "2006-01-02"
	fileDateFormat     = "20060102"
	project            = "camlistore-website"
	bucket             = "perkeep-release"
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "%s [-rev perkeep_revision | -rev WIP:/path/to/perkeep/source]\n", os.Args[0])
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
}

func main() {
	flag.Usage = usage
	flag.Parse()
	checkFlags()

	var err error
	srcRoot, err = osutil.PkSourceRoot()
	if err != nil {
		log.Fatalf("Error looking up perkeep.org dir: %v", err)
	}
	releaseDir = filepath.Join(srcRoot, "misc", "release")

	workDir, err = os.MkdirTemp("", "pk-build_release")
	if err != nil {
		log.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		workDir = "/private" + workDir
	}
	defer os.RemoveAll(workDir)

	archives := []string{
		"perkeep-darwin.tar.gz",
		"perkeep-linux.tar.gz",
		"perkeep-src.zip",
		"perkeep-windows.zip",
	}
	if !*flagSkipGen {
		archives, err = genArchive()
		if err != nil {
			log.Fatal(err)
		}
	}
	if *flagUpload {
		if !*flagSkipGen && *flagArchiveType != "all" {
			archives = []string{archiveName(*flagArchiveType)}
		}
		for _, v := range archives {
			upload(filepath.Join(releaseDir, v))
		}
	}

	if *flagArchiveType != "all" {
		// do not bother generating the release page if we're not doing a full blown release build.
		return
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

	if *flagStatsFrom != "" {
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

	if err := genReleasePage(releaseData); err != nil {
		log.Fatal(err)
	}
}

// genArchive generates the requested tarball(s) and zip archive(s), and returns
// their filenames.
func genArchive() ([]string, error) {
	switch *flagArchiveType {
	case "linux", "darwin", "windows":
		genBinaries(*flagArchiveType)
		return []string{packBinaries(*flagArchiveType)}, nil
	case "src":
		return []string{zipSource()}, nil
	default:
		return genAll()
	}
}

// genAll creates all the zips and tarballs, and returns their filenames.
func genAll() ([]string, error) {
	zipSource()
	for _, platform := range []string{"linux", "darwin", "windows"} {
		genBinaries(platform)
		packBinaries(platform)
	}
	getVersions()
	return []string{
		"perkeep-darwin.tar.gz",
		"perkeep-linux.tar.gz",
		"perkeep-src.zip",
		"perkeep-windows.zip",
	}, nil
}

// getVersions uses the freshly built perkeepd binary to get the Perkeep and Go
// versions used to build the release.
func getVersions() {
	pkBin := filepath.Join(workDir, runtime.GOOS, "bin", "perkeepd")
	cmd := exec.Command(pkBin, "-version")
	var buf bytes.Buffer
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error getting version from perkeepd: %v, %v", err, buf.String())
	}
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		l := sc.Text()
		fields := strings.Fields(l)
		if pkVersion == "" {
			if len(fields) != 4 || fields[0] != "perkeepd" {
				log.Fatalf("Unexpected perkeepd -version output: %q", l)
			}
			pkVersion = fields[3]
			continue
		}
		if len(fields) != 4 || fields[0] != "Go" {
			log.Fatalf("Unexpected perkeepd -version output: %q", l)
		}
		goVersion = fields[2]
		break
	}
	if err := sc.Err(); err != nil {
		log.Fatal(err)
	}
}

// genBinaries runs go run make.go for the given osType in a docker container.
func genBinaries(osType string) {
	cwd := filepath.Join(workDir, osType)
	check(os.MkdirAll(cwd, 0755))
	image := goDockerImage
	args := []string{
		"run",
		"--rm",
		"--volume=" + cwd + ":/OUT",
		"--volume=" + filepath.Join(releaseDir, "build-binaries.go") + ":" + genBinariesProgram + ":ro",
	}
	if isWIP() {
		args = append(args, "--volume="+localCamliSource()+":/IN:ro",
			image, goCmd, "run", genBinariesProgram, "--rev=WIP:/IN", "--os="+osType)
	} else {
		args = append(args, image, goCmd, "run", genBinariesProgram, "--rev="+rev(), "--os="+osType)
	}
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building binaries in go container: %v", err)
	}
	fmt.Printf("Perkeep binaries successfully generated in %v\n", filepath.Join(cwd, "bin"))
}

// packBinaries builds the archive that contains the binaries built by
// genBinaries.
func packBinaries(osType string) string {
	fileName := archiveName(osType)
	binaries := map[string]bool{
		exeName("perkeepd", osType):        false,
		exeName("pk-get", osType):          false,
		exeName("pk-put", osType):          false,
		exeName("pk", osType):              false,
		exeName("publisher", osType):       false,
		exeName("scancab", osType):         false,
		exeName("scanningcabinet", osType): false,
	}
	switch osType {
	case "linux", "darwin":
		binaries["pk-mount"] = false
	}
	toPack := func(bin string) bool {
		for k := range binaries {
			if bin == k {
				binaries[k] = true
				return true
			}
		}
		return false
	}
	archivePath := filepath.Join(releaseDir, fileName)
	defer func() {
		for name, found := range binaries {
			if !found {
				log.Fatalf("%v was not packed in tarball", name)
			}
		}
		fmt.Printf("Perkeep binaries successfully packed in %v\n", archivePath)
	}()

	binDir := path.Join(workDir, osType, "bin")
	check(os.Chdir(binDir))
	dir, err := os.Open(binDir)
	check(err)
	defer dir.Close()

	if osType == "windows" {
		fw, err := os.Create(archivePath)
		check(err)
		defer func() {
			check(fw.Close())
		}()
		w := zip.NewWriter(fw)
		defer func() {
			check(w.Close())
		}()
		names, err := dir.Readdirnames(-1)
		check(err)
		for _, name := range names {
			if !toPack(name) {
				continue
			}
			b, err := os.ReadFile(path.Join(binDir, name))
			check(err)
			f, err := w.Create(name)
			check(err)
			_, err = f.Write(b)
			check(err)
		}
		return fileName
	}

	fw, err := os.Create(archivePath)
	check(err)
	defer func() {
		check(fw.Close())
	}()
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		fis, err := dir.Readdir(-1)
		check(err)
		for _, file := range fis {
			if !toPack(file.Name()) {
				continue
			}
			hdr, err := tar.FileInfoHeader(file, "")
			check(err)
			check(tw.WriteHeader(hdr))
			fr, err := os.Open(file.Name())
			check(err)
			n, err := io.Copy(tw, fr)
			check(err)
			fr.Close()
			if n != file.Size() {
				log.Fatalf("failed to tar all of %v; got %v, wanted %v", file.Name(), n, file.Size())
			}
		}
		check(tw.Close())
		check(pw.CloseWithError(io.EOF))
	}()
	zw := gzip.NewWriter(fw)
	n, err := io.Copy(zw, pr)
	if err != nil {
		log.Fatalf("Error copying to gzip writer: after %d bytes, %v", n, err)
	}
	if err := zw.Close(); err != nil {
		log.Fatalf("gzip.Close: %v", err)
	}
	return fileName
}

// zipSource builds the zip archive that contains the source code of Perkeep.
func zipSource() string {
	cwd := filepath.Join(workDir, "src")
	check(os.MkdirAll(cwd, 0755))
	image := goDockerImage
	args := []string{
		"run",
		"--rm",
		"--volume=" + cwd + ":/OUT",
		"--volume=" + path.Join(releaseDir, "zip-source.go") + ":" + zipSourceProgram + ":ro",
	}
	if isWIP() {
		args = append(args, "--volume="+localCamliSource()+":/IN:ro",
			image, goCmd, "run", zipSourceProgram, "--rev=WIP:/IN")
	} else {
		args = append(args, image, goCmd, "run", zipSourceProgram, "--rev="+rev())
	}
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error zipping Perkeep source in go container: %v", err)
	}
	archiveName := "perkeep-src.zip"
	// can't use os.Rename because invalid cross-device link error likely
	cmd = exec.Command("mv", filepath.Join(cwd, archiveName), releaseDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error moving source zip from %v to %v: %v", filepath.Join(cwd, archiveName), releaseDir, err)
	}
	fmt.Printf("Perkeep source successfully zipped in %v\n", releaseDir)
	return archiveName
}

func upload(srcPath string) {
	uploadName := strings.Replace(filepath.Base(srcPath), "perkeep", "perkeep-"+version(), 1)

	log.Printf("Uploading %s/%s ...", bucket, uploadName)

	ts, err := tokenSource(bucket)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	stoClient, err := storage.NewClient(ctx, option.WithTokenSource(ts), option.WithHTTPClient(oauth2.NewClient(ctx, ts)))
	if err != nil {
		log.Fatal(err)
	}
	w := stoClient.Bucket(bucket).Object(uploadName).NewWriter(ctx)
	w.ACL = publicACL(project)
	w.CacheControl = "no-cache" // TODO: remove for non-tip releases? set expirations?
	contentType := "application/x-gtar"
	if strings.HasSuffix(uploadName, ".zip") {
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
	log.Printf("Uploaded archive to %s/%s", bucket, uploadName)

	// And upload the corresponding checksum
	checkSumFile := uploadName + ".sha256"
	sum := fmt.Sprintf("%x", csw.Sum(nil))
	w = stoClient.Bucket(bucket).Object(checkSumFile).NewWriter(ctx)
	w.ACL = publicACL(project)
	w.CacheControl = "no-cache"
	w.ContentType = "text/plain"
	if _, err := io.Copy(w, strings.NewReader(sum)); err != nil {
		log.Fatalf("error uploading checksum %v: %v", checkSumFile, err)
	}
	if err := w.Close(); err != nil {
		log.Fatalf("closing GCS storage writer: %v", err)
	}
	log.Printf("Uploaded archive checksum to %s", checkSumFile)
}

type DownloadData struct {
	Filename string
	Platform string
	Checksum string
}

type ReleaseData struct {
	Name         string
	Download     []DownloadData
	PkVersion    string
	GoVersion    string
	Stats        *stats
	ReleaseNotes map[string][]string
}

// Note: the space trimming in the range loop is important. Since all of our
// html still goes through a markdown engine, newlines in between items would make
// markdown wrap the items in <p></p>, which breaks the page's style.
var releaseTemplate = `
<h1>Perkeep Release: {{.Name}}</h1>

<p>
Perkeep version <a href='https://github.com/perkeep/perkeep/commit/{{.PkVersion}}'>{{.PkVersion}}</a> built with Go {{.GoVersion}}.
</p>

<h2>Downloads</h2>

<center>
{{- range $d := .Download}}
<a class="downloadBox" href="/dl/{{$d.Filename}}">
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
{{.Stats.TotalCommitters}} total committers over {{.Stats.Commits}} commits since <a href='https://github.com/perkeep/perkeep/commit/{{.Stats.FromRev}}'>{{.Stats.FromRev}}</a> including {{.Stats.NamesList}}.
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

// listDownloads lists all the files found in the release bucket, and from them,
// builds the data that we'll feed to the template to generate the release
// downloads page for pk-web.
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
	fileVersion := version()
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
		return fmt.Errorf("archives for version %s have not been uploaded or updated the same day", fileVersion)
	}

	var (
		downloadData []DownloadData
		nameToSum    = make(map[string]string)
	)

	log.Printf("Now looking for perkeep-%s-* files in bucket %s", fileVersion, bucket)
	objPrefix := "perkeep-" + fileVersion
	objIt := stoClient.Bucket(bucket).Objects(ctx, &storage.Query{Prefix: objPrefix})
	for {
		attrs, err := objIt.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error listing objects in %s: %v", bucket, err)
		}
		if !strings.Contains(attrs.Name, fileVersion) {
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
	objIt = stoClient.Bucket(bucket).Objects(ctx, &storage.Query{Prefix: objPrefix})
	for {
		attrs, err := objIt.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error listing objects in %s: %v", bucket, err)
		}
		if !strings.Contains(attrs.Name, fileVersion) {
			continue
		}
		if strings.HasSuffix(attrs.Name, ".sha256") {
			continue
		}
		sum, ok := nameToSum[attrs.Name]
		if !ok {
			return nil, fmt.Errorf("%v has no checksum file", attrs.Name)
		}
		downloadData = append(downloadData, DownloadData{
			Filename: filepath.Base(attrs.Name),
			Platform: getPlatform(attrs.Name),
			Checksum: sum,
		})
	}

	return &ReleaseData{
		Name:      version(),
		Download:  downloadData,
		PkVersion: pkVersion,
		GoVersion: goVersion,
	}, nil
}

func genReleasePage(releaseData *ReleaseData) error {
	tpl, err := template.New("release").Parse(releaseTemplate)
	if err != nil {
		return fmt.Errorf("could not parse template: %v", err)
	}

	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "release", releaseData); err != nil {
		return fmt.Errorf("could not execute template: %v", err)
	}

	releaseDocDir := filepath.Join(srcRoot, filepath.FromSlash("doc/release"))
	if err := os.MkdirAll(releaseDocDir, 0755); err != nil {
		return err
	}
	releaseDocPage := filepath.Join(releaseDocDir, "release.html")
	if err := os.WriteFile(releaseDocPage, buf.Bytes(), 0700); err != nil {
		return fmt.Errorf("could not write template to file %v: %v", releaseDocPage, err)
	}
	return nil
}

type stats struct {
	FromRev         string
	TotalCommitters int
	Commits         int
	NamesList       string
}

// returns committers names mapped by e-mail, uniqued first by e-mail, then by name.
// When uniquing, higher count of commits wins.
func committers() (map[string]string, error) {
	cmd := exec.Command("git", "shortlog", "-n", "-e", "-s", *flagStatsFrom+".."+revOrHEAD())
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
		c1 := commitCountByEmail[firstEmail]
		c2 := commitCountByEmail[email]
		if c1 < c2 {
			delete(committers, firstEmail)
		} else {
			delete(committers, email)
		}
	}
	return committers, nil
}

func countCommits() (int, error) {
	cmd := exec.Command("git", "log", "--format=oneline", *flagStatsFrom+".."+revOrHEAD())
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
	cmd := exec.Command("git", "log", "--format=oneline", "--no-merges", *flagStatsFrom+".."+revOrHEAD())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%v; %v", err, string(out))
	}

	// We define the "context" of a commit message as the very first
	// part of the message, before the first colon.
	startsWithContext := regexp.MustCompile(`^(.+?):\s+(.*)$`)
	// Any of the keys in webUIContext, when encountered as context of
	// a commit message, means the commit is about the web UI. So we group
	// them all together under the "perkeepd/ui" context.
	webUIContext := map[string]bool{
		"server/perkeepd/ui": true,
		"ui":                 true,
		"web ui":             true,
		"webui":              true,
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
			commitContext = "perkeepd/ui"
		}
		if commitContext == "server/perkeepd" {
			commitContext = "perkeepd"
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

func isWIP() bool {
	return strings.HasPrefix(*flagRev, "WIP")
}

// localCamliSource returns the path to the local Perkeep source tree
// that should be specified in *flagRev if *flagRev starts with "WIP:",
// empty string otherwise.
func localCamliSource() string {
	if !isWIP() {
		return ""
	}
	return strings.TrimPrefix(*flagRev, "WIP:")
}

func rev() string {
	if isWIP() {
		return "WORKINPROGRESS"
	}
	return (*flagRev)[0:10]
}

func revOrHEAD() string {
	if isWIP() {
		return "HEAD"
	}
	return (*flagRev)[0:10]
}

func version() string {
	if *flagVersion != "" {
		return *flagVersion
	}
	return time.Now().Format(fileDateFormat)
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func exeName(s, osType string) string {
	if osType == "windows" {
		return s + ".exe"
	}
	return s
}

func homedir() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
	}
	return os.Getenv("HOME")
}

func archiveName(archiveType string) string {
	switch archiveType {
	case "windows", "src":
		return "perkeep-" + archiveType + ".zip"
	default:
		return "perkeep-" + archiveType + ".tar.gz"
	}
}

// ProjectTokenSource returns an OAuth2 TokenSource for the given Google Project ID.
func ProjectTokenSource(proj string, scopes ...string) (oauth2.TokenSource, error) {
	// TODO(bradfitz): try different strategies too, like
	// three-legged flow if the service account doesn't exist, and
	// then cache the token file on disk somewhere. Or maybe that should be an
	// option, for environments without stdin/stdout available to the user.
	// We'll figure it out as needed.
	fileName := filepath.Join(homedir(), "keys", proj+".key.json")
	jsonConf, err := os.ReadFile(fileName)
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
	return conf.TokenSource(context.TODO()), nil
}

var bucketProject = map[string]string{
	"perkeep-release": "camlistore-website",
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
