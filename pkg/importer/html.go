/*
Copyright 2013 Google Inc.

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

	"camlistore.org/pkg/blob"
)

func (h *Host) execTemplate(w http.ResponseWriter, r *http.Request, data interface{}) {
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
	Host      *Host
	Importer  *importer
	SetupHelp template.HTML
}

type acctPage struct {
	Title string
	Body  acctBody
}

type acctBody struct {
	Acct       *importerAcct
	AcctType   string
	Running    bool
	LastStatus string
	StartedAgo time.Duration // or zero if !Running
	LastAgo    time.Duration // non-zero if previous run && !Running
	LastError  string
}

var tmpl = template.Must(template.New("root").Funcs(map[string]interface{}{
	"bloblink": func(br blob.Ref) string {
		panic("should be overridden; this one won't be called")
	},
}).Parse(`
{{define "pageTop"}}
<html>
<head>
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
      {{$base := .Host.ImporterBaseURL}}
      {{range .Importers}}
         <li><a href="{{$base}}{{.Name}}">{{.Name}}</a></li>
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
    <h1>Client ID &amp; Client Secret</h1>
    <form method='post'>
      <input type='hidden' name="mode" value="saveclientidsecret">
      <table border=0 cellpadding=3>
      <tr><td align=right>Client ID</td><td><input name="clientID" size=50 value="{{.Importer.ClientID}}"></td></tr>
      <tr><td align=right>Client Secret</td><td><input name="clientSecret" size=50 value="{{.Importer.ClientSecret}}"></td></tr>
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
{{if .Importer.CanAddNewAccount}}
    <form method='post'>
      <input type='hidden' name="mode" value="newacct">
      <input type='submit' value="Add new account">
    </form>
{{end}}

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
   <li>Import interval: {{if .Acct.RefreshInterval}}{{.Acct.RefreshInterval}}{{else}}(manual){{end}}</li>
   <li>Running: {{.Running}}</li>
   {{if .Running}}
     <li>Started: {{.StartedAgo}} ago</li>
     <li>Last status: {{.LastStatus}}</li>
   {{else}}
     {{if .LastAgo}}
        <li>Previous run: {{.LastAgo}} ago{{if .LastError}}: {{.LastError}}{{else}} (success){{end}}</li>
     {{end}}
   {{end}}
</ul>

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

<form method='post' style='display: inline'>
<input type='hidden' name='mode' value='delete'>
<input type='submit' value='Delete Account' onclick='return confirm("Delete account?")'>
</form>

{{end}}

`))
