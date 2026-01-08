/*
Copyright 2017 The Perkeep Authors.

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
	"fmt"
	"strings"
	"time"

	"perkeep.org/pkg/blob"
)

const (
	// the format in which all dates are displayed and entered
	dateformatYyyyMmDd = "2006-01-02"
)

// Document is a structure that groups adds information to a pdf.
// It is stored as a permanode with the "pdfcabinet:doc" camliNodeType value.
type document struct {
	// permanode for this document.
	permanode blob.Ref

	// PDF for this document
	pdf blob.Ref

	// Creation is the date the Document struct was created
	// Stored as nodeAttr.DateCreated.
	creation time.Time

	// Fields below are user-set, and hence optional.

	// DocDate is the user-nominated date associated with this document. It can
	// store any date the user likes but is intended to be when the document was
	// received, or, perhaps, written or sent.
	// Stored as nodeattr.StartDate.
	docDate time.Time

	// Title is the user-nominated title of the document.  We default
	// to be the same as the pdf file name.
	// Stored as nodeattr.Title.
	title string

	// Tags is the slice of zero or more tags associated with the document by the user.
	// Stored as tag.
	tags separatedString

	// DueDate is the user-nominated date that the document is "due". The
	// meaning of what "due" means in relation to each particular document
	// is up to the user
	// Stored as nodeAttr.PaymentDueDate.
	dueDate time.Time
}

// displayURL returns the url that displays this struct
func (doc *document) displayURL() string {
	return fmt.Sprintf("doc/%s", doc.permanode)
}

// SomeTitle returns this struct's title or, failing that, its tags -
// and even failing that, its IntID
func (doc *document) someTitle() string {
	if doc.title != "" {
		return doc.title
	}
	if doc.tags.isEmpty() {
		return fmt.Sprintf("Doc Ref %s", doc.permanode)
	}
	return strings.Join(doc.tags, ",")
}

// DateYyyyMmDd formats this struct's DocDate according to the DateformatYyyyMmDd const
func (doc *document) dateYyyyMmDd() string {
	return formatYyyyMmDd(doc.docDate)
}

// DateYyyyMmDd formats this struct's DueDate according to the DateformatYyyyMmDd const
func (doc *document) dueYyyyMmDd() string {
	return formatYyyyMmDd(doc.dueDate)
}

// formatYyyyMmDd is a convenience function that formats a given Time according to
// the DateformatYyyyMmDd const
func formatYyyyMmDd(indate time.Time) string {
	if indate.IsZero() {
		return ""
	}
	return indate.Format(dateformatYyyyMmDd)
}

// DocumentVM stores the Document data required by the view templates
type DocumentVM struct {
	BlobRef      blob.Ref
	Pdf          blob.Ref
	Title        string
	SomeTitle    string
	DateYyyyMmDd string
	DueYyyyMmDd  string
	DisplayUrl   string
	Tags         separatedString
}

// MakeViewModel returns a new DocumentVM with the data from this struct
func (doc *document) MakeViewModel() DocumentVM {
	return DocumentVM{
		BlobRef:      doc.permanode,
		Pdf:          doc.pdf,
		DisplayUrl:   doc.displayURL(),
		Title:        doc.title,
		SomeTitle:    doc.someTitle(),
		DateYyyyMmDd: doc.dateYyyyMmDd(),
		DueYyyyMmDd:  doc.dueYyyyMmDd(),
		Tags:         doc.tags,
	}
}

// MakeDocumentViewModels takes a slice of Documents and returns a slice of
// the same number of DocumentVMs with the data converted.
func MakeDocumentViewModels(docs []*document) []DocumentVM {
	models := make([]DocumentVM, len(docs))
	for i := 0; i < len(docs); i++ {
		models[i] = docs[i].MakeViewModel()
	}
	return models
}
