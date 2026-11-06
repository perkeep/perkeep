/*
Copyright 2017 The Perkeep Authors

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

// Package plaid implements an importer for financial transactions from plaid.com
package plaid // import "perkeep.org/pkg/importer/plaid"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"

	"github.com/plaid/plaid-go/plaid"
)

func init() {
	importer.Register("plaid", &imp{})
}

type imp struct{}

func (*imp) Properties() importer.Properties {
	return importer.Properties{
		CallbackRequiresPOST: true,
		Title:                "Plaid",
		Description:          "import your financial transactions from plaid.com",
		SupportsIncremental:  true,
		NeedsAPIKey:          true,
	}
}

const (
	acctAttrToken    = "plaidAccountToken"
	acctAttrUsername = "username"
	acctInstitution  = "institutionType"

	plaidTransactionTimeFormat = "2006-01-02"
	plaidTransactionNodeType   = "plaid.io:transaction"
	plaidLastTransaction       = "lastTransactionSyncDate"
)

func (*imp) IsAccountReady(acct *importer.Object) (ready bool, err error) {
	return acct.Attr(acctAttrToken) != "" && acct.Attr(acctAttrUsername) != "", nil
}

func (*imp) SummarizeAccount(acct *importer.Object) string {
	return fmt.Sprintf("%s (%s)", acct.Attr(acctAttrUsername), acct.Attr(acctInstitution))
}

func (*imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	args := struct {
		Ctx  *importer.SetupContext
		Inst InstitutionNameMap
	}{
		ctx,
		supportedInstitutions,
	}

	return tmpl.ExecuteTemplate(w, "serveSetup", args)
}

var tmpl = template.Must(template.New("root").Parse(`
{{define "serveSetup"}}
<h1>Configuring Bank Account</h1>
<p>Enter your username/password credentials for your bank/card account and select the institution type.
<form method="post" action="{{.Ctx.CallbackPath}}">
  <input type="hidden" name="acct" value="{{.Ctx.AccountNode.PermanodeRef}}">
  <table border=0 cellpadding=3>
  <tr><td align=right>Username</td><td><input name="username" size=50></td></tr>
  <tr><td align=right>Password</td><td><input name="password" size=50 type="password"></td></tr>
  <tr><td>Institution</td><td><select name="institution">
    {{range .Inst}}
        <option value="{{.CodeName}}">{{.DisplayName}}</option>
    {{end}}
  </select></td></tr>
  <tr><td align=right></td><td align=right><input type="submit" value="Add"></td></tr>
  </table>
</form>
{{end}}
`))

var _ importer.ImporterSetupHTMLer = (*imp)(nil)

func (im *imp) AccountSetupHTML(host *importer.Host) string {
	return `
<h1>Configuring Plaid</h1>
<p>Signup for a developer account on <a href='https://dashboard.plaid.com/signup'>Plaid dashboard</a>
<p>After following signup steps and verifying your email, get your developer credentials
(under "Send your first request"), and copy your client ID and secret above.
<p>
`
}

func (im *imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	username := r.FormValue("username")
	password := r.FormValue("password")
	if username == "" || password == "" {
		http.Error(w, "Username and password are both required", http.StatusBadRequest)
		return
	}
	institution := r.FormValue("institution")

	clientID, secret, err := ctx.Credentials()
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Credentials error: %w", err))
		return
	}

	client := plaid.NewClient(clientID, secret, plaid.Tartan)
	res, _, err := client.ConnectAddUser(username, password, "", institution, nil)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("ConnectAddUser error: %w", err))
		return
	}

	if err := ctx.AccountNode.SetAttrs(
		importer.KV("title", fmt.Sprintf("%s account: %s", institution, username)),
		importer.KV(acctAttrUsername, username),
		importer.KV(acctAttrToken, res.AccessToken),
		importer.KV(acctInstitution, institution),
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attributes: %w", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

func (im *imp) Run(ctx *importer.RunContext) (err error) {
	ctx.Logf("Running importer.")
	defer func() {
		ctx.Logf("Importer returned: %v", err)
	}()

	clientID, secret, err := ctx.Credentials()
	if err != nil {
		return err
	}

	var opt plaid.ConnectGetOptions
	if start := ctx.AccountNode().Attr(plaidLastTransaction); start != "" {
		opt.GTE = start
	}

	client := plaid.NewClient(clientID, secret, plaid.Tartan)
	resp, _, err := client.ConnectGet(ctx.AccountNode().Attr(acctAttrToken), &opt)
	if err != nil {
		return fmt.Errorf("connectGet: %w", err)
	}

	bar := ctx.Progress().Bar("transactions", "Importing transactions")
	bar.SetTotal(int64(len(resp.Transactions)))
	var latestTrans string
	for _, t := range resp.Transactions {
		if err := ctx.Context().Err(); err != nil {
			return err
		}
		tdate, err := im.importTransaction(ctx, &t)
		if err != nil {
			return err
		} else if tdate > latestTrans {
			latestTrans = tdate
			ctx.AccountNode().SetAttr(plaidLastTransaction, latestTrans)
		}
		bar.Add(1)
	}
	bar.Finish()

	return nil
}

func (im *imp) importTransaction(ctx *importer.RunContext, t *plaid.Transaction) (string, error) {
	itemNode, err := ctx.RootNode().ChildPathObject(t.ID)
	if err != nil {
		return "", err
	}

	transJSON, err := json.Marshal(t)
	if err != nil {
		return "", err
	}

	fileRef, err := schema.WriteFileFromReader(ctx.Context(), ctx.Host.Target(), "", bytes.NewBuffer(transJSON))
	if err != nil {
		return "", err
	}

	transactionTime, err := time.Parse(plaidTransactionTimeFormat, t.Date)
	if err != nil {
		return "", err
	}

	if err := itemNode.SetAttrs(
		importer.KV(nodeattr.Type, plaidTransactionNodeType),
		importer.KV(nodeattr.DateCreated, schema.RFC3339FromTime(transactionTime)),
		importer.KV("transactionId", t.ID),
		importer.KV("vendor", t.Name),
		importer.KV("amount", fmt.Sprintf("%f", t.Amount)),
		importer.KV("currency", "USD"),
		importer.KV("categoryId", t.CategoryID),
		importer.KV(nodeattr.Title, t.Name),
		importer.KV(nodeattr.CamliContent, fileRef.String()),
	); err != nil {
		return "", err
	}

	// if the transaction includes location information (rare), use the supplied
	// lat/long. Partial address data (eg, the US state) without corresponding lat/long
	// is also sometimes returned; no attempt is made to geocode that info currently.
	if t.Meta.Location.Coordinates.Lat != 0 && t.Meta.Location.Coordinates.Lon != 0 {
		if err := itemNode.SetAttrs(
			importer.KV("latitude", fmt.Sprintf("%f", t.Meta.Location.Coordinates.Lat)),
			importer.KV("longitude", fmt.Sprintf("%f", t.Meta.Location.Coordinates.Lon)),
		); err != nil {
			return "", err
		}
	}

	return t.Date, nil
}

func (im *imp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	httputil.BadRequestError(w, "Unexpected path: %s", r.URL.Path)
}

func (im *imp) CallbackRequestAccount(r *http.Request) (blob.Ref, error) {
	return importer.OAuth1{}.CallbackRequestAccount(r)
}

func (im *imp) CallbackURLParameters(acctRef blob.Ref) url.Values {
	return importer.OAuth1{}.CallbackURLParameters(acctRef)
}
