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

package access

import (
	"cmp"
	"errors"
	"html/template"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/xsrftoken"
	"perkeep.org/pkg/auth"
	"rsc.io/qr"
)

const xsrfAction = "access"

// ServeHTTP serves the Client access page, its forms for a new access key
// and for the server's name, the POSTs of their forms, and the QR codes of
// access keys.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var d pageData
	switch r.URL.Path {
	case h.prefix:
	case h.prefix + "new":
		d.Form = "new"
	case h.prefix + "name":
		d.Form = "name"
	case h.prefix + "qr":
		h.serveQR(w, r)
		return
	default:
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case "GET", "HEAD":
		h.servePage(w, r, d)
	case "POST":
		h.serveAction(w, r)
	default:
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) serveAction(w http.ResponseWriter, r *http.Request) {
	if !xsrftoken.Valid(r.FormValue("token"), auth.Token(), "user", xsrfAction) {
		http.Error(w, "Invalid or expired form token; reload the Client access page.", http.StatusForbidden)
		return
	}
	var err error
	action := r.FormValue("action")
	switch action {
	case "enable":
		err = h.enable()
	case "disable":
		err = h.disable()
	case "forget":
		err = h.forgetIdentity()
	case "setname":
		err = h.setName(r.FormValue("name"))
	case "create":
		var perm Perm
		if perm, err = ParsePerms(r.Form["perm"]); err == nil {
			var id string
			if id, err = h.createKey(r.FormValue("name"), perm); err == nil {
				http.Redirect(w, r, h.prefix+"?show="+id+"#key-"+id, http.StatusFound)
				return
			}
		}
	case "setperms":
		var perm Perm
		if perm, err = ParsePerms(r.Form["perm"]); err == nil {
			err = h.setKeyPerms(r.FormValue("id"), perm)
		}
	case "delete":
		err = h.deleteKey(r.FormValue("id"))
	default:
		err = errors.New("unknown action")
	}
	if err != nil {
		log.Printf("access: %s: %v", action, err)
		d := pageData{Err: err}
		switch action {
		case "create":
			d.Form = "new"
		case "setname":
			d.Form = "name"
		}
		w.WriteHeader(http.StatusBadRequest)
		h.servePage(w, r, d)
		return
	}
	http.Redirect(w, r, h.prefix, http.StatusFound)
}

// serveQR serves a PNG QR code of the clientURL of the access key whose ID
// is the "id" parameter.
func (h *Handler) serveQR(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	name := h.name()
	t, err := h.clientTarget()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.mu.Lock()
	i := slices.IndexFunc(h.st.Keys, func(k *accessKey) bool { return k.ID == id })
	var k *accessKey
	if i >= 0 {
		k = h.st.Keys[i]
	}
	h.mu.Unlock()
	if k == nil {
		http.NotFound(w, r)
		return
	}
	if t == (target{}) {
		http.Error(w, "Clients can't reach the server: it needs the tailcat listener, or an HTTPS base URL.", http.StatusConflict)
		return
	}
	code, err := qr.Encode(clientURL(t, k, name), qr.L)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(code.PNG())
}

type pageData struct {
	Err    error
	Form   string // "new" or "name", to show that form instead of the page
	Prefix string
	Token  string

	Name        string // for clients to show for the server
	DefaultName string
	NameSet     bool // whether the owner set Name

	HaveIdentity bool
	Enabled      bool
	Listening    bool
	Dot, Status  string // the listener's status
	Addr         string // the tailcat address, while listening
	Region       string // the identity's DERP region or server

	// Via says how the QR codes and links send clients to the server,
	// if they can reach it; else ViaErr may say why not.
	Via    string
	ViaErr error

	Perms []permData // for the new key form
	Keys  []keyData

	ShowQR string // the ID of the key whose QR code to show to start, if any
}

type permData struct {
	Name, Title, Desc string
	Checked           bool
}

type keyData struct {
	ID, Name, Secret string
	Created          time.Time
	Perms            []permData
	PermTitles       string       // of the permissions it has, or "none"
	AppURL           template.URL // the clientURL, if clients can reach the server
	ShowKey          bool         // whether to show its secret to start
}

func (h *Handler) servePage(w http.ResponseWriter, r *http.Request, d pageData) {
	d.Prefix = h.prefix
	d.Token = xsrftoken.Generate(auth.Token(), "user", xsrfAction)
	permsOf := func(p Perm) []permData {
		var ps []permData
		for _, pi := range permInfo {
			ps = append(ps, permData{pi.name, pi.title, pi.desc, p.Has(pi.perm)})
		}
		return ps
	}
	d.Perms = permsOf(PermUpload | PermSign)
	show := r.URL.Query().Get("show")
	d.DefaultName = h.defaultName()
	t, err := h.clientTarget()
	switch {
	case err != nil:
		d.ViaErr = err
	case t.tailcat != "":
		d.Via = "through the tailcat listener"
	case t.certFP != "":
		d.Via = "at " + t.url + ", checking its certificate by its fingerprint, as clients can't verify it otherwise"
	case t.url != "":
		d.Via = "at " + t.url
	}

	h.mu.Lock()
	d.Name, d.NameSet = h.st.Name, h.st.Name != ""
	if !d.NameSet {
		d.Name = d.DefaultName
	}
	d.HaveIdentity = h.st.Identity != nil
	d.Region = h.regionLocked()
	d.Enabled = h.st.Enabled
	d.Listening = h.ts != nil
	d.Addr = string(h.addr)
	d.Dot, _, d.Status = h.statusLocked()
	for _, k := range h.st.Keys {
		kd := keyData{
			ID:      k.ID,
			Name:    k.Name,
			Secret:  k.Secret,
			Created: k.Created,
			Perms:   permsOf(k.perm),
		}
		var titles []string
		for _, p := range kd.Perms {
			if p.Checked {
				titles = append(titles, p.Title)
			}
		}
		kd.PermTitles = cmp.Or(strings.Join(titles, ", "), "none")
		if d.Via != "" {
			// clientURL's are perkeep: URLs, which html/template
			// would otherwise refuse to link to.
			kd.AppURL = template.URL(clientURL(t, k, d.Name))
			if k.ID == show {
				d.ShowQR = k.ID
			}
		} else {
			kd.ShowKey = k.ID == show
		}
		d.Keys = append(d.Keys, kd)
	}
	h.mu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTmpl.Execute(w, d); err != nil {
		log.Printf("access: rendering page: %v", err)
	}
}

var pageTmpl = template.Must(template.New("access").Parse(`<!doctype html>
<html><head><title>Client access</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
body { font-family: system-ui, sans-serif; line-height: 1.4; max-width: 46em; margin: 0 auto; padding: 0 1em 2em; }
h1 { font-size: 1.6em; }
h2 { font-size: 1.25em; margin-top: 1.5em; }
.err { color: #b00; }
.addr { font-family: monospace; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; color: #555; }
button { font-size: 1em; padding: 0.4em 1em; margin: 0.2em 0.4em 0.2em 0; }
input[type=text] { font-size: 1em; padding: 0.4em; width: 100%; max-width: 30em; box-sizing: border-box; }
input.secret { font-family: monospace; }
label.perm { display: block; padding: 0.3em 0; }
form.inline { display: inline; }
.key { border: 1px solid #ddd; border-radius: 8px; padding: 0.6em 0.9em; margin: 0.8em 0; }
.key .created { color: #666; font-size: 0.9em; }
.key .action { display: block; padding: 0.3em 0; }
summary { color: #00c; text-decoration: underline; cursor: pointer; list-style: none; padding: 0.3em 0; }
summary::-webkit-details-marker { display: none; }
details[open] > summary { color: #555; }
dialog#qr { width: 90vw; height: 90vh; max-width: none; max-height: none; box-sizing: border-box; text-align: center; border: none; border-radius: 8px; }
dialog#qr::backdrop { background: rgba(0, 0, 0, 0.6); }
dialog#qr img { width: min(80vw, 65vh); height: min(80vw, 65vh); image-rendering: pixelated; }
</style>
</head><body>
{{if eq .Form "new"}}
<h1>New access key</h1>
{{if .Err}}<p class="err"><b>Error:</b> {{.Err}}</p>{{end}}
<form method="post" action="{{.Prefix}}">
<input type="hidden" name="token" value="{{.Token}}">
<p><label>Name<br><input type="text" name="name" placeholder="My phone" autofocus></label></p>
{{range .Perms}}<label class="perm"><input type="checkbox" name="perm" value="{{.Name}}"{{if .Checked}} checked{{end}}> <b>{{.Title}}</b>: {{.Desc}}</label>
{{end}}
<button name="action" value="create">Create</button> <a href="{{.Prefix}}">Cancel</a>
</form>
{{else if eq .Form "name"}}
<h1>Server name</h1>
{{if .Err}}<p class="err"><b>Error:</b> {{.Err}}</p>{{end}}
<p>The name that clients show for this server. Leave it empty for the
default, {{.DefaultName}}.</p>
<form method="post" action="{{.Prefix}}">
<input type="hidden" name="token" value="{{.Token}}">
<p><input type="text" name="name" value="{{if .NameSet}}{{.Name}}{{end}}" placeholder="{{.DefaultName}}" autofocus></p>
<button name="action" value="setname">Save</button> <a href="{{.Prefix}}">Cancel</a>
</form>
{{else}}
<h1>Client access</h1>

{{if .Err}}<p class="err"><b>Error:</b> {{.Err}}</p>{{end}}

<h2>Tailcat listener</h2>
<p>A <a href="https://github.com/tailscale/tailcat">tailcat</a> listener lets
clients like mobile apps reach this server through an encrypted WireGuard
tunnel, relayed through DERP until a direct connection works, without a VPN
or Tailscale account. It's in addition to however else the server listens.
Clients connect with its tailcat address and an access key (below).</p>

<p>{{.Dot}} <b>{{.Status}}</b></p>
{{if .Enabled}}
  {{if .Listening}}<div class="addr" title="The listener's tailcat address, a secret, which the access keys' QR codes include">{{.Addr}}</div>{{end}}
  <form method="post" action="{{.Prefix}}">
  <input type="hidden" name="token" value="{{.Token}}">
  <p><button name="action" value="disable">Disable</button></p>
  </form>
{{else if .HaveIdentity}}
  <p>Enabling it again keeps its tailcat address, with {{.Region}}.</p>
  <form method="post" action="{{.Prefix}}" class="inline">
  <input type="hidden" name="token" value="{{.Token}}">
  <button name="action" value="enable">Enable</button>
  </form>
  <form method="post" action="{{.Prefix}}" class="inline" onsubmit="return confirm('Forget the tailcat identity? Clients using its address will need the new one.')">
  <input type="hidden" name="token" value="{{.Token}}">
  <button name="action" value="forget">Forget identity</button>
  </form>
  (for a new address)
{{else}}
  <p>Enabling it makes a new identity for it, with a fixed tailcat address,
  using the nearest of tailcat's free, rate-limited DERP relays. (To use a
  <a href="https://github.com/tailscale/tailscale/tree/main/cmd/derper#derp">DERP server you run</a>
  instead, see the
  <a href="https://perkeep.org/doc/server-config#access">server configuration docs</a>.)</p>
  <form method="post" action="{{.Prefix}}">
  <input type="hidden" name="token" value="{{.Token}}">
  <button name="action" value="enable">Enable</button>
  </form>
{{end}}
<p>Clients show this server as <b>{{.Name}}</b> (<a href="{{.Prefix}}name">change</a>).</p>

<h2>Access keys</h2>
<p>Access keys let clients like mobile apps use this server, with only the
permissions you give them.
{{if .Via}}Their QR codes and links send clients to it {{.Via}}.
{{else if .ViaErr}}<span class="err">They have no QR codes or links: {{.ViaErr}}</span>
{{else}}For QR codes and links, enable the tailcat listener, or serve the server over HTTPS.
{{end}}</p>
{{if .Keys}}
{{range .Keys}}
<div class="key" id="key-{{.ID}}">
<b>{{.Name}}</b> <span class="created">created {{.Created.Format "2006-01-02"}}</span>
{{if .AppURL}}<a class="action" href="#" id="qr-link-{{.ID}}" data-qr="{{$.Prefix}}qr?id={{.ID}}" data-name="{{.Name}}" data-app="{{.AppURL}}" onclick="showQR(this); return false">Show QR code</a>
<a class="action" href="{{.AppURL}}">Open in the Perkeep app</a>
{{end}}<details{{if .ShowKey}} open{{end}}><summary>Show key</summary>
<input type="text" class="secret" readonly value="{{.Secret}}" onclick="this.select()">
</details>
<details><summary>Permissions: {{.PermTitles}}</summary>
<form method="post" action="{{$.Prefix}}">
<input type="hidden" name="token" value="{{$.Token}}">
<input type="hidden" name="id" value="{{.ID}}">
{{range .Perms}}<label class="perm" title="{{.Desc}}"><input type="checkbox" name="perm" value="{{.Name}}"{{if .Checked}} checked{{end}}> {{.Title}}</label>
{{end}}<button name="action" value="setperms">Save</button>
</form>
</details>
<form method="post" action="{{$.Prefix}}" onsubmit="return confirm('Delete this access key? Clients using it will lose access.')">
<input type="hidden" name="token" value="{{$.Token}}">
<input type="hidden" name="id" value="{{.ID}}">
<button name="action" value="delete">Delete</button>
</form>
</div>
{{end}}
{{else}}
<p>None.</p>
{{end}}
<p><a href="{{.Prefix}}new">Add an access key</a></p>

<dialog id="qr" onclick="if (event.target === this) this.close()">
<h2 id="qr-title"></h2>
<img id="qr-img" alt="QR code">
<p>Scan it with the Perkeep app, or <a id="qr-app" href="#">open it in the Perkeep app</a>.</p>
<p><button onclick="document.getElementById('qr').close()">Close</button></p>
</dialog>
<script>
function showQR(link) {
	document.getElementById('qr-title').textContent = link.dataset.name;
	document.getElementById('qr-img').src = link.dataset.qr;
	document.getElementById('qr-app').href = link.dataset.app;
	document.getElementById('qr').showModal();
}
{{with .ShowQR}}showQR(document.getElementById({{printf "qr-link-%s" .}}));{{end}}
</script>
{{end}}
</body></html>
`))
