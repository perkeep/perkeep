//go:build js
// +build js

/*
Copyright 2016 The Perkeep Authors.

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
	"encoding/json"
	"fmt"
	"html"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"

	"perkeep.org/pkg/blob"

	"github.com/gopherjs/gopherjs/js"
	"github.com/gopherjs/jquery"
)

const (
	ficDiv              = "div#fileitemcontainer" // jquery matching for the top file container element
	fileThumbnailHeight = 600
)

var theFic *fileItemContainer

// StartRenderFile displays a view of the template subject when the subject is a
// file. It relies on the presence of a div with id "fileitemcontainer", to create
// DOM elements as children of the mentioned div. As the actual rendering is run in
// a goroutine, it is not guaranteed to be finished when StartRenderFile returns.
func StartRenderFile() {
	// renderFile calls funcs that wait on http requests or channels,
	// which is not allowed in a javascript callback, so they have to be called
	// from within a goroutine.
	go renderFile()
}

// renderFile creates a fileItemContainer, populates it, renders it, and
// binds the left and right arrow keys for to it for navigation.
func renderFile() {
	var err error
	theFic, err = newFileItemContainer(fileThumbnailHeight)
	if err != nil {
		fmt.Printf("error creating file container: %v\n", err)
		return
	}
	if err := theFic.populate(); err != nil {
		fmt.Printf("Error initializing file container: %v", err)
		return
	}
	if err := theFic.render(); err != nil {
		fmt.Printf("Error rendering file container: %v", err)
		return
	}

	jQuery(js.Global).Call(jquery.KEYUP, func(e jquery.Event) {
		if e.Which == 37 {
			theFic.doPrev()
			go func() {
				if err := theFic.render(); err != nil {
					fmt.Printf("Error rendering file container: %v", err)
				}
			}()
			return
		}
		if e.Which == 39 {
			theFic.doNext()
			go func() {
				if err := theFic.render(); err != nil {
					fmt.Printf("Error rendering file container: %v", err)
				}
			}()
			return
		}
	})
}

type fileItemContainer struct {
	mu sync.Mutex // guards the whole container

	parent     blob.Ref
	basePath   string
	host       string
	scheme     string
	pathPrefix string // app handler's prefix if it applies, e.g. "/pics/", or "/" otherwise.
	// isTopNode indicates whether we're a direct child of the publish root.
	// Because most of the URL paths change if that's the case. I think we
	// could test for !parent.Valid() instead but it does not seem very clean.
	isTopNode bool

	current          *fileItem
	next             *fileItem
	prev             *fileItem
	items            []*fileItem
	currentIdx       int  // pos of current in items
	beginningReached bool // Nothing left to fetch at the beginning
	endReached       bool // Nothing left to fetch at the end

	thumbHeight int // 600
}

type fileItem struct {
	pn         blob.Ref // containing permanode
	contentRef blob.Ref

	fileName string
	size     int64
	mimeType string
	isImage  bool
	thumb    string
	download string
}

func newFileItemContainer(thumbHeight int) (*fileItemContainer, error) {
	host, err := host()
	if err != nil {
		return nil, err
	}
	scheme, err := scheme()
	if err != nil {
		return nil, err
	}
	basePath, err := subjectBasePath()
	if err != nil {
		return nil, err
	}
	root, err := publishedRoot()
	if err != nil {
		return nil, err
	}
	prefix, err := pathPrefix()
	if err != nil {
		return nil, err
	}
	var parent blob.Ref
	isTopNode := false
	if !strings.Contains(basePath, "/-") {
		isTopNode = true
		basePath += "/-"
	} else {
		basePath = path.Dir(basePath)
		if strings.HasSuffix(basePath, "/-") {
			parent = root
		} else {
			_, parentPrefixPath := path.Split(basePath)
			parentPrefix := strings.TrimPrefix(parentPrefixPath, "h")
			parent, err = getFullRef(scheme, host, prefix, parentPrefix)
			if err != nil {
				return nil, err
			}
		}
	}

	return &fileItemContainer{
		basePath:    basePath,
		parent:      parent,
		host:        host,
		scheme:      scheme,
		pathPrefix:  prefix,
		thumbHeight: thumbHeight,
		isTopNode:   isTopNode,
	}, nil
}

func (fic *fileItemContainer) populate() error {
	if fic == nil {
		return fmt.Errorf("uninitialized file container")
	}

	pn, err := subject()
	if err != nil {
		return err
	}

	var sr *SearchResult
	if fic.isTopNode {
		sr, err = fic.describe(pn)
	} else {
		sr, err = fic.getPeers(pn, 3)
	}
	if err != nil {
		return err
	}

	// TODO(mpl): see if the code below can be refactored with updateItems.

	meta := sr.Describe.Meta
	itemIdx := 0
	for _, v := range sr.Blobs {
		desbr, ok := meta[v.Blob.String()]
		if !ok {
			continue
		}
		if desbr.Permanode == nil {
			continue
		}
		camliContent := desbr.Permanode.Attr.Get("camliContent")
		if camliContent == "" {
			continue
		}
		contentRef := blob.MustParse(camliContent)
		cdes := meta[contentRef.String()]
		if cdes == nil {
			continue
		}
		file := cdes.File
		if file == nil {
			continue
		}
		item := &fileItem{
			pn:         desbr.BlobRef,
			fileName:   file.FileName,
			size:       file.Size,
			mimeType:   file.MIMEType,
			isImage:    file.IsImage(),
			contentRef: contentRef,
		}
		fic.items = append(fic.items, item)
		if item.pn == pn {
			fic.current = item
			fic.currentIdx = itemIdx
		}
		itemIdx++
	}
	if fic.currentIdx > 0 {
		fic.prev = fic.items[fic.currentIdx-1]
	}
	if fic.currentIdx < len(fic.items)-1 {
		fic.next = fic.items[fic.currentIdx+1]
	}
	return nil
}

func getFullRef(scheme, host, pathPrefix, digestPrefix string) (blob.Ref, error) {
	var br blob.Ref
	ca := fmt.Sprintf(`{"blobRefPrefix":"sha224-%s"}`, digestPrefix)
	cb := fmt.Sprintf(`{"blobRefPrefix":"sha1-%s"}`, digestPrefix)
	query := fmt.Sprintf(`{"constraint":{"logical":{"op":"or","a":%s,"b":%s}}}`, ca, cb)
	resp, err := http.Post(fmt.Sprintf("%s://%s%ssearch", scheme, host, pathPrefix), "application/json", strings.NewReader(query))
	if err != nil {
		return br, err
	}
	if resp.StatusCode != 200 {
		return br, fmt.Errorf("search error: %v", resp.Status)
	}
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return br, err
	}
	var sr SearchResult
	if err := json.Unmarshal(data, &sr); err != nil {
		return br, err
	}
	if len(sr.Blobs) == 0 {
		return br, fmt.Errorf("full blobref for prefix %v not found", digestPrefix)
	}
	return sr.Blobs[0].Blob, nil
}

func (fic fileItemContainer) describe(pn blob.Ref) (*SearchResult, error) {
	query := fmt.Sprintf(`{"constraint":{"blobRefPrefix": "%s"},"describe":{"depth":1,"rules":[{"attrs":["camliContent","camliContentImage"]}]}}`, pn)

	resp, err := http.Post(fmt.Sprintf("%s://%s%ssearch", fic.scheme, fic.host, fic.pathPrefix), "application/json", strings.NewReader(query))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("search error: %v", resp.Status)
	}
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var sr SearchResult
	if err := json.Unmarshal(data, &sr); err != nil {
		return nil, err
	}
	return &sr, nil
}

func (fic fileItemContainer) getPeers(around blob.Ref, limit int) (*SearchResult, error) {
	// TODO(mpl): use types from search pkg instead of raw JSON if we ever import the search pkg.
	query := fmt.Sprintf(`{"sort":"-created","constraint":{"permanode":{"relation":{"relation": "parent", "any": {"blobRefPrefix": "%s"}}}},"describe":{"depth":1,"rules":[{"attrs":["camliContent","camliContentImage"]}]},"limit":%d, "around": "%s"}`, fic.parent, limit, around)

	resp, err := http.Post(fmt.Sprintf("%s://%s%ssearch", fic.scheme, fic.host, fic.pathPrefix), "application/json", strings.NewReader(query))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("search error: %v", resp.Status)
	}
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var sr SearchResult
	if err := json.Unmarshal(data, &sr); err != nil {
		return nil, err
	}
	return &sr, nil
}

func (fic *fileItemContainer) doPrev() {
	fic.mu.Lock()
	defer fic.mu.Unlock()
	if fic.prev == nil {
		return
	}
	if fic.currentIdx > 0 {
		fic.currentIdx--
		fic.current = fic.prev
	}
}

func (fic *fileItemContainer) doNext() {
	fic.mu.Lock()
	defer fic.mu.Unlock()
	if fic.next == nil {
		return
	}
	if fic.currentIdx < len(fic.items)-1 {
		fic.currentIdx++
		fic.current = fic.next
	}
}

// No need for locking because only caller is render.
func (fic *fileItemContainer) refreshTitle() {
	if fic.current == nil {
		return
	}
	// TODO(mpl): other title sources. or not? after all, we're a file
	// container, so should we use anything other than the file name (which
	// should always exist)? If/when the publisher supports anything other than
	// files, then reconsider (and probably make another, different container
	// anyway).
	jQuery("h1#title").SetText(html.EscapeString(fic.current.fileName))
}

// No need for locking because only caller is render.
func (fic *fileItemContainer) refreshLocation() {
	if fic.current == nil {
		return
	}
	if fic.isTopNode {
		return
	}
	hash := fic.current.pn.DigestPrefix(10)
	js.Global.Get("history").Call("pushState", hash, "", fic.basePath+"/h"+hash)
}

// No need for locking because only caller is render.
func (fic *fileItemContainer) updateItems(sr *SearchResult, from blob.Ref, prepend bool) {
	// TODO(mpl): "forget" old items so we don't grow fic.items indefinitely? (sliding window)
	if sr == nil {
		return
	}

	var newBlobs []*SearchResultBlob
	fromIdx := 0
	for k, v := range sr.Blobs {
		if v.Blob == from {
			fromIdx = k
			break
		}
	}
	if prepend {
		newBlobs = sr.Blobs[:fromIdx]
	} else {
		newBlobs = sr.Blobs[fromIdx+1:]
	}

	var newItems []*fileItem
	meta := sr.Describe.Meta
	for _, v := range newBlobs {
		desbr, ok := meta[v.Blob.String()]
		if !ok {
			continue
		}
		if desbr.Permanode == nil {
			continue
		}
		camliContent := desbr.Permanode.Attr.Get("camliContent")
		if camliContent == "" {
			continue
		}
		contentRef := blob.MustParse(camliContent)
		cdes := meta[contentRef.String()]
		if cdes == nil {
			continue
		}
		file := cdes.File
		if file == nil {
			continue
		}
		newItems = append(newItems, &fileItem{
			pn:         desbr.BlobRef,
			fileName:   file.FileName,
			size:       file.Size,
			mimeType:   file.MIMEType,
			isImage:    file.IsImage(),
			contentRef: contentRef,
		})
	}

	if prepend {
		if len(newItems) == 0 {
			// Nothing left to fetch at the beginning
			fic.beginningReached = true
			return
		}
		fic.items = append(newItems, fic.items...)
		fic.currentIdx += len(newItems)
		return
	}
	if len(newItems) == 0 {
		// Nothing left to fetch at the end
		fic.endReached = true
		return
	}
	fic.items = append(fic.items, newItems...)
}

// No need for locking because only caller is render.
func (fic *fileItemContainer) refreshNav() {
	fic.prev, fic.next = nil, nil
	fic.current = fic.items[fic.currentIdx]
	if fic.currentIdx > 0 {
		fic.prev = fic.items[fic.currentIdx-1]
	}
	if fic.currentIdx < len(fic.items)-1 {
		fic.next = fic.items[fic.currentIdx+1]
	}
	fit := fic.current

	// TODO(mpl): make sure the href='javascript:;' trick is ok. Also see if we can't
	// have a working href as a backup for users with no javascript?
	navDiv := fmt.Sprintf(`<div id='nav-%s' class='camlifile'></div>`, fit.pn)
	jQuery(ficDiv).Append(navDiv)
	if fic.prev != nil {
		prevNav := `[<a id='prev' href='javascript:;'>prev</a>]`
		jQuery(fmt.Sprintf("div#nav-%s", fit.pn)).Append(prevNav)
		jQuery("a#prev").Call(jquery.CLICK, func(e jquery.Event) {
			theFic.doPrev()
			go func() {
				theFic.render()
			}()
		})
	}
	if fic.next != nil {
		nextNav := `[<a id='next' href='javascript:;'>next</a>]`
		jQuery(fmt.Sprintf("div#nav-%s", fit.pn)).Append(nextNav)
		jQuery("a#next").Call(jquery.CLICK, func(e jquery.Event) {
			theFic.doNext()
			go func() {
				theFic.render()
			}()
		})
	}
}

// TODO(mpl): optimization: it might be interesting to let render return as soon
// as the actual rendering is done, and not have to wait for getPeers and
// updateItems. But it would make locking more complicated, so later.
func (fic *fileItemContainer) render() error {
	fic.mu.Lock()
	defer fic.mu.Unlock()
	if len(fic.items) == 0 {
		return nil
	}

	fit := fic.current

	needsUpdate := false
	prepend := false
	var around blob.Ref
	if !fic.endReached && fic.currentIdx+1 >= len(fic.items)-1 {
		needsUpdate = true
		around = fic.items[len(fic.items)-1].pn
	} else if !fic.beginningReached && fic.currentIdx <= 1 {
		needsUpdate = true
		prepend = true
		around = fic.items[0].pn
	}

	c := make(chan error)
	var sr *SearchResult
	go func() {
		var err error
		if needsUpdate {
			sr, err = fic.getPeers(around, 9)
		}
		c <- err
	}()

	// Do the main rendering work while waiting for getPeers
	fit.setThumb(fic)
	fit.setDownload(fic)
	jQuery(ficDiv).Empty()
	fic.refreshTitle()
	fic.refreshLocation()
	fit.render()

	err := <-c
	if err != nil {
		return fmt.Errorf("cannot get peers of %v: %v", fit.pn, err)
	}

	if needsUpdate {
		fic.updateItems(sr, around, prepend)
	}

	fic.refreshNav()
	return nil
}

func (fit *fileItem) setThumb(fic *fileItemContainer) {
	if !fit.isImage {
		fit.thumb = fmt.Sprintf("%s=s/file.png", fic.pathPrefix)
		return
	}
	if fic.isTopNode {
		fit.thumb = fmt.Sprintf("%s/h%s/=i/%s/?mw=%d&mh=%d", fic.basePath, fit.contentRef.DigestPrefix(10), url.QueryEscape(fit.fileName), maxThumbWidthRatio*fic.thumbHeight, fic.thumbHeight)
		return
	}
	fit.thumb = fmt.Sprintf("%s/h%s/h%s/=i/%s/?mw=%d&mh=%d", fic.basePath, fit.pn.DigestPrefix(10), fit.contentRef.DigestPrefix(10), url.QueryEscape(fit.fileName), maxThumbWidthRatio*fic.thumbHeight, fic.thumbHeight)
}

func (fit *fileItem) setDownload(fic *fileItemContainer) {
	if fic.isTopNode {
		fit.download = fmt.Sprintf("%s/h%s/=f/%s", fic.basePath, fit.contentRef.DigestPrefix(10), url.QueryEscape(fit.fileName))
		return
	}
	fit.download = fmt.Sprintf("%s/h%s/h%s/=f/%s", fic.basePath, fit.pn.DigestPrefix(10), fit.contentRef.DigestPrefix(10), url.QueryEscape(fit.fileName))
}

func (fit *fileItem) render() {
	fileInfo := fmt.Sprintf(`<div id='%s'>File: %s, %d bytes, type %s</div>`, fit.pn, html.EscapeString(fit.fileName), fit.size, fit.mimeType)
	jQuery(ficDiv).Append(fileInfo)
	anchor := fmt.Sprintf("<a id='%s' href='%s'><img src='%s'></a>", fit.pn, fit.download, fit.thumb)
	jQuery(ficDiv).Append(anchor)
	downloadDiv := fmt.Sprintf(`<div id='camli-%s' class='camlifile'>[<a href='%s'>download</a>]</div>`, fit.contentRef, fit.download)
	jQuery(ficDiv).Append(downloadDiv)
}
