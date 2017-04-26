/*
Copyright 2017 The Camlistore Authors.

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

// Package downloadbutton provides a Button element that is used in the sidebar of
// the web UI, to download as a zip file all selected files.
package downloadbutton

import (
	"fmt"
	"strings"

	"camlistore.org/pkg/blob"

	"github.com/myitcv/gopherjs/react"
	"honnef.co/go/js/dom"
)

// New returns the button element. It should be used as the entry point, to
// create the needed React element.
//
// key is the id for when the button is in a list, see
// https://facebook.github.io/react/docs/lists-and-keys.html
//
// config is the web UI config that was fetched from the server.
//
// getSelection returns the list of files (blobRefs) selected for downloading.
func New(key string, config map[string]string, getSelection func() []string) react.Element {
	if config == nil {
		fmt.Println("Nil config for DownloadItemsBtn")
		return nil
	}
	downloadHelper, ok := config["downloadHelper"]
	if !ok {
		fmt.Println("No downloadHelper in config for DownloadItemsBtn")
		return nil
	}
	if getSelection == nil {
		fmt.Println("Nil getSelection for DownloadItemsBtn")
		return nil
	}
	if key == "" {
		// A key is only needed in the context of a list, which is why
		// it is up to the caller to choose it. Just creating it here for
		// the sake of consistency.
		key = "downloadItemsButton"
	}
	props := DownloadItemsBtnProps{
		Key:            key,
		GetSelection:   getSelection,
		Config:         config,
		downloadHelper: downloadHelper,
	}
	return DownloadItemsBtn(props).Render()
}

// DownloadItemsBtnDef is the definition for the button, that Renders as a React
// Button.
type DownloadItemsBtnDef struct {
	react.ComponentDef
}

type DownloadItemsBtnProps struct {
	// Key is the id for when the button is in a list, see
	// https://facebook.github.io/react/docs/lists-and-keys.html
	Key string
	// GetSelection returns the list of files (blobRefs) selected
	// for downloading.
	GetSelection func() []string
	// Config is the web UI config that was fetched from the server.
	Config         map[string]string
	downloadHelper string
}

func (p *DownloadItemsBtnDef) Props() DownloadItemsBtnProps {
	uprops := p.ComponentDef.Props()
	return uprops.(DownloadItemsBtnProps)
}

func DownloadItemsBtn(p DownloadItemsBtnProps) *DownloadItemsBtnDef {
	res := &DownloadItemsBtnDef{}

	react.BlessElement(res, p)

	return res
}

func (d *DownloadItemsBtnDef) Render() react.Element {
	return react.Button(
		react.ButtonProps(func(bp *react.ButtonPropsDef) {
			bp.OnClick = d.handleDownloadSelection
			bp.Key = d.Props().Key
		}),
		react.S("Download"),
	)
}

func (d *DownloadItemsBtnDef) handleDownloadSelection(*react.SyntheticMouseEvent) {
	// Note: there's a "memleak", as in: until the selection is cleared and
	// another one is started, this button stays allocated. It is of no
	// consequence in this case as we don't allocate a lot for this element (in
	// previous experiments where the zip archive was in memory, the leak was
	// definitely noticeable then), but it is something to keep in mind for
	// future elements.
	go func() {
		if err := d.downloadSelection(); err != nil {
			dom.GetWindow().Alert(fmt.Sprintf("%v", err))
		}
	}()
}

func (d *DownloadItemsBtnDef) downloadSelection() error {
	selection := d.Props().GetSelection()
	downloadPrefix := d.Props().downloadHelper
	fileRefs := []string{}
	for _, file := range selection {
		ref, ok := blob.Parse(file)
		if !ok {
			return fmt.Errorf("Cannot download %q, not a valid blobRef\n", file)
		}
		fileRefs = append(fileRefs, ref.String())
	}

	el := dom.GetWindow().Document().CreateElement("input")
	input := el.(*dom.HTMLInputElement)
	input.Type = "text"
	input.Name = "files"
	input.Value = strings.Join(fileRefs, ",")

	el = dom.GetWindow().Document().CreateElement("form")
	form := el.(*dom.HTMLFormElement)
	form.Action = downloadPrefix
	form.Method = "POST"
	form.AppendChild(input)
	// As per
	// https://html.spec.whatwg.org/multipage/forms.html#form-submission-algorithm
	// step 2., a form must be connected to the DOM for submission.
	body := dom.GetWindow().Document().QuerySelector("body")
	body.AppendChild(form)
	defer body.RemoveChild(form)
	form.Submit()
	return nil
}
