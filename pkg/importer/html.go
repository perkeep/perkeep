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
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sort"
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
	Accounts  []accountRow // of all importers
}

type importerPage struct {
	Title string
	Body  importerBody
}

type importerBody struct {
	Host                       *Host
	Importer                   *importer
	Accounts                   []accountRow
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
	Importer       *importer
	Status         string
	Running        bool
	HasSomePaidAPI bool
	ArchiveUpload  *archiveUploadData
	ArchiveOnly    bool
}

// An accountRow is an account as the importer pages list it.
type accountRow struct {
	Importer *importer
	Acct     *importerAcct
	Name     string        // the importer's summary of the account
	Auto     time.Duration // between automatic imports, or zero
	Status   string
	Failed   bool // whether its last run failed
	Running  bool
	Root     blob.Ref
}

// accountRows returns im's accounts, for listing. Without a search
// handler (as in some tests), it has none.
func (im *importer) accountRows() ([]accountRow, error) {
	if im.host.search == nil {
		return nil, nil
	}
	accts, err := im.Accounts()
	if err != nil {
		return nil, err
	}
	var rows []accountRow
	for _, ia := range accts {
		status, running, failed := ia.status()
		row := accountRow{
			Importer: im,
			Acct:     ia,
			Name:     ia.AccountLinkSummary(),
			Auto:     ia.RefreshInterval(),
			Status:   status,
			Running:  running,
			Failed:   failed,
		}
		if ia.root != nil {
			row.Root = ia.root.PermanodeRef()
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

// status returns a short description of the state of ia's runs, and
// whether it's running and whether its last run failed.
func (ia *importerAcct) status() (status string, running, failed bool) {
	ready, _ := ia.IsAccountReady()
	ia.mu.Lock()
	defer ia.mu.Unlock()
	switch {
	case ia.current != nil:
		return "Running for " + roughDuration(time.Since(ia.lastRunStart)), true, false
	case !ia.lastRunDone.IsZero() && ia.lastRunErr != nil:
		var rae RetryAfterError
		if errors.As(ia.lastRunErr, &rae) {
			s := "Paused by the service's rate limit"
			if t := rae.RetryAfter(); t.After(time.Now()) {
				s += " until " + t.Local().Format("Jan 2 15:04 MST")
			}
			if ia.RefreshInterval() > 0 {
				return s + "; the next automatic import goes on", false, false
			}
			return s + "; start the import again then to go on", false, false
		}
		return fmt.Sprintf("Failed %s ago: %v", roughDuration(time.Since(ia.lastRunDone)), ia.lastRunErr), false, true
	case !ia.lastRunDone.IsZero():
		return fmt.Sprintf("Imported %s ago", roughDuration(time.Since(ia.lastRunDone))), false, false
	case ia.acct.isPending():
		return "Not saved: finish setting it up", false, false
	case !ready:
		return "Not set up", false, false
	case ia.im.props.ArchiveOnly:
		return "Imports uploaded archives", false, false
	}
	return "Not run since the server started", false, false
}

// roughDuration formats d to about two significant units, such as "3m",
// "2h5m", or "4d".
func roughDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return d.Round(time.Second).String()
	case d < time.Hour:
		return d.Round(time.Minute).String()
	case d < 48*time.Hour:
		s := d.Round(time.Minute).String()
		return strings.TrimSuffix(s, "0s")
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// NeedsAPIKey reports whether im needs an API key (an OAuth client ID
// and secret) for its accounts.
func (im *importer) NeedsAPIKey() bool { return im.props.NeedsAPIKey }

// ImportersURL returns the path of the page of all importers.
func (im *importer) ImportersURL() string { return im.host.ImporterBasePath() }

// ConfigNodeRef returns im's configuration permanode, where its API key
// is kept unless the server's configuration has it, or a zero Ref if
// it has none.
func (im *importer) ConfigNodeRef() blob.Ref {
	n, err := im.existingNode()
	if err != nil || n == nil {
		return blob.Ref{}
	}
	return n.PermanodeRef()
}

// IsPending reports whether ia's setup isn't saved yet, so its
// permanode isn't stored.
func (ia *importerAcct) IsPending() bool { return ia.acct.isPending() }

var tmpl = template.Must(template.New("root").Funcs(map[string]any{
	"bloblink": func(br blob.Ref) string {
		panic("should be overridden; this one won't be called")
	},
	"bloburl": func(br blob.Ref) string {
		panic("should be overridden; this one won't be called")
	},
	"shortref": func(br blob.Ref) string {
		s := br.String()
		if i := strings.IndexByte(s, '-'); i >= 0 && len(s) > i+11 {
			return s[:i+11] + "…"
		}
		return s
	},
	"accountsTable": func(rows []accountRow, showType bool) accountsTableData {
		return accountsTableData{Rows: rows, ShowType: showType}
	},
}).Parse(`
{{define "pageTop"}}
<!doctype html>
<html>
<head>
   <meta charset="utf-8">
   <meta name="viewport" content="width=device-width, initial-scale=1">
   <title>{{.Title}} - Perkeep</title>
   <style>
   body {margin: 0; background: #f6f7f9; color: #1d2329; font: 15px/1.45 system-ui, -apple-system, "Segoe UI", sans-serif;}
   main {max-width: 66em; margin: 0 auto; padding: 1.2em 2em 3em;}
   nav.crumbs {font-size: .9em; color: #6a737d; margin-bottom: .4em;}
   nav.crumbs a {color: inherit;}
   h1 {font-size: 1.6em; font-weight: 600; margin: .1em 0 .6em;}
   h2 {font-size: 1.15em; font-weight: 600; margin: 0 0 .7em;}
   a {color: #1f5fbf; text-decoration: none;}
   a:hover {text-decoration: underline;}
   .card {background: #fff; border: 1px solid #e1e4e8; border-radius: 8px; padding: 1.1em 1.3em; margin: 0 0 1.2em;}
   .muted {color: #6a737d;}
   .lede {color: #4f5861; margin-top: -.3em;}
   table.list {border-collapse: collapse; width: 100%;}
   table.list th {text-align: left; font-size: .8em; font-weight: 600; color: #6a737d; text-transform: uppercase; letter-spacing: .03em; padding: .3em .7em; border-bottom: 1px solid #e1e4e8;}
   table.list td {padding: .55em .7em; border-bottom: 1px solid #f0f1f3; vertical-align: top;}
   table.list tr:last-child td {border-bottom: none;}
   .ref {font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .85em;}
   .running {color: #1a7f37;}
   .failed {color: #b42318;}
   button, input[type=submit] {font: inherit; padding: .35em .95em; border: 1px solid #c9ced4; border-radius: 6px; background: #fff; color: #1d2329; cursor: pointer;}
   button:hover {background: #f3f4f6;}
   button.primary {background: #1f5fbf; border-color: #1f5fbf; color: #fff;}
   button.primary:hover {background: #1b52a6;}
   button.danger {color: #b42318; border-color: #e5b4ae;}
   button.danger:hover {background: #fdf1f0;}
   select, input[type=text], input[type=password], input:not([type]) {font: inherit; padding: .3em .45em; border: 1px solid #c9ced4; border-radius: 6px;}
   form.inline {display: inline;}
   .actions {display: flex; flex-wrap: wrap; gap: .5em; margin-top: 1em;}
   dl.props {display: grid; grid-template-columns: max-content 1fr; gap: .45em 1.6em; margin: 0;}
   dl.props dt {color: #6a737d;}
   dl.props dd {margin: 0;}
   .warning {color: #b42318; font-weight: 600;}
   pre.log {max-height: 32em; overflow: auto; margin: 0; padding: .6em .8em; background: #f6f8fa; border-radius: 6px; font: .82em/1.4 ui-monospace, SFMono-Regular, Menlo, monospace; white-space: pre-wrap; overflow-wrap: anywhere;}
   pre.log:empty {display: none;}
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

{{define "accountsTable"}}
{{if .Rows}}
<table class="list">
  <thead><tr>{{if .ShowType}}<th>Type</th>{{end}}<th>Account</th><th>Auto</th><th>Status</th><th>Root</th></tr></thead>
  <tbody>
  {{range .Rows}}
    <tr>
      {{if $.ShowType}}<td><a href="{{.Importer.URL}}">{{.Importer.Title}}</a></td>{{end}}
      <td><a href="{{.Acct.AccountURL}}">{{or .Name "(unnamed)"}}</a></td>
      <td><input type="checkbox" disabled{{if .Auto}} checked title="Imports every {{.Auto}}"{{else}} title="Imports only when started"{{end}}></td>
      <td{{if .Running}} class="running"{{else if .Failed}} class="failed"{{end}}>{{.Status}}</td>
      <td>{{if .Root.Valid}}{{$root := .Root}}{{with bloburl $root}}<a class="ref" href="{{.}}">{{shortref $root}}</a>{{else}}<span class="ref">{{shortref $root}}</span>{{end}}{{end}}</td>
    </tr>
  {{end}}
  </tbody>
</table>
{{else}}
<p class="muted">No accounts yet.</p>
{{end}}
{{end}}

{{define "importersRootPage"}}
  {{template "pageTop" .}}
  <h1>{{.Title}}</h1>
  {{template "importersRootBody" .Body}}
  {{template "pageBottom"}}
{{end}}

{{define "importersRootBody"}}
{{$base := .Host.ImporterBasePath}}
<section class="card">
  <h2>Accounts</h2>
  {{template "accountsTable" (accountsTable .Accounts true)}}
</section>

<section class="card">
  <h2>Add an account</h2>
  <form method="post" action="{{$base}}">
    <input type="hidden" name="mode" value="newacct">
    <select name="importer" required>
      <option value="">Choose a service…</option>
      {{range .Importers}}{{if not .TODOIssue}}
        <option value="{{.Name}}">{{.Title}}{{if .ArchiveOnly}} (upload an archive){{else if not .CanAddNewAccount}} (needs an API key first){{end}}</option>
      {{end}}{{end}}
    </select>
    <button class="primary" type="submit">Add account</button>
  </form>
  <p class="muted">Nothing is saved until you finish setting the account up.</p>
</section>

<section class="card">
  <h2>Importers</h2>
  <table class="list">
    <thead><tr><th>Importer</th><th>Imports</th><th>Setup</th></tr></thead>
    <tbody>
    {{range .Importers}}
      <tr>
      {{if .TODOIssue}}
        <td class="muted">{{.Title}}</td><td class="muted">Not yet: <a href="https://perkeep.org/issue/{{.TODOIssue}}">issue {{.TODOIssue}}</a></td><td></td>
      {{else}}
        <td><a href="{{$base}}{{.Name}}">{{.Title}}</a></td>
        <td>{{.Description}}</td>
        <td class="muted">{{if .NeedsAPIKey}}{{if .CanAddNewAccount}}API key set{{else}}<a href="{{$base}}{{.Name}}">Needs an API key</a>{{end}}{{else if .ArchiveOnly}}Archive upload{{end}}</td>
      {{end}}
      </tr>
    {{end}}
    </tbody>
  </table>
</section>
{{end}}


{{define "importerPage"}}
  {{template "pageTop" .}}
  {{template "importerBody" .Body}}
  {{template "pageBottom"}}
{{end}}

{{define "importerBody"}}
<nav class="crumbs"><a href="{{.Host.ImporterBasePath}}">Importers</a> › {{.Importer.Title}}</nav>
<h1>{{.Importer.Title}}</h1>
{{with .Importer.Description}}<p class="lede">Imports {{.}}.</p>{{end}}

{{if .Importer.NeedsAPIKey}}
<section class="card">
  <h2>API key</h2>
  <p>{{.Importer.Status}}{{with .Importer.ConfigNodeRef}}{{if .Valid}}, in its configuration permanode {{bloblink .}}{{end}}{{end}}.</p>
  {{if .Importer.ShowClientAuthEditForm}}
    {{if .ShowSecureTransportWarning}}
    <p class="warning">This page is not served securely (no https). Proceed at your own risk.</p>
    {{end}}
    {{.SetupHelp}}
    <form method="post">
      <input type="hidden" name="mode" value="saveclientidsecret">
      <dl class="props">
        <dt><label for="clientID">Client ID</label></dt><dd><input id="clientID" name="clientID" size="50" value="{{.Importer.ClientID}}"></dd>
        <dt><label for="clientSecret">Client secret</label></dt><dd><input id="clientSecret" name="clientSecret" type="password" size="50" value="{{.Importer.ClientSecret}}"></dd>
      </dl>
      <div class="actions"><button class="primary" type="submit">Save</button></div>
    </form>
  {{end}}
</section>
{{end}}

<section class="card">
  <h2>Accounts</h2>
  {{template "accountsTable" (accountsTable .Accounts false)}}
  {{if not .Importer.ArchiveOnly}}
    {{if .Importer.CanAddNewAccount}}
    <form method="post" class="actions">
      <input type="hidden" name="mode" value="newacct">
      <button class="primary" type="submit">Add account</button>
    </form>
    {{else}}
    <p class="muted">Save a client ID and secret above to add an account.</p>
    {{end}}
  {{end}}
</section>

{{if .ArchiveUpload}}
  {{template "archiveUpload" .ArchiveUpload}}
{{end}}
{{end}}

` + archiveUploadTmpl + `

{{define "acctPage"}}
  {{template "pageTop" .}}
  {{template "acctBody" .Body}}
  {{template "pageBottom"}}
{{end}}

{{define "acctBody"}}
<nav class="crumbs"><a href="{{.Importer.ImportersURL}}">Importers</a> › <a href="{{.Importer.URL}}">{{.Importer.Title}}</a></nav>
<h1>{{.Acct.AccountLinkSummary}}</h1>

<section class="card">
  {{if .Acct.IsPending}}<p class="muted">This account isn't saved yet: <a href="#" onclick="document.getElementById('setup').submit(); return false">finish setting it up</a> to keep it.</p>{{end}}
  <dl class="props">
    <dt>Status</dt><dd id="acct-status"{{if .Running}} class="running"{{end}}>{{.Status}}</dd>
    {{if .ArchiveOnly}}
    <dt>Imports</dt><dd>From manual archive uploads</dd>
    {{else}}
    <dt>Automatic import</dt><dd>{{if .Acct.RefreshInterval}}Every {{.Acct.RefreshInterval}}{{else}}Off{{end}}</dd>
    {{end}}
    {{if .HasSomePaidAPI}}
    <dt>Paid API</dt><dd>
      <form method="post" class="inline">
        <input type="hidden" name="mode" value="togglepaidapi">
        <label><input type="checkbox" name="usePaidAPI" {{if .Acct.UsePaidAPI}}checked{{end}} onchange="this.form.submit()"> Use the service's paid API for extra data</label>
      </form>
    </dd>
    {{end}}
    <dt>Account permanode</dt><dd class="ref">{{bloblink .Acct.AccountObject.PermanodeRef}}</dd>
    <dt>Import root</dt><dd class="ref">{{if .Acct.RootObject}}{{bloblink .Acct.RootObject.PermanodeRef}}{{else}}(none){{end}}</dd>
  </dl>

  <div class="actions">
  {{if .ArchiveOnly}}
    <form method="post" class="inline">
      <input type="hidden" name="mode" value="stop">
      <button id="archive-pause" type="submit"{{if not .Running}} disabled{{end}}>Pause import</button>
    </form>
  {{else}}
    {{if .Acct.IsAccountReady}}
    <form method="post" class="inline">
    {{if .Running}}
      <input type="hidden" name="mode" value="stop">
      <button type="submit">Pause import</button>
    {{else}}
      <input type="hidden" name="mode" value="start">
      <button class="primary" type="submit">Start import</button>
    {{end}}
    </form>
    {{end}}
    <form method="post" class="inline" id="setup">
      <input type="hidden" name="mode" value="login">
      <button type="submit">{{if .Acct.IsAccountReady}}Change setup{{else}}Set up{{end}}</button>
    </form>
    <form method="post" class="inline">
      <input type="hidden" name="mode" value="toggleauto">
      <button type="submit">{{if .Acct.RefreshInterval}}Turn off automatic import{{else}}Import automatically{{end}}</button>
    </form>
  {{end}}
    <form method="post" class="inline">
      <input type="hidden" name="mode" value="delete">
      <button class="danger" type="submit" onclick="return confirm('Delete this {{.Importer.Title}} account? What it imported stays in Perkeep; only the account is removed.')">Delete account</button>
    </form>
  </div>
</section>

<section class="card">
  <h2>Log</h2>
  <pre id="acct-log" class="log"></pre>
  <p id="acct-log-empty" class="muted">Nothing logged since the server started.</p>
</section>
<script>
(function() {
	var pre = document.getElementById("acct-log");
	var empty = document.getElementById("acct-log-empty");
	var status = document.getElementById("acct-status");
	var next = 0;
	function poll() {
		fetch(location.pathname + "?log=" + next, {cache: "no-store"}).then(function(res) {
			return res.json();
		}).then(function(res) {
			var atBottom = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 4;
			if (res.text) {
				pre.textContent += res.text;
				empty.hidden = true;
				if (atBottom) {
					pre.scrollTop = pre.scrollHeight;
				}
			}
			next = res.next;
			status.textContent = res.status;
			status.className = res.running ? "running" : "";
			// Poll often while running, and now and then otherwise, to
			// notice a run that starts.
			setTimeout(poll, res.running ? 1000 : 10000);
		}).catch(function() {
			setTimeout(poll, 10000);
		});
	}
	poll();
})();
</script>

{{if .ArchiveUpload}}
  {{template "archiveUpload" .ArchiveUpload}}
{{end}}
{{end}}
`))

// accountsTableData is the data of the accountsTable template.
type accountsTableData struct {
	Rows     []accountRow
	ShowType bool // whether to have a column of the accounts' importers
}
