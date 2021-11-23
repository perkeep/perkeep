/*
Copyright 2013 The Perkeep Authors

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
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"perkeep.org/pkg/blob"
)

func (h *Host) execTemplate(w http.ResponseWriter, r *http.Request, data any) {
	tmplName := strings.TrimPrefix(fmt.Sprintf("%T", data), "importer.")
	var buf bytes.Buffer
	err := h.tmpl.ExecuteTemplate(&buf, tmplName, data)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error executing template %q: %v", tmplName, err), 500)
		return
	}
	w.Write(buf.Bytes())
}

type importersRootPage struct {
	Title string
	Body  importersRootBody
}

type importersRootBody struct {
	Host      *Host
	Importers []*importer
}

type importerPage struct {
	Title string
	Body  importerBody
}

type importerBody struct {
	Host                       *Host
	Importer                   *importer
	ShowSecureTransportWarning bool
	SetupHelp                  template.HTML
	ArchiveUpload              *archiveUploadData
}

type acctPage struct {
	Title string
	Body  acctBody
}

type acctBody struct {
	Acct           *importerAcct
	AcctType       string
	Running        bool
	LastStatus     string
	StartedAgo     time.Duration // or zero if !Running
	LastAgo        time.Duration // non-zero if previous run && !Running
	LastError      string
	HasSomePaidAPI bool
	ArchiveUpload  *archiveUploadData
	ArchiveOnly    bool
}

var tmpl = template.Must(template.New("root").Funcs(map[string]any{
	"bloblink": func(br blob.Ref) string {
		panic("should be overridden; this one won't be called")
	},
}).Parse(`
{{define "pageTop"}}
<!doctype html>
<html>
<head>
   <meta charset="utf-8">
   <meta name="viewport" content="width=device-width, initial-scale=1">
   <title>{{.Title}}</title>
</head>
<body>
   <h1>{{.Title}}</h1>
{{end}}

{{define "pageBottom"}}
</body>
</html>
{{end}}


{{define "importersRootPage"}}
  {{template "pageTop" .}}
  {{template "importersRootBody" .Body}}
  {{template "pageBottom"}}
{{end}}

{{define "importersRootBody"}}
   <ul>
      {{$base := .Host.ImporterBasePath}}
      {{range .Importers}}
         {{if .TODOIssue}}
             <li><b>{{.Title}}</b>: TODO: <a href="https://perkeep.org/issue/{{.TODOIssue}}">Issue {{.TODOIssue}}</a></li>
         {{else}}
             <li><b><a href="{{$base}}{{.Name}}">{{.Title}}</a></b>{{if .Description}}: {{.Description}}{{end}}</li>
         {{end}}
      {{end}}
   </ul>
{{end}}


{{define "importerPage"}}
  {{template "pageTop" .}}
  {{template "importerBody" .Body}}
  {{template "pageBottom"}}
{{end}}

{{define "importerBody"}}
<p>[<a href="{{.Host.ImporterBaseURL}}">&lt;&lt; Back</a>]</p>
<ul>
  <li>Importer configuration permanode: {{.Importer.Node.PermanodeRef | bloblink}}</li>
  <li>Status: {{.Importer.Status}}</li>
</ul>

{{if .Importer.ShowClientAuthEditForm}}
	{{if .ShowSecureTransportWarning}}
	<h1 style="color:red;">This page is not served securely (no https). Proceed at your own risk.</h1>
	{{end}}
    <h1>Client ID &amp; Client Secret</h1>
    <form method='post'>
      <input type='hidden' name="mode" value="saveclientidsecret">
      <table border=0 cellpadding=3>
      <tr><td align=right>Client ID</td><td><input name="clientID" size=50 value="{{.Importer.ClientID}}"></td></tr>
      <tr><td align=right>Client Secret</td><td><input name="clientSecret" type=password size=50 value="{{.Importer.ClientSecret}}"></td></tr>
      <tr><td align=right></td><td><input type='submit' value="Save"></td></tr>
      </table>
    </form>
{{end}}

{{.SetupHelp}}


<h1>Accounts</h1>
<ul>
    {{range .Importer.Accounts}}
       <li><a href="{{.AccountURL}}">{{.AccountLinkText}}</a> {{.AccountLinkSummary}}</li>
    {{end}}
</ul>
{{if .ArchiveUpload}}
  {{template "archiveUpload" .ArchiveUpload}}
{{end}}
{{if and .Importer.CanAddNewAccount (not .Importer.ArchiveOnly)}}
    <form method='post'>
      <input type='hidden' name="mode" value="newacct">
      <input type='submit' value="Add new account">
    </form>
{{end}}

{{end}}

{{define "archiveUpload"}}
<style>
.archive-import {max-width: 48em; padding: 1em; margin: 1em 0; border: 1px solid #ccc; border-radius: .3em; font-family: sans-serif;}
.archive-import h2 {margin-top: 0;}
.archive-import button, .archive-import input {margin: .3em 0; font: inherit;}
.archive-import progress {width: 100%;}
.archive-import .archive-error {color: #a00; white-space: pre-wrap;}
.archive-import .archive-ref {font-family: monospace; overflow-wrap: anywhere;}
.archive-import ul {max-height: 20em; overflow: auto; white-space: pre-wrap;}
</style>
<section class="archive-import" id="archive-import"
  data-discovery-url="{{.DiscoveryURL}}"
  data-archive-url="{{.ArchiveURL}}"
  data-account-base="{{.AccountBase}}"
  data-account-ref="{{.AccountRef}}"
  data-file-ref="{{.FileRef}}">
  <h2>{{if .AccountPage}}Archive import{{else}}Upload a {{.Title}} ZIP{{end}}</h2>
  <noscript>JavaScript is required to upload and import an archive.</noscript>
  {{if .AccountPage}}
    <p><a href="{{.AccountBase}}">Upload another ZIP</a>. Its identity will be checked before choosing an account.</p>
  {{else}}
    <p>Choose an account export ZIP. After uploading, check the detected identity and confirm the import.
       Existing accounts and activities are reused on later imports.</p>
  {{end}}
  <form id="archive-upload-form"{{if .AccountPage}} hidden{{end}}>
    <label for="archive-file">Account export ZIP</label><br>
    <input id="archive-file" name="archive" type="file" accept=".zip,application/zip" required><br>
    <button id="archive-upload" type="submit" disabled>Upload ZIP</button>
    <button id="archive-cancel" type="button" hidden>Cancel upload</button>
  </form>
  <progress id="archive-progress" max="1" value="0" hidden></progress>
  <p id="archive-message" role="status" aria-live="polite"></p>
  <p id="archive-error" class="archive-error" role="alert" hidden></p>
  <p id="archive-file-info" hidden>Stored ZIP: <span id="archive-file-ref" class="archive-ref"></span>.
    Inspection and import retries reuse this file without uploading it again.</p>
  <div id="archive-confirm" hidden>
    <p id="archive-identity"></p>
    <p id="archive-account-match"></p>
    <button id="archive-start" type="button">Import archive</button>
  </div>
  <p id="archive-account-link-container" hidden><a id="archive-account-link">Account details</a></p>
  <ul id="archive-warnings" hidden aria-label="Import warnings"></ul>
  <button id="archive-retry" type="button" hidden>Retry</button>
</section>
<script src="{{.ScriptURL}}" defer></script>
{{end}}

{{define "acctPage"}}
  {{template "pageTop" .}}
  {{template "acctBody" .Body}}
  {{template "pageBottom"}}
{{end}}

{{define "acctBody"}}
<p>[<a href="./">&lt;&lt; Back</a>]</p>
<ul>
   <li>Account type: {{.AcctType}}</li>
   <li>Account metadata permanode: {{.Acct.AccountObject.PermanodeRef | bloblink}}</li>
   <li>Import root permanode: {{if .Acct.RootObject}}{{.Acct.RootObject.PermanodeRef | bloblink}}{{else}}(none){{end}}</li>
   <li>Configured: {{.Acct.IsAccountReady}}</li>
   <li>Summary: {{.Acct.AccountLinkSummary}}</li>
   <li>Import interval: {{if .ArchiveOnly}}(manual archive uploads){{else if .Acct.RefreshInterval}}{{.Acct.RefreshInterval}}{{else}}(manual){{end}}</li>
   {{if not .ArchiveOnly}}
   <li>Running: {{.Running}}</li>
   {{if .Running}}
     <li>Started: {{.StartedAgo}} ago</li>
     <li>Last status: {{.LastStatus}}</li>
   {{else}}
     {{if .LastAgo}}
        <li>Previous run: {{.LastAgo}} ago{{if .LastError}}: {{.LastError}}{{else}} (success){{end}}</li>
     {{end}}
   {{end}}
   {{end}}
  {{if .HasSomePaidAPI}}
    <form method='post' style='display: inline'>
        <input type='hidden' name='mode' value='togglepaidapi'>
        <input type='checkbox' name='usePaidAPI' {{if .Acct.UsePaidAPI}}checked{{end}} onchange='this.form.submit()'>
          <label>Use Paid API</label>
      </form>
  {{end}}
</ul>

{{if .ArchiveUpload}}
  {{template "archiveUpload" .ArchiveUpload}}
{{end}}

{{if .ArchiveOnly}}
   <form method='post' style='display: inline'>
     <input type='hidden' name='mode' value='stop'>
     <button id="archive-pause" type='submit'{{if not .Running}} disabled{{end}}>Pause Import</button>
   </form>
{{else}}
{{if .Acct.IsAccountReady}}
   <form method='post' style='display: inline'>
   {{if .Running}}
     <input type='hidden' name='mode' value='stop'>
     <input type='submit' value='Pause Import'>
   {{else}}
     <input type='hidden' name='mode' value='start'>
     <input type='submit' value='Start Import'>
   {{end}}
   </form>
{{end}}

<form method='post' style='display: inline'>
<input type='hidden' name='mode' value='login'>
<input type='submit' value='Re-login'>
</form>

<form method='post' style='display: inline'>
<input type='hidden' name='mode' value='toggleauto'>
<input type='submit' value='Toggle auto'>
</form>
{{end}}

<form method='post' style='display: inline'>
<input type='hidden' name='mode' value='delete'>
<input type='submit' value='Delete Account' onclick='return confirm("Delete account?")'>
</form>

{{end}}

`))
