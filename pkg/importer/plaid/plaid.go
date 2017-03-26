/*
Copyright 2017 The Camlistore Authors

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
package plaid // import "camlistore.org/pkg/importer/plaid"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/schema/nodeattr"

	"github.com/plaid/plaid-go/plaid"
)

func init() {
	importer.Register("plaid", &imp{})
}

type imp struct{}

func (*imp) SupportsIncremental() bool {
	return true
}

func (*imp) NeedsAPIKey() bool {
	return true
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
<form method="get" action="{{.Ctx.CallbackURL}}">
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
	return fmt.Sprintf(`
<h1>Configuring Plaid</h1>
<p>Signup for a developer account on <a href='https://dashboard.plaid.com/signup'>Plaid dashboard</a>
<p>After following signup steps and verifying your email, get your developer credentials
(under "Send your first request"), and copy your client ID and secret above.
<p>
`)
}

func (im *imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	username := r.FormValue("username")
	password := r.FormValue("password")
	if username == "" || password == "" {
		http.Error(w, "Username and password are both required", 400)
		return
	}
	institution := r.FormValue("institution")

	clientID, secret, err := ctx.Credentials()
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Credentials error: %v", err))
		return
	}

	client := plaid.NewClient(clientID, secret, plaid.Tartan)
	res, _, err := client.ConnectAddUser(username, password, "", institution, nil)
	if err != nil {
		httputil.ServeError(w, r, fmt.Errorf("ConnectAddUser error: %v", err))
		return
	}

	if err := ctx.AccountNode.SetAttrs(
		"title", fmt.Sprintf("%s account: %s", institution, username),
		acctAttrUsername, username,
		acctAttrToken, res.AccessToken,
		acctInstitution, institution,
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attributes: %v", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

func (im *imp) Run(ctx *importer.RunContext) (err error) {
	log.Printf("Running plaid importer.")
	defer func() {
		log.Printf("plaid importer returned: %v", err)
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
		fmt.Errorf("ConnectGet: %s\n", err)
		return
	}

	var latestTrans string
	for _, t := range resp.Transactions {
		tdate, err := im.importTransaction(ctx, &t)
		if err != nil {
			return err
		} else if tdate > latestTrans {
			latestTrans = tdate
			ctx.AccountNode().SetAttr(plaidLastTransaction, latestTrans)
		}
	}

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

	fileRef, err := schema.WriteFileFromReader(ctx.Host.Target(), "", bytes.NewBuffer(transJSON))
	if err != nil {
		return "", err
	}

	transactionTime, err := time.Parse(plaidTransactionTimeFormat, t.Date)
	if err != nil {
		return "", err
	}

	if err := itemNode.SetAttrs(
		nodeattr.Type, plaidTransactionNodeType,
		nodeattr.DateCreated, schema.RFC3339FromTime(transactionTime),
		"transactionId", t.ID,
		"vendor", t.Name,
		"amount", fmt.Sprintf("%f", t.Amount),
		"currency", "USD",
		"categoryId", t.CategoryID,
		nodeattr.Title, t.Name,
		nodeattr.CamliContent, fileRef.String(),
	); err != nil {
		return "", err
	}

	// if the transaction includes location information (rare), use the supplied
	// lat/long. Partial address data (eg, the US state) without corresponding lat/long
	// is also sometimes returned; no attempt is made to geocode that info currently.
	if t.Meta.Location.Coordinates.Lat != 0 && t.Meta.Location.Coordinates.Lon != 0 {
		if err := itemNode.SetAttrs(
			"latitude", fmt.Sprintf("%f", t.Meta.Location.Coordinates.Lat),
			"longitude", fmt.Sprintf("%f", t.Meta.Location.Coordinates.Lon),
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
