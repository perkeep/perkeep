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
	"strings"
	"sync"

	"perkeep.org/pkg/blob"

	"github.com/gopherjs/gopherjs/js"
	"github.com/gopherjs/jquery"
)

const (
	bicMembersList = "ul#members" // jquery matching convenience
	// TODO(mpl): derive precisely how much blobItemHeight should be from thumbHeight + various margins in css
	thumbnailHeight    = 200
	maxThumbWidthRatio = 3   // maximum thumb width is height*maxThumbWidthRatio
	blobItemHeight     = 250 // thumbHeight + some slack
	getMembersLimit    = 50  // how many more items to fetch in search queries
)

var theBic *blobItemContainer

func RenderMembers() {
	var err error
	theBic, err = newblobItemContainer(thumbnailHeight)
	if err != nil {
		fmt.Printf("error creating members container: %v\n", err)
		return
	}
	go func() {
		// Both populate and render need to be in a goroutine because
		// they wait on http requests or channels, which is not allowed
		// directly in javascript callbacks.
		if err := theBic.populate(); err != nil {
			fmt.Printf("error populating members: %v\n", err)
			return
		}
		if err := theBic.render(); err != nil {
			fmt.Printf("members rendering error: %v\n", err)
			return
		}

		go func() {
			// This is for when we need to force a render. e.g. when
			// there are more items to display, but we can't trigger a scroll
			// event because there are fewer items displayed than the size
			// of the window.
			for {
				<-theBic.c
				if theBic.isRendering() {
					continue
				}
				if err := theBic.render(); err != nil {
					fmt.Printf("members rendering error: %v\n", err)
				}
			}
		}()

		// recompute and redraw on each scroll event
		jQuery(js.Global).Scroll(func(e jquery.Event) {
			go func() {
				// isRendering is not atomic with render, so it
				// could happen that even if isRendering returns
				// false, it becomes true before render is called.
				// But that's ok because the first thing render
				// does is locking renderingMu anyway. IsRendering
				// is basically just an optimization to return
				// earlier.
				if theBic.isRendering() {
					return
				}
				if err := theBic.render(); err != nil {
					fmt.Printf("members rendering error: %v\n", err)
					return
				}
			}()
		})
	}()
}

type blobItemContainer struct {
	// the whole blobItemContainer does need to be locked during render.
	// However, we instead rely on locking this one variable, which allows us
	// to terminate early all rendering attempts if there's already one going
	// on, instead of letting them pile up.
	renderingMu sync.RWMutex
	rendering   bool // are we already rendering right now?

	pn     blob.Ref // the permanode that contains the members/items
	host   string
	scheme string
	// path to the current container. can be root level, or a child container. e.g.:
	// /pics/foo/-
	// /pics/foo/-/h341b133369
	basePath      string
	pathPrefix    string        // app handler's prefix if it applies, e.g. "/pics/", or "/" otherwise.
	items         []*blobItem   // all the items (members) we can display
	continueToken string        // for search queries
	c             chan struct{} // signal to force a render

	visibleHeight int // window height at last render
	visibleWidth  int // window width at last render
	thumbHeight   int // 200 for now
}

func newblobItemContainer(thumbHeight int) (*blobItemContainer, error) {
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
	basePath = strings.TrimSuffix(basePath, "/")
	if !strings.Contains(basePath, "/-") {
		// root level
		basePath += "/-"
	}
	pn, err := subject()
	if err != nil {
		return nil, err
	}
	prefix, err := pathPrefix()
	if err != nil {
		return nil, err
	}

	return &blobItemContainer{
		thumbHeight: thumbHeight,
		basePath:    basePath,
		pathPrefix:  prefix,
		pn:          pn,
		c:           make(chan struct{}, 1),
		scheme:      scheme,
		host:        host,
	}, nil
}

func (bic *blobItemContainer) populate() error {
	if bic == nil {
		return fmt.Errorf("nil blob container")
	}

	// technically we had all the info already in camliPageMeta EXCEPT that
	// we don't have the order of the members. So we still need to do the query
	// ourselves if we want to display them in the correct order.
	// I'd like to offload most of the querying done in ../main.go and do it client side anyway.
	items, cont, err := bic.getMembers()
	if err != nil {
		return err
	}
	bic.continueToken = cont
	bic.items = items
	return nil
}

// TODO(mpl): it's a bit awkward that that getMembers returns items and cont,
// that are eventually going to be used to set bic.items, and bic.cont, but we
// don't want to set them immediately. And getMembers needs bic.parent,
// bic.continueToken, bic.host, and bic.scheme, which would be too many parameters.
// Try and do better later? Maybe do like getPeers + updateItems?

func (bic blobItemContainer) getMembers() (items []*blobItem, cont string, err error) {
	query := fmt.Sprintf(`{"sort":"-created","constraint":{"permanode":{"relation":{"relation": "parent", "any": {"blobRefPrefix": "%s"}}}},"describe":{"depth":1,"rules":[{"attrs":["camliContent","camliContentImage"]}]},"limit":%d,"continue":"%s"}`, bic.pn, getMembersLimit, bic.continueToken)

	resp, err := http.Post(fmt.Sprintf("%s://%s%ssearch", bic.scheme, bic.host, bic.pathPrefix), "application/json", strings.NewReader(query))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("search error: %v", resp.Status)
	}
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	var sr SearchResult
	if err := json.Unmarshal(data, &sr); err != nil {
		return nil, "", err
	}

	meta := sr.Describe.Meta
	for _, v := range sr.Blobs {
		desbr, ok := meta[v.Blob.String()]
		if !ok {
			continue
		}
		if desbr.Permanode == nil {
			continue
		}
		item := &blobItem{
			pn:      desbr.BlobRef,
			pnTitle: desbr.Permanode.Attr.Get("title"),
		}
		camliContent := desbr.Permanode.Attr.Get("camliContent")
		if camliContent == "" {
			items = append(items, item)
			continue
		}
		contentRef := blob.MustParse(camliContent)
		item.contentRef = contentRef
		cdes, ok := meta[contentRef.String()]
		if !ok {
			items = append(items, item)
			continue
		}
		if cdes.Dir != nil {
			item.fileName = cdes.Dir.FileName
			item.isDir = true
			items = append(items, item)
			continue
		}
		if cdes.File != nil {
			item.fileName = cdes.File.FileName
			item.isImage = cdes.File.IsImage()
		}
		items = append(items, item)
	}

	return items, sr.Continue, nil
}

func (bic blobItemContainer) isRendering() bool {
	bic.renderingMu.RLock()
	defer bic.renderingMu.RUnlock()
	return bic.rendering
}

func (bic *blobItemContainer) render() error {
	bic.renderingMu.Lock()
	bic.rendering = true
	bic.renderingMu.Unlock()
	defer func() {
		bic.renderingMu.Lock()
		bic.rendering = false
		bic.renderingMu.Unlock()
	}()
	if len(bic.items) == 0 {
		return nil
	}

	scrollTop := jQuery(js.Global).ScrollTop()
	visibleHeight := jQuery(js.Global).Height()
	visibleWidth := jQuery(js.Global).Width()
	// rows that would fit in the screen.
	nbVisibleRows := visibleHeight/blobItemHeight + 1

	if visibleHeight != bic.visibleHeight || visibleWidth != bic.visibleWidth {
		// window dimensions changed -> full rerender
		jQuery(bicMembersList).Empty()
		for _, it := range bic.items {
			it.rendered = false
		}
	}
	bic.visibleHeight, bic.visibleWidth = visibleHeight, visibleWidth

	// refetchTrigger is the sentinel for when we need to fetch more items
	refetchTrigger := len(bic.items)
	// continue token will be set to "" by search pkg when there's no more results to fetch
	if bic.continueToken != "" {
		// gross approx of the number of items that fit in a screen. assume square items.
		nbVisibleCols := visibleWidth / blobItemHeight
		itemsInAScreen := nbVisibleCols * nbVisibleRows
		refetchTrigger -= itemsInAScreen
		if refetchTrigger < itemsInAScreen {
			// if we've got fewer items than a window size
			refetchTrigger = len(bic.items) - 1
		}
	}

	currentRow := -1
	currentPos := 0
	bottomRow := -1
	var moreItems []*blobItem
	var fetchingMore bool
	errc := make(chan error)
	for idx, it := range bic.items {
		if idx == refetchTrigger {
			fetchingMore = true
			// fetch in a goroutine so we can finish some rendering work in the meantime
			go func() {
				newItems, cont, err := bic.getMembers()
				if err != nil {
					errc <- err
					return
				}
				moreItems = newItems
				bic.continueToken = cont
				errc <- nil
			}()
		}
		if it.rendered {
			currentRow = it.row
			currentPos = it.pos
			if it.pos+blobItemHeight > scrollTop && bottomRow == -1 {
				bottomRow = it.row + nbVisibleRows
			}
			if bottomRow >= 0 && it.row >= bottomRow {
				break
			}
			continue
		}
		it.render(bic.pathPrefix, bic.basePath, bic.thumbHeight)
		coords := jQuery(fmt.Sprintf("li#camli-%s", it.pn)).Offset()
		it.pos = coords.Top
		if it.pos != currentPos {
			currentRow++
		}
		currentPos = it.pos
		it.row = currentRow

		if it.pos+blobItemHeight > scrollTop && bottomRow == -1 {
			bottomRow = it.row + nbVisibleRows
		}
		if bottomRow >= 0 && it.row >= bottomRow {
			break
		}
	}
	if !fetchingMore {
		return nil
	}
	if err := <-errc; err != nil {
		return err
	}
	if len(moreItems) > 0 {
		bic.items = append(bic.items, moreItems...)
		bic.c <- struct{}{}
	}
	return nil
}

type blobItem struct {
	pn         blob.Ref
	contentRef blob.Ref
	thumb      string
	fileName   string
	pnTitle    string
	isImage    bool
	isDir      bool

	rendered bool // already added to the DOM?
	pos      int  // absolute pos in pixels, from top of the page.
	row      int  // absolute row number, from top of the page.
}

func (bi *blobItem) title() string {
	// TODO(mpl): if we import "search", there's probably already a method
	// to give us the title, given a searchResult.
	if bi.pnTitle != "" {
		return bi.pnTitle
	}
	if bi.fileName != "" {
		return html.EscapeString(bi.fileName)
	}
	return bi.pn.DigestPrefix(10)
}

func (bi *blobItem) thumbnail(pathPrefix, basePath string, thumbHeight int) string {
	if bi.thumb != "" {
		return bi.thumb
	}
	switch {
	case bi.isImage:
		// TODO(mpl): should we/can we prefetch the image?
		bi.thumb = fmt.Sprintf("%s/h%s/h%s/=i/%s/?mw=%d&mh=%d", basePath, bi.pn.DigestPrefix(10), bi.contentRef.DigestPrefix(10), url.QueryEscape(bi.fileName), maxThumbWidthRatio*thumbHeight, thumbHeight)
	case bi.isDir:
		bi.thumb = fmt.Sprintf("%s=s/folder.png", pathPrefix)
	case bi.fileName != "": // isFile, but not image
		bi.thumb = fmt.Sprintf("%s=s/file.png", pathPrefix)
	default: // Any other permanode
		bi.thumb = fmt.Sprintf("%s=s/folder.png", pathPrefix)
	}
	return bi.thumb
}

func (it *blobItem) render(pathPrefix, basePath string, thumbHeight int) {
	li := fmt.Sprintf(`<li id='camli-%s' style="height: %dpx;"></li>`, it.pn, blobItemHeight)
	anchor := fmt.Sprintf("<a id='%s' href='%s/h%s'></a>", it.pn, basePath, it.pn.DigestPrefix(10))
	img := fmt.Sprintf(`<img id='%s' src="%s" height=%dpx>`, it.pn, it.thumbnail(pathPrefix, basePath, thumbHeight), thumbHeight)
	span := fmt.Sprintf("<span id='%s'>%s</span>", it.pn, it.title())
	jQuery(bicMembersList).Append(li)
	jQuery(fmt.Sprintf("li#camli-%s", it.pn)).Append(anchor)
	jQuery(fmt.Sprintf("a#%s", it.pn)).Append(img).Append(span)
	it.rendered = true
}
