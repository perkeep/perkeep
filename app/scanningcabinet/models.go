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

package main

import (
	"fmt"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
)

const (
	// the format in which all dates are displayed and entered
	dateformatYyyyMmDd = "2006-01-02"
)

// mediaObject represents the metadata associated with each individual uploaded scan.
// It is stored as a permanode with the "scanningcabinet:scan" camliNodeType value.
type mediaObject struct {
	// permanode for this scan.
	permanode blob.Ref

	// contentRef is the image file blobRef.
	// Stored as nodeattr.CamliContent.
	contentRef blob.Ref

	// Creation is the time when this struct was originally created.
	// Stored as nodeattr.DateCreated, which makes this field the default
	// sorting criterion when searching for scans.
	creation time.Time

	// TODO(mpl): as with the ContentType, I've removed the filename, because it is
	// already stored in the file schema blob, so it would be redundant to store it in
	// the scan permanode. It is not needed anywhere for now, but I think it could be
	// used as the "alt" field for the raw scan img (which the original app didn't seem
	// to do).

	// DocumentRef is the blobRef of the associated Document permanode.
	// A Document has many MediaObjects. When newly uploaded,
	// a MediaObject is not associated with a Document.
	documentRef blob.Ref
}

// urlResize returns the URL that displays this struct at an unspecified size.
// The size must by subsequently specified by concatenating an integer to make a legal URL.
func (mo *mediaObject) urlResize() string {
	return fmt.Sprintf("%s?resize=", mo.displayURL())
}

// thumbUrl returns the URL that displays this struct at a thumbnail size.
func (mo *mediaObject) thumbUrl() string {
	return fmt.Sprintf("%s?resize=300", mo.displayURL())
}

// displayURL returns the URL that displays this struct at its original size
func (mo *mediaObject) displayURL() string {
	return fmt.Sprintf("resource/%s", mo.permanode.String())
}

// MediaObjectVM stores the MediaObject data required by the view templates
type MediaObjectVM struct {
	BlobRef   blob.Ref
	UrlResize string
	ThumbUrl  string
}

// MakeViewModel returns a new DocumentVM with the data from this struct
func (mo *mediaObject) MakeViewModel() MediaObjectVM {
	return MediaObjectVM{
		BlobRef:   mo.permanode,
		UrlResize: mo.urlResize(),
		ThumbUrl:  mo.thumbUrl(),
	}
}

// MakeMediaObjectViewModels takes a slice of MediaObjects and returns a slice of
// the same number of MediaObjectVMs with the data converted.
func MakeMediaObjectViewModels(mediaObjects []mediaObject) []MediaObjectVM {
	models := make([]MediaObjectVM, len(mediaObjects))
	for i := 0; i < len(mediaObjects); i++ {
		models[i] = mediaObjects[i].MakeViewModel()
	}
	return models
}

// Document is a structure that groups scans into a logical unit.
// A letter (Stored as a document) could have several pages
// (each is a MediaObject), for example.
// It is stored as a permanode with the "scanningcabinet:doc" camliNodeType value.
type document struct {
	// permanode for this document.
	permanode blob.Ref

	// Pages are the blobRefs of each Media Object that contitute this Document.
	// Each page is stored as camliPath:pageNumber = blobRef.
	// The first pageNumber is zero.
	pages []blob.Ref

	// Creation is the date the Document struct was created
	// Stored as nodeAttr.DateCreated.
	creation time.Time

	// Fields below are user-set, and hence optional.

	// DocDate is the user-nominated date associated with this document. It can
	// store any date the user likes but is intended to be when the document was
	// received, or, perhaps, written or sent.
	// Stored as nodeattr.StartDate.
	docDate time.Time

	// Title is the user-nominated title of the document.
	// Stored as nodeattr.Title.
	title string

	// Tags is the slice of zero or more tags associated with the document by the user.
	// Stored as tag.
	tags separatedString

	// PhysicalLocation is the user-nominated description of the location
	// of the physical document of which the MediaObjects associated with this
	// Document are scans.
	// Stored as nodeAttr.LocationText.
	physicalLocation string

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
	BlobRef          blob.Ref
	Title            string
	SomeTitle        string
	DateYyyyMmDd     string
	DueYyyyMmDd      string
	DisplayUrl       string
	Tags             separatedString
	PhysicalLocation string
}

// MakeViewModel returns a new DocumentVM with the data from this struct
func (doc *document) MakeViewModel() DocumentVM {
	return DocumentVM{
		BlobRef:          doc.permanode,
		DisplayUrl:       doc.displayURL(),
		Title:            doc.title,
		SomeTitle:        doc.someTitle(),
		DateYyyyMmDd:     doc.dateYyyyMmDd(),
		DueYyyyMmDd:      doc.dueYyyyMmDd(),
		Tags:             doc.tags,
		PhysicalLocation: doc.physicalLocation,
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
