/*
Copyright 2026 The Perkeep Authors

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

// Package googletakeout implements an importer of Google Takeout exports,
// read from a file source, such as a Google Drive. It keeps an archive of
// each whole export, of all of its Google products' files, as pk takeout
// upload does, and imports the data of the products it knows: for now,
// Google Photos' photos, videos and albums (in photos.go). Each product
// imported has its part of a run's scan, overview and result.
//
// Each export is imported by an account of its own. The owner picks its
// zip files' source and folder, and the export, on the account's setup
// pages. Its first run reads the zip files' tables of contents and asks
// the server what of the export it has, for the owner to review on the
// account's page and confirm; the next imports it. Runs interrupted by
// the server stopping resume when it starts again.
package googletakeout // import "perkeep.org/pkg/importer/googletakeout"

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"perkeep.org/internal/attrclaims"
	"perkeep.org/internal/takeout"
	"perkeep.org/internal/takeoutimport"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/filesource"
	"perkeep.org/pkg/filesource/gdrive"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/search"
)

// Attributes of accounts.
const (
	attrFileSource = "googleTakeoutFileSource"   // the permanode of the file source of the zip files
	attrFolder     = "googleTakeoutFolder"       // the ID of the folder of the zip files, in the file source, or none for the default ones
	attrExport     = "googleTakeoutExport"       // the export's name, such as takeout-20240101T120000Z
	attrZip        = "googleTakeoutZip"          // a zip file's filesource.File, as JSON, one value for each
	attrEdited     = "googleTakeoutPhotosEdited" // "true" to import edited copies of Google Photos photos too
	attrState      = "googleTakeoutState"        // one of the states below
	attrPaused     = "googleTakeoutPaused"       // "true" if the owner stopped a run, which isn't resumed
	attrOverview   = "googleTakeoutOverview"     // the overview, as JSON, once scanned
	attrResult     = "googleTakeoutResult"       // the result, as JSON, once done

	// attrExportPermanode is the permanode of the archived export.
	attrExportPermanode = "googleTakeoutExportPermanode"
)

// States of an account's import, in order.
const (
	stateScanning  = "scanning"  // reading the tables of contents and checking the server
	stateReview    = "review"    // waiting for the owner to confirm the import
	stateImporting = "importing" // importing
	stateDone      = "done"
)

// workers is how many zip files' tables of contents to read, and how
// many files to check or upload, at once.
const workers = 4

func init() {
	importer.Register("googletakeout", imp{})
}

var _ importer.Resumer = imp{}
var _ importer.AccountStatusHTMLer = imp{}

type imp struct {
	importer.OAuth1 // for the acct parameter of CallbackRequestAccount and CallbackURLParameters
}

func (imp) Properties() importer.Properties {
	return importer.Properties{
		Title:                "Google Takeout",
		Description:          "Google Takeout exports, keeping each whole export, and their Google Photos photos, videos and albums",
		CallbackRequiresPOST: true,
	}
}

func (imp) IsAccountReady(acct *importer.Object) (bool, error) {
	return acct.Attr(attrExport) != "" && len(acct.Attrs(attrZip)) > 0, nil
}

func (imp) SummarizeAccount(acct *importer.Object) string {
	if name := acct.Attr(attrExport); name != "" {
		return "Google Takeout export " + name
	}
	return "Not set up"
}

// ResumeRun resumes the runs that were scanning or importing when the
// server stopped, unless the owner stopped them.
func (imp) ResumeRun(acct *importer.Object) bool {
	switch acct.Attr(attrState) {
	case stateScanning, stateImporting:
		return acct.Attr(attrPaused) != "true"
	}
	return false
}

// zips returns the account's zip files.
func zips(acct *importer.Object) ([]filesource.File, error) {
	var files []filesource.File
	for _, v := range acct.Attrs(attrZip) {
		var f filesource.File
		if err := json.Unmarshal([]byte(v), &f); err != nil {
			return nil, fmt.Errorf("bad %s: %w", attrZip, err)
		}
		files = append(files, f)
	}
	slices.SortFunc(files, func(a, b filesource.File) int { return strings.Compare(a.Name, b.Name) })
	return files, nil
}

// fileSourceRef returns the account's file source.
func fileSourceRef(acct *importer.Object) (blob.Ref, error) {
	ref, ok := blob.Parse(acct.Attr(attrFileSource))
	if !ok {
		return blob.Ref{}, errors.New("the account has no file source; set it up again")
	}
	return ref, nil
}

// Run scans or imports the account's export, as its state says.
func (im imp) Run(rc *importer.RunContext) error {
	ctx := rc.Context()
	acct := rc.AccountNode()
	state := acct.Attr(attrState)
	switch state {
	case stateReview:
		rc.Logf("Waiting for the import to be confirmed, on this page.")
		return nil
	case stateDone:
		rc.Logf("The export's imported already.")
		return nil
	case stateScanning, stateImporting:
	default:
		return fmt.Errorf("the account's in the unknown state %q; set it up again", state)
	}
	if err := acct.DelAttr(attrPaused, ""); err != nil {
		return err
	}
	r, err := newRun(rc)
	if err != nil {
		return err
	}
	if state == stateScanning {
		err = r.scan(ctx)
	} else {
		err = r.importExport(ctx)
	}
	if err != nil && rc.StopRequested() {
		if err := acct.SetAttr(attrPaused, "true"); err != nil {
			rc.Logf("Marking the account paused: %v", err)
		}
	}
	return explain(rc, err)
}

// explain returns err with what to do about it, if it's of a kind the
// owner can do something about.
func explain(rc *importer.RunContext, err error) error {
	switch {
	case errors.Is(err, importer.ErrNeedsAuthorization):
		ref, _ := fileSourceRef(rc.AccountNode())
		return fmt.Errorf("%w; authorize the file source again, at %s, then start the import again", err, rc.Host.FileSourcePath(ref))
	case errors.Is(err, filesource.ErrChanged):
		return fmt.Errorf("%w; the export's zip files have changed since they were picked, so set the account up again", err)
	}
	return err
}

// A run is one run of an account.
type run struct {
	rc   *importer.RunContext
	acct *importer.Object
	exp  *takeout.Export
	im   *takeoutimport.Importer
}

// newRun returns a run of rc's account, with its export's tables of
// contents loaded.
func newRun(rc *importer.RunContext) (*run, error) {
	ctx := rc.Context()
	acct := rc.AccountNode()
	ref, err := fileSourceRef(acct)
	if err != nil {
		return nil, err
	}
	src, err := rc.Host.FileSource(ctx, ref)
	if err != nil {
		return nil, err
	}
	files, err := zips(acct)
	if err != nil {
		return nil, err
	}
	var cache takeout.Cache
	if dir, err := rc.ImporterCacheDir(); err != nil {
		return nil, err
	} else if dir != "" {
		cache = takeout.DirCache(dir)
	}
	exps := takeout.ExportsFromFiles(src, files, cache)
	name := acct.Attr(attrExport)
	if len(exps) != 1 || exps[0].Name != name {
		return nil, fmt.Errorf("the account's zip files aren't all of export %s; set it up again", name)
	}
	exp := exps[0]
	rc.Logf("Reading the tables of contents of %d zip files...", len(exp.Zips))
	if err := exp.LoadTOCs(ctx, workers); err != nil {
		return nil, err
	}
	im := &takeoutimport.Importer{
		Server:   hostServer{rc.Host},
		Signer:   rc.Host.Signer(),
		Workers:  workers,
		Logf:     rc.Logf,
		Progress: rc.Progress(),
	}
	if err := im.CheckServer(ctx); err != nil {
		return nil, err
	}
	return &run{rc: rc, acct: acct, exp: exp, im: im}, nil
}

// scan finds what the export has, and what of it the server has, for
// the owner to review.
func (r *run) scan(ctx context.Context) error {
	ov := newOverview(r.exp)
	var err error
	if ov.Photos, err = r.scanPhotos(ctx); err != nil {
		return err
	}
	oj, err := json.Marshal(ov)
	if err != nil {
		return err
	}
	if err := r.acct.SetAttrs(attrOverview, string(oj), attrState, stateReview); err != nil {
		return err
	}
	r.rc.Logf("Scanned. Confirm the import on this page.")
	return nil
}

// importExport imports the export.
func (r *run) importExport(ctx context.Context) error {
	res := &result{}
	var err error
	// Importing Google Photos archives the whole export too.
	if res.Photos, err = r.importPhotos(ctx); err != nil {
		return err
	}
	_, pn, err := attrclaims.PlannedPermanode(ctx, r.rc.Host.Signer(), takeoutimport.ExportPermanodeKeyPrefix+r.exp.Name)
	if err != nil {
		return err
	}
	res.Finished = time.Now().UTC()
	rj, err := json.Marshal(res)
	if err != nil {
		return err
	}
	if err := r.acct.SetAttrs(attrResult, string(rj), attrExportPermanode, pn.String(), attrState, stateDone); err != nil {
		return err
	}
	r.rc.Logf("Imported the export, archived as permanode %v.", pn)
	return nil
}

// An overview is what an export has, for the owner to review before
// confirming its import: all of its products' files, which its archive
// keeps, and, for each product that's imported, what of it is.
type overview struct {
	Zips     int
	ZipBytes int64
	Products []product // by size, largest first

	Photos *photosOverview // of Google Photos
}

// A product is a Google product's data in an export, such as Google
// Photos', in its directory of the same name.
type product struct {
	Name  string
	Files int
	Bytes int64
}

// productDir returns the product directory of name, the path of a file in
// an export, such as "Google Photos" for
// "Takeout/Google Photos/Photos from 2023/IMG_0001.JPG".
func productDir(name string) (dir string, ok bool) {
	rest, ok := strings.CutPrefix(name, "Takeout/")
	dir, _, ok2 := strings.Cut(rest, "/")
	return dir, ok && ok2
}

// newOverview returns the overview of exp's products.
func newOverview(exp *takeout.Export) *overview {
	ov := &overview{Zips: len(exp.Zips)}
	for _, z := range exp.Zips {
		ov.ZipBytes += z.Size
	}
	products := make(map[string]*product)
	for _, e := range exp.Entries() {
		if strings.HasSuffix(e.File.Name, "/") {
			continue
		}
		dir, ok := productDir(e.File.Name)
		if !ok {
			dir = "(other)"
		}
		p := products[dir]
		if p == nil {
			p = &product{Name: dir}
			products[dir] = p
		}
		p.Files++
		p.Bytes += e.Size()
	}
	for _, p := range products {
		ov.Products = append(ov.Products, *p)
	}
	slices.SortFunc(ov.Products, func(a, b product) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), strings.Compare(a.Name, b.Name))
	})
	return ov
}

// A result is what an import did, for each product imported.
type result struct {
	Photos   *photosResult
	Finished time.Time
}

// ServeSetup serves the first page of an account's setup, to pick the
// file source of the export's zip files.
func (imp) ServeSetup(w http.ResponseWriter, r *http.Request, sc *importer.SetupContext) error {
	srcs, err := sc.Host.FileSources(sc)
	if err != nil {
		return err
	}
	return execTemplate(w, "setup", setupData{
		CallbackPath:   sc.CallbackPath(),
		AccountURL:     sc.AccountURL(),
		SourcesPath:    sc.Host.FileSourcesPath(),
		Sources:        srcs,
		Current:        sc.AccountNode.Attr(attrFileSource),
		AlreadyStarted: sc.AccountNode.Attr(attrState) != "",
	})
}

type setupData struct {
	CallbackPath, AccountURL, SourcesPath string
	Sources                               []importer.FileSourceInfo
	Current                               string // the account's file source, if any
	AlreadyStarted                        bool   // whether the account was set up already
}

// ServeCallback serves the setup pages' forms: to browse a source's
// folders, to pick an export, and to confirm its import.
func (im imp) ServeCallback(w http.ResponseWriter, r *http.Request, sc *importer.SetupContext) {
	var err error
	switch r.FormValue("step") {
	case "browse":
		err = im.serveBrowse(w, r, sc)
	case "pick":
		err = im.servePick(w, r, sc)
	case "confirm":
		err = setState(sc, stateReview, stateImporting)
	case "rescan":
		err = setState(sc, stateReview, stateScanning)
	default:
		err = errors.New("unknown step")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch r.FormValue("step") {
	case "confirm", "rescan":
		http.Redirect(w, r, sc.AccountURL(), http.StatusSeeOther)
	}
}

// setState changes the account's state from the state from to to, and
// starts a run, for the next step of its import.
func setState(sc *importer.SetupContext, from, to string) error {
	if s := sc.AccountNode.Attr(attrState); s != from {
		return fmt.Errorf("the import is %s, not %s", cmp.Or(s, "not set up"), from)
	}
	if err := sc.AccountNode.SetAttr(attrState, to); err != nil {
		return err
	}
	sc.StartRun()
	return nil
}

// browsed is where a browse page looks: a folder of a source.
type browsed struct {
	src    filesource.Source
	info   importer.FileSourceInfo
	folder *filesource.File // or nil, for the source's default Takeout folders
	dirs   []filesource.File
}

// browse returns where the form of r says to look, for the account of sc.
// It's the folder with the ID in its folder value, or the one typed in
// its goto value, or else the source's default Takeout folders.
func browse(r *http.Request, sc *importer.SetupContext) (*browsed, error) {
	ref, ok := blob.Parse(r.FormValue("source"))
	if !ok {
		return nil, errors.New("pick a file source")
	}
	info, err := sc.Host.FileSourceInfo(ref)
	if err != nil {
		return nil, err
	}
	src, err := sc.Host.FileSource(sc, ref)
	if err != nil {
		return nil, err
	}
	b := &browsed{src: src, info: info}
	id := r.FormValue("folder")
	if s := strings.TrimSpace(r.FormValue("goto")); s != "" {
		if id, err = folderID(info, s); err != nil {
			return nil, err
		}
	}
	if id != "" {
		f, err := src.Stat(sc, id)
		if err != nil {
			return nil, fmt.Errorf("finding the folder: %w", err)
		}
		if !f.Dir {
			return nil, fmt.Errorf("%s is not a folder", f.Name)
		}
		b.folder = &f
		b.dirs = []filesource.File{f}
	} else if b.dirs, err = takeout.DefaultDirs(sc, src); err != nil {
		return nil, err
	}
	return b, nil
}

// folderID returns the ID of the folder s, as typed to go to it: for a
// Google Drive, its URL, as the Drive web app shows it, or its ID; for a
// directory on the server, its path beneath the directory.
func folderID(info importer.FileSourceInfo, s string) (string, error) {
	if info.Type == "gdrive" {
		return gdrive.FolderID(s)
	}
	return path.Clean(strings.TrimPrefix(s, "/")), nil
}

// exports returns the exports in b's folders.
func (b *browsed) exports(ctx context.Context) ([]*takeout.Export, error) {
	var files []filesource.File
	for _, d := range b.dirs {
		fs, err := b.src.List(ctx, d)
		if err != nil {
			return nil, err
		}
		files = append(files, fs...)
	}
	return takeout.ExportsFromFiles(b.src, files, nil), nil
}

// serveBrowse serves the page of a folder of a source, with the exports
// in it, and its folders to look in instead.
func (imp) serveBrowse(w http.ResponseWriter, r *http.Request, sc *importer.SetupContext) error {
	b, err := browse(r, sc)
	if err != nil {
		return err
	}
	exps, err := b.exports(sc)
	if err != nil {
		return err
	}
	// The folders to look in: those in the folder looked in, or else in
	// the source's top folder.
	top := b.folder
	if top == nil {
		root, err := b.src.Root(sc)
		if err != nil {
			return err
		}
		top = &root
	}
	all, err := b.src.List(sc, *top)
	if err != nil {
		return err
	}
	var dirs []filesource.File
	for _, f := range all {
		if f.Dir {
			dirs = append(dirs, f)
		}
	}
	data := browseData{
		CallbackPath: sc.CallbackPath(),
		AccountURL:   sc.AccountURL(),
		Source:       b.info,
		Folders:      listedFolders(dirs),
		Default:      b.folder == nil,
		Top:          top.Name,
	}
	if b.folder != nil {
		data.FolderID = b.folder.ID
	}
	for _, d := range b.dirs {
		data.Looked = append(data.Looked, d.Name)
	}
	for _, e := range exps {
		var size int64
		for _, z := range e.Zips {
			size += z.Size
		}
		data.Exports = append(data.Exports, exportRow{Name: e.Name, Zips: len(e.Zips), Size: takeoutimport.FormatBytes(size), Time: e.Time})
	}
	return execTemplate(w, "browse", data)
}

type browseData struct {
	CallbackPath, AccountURL string
	Source                   importer.FileSourceInfo
	FolderID                 string   // of the folder looked in, or empty for the default ones
	Default                  bool     // whether the default Takeout folders were looked in
	Looked                   []string // the names of the folders looked in
	Top                      string   // the name of the folder whose folders are Folders
	Folders                  []listedFolder
	Exports                  []exportRow
}

type exportRow struct {
	Name string
	Zips int
	Size string
	Time time.Time
}

// A listedFolder is a folder, as the browse page lists it.
type listedFolder struct {
	filesource.File

	// Ambiguous is whether others listed with it have its name, as
	// Google Drive allows, so that the page shows its ID too.
	Ambiguous bool
}

// listedFolders returns dirs for listing, by name.
func listedFolders(dirs []filesource.File) []listedFolder {
	named := make(map[string]int)
	for _, f := range dirs {
		named[f.Name]++
	}
	listed := make([]listedFolder, len(dirs))
	for i, f := range dirs {
		listed[i] = listedFolder{f, named[f.Name] > 1}
	}
	slices.SortStableFunc(listed, func(a, b listedFolder) int { return strings.Compare(a.Name, b.Name) })
	return listed
}

// servePick sets the account up to import the export picked on the
// browse page, and starts scanning it.
func (imp) servePick(w http.ResponseWriter, r *http.Request, sc *importer.SetupContext) error {
	acct := sc.AccountNode
	switch acct.Attr(attrState) {
	case "", stateScanning, stateReview:
	default:
		return errors.New("the account's import has started; add another account to import another export")
	}
	b, err := browse(r, sc)
	if err != nil {
		return err
	}
	exps, err := b.exports(sc)
	if err != nil {
		return err
	}
	exp, err := takeout.FindExport(exps, r.FormValue("export"))
	if err != nil {
		return err
	}
	if other, err := otherAccount(sc, exp.Name); err != nil {
		return err
	} else if other.Valid() {
		return fmt.Errorf("export %s is imported by another account already, %v", exp.Name, other)
	}
	var zipFiles []string
	for _, z := range exp.Zips {
		f, ok := z.File()
		if !ok {
			return fmt.Errorf("zip file %s has no source", z.Name)
		}
		zj, err := json.Marshal(f)
		if err != nil {
			return err
		}
		zipFiles = append(zipFiles, string(zj))
	}
	edited := ""
	if r.FormValue("edited") == "on" {
		edited = "true"
	}
	if err := acct.SetAttrValues(attrZip, zipFiles); err != nil {
		return err
	}
	if err := acct.SetAttrsWithDeletes([]string{attrOverview, attrPaused, attrFolder},
		attrFileSource, b.info.Ref.String(),
		attrFolder, b.folderID(),
		attrExport, exp.Name,
		attrEdited, edited,
		nodeattr.Title, "Google Takeout export "+exp.Name,
		attrState, stateScanning,
	); err != nil {
		return err
	}
	sc.StartRun()
	http.Redirect(w, r, sc.AccountURL(), http.StatusSeeOther)
	return nil
}

func (b *browsed) folderID() string {
	if b.folder == nil {
		return ""
	}
	return b.folder.ID
}

// otherAccount returns another account importing the export named name,
// if any.
func otherAccount(sc *importer.SetupContext, name string) (blob.Ref, error) {
	attr := func(k, v string) *search.Constraint {
		return &search.Constraint{Permanode: &search.PermanodeConstraint{Attr: k, Value: v}}
	}
	res, err := sc.Host.Searcher().Query(sc, &search.SearchQuery{
		Limit: -1,
		Constraint: &search.Constraint{Logical: &search.LogicalConstraint{
			Op: "and",
			A:  attr(nodeattr.Type, "importerAccount"),
			B:  attr(attrExport, name),
		}},
	})
	if err != nil {
		return blob.Ref{}, err
	}
	for _, b := range res.Blobs {
		if b.Blob != sc.AccountNode.PermanodeRef() {
			return b.Blob, nil
		}
	}
	return blob.Ref{}, nil
}

// AccountStatusHTML shows the account's overview and result, and the
// form to confirm its import.
func (imp) AccountStatusHTML(sc *importer.SetupContext) template.HTML {
	acct := sc.AccountNode
	data := statusData{
		CallbackPath: sc.CallbackPath(),
		State:        acct.Attr(attrState),
		Paused:       acct.Attr(attrPaused) == "true",
		Export:       acct.Attr(attrExport),
	}
	if ref, ok := blob.Parse(acct.Attr(attrFileSource)); ok {
		if info, err := sc.Host.FileSourceInfo(ref); err == nil {
			data.Source = &info
			data.SourcePath = sc.Host.FileSourcePath(ref)
		}
	}
	if v := acct.Attr(attrOverview); v != "" {
		data.Overview = new(overview)
		if err := json.Unmarshal([]byte(v), data.Overview); err != nil {
			data.Overview = nil
		}
	}
	if v := acct.Attr(attrResult); v != "" {
		data.Result = new(result)
		if err := json.Unmarshal([]byte(v), data.Result); err != nil {
			data.Result = nil
		}
	}
	if ref, ok := blob.Parse(acct.Attr(attrExportPermanode)); ok {
		data.ExportPermanode = ref
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "status", data); err != nil {
		return template.HTML(template.HTMLEscapeString(err.Error()))
	}
	return template.HTML(buf.String())
}

type statusData struct {
	CallbackPath    string
	State           string
	Paused          bool
	Export          string
	Source          *importer.FileSourceInfo
	SourcePath      string
	Overview        *overview
	Result          *result
	ExportPermanode blob.Ref
}

func execTemplate(w http.ResponseWriter, name string, data any) error {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := w.Write(buf.Bytes())
	return err
}

var tmpl = template.Must(template.New("root").Funcs(template.FuncMap{
	"bytes": takeoutimport.FormatBytes,
	"num":   func(n int) string { return strconv.Itoa(n) },
}).Parse(`
{{define "pageTop"}}
<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Google Takeout - Perkeep</title>
<style>
body {margin: 0; background: #f6f7f9; color: #1d2329; font: 15px/1.45 system-ui, -apple-system, "Segoe UI", sans-serif;}
main {max-width: 66em; margin: 0 auto; padding: 1.2em 2em 3em;}
h1 {font-size: 1.6em; font-weight: 600;}
a {color: #1f5fbf; text-decoration: none;}
.card {background: #fff; border: 1px solid #e1e4e8; border-radius: 8px; padding: 1.1em 1.3em; margin: 0 0 1.2em;}
.muted {color: #6a737d;}
.ref {font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .85em;}
table.list {border-collapse: collapse;}
table.list td, table.list th {padding: .35em .8em .35em 0; text-align: left;}
button {font: inherit; padding: .3em .9em; border: 1px solid #c9ced4; border-radius: 6px; background: #fff; cursor: pointer;}
button.primary {background: #1f5fbf; border-color: #1f5fbf; color: #fff;}
button.link {border: none; background: none; color: #1f5fbf; padding: 0;}
</style>
</head>
<body>
<main>
{{end}}

{{define "pageBottom"}}
</main>
</body>
</html>
{{end}}

{{define "setup"}}
{{template "pageTop"}}
<p><a href="{{.AccountURL}}">← Account</a></p>
<h1>Import a Google Takeout export</h1>
{{if .AlreadyStarted}}<p class="muted">Picking an export again starts over, scanning it again.</p>{{end}}
<section class="card">
<p>Where are the export's zip files?</p>
{{if .Sources}}
<form method="post" action="{{.CallbackPath}}">
  <input type="hidden" name="step" value="browse">
  {{range .Sources}}
  <p><label><input type="radio" name="source" value="{{.Ref}}"{{if eq .Ref.String $.Current}} checked{{end}}{{if not .Authorized}} disabled{{end}}> {{.Title}} <span class="muted">({{.TypeTitle}}{{if not .Authorized}}, needs authorizing{{end}})</span></label></p>
  {{end}}
  <p><button class="primary" type="submit">Look for exports</button></p>
</form>
{{else}}
<p>There are no file sources yet.</p>
{{end}}
<p class="muted">Add a Google Drive, or a directory on this server, on the <a href="{{.SourcesPath}}">file sources</a> page.</p>
</section>
{{template "pageBottom"}}
{{end}}

{{define "browse"}}
{{template "pageTop"}}
<p><a href="{{.AccountURL}}">← Account</a></p>
<h1>Exports in {{.Source.Title}}</h1>
<section class="card">
<p class="muted">Looked in {{if .Default}}the default folders: {{end}}{{range $i, $n := .Looked}}{{if $i}}, {{end}}{{$n}}/{{end}}</p>
{{if .Exports}}
<form method="post" action="{{.CallbackPath}}">
  <input type="hidden" name="step" value="pick">
  <input type="hidden" name="source" value="{{.Source.Ref}}">
  <input type="hidden" name="folder" value="{{.FolderID}}">
  <table class="list">
  <tr><th></th><th>Export</th><th>Made</th><th>Zip files</th><th>Size</th></tr>
  {{range $i, $e := .Exports}}
  <tr><td><input type="radio" name="export" value="{{.Name}}" id="exp{{$i}}"{{if eq $i 0}} checked{{end}}></td><td><label for="exp{{$i}}" class="ref">{{.Name}}</label></td><td>{{if not .Time.IsZero}}{{.Time.Format "2006-01-02 15:04 MST"}}{{end}}</td><td>{{.Zips}}</td><td>{{.Size}}</td></tr>
  {{end}}
  </table>
  <p><label><input type="checkbox" name="edited"> Import edited copies of Google Photos photos too (named like IMG_1234-edited.jpg)</label></p>
  <p><button class="primary" type="submit">Scan this export</button> <span class="muted">It's read and checked against this server, for you to review before it's imported.</span></p>
</form>
{{else}}
<p>No Takeout exports here.</p>
{{end}}
</section>

<section class="card">
<p>Or look in another folder{{with .Top}}, in {{.}}/{{end}}:</p>
<form method="post" action="{{.CallbackPath}}">
  <input type="hidden" name="step" value="browse">
  <input type="hidden" name="source" value="{{.Source.Ref}}">
  {{range .Folders}}
  <button class="link" type="submit" name="folder" value="{{.ID}}">{{.Name}}/</button>{{if .Ambiguous}} <span class="muted ref">{{.ID}}</span>{{end}}<br>
  {{else}}
  <p class="muted">It has no folders.</p>
  {{end}}
  {{if not .Default}}<p><button class="link" type="submit" name="folder" value="">Back to the default folders</button></p>{{end}}
</form>
<form method="post" action="{{.CallbackPath}}">
  <input type="hidden" name="step" value="browse">
  <input type="hidden" name="source" value="{{.Source.Ref}}">
  {{if eq .Source.Type "gdrive"}}
  <p><label for="goto">Or go to a folder by its URL, from Google Drive in a browser:</label><br>
  <input id="goto" name="goto" type="text" size="70" placeholder="https://drive.google.com/drive/folders/..." required>
  {{else}}
  <p><label for="goto">Or go to a folder by its path, in the directory:</label><br>
  <input id="goto" name="goto" type="text" size="50" placeholder="Takeout/2026" required>
  {{end}}
  <button type="submit">Go</button></p>
</form>
</section>
{{template "pageBottom"}}
{{end}}

{{define "status"}}
<h2>Export {{.Export}}</h2>
<p>
{{if eq .State "scanning"}}{{if .Paused}}Scanning was paused.{{else}}Reading the export's tables of contents and checking what of it this server has; see the log below.{{end}}
{{else if eq .State "review"}}Scanned. Review what it has, and confirm the import.
{{else if eq .State "importing"}}{{if .Paused}}The import was paused.{{else}}Importing; see the log below.{{end}}
{{else if eq .State "done"}}Imported{{with .Result}} {{.Finished.Format "2006-01-02 15:04 MST"}}{{end}}.
{{end}}
{{with .Source}}From <a href="{{$.SourcePath}}">{{.Title}}</a>{{if not .Authorized}} <span class="failed">(needs authorizing)</span>{{end}}.{{end}}
</p>
{{with .Overview}}
<dl class="props">
  <dt>Zip files</dt><dd>{{.Zips}} ({{bytes .ZipBytes}})</dd>
  <dt>Products</dt><dd>{{range $i, $p := .Products}}{{if $i}}, {{end}}{{$p.Name}} <span class="muted">({{num $p.Files}} files, {{bytes $p.Bytes}})</span>{{end}}</dd>
</dl>
{{with .Photos}}{{template "photosOverview" .}}{{end}}
{{end}}
{{if eq .State "review"}}
<p class="muted">Importing keeps the whole export, of all its products, and imports Google Photos: it uploads the photos and videos this server doesn't have, with their metadata, and updates the metadata of the photos it has, and the albums.</p>
<div class="actions">
<form method="post" action="{{.CallbackPath}}" class="inline">
  <input type="hidden" name="step" value="confirm">
  <button class="primary" type="submit">Import</button>
</form>
<form method="post" action="{{.CallbackPath}}" class="inline">
  <input type="hidden" name="step" value="rescan">
  <button type="submit">Scan again</button>
</form>
</div>
{{end}}
{{with .Result}}
{{if $.ExportPermanode.Valid}}<dl class="props"><dt>Archived export</dt><dd class="ref">{{$.ExportPermanode}}</dd></dl>{{end}}
{{with .Photos}}{{template "photosResult" .}}{{end}}
{{end}}
{{end}}

{{define "photosOverview"}}
<h3>Google Photos</h3>
<dl class="props">
  <dt>Photos and videos</dt><dd>{{.Files}} files, {{.Distinct}} distinct, {{bytes .Bytes}}; {{.Edited}} edited copies{{if .WithEdited}} included{{else}} skipped{{end}}</dd>
  <dt>Albums</dt><dd>{{.Albums}}</dd>
  <dt>This server has</dt><dd>{{.Have}} ({{bytes .HaveBytes}})</dd>
  <dt>To upload</dt><dd>{{.Missing}} ({{bytes .MissingBytes}})</dd>
</dl>
{{end}}

{{define "photosResult"}}
<h3>Google Photos, imported</h3>
<dl class="props">
  <dt>Uploaded</dt><dd>{{.Uploaded}} photos and videos ({{bytes .UploadedBytes}})</dd>
  <dt>Metadata</dt><dd>{{.MetaString}}</dd>
</dl>
{{end}}
`))
