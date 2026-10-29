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

package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sync"
	"time"

	"perkeep.org/pkg/client"
	"perkeep.org/pkg/upload"
)

// progressServer serves the progress of pk-put file's uploads of
// directory trees over HTTP (with -progress-listen): an HTML page at /,
// and its data as JSON at /json.
type progressServer struct {
	start time.Time
	c     *client.Client // for its HTTP statistics, or nil

	mu    sync.Mutex
	trees []*progressTree
}

// progressTree is an upload of a directory tree that a progressServer
// shows.
type progressTree struct {
	Dir      string
	Started  time.Time
	Finished time.Time // or zero, while running
	Err      string
	t        *upload.Tree
}

func newProgressServer(c *client.Client) *progressServer {
	return &progressServer{start: time.Now(), c: c}
}

// add starts showing t, an upload of dir, and returns a func to call when
// it's done.
func (ps *progressServer) add(dir string, t *upload.Tree) (done func(error)) {
	pt := &progressTree{Dir: dir, Started: time.Now(), t: t}
	ps.mu.Lock()
	ps.trees = append(ps.trees, pt)
	ps.mu.Unlock()
	return func(err error) {
		ps.mu.Lock()
		defer ps.mu.Unlock()
		pt.Finished = time.Now()
		if err != nil {
			pt.Err = err.Error()
		}
	}
}

// progressStatus is what a progressServer serves.
type progressStatus struct {
	Now     time.Time
	Started time.Time
	Trees   []progressTreeStatus
	Client  *client.Stats `json:",omitempty"`
}

type progressTreeStatus struct {
	Dir      string
	Started  time.Time
	Finished time.Time `json:",omitzero"`
	Err      string    `json:",omitempty"`
	Status   upload.TreeStatus

	// Derived, for showing.
	Elapsed       time.Duration
	BytesInFlight int64   // hashed or cut of the files in flight
	BytesPerSec   float64 // of files done or in flight, read
	UploadPerSec  float64 // of chunks uploaded, of files done
	ETA           time.Duration
}

func (ps *progressServer) status() progressStatus {
	now := time.Now()
	ps.mu.Lock()
	trees := make([]progressTree, len(ps.trees))
	for i, pt := range ps.trees {
		trees[i] = *pt
	}
	ps.mu.Unlock()
	st := progressStatus{Now: now, Started: ps.start}
	if ps.c != nil {
		cs := ps.c.Stats()
		st.Client = &cs
	}
	for _, pt := range trees {
		ts := progressTreeStatus{
			Dir:      pt.Dir,
			Started:  pt.Started,
			Finished: pt.Finished,
			Err:      pt.Err,
			Status:   pt.t.Status(),
		}
		end := now
		if !pt.Finished.IsZero() {
			end = pt.Finished
		}
		ts.Elapsed = end.Sub(pt.Started)
		for _, f := range ts.Status.InFlight {
			ts.BytesInFlight += max(f.Progress.BytesHashed, f.Progress.BytesCut)
		}
		p := ts.Status.Progress
		if secs := ts.Elapsed.Seconds(); secs > 0 {
			ts.BytesPerSec = float64(p.BytesDone+ts.BytesInFlight) / secs
			ts.UploadPerSec = float64(p.BytesUploaded) / secs
		}
		if p.Walked && pt.Finished.IsZero() && ts.BytesPerSec > 0 {
			left := p.Bytes - p.BytesDone - ts.BytesInFlight
			ts.ETA = time.Duration(float64(max(left, 0)) / ts.BytesPerSec * float64(time.Second))
		}
		st.Trees = append(st.Trees, ts)
	}
	return st
}

func (ps *progressServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/json":
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(ps.status())
	case "/":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := progressTmpl.Execute(w, ps.status()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	default:
		http.NotFound(w, r)
	}
}

// humanBytes formats n bytes, such as "1.5 GiB".
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 5 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func percent(n, total int64) string {
	if total <= 0 {
		return "–"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(total))
}

func roundDur(d time.Duration) time.Duration {
	if d > time.Minute {
		return d.Round(time.Second)
	}
	return d.Round(100 * time.Millisecond)
}

var progressTmpl = template.Must(template.New("progress").Funcs(template.FuncMap{
	"bytes": humanBytes,
	"bytesf": func(f float64) string {
		return humanBytes(int64(f))
	},
	"pct":  percent,
	"pcti": func(n, total int) string { return percent(int64(n), int64(total)) },
	"dur":  roundDur,
	"since": func(now, t time.Time) time.Duration {
		return roundDur(now.Sub(t))
	},
	"sub": func(a, b time.Time) time.Duration { return roundDur(a.Sub(b)) },
}).Parse(`<!DOCTYPE html>
<html><head>
<meta http-equiv="refresh" content="2">
<title>pk-put progress</title>
<style>
body { font-family: sans-serif; margin: 1em 2em; }
table { border-collapse: collapse; margin-bottom: 1.5em; }
th, td { padding: 2px 10px; text-align: left; border-bottom: 1px solid #ddd; }
td.n { text-align: right; font-variant-numeric: tabular-nums; }
.err { color: #b00; }
.dup { color: #777; }
h2 { margin-top: 1.2em; }
</style>
</head><body>
<h1>pk-put progress</h1>
<p>Running {{since .Now .Started}}. Updated {{.Now.Format "15:04:05"}}; this page reloads every 2 seconds. <a href="/json">JSON</a></p>
{{with .Client}}<p>HTTP: {{.UploadRequests.Blobs}} blobs asked to upload ({{bytes .UploadRequests.Bytes}}); {{.Uploads.Blobs}} sent ({{bytes .Uploads.Bytes}}).</p>{{end}}
{{range .Trees}}{{$now := $.Now}}
<h2>{{.Dir}}</h2>
{{with .Status.Progress}}
<table>
<tr><th>Files</th><td class=n>{{.FilesDone}} / {{.Files}}{{if not .Walked}}+ (still walking){{end}}</td><td class=n>{{pcti .FilesDone .Files}}</td><td>{{.FilesCached}} from the stat cache, {{.FilesDup}} the server had</td></tr>
<tr><th>Bytes</th><td class=n>{{bytes .BytesDone}} / {{bytes .Bytes}}</td><td class=n>{{pct .BytesDone .Bytes}}</td><td>of files done</td></tr>
<tr><th>Directories</th><td class=n>{{.DirsDone}} / {{.Dirs}}</td><td class=n>{{pcti .DirsDone .Dirs}}</td><td></td></tr>
<tr><th>Uploaded</th><td class=n>{{bytes .BytesUploaded}}</td><td></td><td>{{.ChunksUploaded}} chunks sent, {{.ChunksPresent}} the server had</td></tr>
</table>
{{end}}
<p>
{{if .Finished.IsZero}}Running for {{dur .Elapsed}}{{else}}Finished in {{dur .Elapsed}}{{end}}.
Reading {{bytesf .BytesPerSec}}/s (including {{bytes .BytesInFlight}} of files in flight); uploading {{bytesf .UploadPerSec}}/s.
{{if .ETA}}About {{dur .ETA}} left.{{end}}
{{if .Err}}<span class=err>Error: {{.Err}}</span>{{end}}
</p>
<h3>In flight ({{len .Status.InFlight}})</h3>
<table>
<tr><th>File</th><th>Size</th><th>Phase</th><th>Hashed</th><th>Cut</th><th>Uploaded</th><th>Chunks (sent/had/cut)</th><th>For</th></tr>
{{range .Status.InFlight}}<tr><td>{{.Path}}</td><td class=n>{{bytes .Size}}</td><td>{{.Progress.Phase}}</td>
<td class=n>{{pct .Progress.BytesHashed .Size}}</td><td class=n>{{pct .Progress.BytesCut .Size}}</td><td class=n>{{bytes .Progress.BytesUploaded}}</td>
<td class=n>{{.Progress.ChunksUploaded}}/{{.Progress.ChunksPresent}}/{{.Progress.Chunks}}</td><td class=n>{{since $now .Started}}</td></tr>
{{end}}</table>
<h3>Recently done</h3>
<table>
<tr><th>File</th><th>Size</th><th></th><th>Uploaded</th><th>Took</th><th>Done</th></tr>
{{range .Status.Recent}}<tr class="{{if .Dup}}dup{{end}}"><td>{{.Path}}</td><td class=n>{{bytes .Size}}</td>
<td>{{if .Err}}<span class=err>{{.Err}}</span>{{else if .Dup}}server had it{{else}}new{{end}}</td>
<td class=n>{{bytes .BytesUploaded}}</td><td class=n>{{sub .Done .Started}}</td><td class=n>{{since $now .Done}} ago</td></tr>
{{end}}</table>
{{else}}
<p>No directory upload yet. (Progress is shown for directories, uploaded with package upload's Tree.)</p>
{{end}}
</body></html>
`))
