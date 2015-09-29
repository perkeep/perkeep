/*
Copyright 2013 The Camlistore Authors

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

// Package dummy is an example importer for development purposes.
package dummy

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/env"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/schema"
)

func init() {
	if !env.IsDev() {
		// For this particular example importer, we only
		// register it if we're in "devcam server" mode.
		// Normally you'd avoid this check.
		return
	}

	// This Register call must happen during init.
	//
	// Register only registers an importer site type and not a
	// specific account on a site.
	importer.Register("dummy", &imp{})
}

// imp is the dummy importer, as a demo of how to write an importer.
//
// It must implement the importer.Importer interface in order for
// it to be registered (in the init above).
type imp struct {
	// The struct or underlying type implementing an importer
	// holds state that is global, and not per-account, so it
	// should not be used to cache account-specific
	// resources. Some importers (e.g. Foursquare) use this space
	// to cache mappings from site-specific global resource URLs
	// (e.g. category icons) to the fileref once it's been copied
	// into Camlistore.

	mu          sync.Mutex          // mu guards cache
	categoryRef map[string]blob.Ref // URL -> file schema ref
}

func (*imp) SupportsIncremental() bool {
	// SupportsIncremental signals to the importer host that this
	// importer has been optimized to be run regularly (e.g. every 5
	// minutes or half hour).  If it returns false, the user must
	// manually start imports.
	return false
}

func (*imp) NeedsAPIKey() bool {
	// This tells the importer framework that we our importer will
	// be calling the {RunContext,SetupContext}.Credentials method
	// to get the OAuth client ID & client secret, which may be
	// either configured on the importer permanode, or statically
	// in the server's config file.
	return true
}

const (
	acctAttrToken     = "my_token"
	acctAttrUsername  = "username"
	acctAttrRunNumber = "run_number" // some state
)

func (*imp) IsAccountReady(acct *importer.Object) (ready bool, err error) {
	// This method tells the importer framework whether this account
	// permanode (accessed via the importer.Object) is ready to start
	// an import.  Here you would typically check whether you have the
	// right metadata/tokens on the account.
	return acct.Attr(acctAttrToken) != "" && acct.Attr(acctAttrUsername) != "", nil
}

func (*imp) SummarizeAccount(acct *importer.Object) string {
	// This method is run by the importer framework if the account is
	// ready (see IsAccountReady) and summarizes the account in
	// the list of accounts on the importer page.
	return acct.Attr(acctAttrUsername)
}

func (*imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	// ServeSetup gets called at the beginning of adding a new account
	// to an importer, or when an account is being re-logged into to
	// refresh its access token.
	// You typically start the OAuth redirect flow here.
	// The importer.OAuth2.RedirectURL and importer.OAuth2.RedirectState helpers can be used for OAuth2.
	http.Redirect(w, r, ctx.CallbackURL(), http.StatusFound)
	return nil
}

// Statically declare that our importer supports the optional
// importer.ImporterSetupHTMLer interface.
//
// We do this in case importer.ImporterSetupHTMLer changes, or if we
// typo the method name below. It turns this into a compile-time
// error. In general you should do this in Go whenever you implement
// optional interfaces.
var _ importer.ImporterSetupHTMLer = (*imp)(nil)

func (im *imp) AccountSetupHTML(host *importer.Host) string {
	return "<h1>Hello from the dummy importer!</h1><p>I am example HTML. This importer is a demo of how to write an importer.</p>"
}

func (im *imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	// ServeCallback is called after ServeSetup, at the end of an
	// OAuth redirect flow.

	code := r.FormValue("code") // e.g. get the OAuth code out of the redirect
	if code == "" {
		code = "some_dummy_code"
	}
	name := ctx.AccountNode.Attr(acctAttrUsername)
	if name == "" {
		names := []string{
			"alfred", "alice", "bob", "bethany",
			"cooper", "claire", "doug", "darla",
			"ed", "eve", "frank", "francine",
		}
		name = names[rand.Intn(len(names))]
	}
	if err := ctx.AccountNode.SetAttrs(
		"title", fmt.Sprintf("dummy account: %s", name),
		acctAttrUsername, name,
		acctAttrToken, code,
	); err != nil {
		httputil.ServeError(w, r, fmt.Errorf("Error setting attributes: %v", err))
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

func (im *imp) Run(ctx *importer.RunContext) (err error) {
	log.Printf("Running dummy importer.")
	defer func() {
		log.Printf("Dummy importer returned: %v", err)
	}()
	root := ctx.RootNode()
	fileRef, err := schema.WriteFileFromReader(ctx.Host.Target(), "foo.txt", strings.NewReader("Some file.\n"))
	if err != nil {
		return err
	}
	obj, err := root.ChildPathObject("foo.txt")
	if err != nil {
		return err
	}
	if err = obj.SetAttr("camliContent", fileRef.String()); err != nil {
		return err
	}
	n, _ := strconv.Atoi(ctx.AccountNode().Attr(acctAttrRunNumber))
	n++
	ctx.AccountNode().SetAttr(acctAttrRunNumber, fmt.Sprint(n))
	// Update the title each time, just to show it working. You
	// wouldn't actually do this:
	return root.SetAttr("title", fmt.Sprintf("dummy: %s import #%d", ctx.AccountNode().Attr(acctAttrUsername), n))
}

func (im *imp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	httputil.BadRequestError(w, "Unexpected path: %s", r.URL.Path)
}

func (im *imp) CallbackRequestAccount(r *http.Request) (blob.Ref, error) {
	// We do not actually use OAuth, but this method works for us anyway.
	// Even if your importer implementation does not use OAuth, you can
	// probably just embed importer.OAuth1 in your implementation type.
	// If OAuth2, embedding importer.OAuth2 should work.
	return importer.OAuth1{}.CallbackRequestAccount(r)
}

func (im *imp) CallbackURLParameters(acctRef blob.Ref) url.Values {
	// See comment in CallbackRequestAccount.
	return importer.OAuth1{}.CallbackURLParameters(acctRef)
}
