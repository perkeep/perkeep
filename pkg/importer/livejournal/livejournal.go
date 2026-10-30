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

/*
Package livejournal imports a LiveJournal journal: a permanode of each
entry, including private ones, and of each comment on them, and a set of
the entries, as pk livejournal keeps them (see internal/livejournal).

The password isn't kept in Perkeep: the account names a file, on the
server's disk, that has it, which the server reads for each run. Only a
hash of it, with a one-time challenge, is sent to LiveJournal.

Each run reads the whole journal and changes only the permanodes that
differ, so it isn't run automatically. Nothing is deleted: entries and
comments that LiveJournal no longer has keep their permanodes, and the
text that a comment had before it was deleted stays.
*/
package livejournal // import "perkeep.org/pkg/importer/livejournal"

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"perkeep.org/internal/httputil"
	lj "perkeep.org/internal/livejournal"
	search "perkeep.org/internal/searchtypes"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
)

func init() {
	importer.Register("livejournal", imp{})
}

// Attributes of the importer's account permanodes. The user is in
// importer.AcctAttrUserName.
const (
	acctAttrPasswordFile = "livejournalPasswordFile"
	acctAttrTimeZone     = "livejournalTimeZone"
	acctAttrServer       = "livejournalServer" // if not LiveJournal's own
)

type imp struct {
	importer.OAuth1 // for CallbackRequestAccount and CallbackURLParameters
}

func (imp) Properties() importer.Properties {
	return importer.Properties{
		Title:       "LiveJournal",
		Description: "import a LiveJournal journal's entries and the comments on them",
	}
}

func (imp) IsAccountReady(acct *importer.Object) (bool, error) {
	return acct.Attr(importer.AcctAttrUserName) != "" && acct.Attr(acctAttrPasswordFile) != "", nil
}

func (im imp) SummarizeAccount(acct *importer.Object) string {
	if ok, _ := im.IsAccountReady(acct); !ok {
		return "Not configured"
	}
	return fmt.Sprintf("LiveJournal journal %s (password from %s)", acct.Attr(importer.AcctAttrUserName), acct.Attr(acctAttrPasswordFile))
}

func (imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	return tmpl.ExecuteTemplate(w, "serveSetup", setupData{
		CallbackPath: ctx.CallbackPath(),
		Account:      ctx.AccountNode.PermanodeRef().String(),
		User:         ctx.AccountNode.Attr(importer.AcctAttrUserName),
		PasswordFile: ctx.AccountNode.Attr(acctAttrPasswordFile),
		TimeZone:     ctx.AccountNode.Attr(acctAttrTimeZone),
		Server:       ctx.AccountNode.Attr(acctAttrServer),
	})
}

type setupData struct {
	CallbackPath, Account                string
	User, PasswordFile, TimeZone, Server string
}

var tmpl = template.Must(template.New("root").Parse(`
{{define "serveSetup"}}
<h1>Configuring LiveJournal account</h1>
<p>The password isn't kept in Perkeep: put it in a file that the Perkeep
server can read, and give that file's path here. The server reads it for
each import, and sends LiveJournal only a hash of it.</p>
<form method="get" action="{{.CallbackPath}}">
  <input type="hidden" name="acct" value="{{.Account}}">
  <table border=0 cellpadding=3>
  <tr><td align=right>User</td><td><input name="user" size=30 value="{{.User}}"></td></tr>
  <tr><td align=right>Password file</td><td><input name="passwordFile" size=50 value="{{.PasswordFile}}"> (a path on the server)</td></tr>
  <tr><td align=right>Time zone</td><td><input name="timeZone" size=30 value="{{.TimeZone}}" placeholder="America/Los_Angeles"> (of the entries' times, which LiveJournal has without a zone)</td></tr>
  <tr><td align=right>Server</td><td><input name="server" size=50 value="{{.Server}}" placeholder="` + lj.DefaultBaseURL + `"> (optional)</td></tr>
  <tr><td align=right></td><td><input type="submit" value="Save"></td></tr>
  </table>
</form>
{{end}}
`))

// config is an account's configuration.
type config struct {
	user, passwordFile, server string
	loc                        *time.Location
}

func accountConfig(acct *importer.Object) (*config, error) {
	c := &config{
		user:         acct.Attr(importer.AcctAttrUserName),
		passwordFile: acct.Attr(acctAttrPasswordFile),
		server:       acct.Attr(acctAttrServer),
	}
	return c, c.check(acct.Attr(acctAttrTimeZone))
}

// check checks c and sets c.loc from tz.
func (c *config) check(tz string) error {
	if c.user == "" {
		return errors.New("no LiveJournal user")
	}
	if c.passwordFile == "" {
		return errors.New("no password file")
	}
	if c.server == "" {
		c.server = lj.DefaultBaseURL
	}
	if tz == "" {
		return errors.New("no time zone")
	}
	var err error
	if c.loc, err = time.LoadLocation(tz); err != nil {
		return fmt.Errorf("bad time zone: %w", err)
	}
	return nil
}

// client returns a client logging in with the password from c's file.
func (c *config) client(hc *http.Client) (*lj.Client, error) {
	pw, err := os.ReadFile(c.passwordFile)
	if err != nil {
		return nil, fmt.Errorf("reading the password file: %w", err)
	}
	password := strings.TrimRight(string(pw), "\r\n")
	if password == "" {
		return nil, fmt.Errorf("password file %s is empty", c.passwordFile)
	}
	cl := lj.NewClient(c.server, c.user, password)
	cl.HTTPClient = hc
	return cl, nil
}

func (im imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	c := &config{
		user:         strings.TrimSpace(r.FormValue("user")),
		passwordFile: strings.TrimSpace(r.FormValue("passwordFile")),
		server:       strings.TrimSpace(r.FormValue("server")),
	}
	tz := strings.TrimSpace(r.FormValue("timeZone"))
	if err := c.check(tz); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cl, err := c.client(ctx.Host.HTTPClient())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := cl.Login(ctx); err != nil {
		http.Error(w, "Logging in to LiveJournal: "+err.Error(), http.StatusBadRequest)
		return
	}
	server := c.server
	if server == lj.DefaultBaseURL {
		server = ""
	}
	keyval := []string{
		importer.AcctAttrUserName, c.user,
		acctAttrPasswordFile, c.passwordFile,
		acctAttrTimeZone, tz,
	}
	var deletes []string
	if server == "" {
		deletes = append(deletes, acctAttrServer)
	} else {
		keyval = append(keyval, acctAttrServer, server)
	}
	if err := ctx.AccountNode.SetAttrsWithDeletes(deletes, keyval...); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attributes: %w", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

func (im imp) Run(rc *importer.RunContext) error {
	ctx := rc.Context()
	c, err := accountConfig(rc.AccountNode())
	if err != nil {
		return err
	}
	cl, err := c.client(rc.Host.HTTPClient())
	if err != nil {
		return err
	}
	j, err := cl.Fetch(ctx, log.Printf)
	if err != nil {
		return err
	}
	res, err := lj.Sync(ctx, hostStore{rc.Host}, j, lj.SyncOptions{
		Location: c.loc,
		Workers:  8,
		Logf:     log.Printf,
	})
	if err != nil {
		return err
	}
	log.Printf("livejournal: %d entries, %d comments: changed %d of %d permanodes (%d new), with %d attribute changes",
		len(j.Entries), len(j.Comments), res.Changed, res.Permanodes, res.Created, res.AttrChanges)
	return rc.RootNode().SetAttrs(
		nodeattr.Title, "LiveJournal data for "+c.user,
		"camliPath:journal", res.Journal.String(),
	)
}

// hostStore is a livejournal.Store of an importer's host.
type hostStore struct {
	h *importer.Host
}

func (s hostStore) Signer() *schema.Signer { return s.h.Signer() }

func (s hostStore) Upload(ctx context.Context, signed string) error {
	_, err := blobserver.ReceiveString(ctx, s.h.Target(), signed)
	return err
}

func (s hostStore) Describe(ctx context.Context, req *search.DescribeRequest) (*search.DescribeResponse, error) {
	return s.h.Searcher().Describe(ctx, req)
}
