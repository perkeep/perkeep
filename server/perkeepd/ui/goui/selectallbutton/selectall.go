//go:generate reactGen

/*
Copyright 2018 The Perkeep Authors.

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

// Package selectallbutton provides a Button element that is used in the sidebar of
// the web UI, to select all the items matching the current search in the web UI.
// The button is disabled if the current search is not an explicit predicate
// search (e.g. "tag:foo"), or a container predicate (ref:<blobref>).
package selectallbutton

import (
	"context"
	"fmt"
	"strings"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/search"

	"github.com/gopherjs/gopherjs/js"
	"honnef.co/go/js/dom"
	"myitcv.io/react"
)

// New returns the button element. It should be used as the entry point, to
// create the needed React element.
//
// key is the id for when the button is in a list, see
// https://facebook.github.io/react/docs/lists-and-keys.html
//
// config is the web UI config that was fetched from the server.
//
// cbs is a wrapper around the callback functions required by this component.
func New(key string, config map[string]string, cbs *Callbacks) react.Element {
	if cbs == nil {
		fmt.Println("Nil callbacks for SelectAllBtn")
		return nil
	}
	if cbs.GetQuery == nil {
		fmt.Println("Nil GetQuery callback for SelectAllBtn")
		return nil
	}
	if cbs.GetQuery() == "" {
		// This makes sure we don't enable the button when we're on the "main" page,
		// with no search.
		return nil
	}
	if cbs.SetSelection == nil {
		fmt.Println("Nil SetSelection callback for SelectAllBtn")
		return nil
	}
	if config == nil {
		fmt.Println("Nil config for SelectAllBtn")
		return nil
	}
	authToken, ok := config["authToken"]
	if !ok {
		fmt.Println("No authToken in config for SelectAllBtn")
		return nil
	}
	if key == "" {
		// A key is only needed in the context of a list, which is why
		// it is up to the caller to choose it. Just creating it here for
		// the sake of consistency.
		key = "selectAllButton"
	}
	props := SelectAllBtnProps{
		key:       key,
		callbacks: cbs,
		authToken: authToken,
	}
	return SelectAllBtn(props)
}

// Callbacks defines the callbacks that must be provided when creating a
// SelectAllBtn instance.
type Callbacks struct {
	o *js.Object

	// GetQuery returns the current search session predicate.
	GetQuery func() string `js:"getQuery"`

	// SetSelection sets the given selection of blobRefs as the selected permanodes
	// in the web UI.
	SetSelection func(map[string]bool) `js:"setSelection"`
}

// SelectAllBtnDef defines a React button to select all items matching the
// current search query.
type SelectAllBtnDef struct {
	react.ComponentDef
}

type SelectAllBtnProps struct {
	// Key is the id for when the button is in a list, see
	// https://facebook.github.io/react/docs/lists-and-keys.html
	key string

	callbacks *Callbacks

	authToken string
}

func SelectAllBtn(p SelectAllBtnProps) *SelectAllBtnElem {
	return buildSelectAllBtnElem(p)
}

func (d SelectAllBtnDef) Render() react.Element {
	return react.Button(
		&react.ButtonProps{
			OnClick: d,
			Key:     d.Props().key,
		},
		react.S("Select all"),
	)
}

func (d SelectAllBtnDef) OnClick(*react.SyntheticMouseEvent) {
	go func() {
		selection, err := d.findAll()
		if err != nil {
			dom.GetWindow().Alert(fmt.Sprintf("%v", err))
			return
		}
		d.Props().callbacks.SetSelection(selection)
	}()
}

// getQuery returns the query corresponding to the current search in the web UI.
func (d SelectAllBtnDef) getQuery() *search.SearchQuery {
	predicate := d.Props().callbacks.GetQuery()
	if !strings.HasPrefix(predicate, "ref:") {
		return &search.SearchQuery{
			Limit:      -1,
			Expression: predicate,
		}
	}

	// If we've got a ref: predicate, assume the given blobRef is a container, and
	// find its children.
	blobRef := strings.TrimPrefix(predicate, "ref:")
	br, ok := blob.Parse(blobRef)
	if !ok {
		println(`Invalid blobRef in "ref:" predicate: ` + blobRef)
		return nil
	}
	return &search.SearchQuery{
		Limit: -1,
		Constraint: &search.Constraint{Permanode: &search.PermanodeConstraint{
			Relation: &search.RelationConstraint{
				Relation: "parent",
				Any: &search.Constraint{
					BlobRefPrefix: br.String(),
				},
			},
		}},
	}
}

// findAll returns all the permanodes matching the current web UI search
// session. The javascript UI code uses a javascript object with blobRefs as
// properties to represent a user's selection of permanodes. Since gopherjs
// converts a Go map to a javascript object, findAll returns such a map so it
// matches directly what the UI code wants as a selection object.
func (d SelectAllBtnDef) findAll() (map[string]bool, error) {
	query := d.getQuery()
	if query == nil {
		return nil, nil
	}
	authToken := d.Props().authToken
	am, err := auth.TokenOrNone(authToken)
	if err != nil {
		return nil, fmt.Errorf("Error setting up auth: %v", err)
	}
	cl, err := client.New(client.OptionAuthMode(am))
	if err != nil {
		return nil, err
	}
	res, err := cl.Query(context.TODO(), query)
	if err != nil {
		return nil, err
	}
	blobs := make(map[string]bool, len(res.Blobs))
	for _, v := range res.Blobs {
		blobs[v.Blob.String()] = true
	}
	return blobs, nil
}
