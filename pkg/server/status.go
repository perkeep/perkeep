/*
Copyright 2013 The Perkeep Authors.

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

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/buildinfo"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/health"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/server/app"
	"perkeep.org/pkg/types/camtypes"

	"cloud.google.com/go/compute/metadata"
	"go4.org/jsonconfig"
	"golang.org/x/net/xsrftoken"
)

// StatusHandler publishes server status information.
type StatusHandler struct {
	prefix        string
	handlerFinder blobserver.FindHandlerByTyper
	health        *health.Monitor // or nil
}

func init() {
	blobserver.RegisterHandlerConstructor("status", newStatusFromConfig)
}

var _ blobserver.HandlerIniter = (*StatusHandler)(nil)

func newStatusFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err error) {
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	return &StatusHandler{
		prefix:        ld.MyPrefix(),
		handlerFinder: ld,
		health:        ld.Health(),
	}, nil
}

func (sh *StatusHandler) InitHandler(hl blobserver.FindHandlerByTyper) error {
	_, h, err := hl.FindHandlerByType("search")
	if errors.Is(err, blobserver.ErrHandlerTypeNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	go func() {
		var lastSend *status
		for {
			cur := sh.currentStatus()
			if reflect.DeepEqual(cur, lastSend) {
				// TODO: something better. get notified on interesting events.
				time.Sleep(10 * time.Second)
				continue
			}
			lastSend = cur
			js, _ := json.MarshalIndent(cur, "", "  ")
			h.(*search.Handler).SendStatusUpdate(js)
		}
	}()
	return nil
}

func (sh *StatusHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	suffix := httputil.PathSuffix(req)
	if suffix == "restart" {
		sh.serveRestart(rw, req)
		return
	}
	if suffix == "health" {
		sh.serveHealthAction(rw, req)
		return
	}
	if !httputil.IsGet(req) {
		http.Error(rw, "Illegal status method.", http.StatusMethodNotAllowed)
		return
	}
	switch suffix {
	case "status.json":
		sh.serveStatusJSON(rw, req)
	case "":
		sh.serveStatusHTML(rw, req)
	default:
		http.Error(rw, "Illegal status path.", http.StatusNotFound)
	}
}

type status struct {
	Version      string                   `json:"version"`
	GoInfo       string                   `json:"goInfo"`
	Errors       []camtypes.StatusError   `json:"errors,omitempty"`
	Health       health.Status            `json:"health"`
	Sync         map[string]syncStatus    `json:"sync"`
	Storage      map[string]storageStatus `json:"storage"`
	importerRoot string
	rootPrefix   string

	ImporterAccounts any `json:"importerAccounts"`
}

func (st *status) addError(msg, url string) {
	st.Errors = append(st.Errors, camtypes.StatusError{
		Error: msg,
		URL:   url,
	})
}

func (st *status) isHandler(pfx string) bool {
	if pfx == st.importerRoot {
		return true
	}
	if _, ok := st.Sync[pfx]; ok {
		return true
	}
	if _, ok := st.Storage[pfx]; ok {
		return true
	}
	return false
}

type storageStatus struct {
	Primary     bool   `json:"primary,omitempty"`
	IsIndex     bool   `json:"isIndex,omitempty"`
	Type        string `json:"type"`
	ApproxBlobs int    `json:"approxBlobs,omitempty"`
	ApproxBytes int    `json:"approxBytes,omitempty"`
	ImplStatus  any    `json:"implStatus,omitempty"`
	// ByHash are the census's estimates of the storage's blobs of each
	// hash type, which ApproxBlobs and ApproxBytes sum, by hash type (and
	// without their Hash), if it has run.
	ByHash map[string]blobserver.HashEstimate `json:"byHash,omitempty"`
}

func (sh *StatusHandler) currentStatus() *status {
	res := &status{
		Version: buildinfo.Summary(),
		GoInfo:  fmt.Sprintf("%s %s/%s cgo=%v", runtime.Version(), runtime.GOOS, runtime.GOARCH, cgoEnabled),
		Storage: make(map[string]storageStatus),
		Sync:    make(map[string]syncStatus),
	}
	if v := os.Getenv("CAMLI_FAKE_STATUS_ERROR"); v != "" {
		res.addError(v, "/status/#fakeerror")
	}
	res.Health = sh.health.Status()
	if res.Health.ReadOnly {
		res.addError("The server is read-only, because of a failed check.", sh.prefix+"#health")
	}
	for _, c := range res.Health.Checks {
		if c.State >= health.StateWarning {
			res.addError(fmt.Sprintf("%s: %s: %s", c.Title, c.State, c.Message), sh.prefix+"#health")
		}
	}
	_, hi, err := sh.handlerFinder.FindHandlerByType("root")
	if err != nil {
		res.addError(fmt.Sprintf("Error finding root handler: %v", err), "")
		return res
	}
	rh := hi.(*RootHandler)
	res.rootPrefix = rh.Prefix

	if pfx, h, err := sh.handlerFinder.FindHandlerByType("importer"); err == nil {
		res.importerRoot = pfx
		as := h.(interface {
			AccountsStatus() (any, []camtypes.StatusError)
		})
		var errs []camtypes.StatusError
		res.ImporterAccounts, errs = as.AccountsStatus()
		res.Errors = append(res.Errors, errs...)
	}

	types, handlers := sh.handlerFinder.AllHandlers()

	// Sync
	for pfx, h := range handlers {
		sh, ok := h.(*SyncHandler)
		if !ok {
			continue
		}
		res.Sync[pfx] = sh.currentStatus()
	}

	// Storage
	for pfx, typ := range types {
		if !strings.HasPrefix(typ, "storage-") {
			continue
		}
		h := handlers[pfx]
		_, isIndex := h.(*index.Index)
		ss := storageStatus{
			Type:    strings.TrimPrefix(typ, "storage-"),
			Primary: pfx == rh.BlobRoot,
			IsIndex: isIndex,
		}
		for _, c := range res.Health.Checks {
			if ests, ok := c.Details.([]blobserver.HashEstimate); ok && c.ID == blobserver.CensusID(pfx) {
				ss.ByHash = make(map[string]blobserver.HashEstimate)
				for _, e := range ests {
					ss.ByHash[e.Hash] = blobserver.HashEstimate{
						Blobs:    e.Blobs,
						Bytes:    e.Bytes,
						Exact:    e.Exact,
						BlobsErr: e.BlobsErr,
						BytesErr: e.BytesErr,
					}
				}
				for _, e := range ests {
					ss.ApproxBlobs += int(e.Blobs)
					ss.ApproxBytes += int(e.Bytes)
				}
			}
		}
		res.Storage[pfx] = ss
	}

	return res
}

func (sh *StatusHandler) serveStatusJSON(rw http.ResponseWriter, req *http.Request) {
	httputil.ReturnJSON(rw, sh.currentStatus())
}

func (sh *StatusHandler) googleCloudConsole() (string, error) {
	if !env.OnGCE() {
		return "", errors.New("not on GCE")
	}
	projID, err := metadata.ProjectID()
	if err != nil {
		return "", fmt.Errorf("Error getting project ID: %w", err)
	}
	return "https://console.cloud.google.com/compute/instances?project=" + projID, nil
}

var quotedPrefix = regexp.MustCompile(`[;"]/(\S+?/)[&"]`)

func (sh *StatusHandler) serveStatusHTML(rw http.ResponseWriter, req *http.Request) {
	st := sh.currentStatus()
	f := func(p string, a ...any) {
		if len(a) == 0 {
			io.WriteString(rw, p)
		} else {
			fmt.Fprintf(rw, p, a...)
		}
	}
	f("<html><head><title>perkeepd status</title></head>")
	f("<body>")

	f("<h1>perkeepd status</h1>")

	f("<h2>Versions</h2><ul>")
	var envStr string
	if env.OnGCE() {
		envStr = " (on GCE)"
	}
	f("<li><b>Perkeep</b>: %s%s</li>", html.EscapeString(buildinfo.Summary()), envStr)
	f("<li><b>Go</b>: %s/%s %s, cgo=%v</li>", runtime.GOOS, runtime.GOARCH, runtime.Version(), cgoEnabled)
	f("<li><b>djpeg</b>: %s", html.EscapeString(buildinfo.DjpegStatus()))
	f("<li><b>HEIC decoder</b>: %s", html.EscapeString(buildinfo.HEICStatus()))
	f("</ul>")

	sh.writeHealthHTML(rw, st.Health)
	writeStorageHTML(rw, st.Storage)

	f("<h2>Logs</h2><ul>")
	f("  <li><a href='/debug/config'>/debug/config</a> - server config</li>\n")
	if env.OnGCE() {
		f("  <li><a href='/debug/logs/perkeepd'>perkeepd logs on Google Cloud Logging</a></li>\n")
		f("  <li><a href='/debug/logs/system'>system logs from Google Compute Engine</a></li>\n")
	}
	f("</ul>")

	f("<h2>Admin</h2>")
	f("<ul>")
	f("  <li><form method='post' action='restart' onsubmit='return confirm(\"Really restart now?\")'><button>restart server</button> ")
	f("<input type='checkbox' name='reindex' id='reindex'><label for='reindex'> reindex </label>")
	f("<select name='recovery'><option selected='true' disabled='disabled'>select recovery mode</option>")
	f("<option value='0'>no recovery</option>")
	f("<option value='1'>fast recovery</option>")
	f("<option value='2'>full recovery</option>")
	f("</select>")
	f("</form></li>")
	if env.OnGCE() {
		console, err := sh.googleCloudConsole()
		if err != nil {
			log.Printf("error getting Google Cloud Console URL: %v", err)
		} else {
			f("   <li><b>Updating:</b> When a new image for Perkeep on GCE is available, you can update by hitting \"Reset\" (or \"Stop\", then \"Start\") for your instance on your <a href='%s'>Google Cloud Console</a>.<br>Alternatively, you can ssh to your instance and restart the Perkeep service with: <b>sudo systemctl restart perkeepd</b>.</li>", console)
		}
	}
	f("</ul>")

	f("<h2>Handlers</h2>")
	f("<p>As JSON: <a href='status.json'>status.json</a>; and the <a href='%s?camli.mode=config'>discovery JSON</a>.</p>", st.rootPrefix)
	f("<p>Not yet pretty HTML UI:</p>")
	js, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		log.Printf("JSON marshal error: %v", err)
	}
	jsh := html.EscapeString(string(js))
	jsh = quotedPrefix.ReplaceAllStringFunc(jsh, func(in string) string {
		pfx := in[1 : len(in)-1]
		if st.isHandler(pfx) {
			return fmt.Sprintf("%s<a href='%s'>%s</a>%s", in[:1], pfx, pfx, in[len(in)-1:])
		}
		return in
	})
	f("<pre>%s</pre>", jsh)
}

// writeStorageHTML writes a table of the server's storage, with the
// census's estimates of their blobs.
func writeStorageHTML(w io.Writer, storage map[string]storageStatus) {
	f := func(p string, a ...any) { fmt.Fprintf(w, p, a...) }
	f("<h2 id='storage'>Storage</h2>")
	f("<p>Blob counts and sizes are estimated by the storage census check above, from a sample of each storage's blobs (or exact, without &asymp;), with their standard errors: they're within twice that of the truth 95%% of the time.</p>")
	f("<table border='1' cellpadding='4' style='border-collapse: collapse'>")
	f("<tr><th rowspan='2'>Prefix</th><th rowspan='2'>Type</th><th colspan='2'>Total</th><th colspan='3'>By hash type</th></tr>")
	f("<tr><th>Blobs</th><th>Size</th><th>Hash</th><th>Blobs</th><th>Size</th></tr>")
	for _, pfx := range slices.Sorted(maps.Keys(storage)) {
		ss := storage[pfx]
		typ := html.EscapeString(ss.Type)
		if ss.Primary {
			typ += " (primary)"
		}
		total := blobserver.HashEstimate{Exact: true}
		for _, e := range ss.ByHash {
			total.Blobs += e.Blobs
			total.Bytes += e.Bytes
			total.Exact = total.Exact && e.Exact
			total.BlobsErr = blobserver.AddErrs(total.BlobsErr, e.BlobsErr)
			total.BytesErr = blobserver.AddErrs(total.BytesErr, e.BytesErr)
		}
		var blobs, size string
		if ss.ByHash != nil {
			blobs = html.EscapeString(total.BlobsWithErr())
			size = html.EscapeString(total.BytesWithErr())
		}
		// The storage's columns span its rows, one for each hash type.
		rows := max(1, len(ss.ByHash))
		f("<tr><td rowspan='%d'>%s</td><td rowspan='%d'>%s</td><td rowspan='%d' align='right'>%s</td><td rowspan='%d' align='right'>%s</td>",
			rows, html.EscapeString(pfx), rows, typ, rows, blobs, rows, size)
		hashes := slices.Sorted(maps.Keys(ss.ByHash))
		if len(hashes) == 0 {
			f("<td></td><td></td><td></td></tr>")
			continue
		}
		for i, hash := range hashes {
			e := ss.ByHash[hash]
			if i > 0 {
				f("<tr>")
			}
			f("<td>%s</td><td align='right'>%s</td><td align='right'>%s</td></tr>",
				html.EscapeString(hash), html.EscapeString(e.BlobsWithErr()), html.EscapeString(e.BytesWithErr()))
		}
	}
	f("</table>")
}

// stateEmoji are the icons of the check states that need noticing on
// the status page; the others (such as "ok", on green) don't have one.
var stateEmoji = map[health.State]string{
	health.StateRunning: "⏳",
	health.StateWarning: "⚠️",
	health.StateFailed:  "❌",
}

// stateColors are the background colors of check states on the status
// page.
var stateColors = map[health.State]string{
	health.StateOK:      "#cfc",
	health.StateWarning: "#ffc",
	health.StateFailed:  "#fcc",
}

func (sh *StatusHandler) writeHealthHTML(w io.Writer, hs health.Status) {
	f := func(p string, a ...any) { fmt.Fprintf(w, p, a...) }
	f("<h2 id='health'>Health</h2>")
	if hs.ReadOnly {
		f("<p style='background: %s'><b>The server is read-only</b>: it refuses to add or remove blobs until the failed checks below marked fatal are fixed.</p>", stateColors[health.StateFailed])
	}
	if len(hs.Checks) == 0 {
		f("<p>No checks.</p>")
		return
	}
	token := xsrftoken.Generate(auth.Token(), "user", healthActionXSRF)
	f("<table border='1' cellpadding='4' style='border-collapse: collapse'>")
	f("<tr><th>State</th><th>Check</th><th>Message</th><th>Updated</th><th>Schedule</th></tr>")
	now := time.Now()
	for _, c := range hs.Checks {
		var updated string
		if !c.Updated.IsZero() {
			updated = c.Updated.UTC().Format(time.RFC3339)
		}
		f("<tr><td style='background: %s'>%s</td><td>%s<br><small>%s</small></td><td>%s</td><td>%s</td><td>",
			stateColors[c.State], stateHTML(c, now),
			html.EscapeString(c.Title), html.EscapeString(c.ID),
			html.EscapeString(c.Message), updated)
		if c.Runnable {
			writeHealthControls(w, c, token)
		}
		f("</td></tr>")
	}
	f("</table>")
}

// stateHTML returns the HTML for the state of check c at time now, with,
// if it's running, how far along it is, as a percentage if its total is
// known (hovering over it shows what it counts), and an estimate of how
// much longer it'll take, from its average rate so far; or, if its full
// pass is paused, how far along that is.
func stateHTML(c health.CheckStatus, now time.Time) string {
	s := c.State.String()
	if e := stateEmoji[c.State]; e != "" {
		s = e + " " + s
	}
	if c.Fatal {
		s += " (fatal)"
	}
	s = html.EscapeString(s)
	title := c.ProgressHint
	if c.Total > 0 {
		title = strings.TrimSpace(fmt.Sprintf("%s (%d / %d)", c.ProgressHint, c.Done, c.Total))
	}
	pct := ""
	if c.Total > 0 {
		pct = fmt.Sprintf("<span title='%s'>%0.1f%%</span>", html.EscapeString(title), 100*float64(c.Done)/float64(c.Total))
	}
	switch {
	case c.State == health.StateRunning && c.Total > 0:
		s += ", " + pct
		if c.Done > 0 && c.Done < c.Total && !c.Started.IsZero() {
			elapsed := now.Sub(c.Started)
			remain := time.Duration(float64(elapsed) * float64(c.Total-c.Done) / float64(c.Done))
			s += "<br><span style='color: #666'>" + formatETA(remain) + "</span>"
		}
	case c.State == health.StateRunning && c.Done > 0:
		s += fmt.Sprintf(", <span title='%s'>%d</span>", html.EscapeString(c.ProgressHint), c.Done)
	case c.Sharded && c.Paused && c.Total > 0:
		s += "<br><small>pass paused at " + pct + "</small>"
	}
	return s
}

// formatETA formats d, the time a check has left, like "-1h:53m", or
// like "-12m:05s" if it's under an hour.
func formatETA(d time.Duration) string {
	d = d.Round(time.Second)
	if d >= time.Hour {
		d = d.Round(time.Minute)
		return fmt.Sprintf("-%dh:%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
	return fmt.Sprintf("-%dm:%02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
}

// healthActionXSRF is the action ID of the XSRF tokens of the health
// forms on the status page.
const healthActionXSRF = "healthAction"

// writeHealthControls writes the forms that run, cancel, and schedule
// the check c, with the XSRF token.
func writeHealthControls(w io.Writer, c health.CheckStatus, token string) {
	f := func(p string, a ...any) { fmt.Fprintf(w, p, a...) }
	id := html.EscapeString(c.ID)
	hidden := fmt.Sprintf("<input type='hidden' name='id' value='%s'><input type='hidden' name='token' value='%s'>", id, token)
	f("<form method='post' action='health' style='display: inline'>%s<select name='schedule'>", hidden)
	for _, sc := range health.Schedules {
		sel := ""
		if sc == c.Schedule {
			sel = " selected"
		}
		def := ""
		if sc == c.DefaultSchedule {
			def = " (default)"
		}
		f("<option value='%s'%s>%s%s</option>", sc, sel, sc, def)
	}
	f("</select> <button name='action' value='schedule'>Set</button></form> ")
	button := func(action, label, title string) {
		f("<form method='post' action='health' style='display: inline'>%s<button name='action' value='%s' title='%s'>%s</button></form> ", hidden, action, title, label)
	}
	switch {
	case c.State == health.StateRunning:
		button("cancel", "Cancel", "Stop it; a full pass of its shards pauses, until Resume")
	case c.Sharded:
		button("run", "Start", "Start a full pass, checking every shard again")
		if !c.PassSince.IsZero() {
			button("resume", "Resume", "Continue the full pass that started at "+c.PassSince.UTC().Format(time.RFC3339)+", checking the shards it hasn't")
		}
	default:
		button("run", "Run now", "Run it now")
	}
	if !c.LastRun.IsZero() {
		f("<br><small>Last run %s</small>", c.LastRun.UTC().Format(time.RFC3339))
	}
	if !c.NextRun.IsZero() {
		f("<br><small>Next run %s</small>", c.NextRun.UTC().Format(time.RFC3339))
	}
}

// serveHealthAction handles the POSTs of the forms from
// writeHealthControls, to run, cancel, or schedule a check.
func (sh *StatusHandler) serveHealthAction(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		http.Error(rw, "POST to run, cancel, or schedule a check", http.StatusMethodNotAllowed)
		return
	}
	if !xsrftoken.Valid(req.FormValue("token"), auth.Token(), "user", healthActionXSRF) {
		http.Error(rw, "Invalid or expired form token; reload the status page.", http.StatusForbidden)
		return
	}
	id := req.FormValue("id")
	var err error
	switch req.FormValue("action") {
	case "run":
		err = sh.health.RunNow(id)
	case "resume":
		err = sh.health.Resume(id)
	case "cancel":
		err = sh.health.Cancel(id)
	case "schedule":
		err = sh.health.SetSchedule(id, health.Schedule(req.FormValue("schedule")))
	default:
		err = errors.New("unknown action")
	}
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(rw, req, sh.prefix+"#health", http.StatusFound)
}

func (sh *StatusHandler) serveRestart(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		http.Error(rw, "POST to restart", http.StatusMethodNotAllowed)
		return
	}

	_, handlers := sh.handlerFinder.AllHandlers()
	for _, h := range handlers {
		ah, ok := h.(*app.Handler)
		if !ok {
			continue
		}
		log.Printf("Sending SIGINT to %s", ah.ProgramName())
		err := ah.Quit()
		if err != nil {
			msg := fmt.Sprintf("Not restarting: couldn't interrupt app %s: %v", ah.ProgramName(), err)
			log.Print(msg)
			http.Error(rw, msg, http.StatusInternalServerError)
			return
		}
	}

	reindex := (req.FormValue("reindex") == "on")
	recovery, _ := strconv.Atoi(req.FormValue("recovery"))

	log.Println("Restarting perkeepd")
	rw.Header().Set("Connection", "close")
	http.Redirect(rw, req, sh.prefix, http.StatusFound)
	if f, ok := rw.(http.Flusher); ok {
		f.Flush()
	}
	osutil.RestartProcess(fmt.Sprintf("-reindex=%t", reindex), fmt.Sprintf("-recovery=%d", recovery))
}

var cgoEnabled bool
