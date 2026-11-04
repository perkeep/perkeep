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

package importer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/filesource"
	"perkeep.org/pkg/filesource/dropbox"
	"perkeep.org/pkg/filesource/gdrive"
	"perkeep.org/pkg/schema"
)

// An archive importer's ZIP can come from a file source, rather than be
// uploaded from the browser: the pages at /importer/TYPE/archive-source
// browse a source's folders for it, and copy the one picked into the
// server as a file, whose ref the importer's page then inspects and
// imports, as it does an uploaded one's.

// maxSourceArchive is the size of the largest ZIP copied from a file
// source, as of the largest uploaded (see archive.js).
const maxSourceArchive = 32 << 30

// FileSourceFolderID returns the ID of the folder of the file source of
// type typ that s names, as typed to go to it: for a Google Drive, its
// URL, as the Drive web app shows it, or its ID; for a Dropbox, its URL,
// as the Dropbox web app shows it, or its path; for a directory on the
// server, its path beneath the directory.
func FileSourceFolderID(typ, s string) (string, error) {
	switch typ {
	case fileSourceGoogleDrive:
		return gdrive.FolderID(s)
	case fileSourceDropbox:
		return dropbox.FolderPath(s)
	}
	return path.Clean(strings.TrimPrefix(strings.TrimSpace(s), "/")), nil
}

// archiveSourceBody is the page of a file source's folder, to pick an
// archive importer's ZIP in it, or, without a source, of the sources.
type archiveSourceBody struct {
	Importer    *importer
	SourcesPath string
	Sources     []FileSourceInfo // to pick from, without Source

	Source  *FileSourceInfo
	Folder  filesource.File
	Folders []listedFile
	Zips    []listedFile
}

type archiveSourcePage struct {
	Title string
	Body  archiveSourceBody
}

// serveArchiveSource serves the pages to pick an archive importer's ZIP
// from a file source, and copies the one picked.
func (h *Host) serveArchiveSource(w http.ResponseWriter, r *http.Request, im *importer) {
	if _, ok := im.impl.(ArchiveImporter); !ok {
		http.NotFound(w, r)
		return
	}
	// Only the owner may browse sources, as they lead to all their files.
	if !h.allowCredentialSetup(w, r) {
		return
	}
	ctx := r.Context()
	if r.Method == http.MethodPost {
		ref, err := h.copyFromSource(ctx, r.FormValue("source"), r.FormValue("file"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, im.URL()+"?fileRef="+url.QueryEscape(ref.String()), http.StatusSeeOther)
		return
	}
	body := archiveSourceBody{Importer: im, SourcesPath: h.FileSourcesPath()}
	srcRef, ok := blob.Parse(r.FormValue("source"))
	if !ok {
		srcs, err := h.FileSources(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		body.Sources = srcs
		h.execTemplate(w, r, archiveSourcePage{Title: "Import a " + im.Title() + " ZIP from a file source", Body: body})
		return
	}
	info, err := h.FileSourceInfo(srcRef)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	src, err := h.FileSource(ctx, srcRef)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := r.FormValue("folder")
	if s := strings.TrimSpace(r.FormValue("goto")); s != "" {
		if id, err = FileSourceFolderID(info.Type, s); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	folder, err := src.Root(ctx)
	if err == nil && id != "" {
		folder, err = src.Stat(ctx, id)
	}
	if err == nil && !folder.Dir {
		err = fmt.Errorf("%s is not a folder", folder.Name)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	files, err := src.List(ctx, folder)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var dirs, zips []filesource.File
	for _, f := range files {
		switch {
		case f.Dir:
			dirs = append(dirs, f)
		case strings.EqualFold(path.Ext(f.Name), ".zip"):
			zips = append(zips, f)
		}
	}
	body.Source, body.Folder = &info, folder
	body.Folders, body.Zips = listedFiles(dirs), listedFiles(zips)
	h.execTemplate(w, r, archiveSourcePage{Title: "Import a " + im.Title() + " ZIP from " + info.Title, Body: body})
}

// copyFromSource copies the file fileID of the file source srcRef into
// the server, as a file, returning its file schema blob's ref.
func (h *Host) copyFromSource(ctx context.Context, srcRef, fileID string) (blob.Ref, error) {
	ref, ok := blob.Parse(srcRef)
	if !ok || fileID == "" {
		return blob.Ref{}, errors.New("pick a ZIP from a file source")
	}
	src, err := h.FileSource(ctx, ref)
	if err != nil {
		return blob.Ref{}, err
	}
	f, err := src.Stat(ctx, fileID)
	if err != nil {
		return blob.Ref{}, err
	}
	switch {
	case f.Dir:
		return blob.Ref{}, fmt.Errorf("%s is a folder", f.Name)
	case f.Size <= 0 || f.Size > maxSourceArchive:
		return blob.Ref{}, fmt.Errorf("%s is %d bytes; a ZIP must be non-empty and no larger than 32 GiB", f.Name, f.Size)
	}
	rc, err := src.OpenRange(ctx, f, 0, f.Size)
	if err != nil {
		return blob.Ref{}, err
	}
	defer rc.Close()
	return schema.WriteFileMap(ctx, h.target, schema.NewFileMap(f.Name), rc)
}

const archiveSourceTmpl = `
{{define "archiveSourcePage"}}
  {{template "pageTop" .}}
  {{template "archiveSourceBody" .Body}}
  {{template "pageBottom"}}
{{end}}

{{define "archiveSourceBody"}}
{{$im := .Importer}}
<nav class="crumbs"><a href="{{$im.ImportersURL}}">Importers</a> › <a href="{{$im.URL}}">{{$im.Title}}</a></nav>
<h1>Import a {{$im.Title}} ZIP from a file source</h1>
{{if not .Source}}
<section class="card">
  {{if .Sources}}
  <form method="get">
    {{range .Sources}}
    <p><label><input type="radio" name="source" value="{{.Ref}}" required{{if not .Authorized}} disabled{{end}}> {{.Title}} <span class="muted">({{.TypeTitle}}{{if not .Authorized}}, needs authorizing{{end}})</span></label></p>
    {{end}}
    <div class="actions"><button class="primary" type="submit">Look for ZIPs</button></div>
  </form>
  {{else}}
  <p>There are no file sources yet.</p>
  {{end}}
  <p class="muted">Add a Google Drive, a Dropbox, or a directory on this server, on the <a href="{{.SourcesPath}}">file sources</a> page.</p>
</section>
{{else}}
{{$src := .Source}}
<section class="card">
  <h2>{{.Folder.Name}}/ <span class="muted">in {{$src.Title}}</span></h2>
  {{if .Zips}}
  <table class="list">
    <thead><tr><th>ZIP</th><th>Size</th><th>Modified</th><th></th></tr></thead>
    <tbody>
    {{range .Zips}}
    <tr>
      <td>{{.Name}}{{if .Ambiguous}} <span class="muted ref">{{.ID}}</span>{{end}}</td>
      <td>{{bytes .Size}}</td>
      <td>{{if not .ModTime.IsZero}}{{.ModTime.Format "2006-01-02 15:04"}}{{end}}</td>
      <td><form method="post" class="inline">
        <input type="hidden" name="source" value="{{$src.Ref}}">
        <input type="hidden" name="file" value="{{.ID}}">
        <button class="primary" type="submit">Import</button>
      </form></td>
    </tr>
    {{end}}
    </tbody>
  </table>
  <p class="muted">The ZIP is copied into Perkeep, then checked, for you to confirm its import.</p>
  {{else}}
  <p class="muted">No ZIPs here.</p>
  {{end}}
</section>
<section class="card">
  <h2>Folders</h2>
  <form method="get">
    <input type="hidden" name="source" value="{{$src.Ref}}">
    {{range .Folders}}
    <button class="link" type="submit" name="folder" value="{{.ID}}">{{.Name}}/</button>{{if .Ambiguous}} <span class="muted ref">{{.ID}}</span>{{end}}<br>
    {{else}}
    <p class="muted">No folders here.</p>
    {{end}}
    <p><button class="link" type="submit" name="folder" value="">Back to the top</button></p>
  </form>
  <form method="get">
    <input type="hidden" name="source" value="{{$src.Ref}}">
    <p><label for="goto">Or go to a folder by {{if eq $src.Type "local"}}its path, in the directory{{else}}its URL, from {{$src.TypeTitle}} in a browser{{if eq $src.Type "dropbox"}}, or its path{{end}}{{end}}:</label><br>
    <input id="goto" name="goto" type="text" size="70" required>
    <button type="submit">Go</button></p>
  </form>
</section>
{{end}}
{{end}}
`
