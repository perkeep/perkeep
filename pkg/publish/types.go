/*
Copyright 2013 The Camlistore Authors.

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

// Package publish exposes the types and functions that can be used
// from a Go template, for publishing.
package publish

import (
	"html/template"

	"camlistore.org/pkg/search"
)

// SubjectPage is the data structure used when serving a
// publishing template. It contains the functions that can be called
// from the template.
type SubjectPage struct {
	Header  func() *PageHeader
	File    func() *PageFile
	Members func() *PageMembers
}

// PageHeader contains the data available to the template,
// and relevant to the page header.
type PageHeader struct {
	Title         string      // Page title.
	CSSFiles      []string    // Available CSS files.
	JSDeps        []string    // Dependencies (for e.g closure) that can/should be included as javascript files.
	CamliClosure  template.JS // Closure namespace defined in the provided js. e.g camlistore.GalleryPage from pics.js
	Subject       string      // Subject of this page (i.e the object which is described and published).
	Meta          string      // All the metadata describing the subject of this page.
	ViewerIsOwner bool        // Whether the viewer of the page is also the owner of the displayed subject. (localhost check for now.)
}

// PageFile contains the file related data available to the subject template,
// if the page describes some file contents.
type PageFile struct {
	FileName     string
	Size         int64
	MIMEType     string
	IsImage      bool
	DownloadURL  string
	ThumbnailURL string
	DomID        string
	Nav          func() *Nav
}

// Nav holds links to the previous, next, and parent elements,
// when displaying members.
type Nav struct {
	ParentPath string
	PrevPath   string
	NextPath   string
}

// PageMembers contains the data relevant to the members if the published subject
// is a permanode with members.
type PageMembers struct {
	SubjectPath string                                      // URL prefix path to the subject (i.e the permanode).
	ZipName     string                                      // Name of the downloadable zip file which contains all the members.
	Members     []*search.DescribedBlob                     // List of the members.
	Description func(*search.DescribedBlob) string          // Returns the description of the given member.
	Title       func(*search.DescribedBlob) string          // Returns the title for the given member.
	Path        func(*search.DescribedBlob) string          // Returns the url prefix path to the given the member.
	DomID       func(*search.DescribedBlob) string          // Returns the Dom ID of the given member.
	FileInfo    func(*search.DescribedBlob) *MemberFileInfo // Returns some file info if the given member is a file permanode.
}

// MemberFileInfo contains the file related data available for each member,
// if the member is the permanode for a file.
type MemberFileInfo struct {
	FileName         string
	FileDomID        string
	FilePath         string
	FileThumbnailURL string
}
