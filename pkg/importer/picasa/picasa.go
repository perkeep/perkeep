/*
Copyright 2014 The Camlistore Authors

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

// Package picasa is an importer for Picasa Web.
package picasa

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/importer"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/syncutil"

	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
	"camlistore.org/third_party/github.com/tgulacsi/picago"
)

var parallelWorkers = 4
var parallelAlbumRoutines = 4

func init() {
	importer.Register("picasa", newFromConfig)
}

func newFromConfig(cfg jsonconfig.Obj, host *importer.Host) (importer.Importer, error) {
	key := cfg.RequiredString("apiKey")
	err := cfg.Validate()
	if err != nil {
		return nil, err
	}
	chunks := strings.SplitN(key, ":", 2)
	if len(chunks) < 2 {
		return nil, fmt.Errorf("Picasa apiKey must be in the format cleintID:clientSecret (got %s)", key)
	}
	im := &imp{
		//clientID:     chunks[0],
		//clientSecret: chunks[1],
		host: host,
	}
	if im.transport, err = picago.NewTransport(chunks[0], chunks[1], im); err != nil {
		return nil, err
	}
	return im, nil
}

type imp struct {
	//clientID     string
	//clientSecret string
	sync.Mutex
	transport *oauth.Transport
	host      *importer.Host
}

func (im *imp) CanHandleURL(url string) bool { return false }
func (im *imp) ImportURL(url string) error   { panic("unused") }

func (im *imp) Prefix() string {
	cid := ""
	if im.transport != nil {
		cid = im.transport.Config.ClientId
	}
	return fmt.Sprintf("picasa:%s", cid)
}

func (im *imp) Run(ctx *context.Context) (err error) {
	log.Printf("Running picasa importer.")
	defer func() {
		log.Printf("picasa importer returned: %v", err)
	}()

	im.Lock()
	client := &http.Client{Transport: im.transport}
	im.Unlock()

	root, err := im.getRootNode()
	if err != nil {
		return err
	}
	itemch := make(chan imageFile)
	errch := make(chan error, parallelWorkers)
	tbd := make(chan imageFile)

	// For caching album name -> imported Object, to skip lookup by path
	// (Attr) as much as possible.
	var albumCacheMu sync.Mutex
	albumCache := make(map[string]*importer.Object)

	getParentObj := func(name, title string) *importer.Object {
		albumCacheMu.Lock()
		defer albumCacheMu.Unlock()
		parent, ok := albumCache[name]
		if ok {
			return parent
		}

		parent, err = im.getChildByPath(name)
		if err != nil {
			log.Printf("getParentObj(%s): %v", name, err)
		}
		if parent == nil {
			parent, err = root.ChildPathObject(name)
			if err != nil {
				log.Printf("error creating ChildPathObject(%s): %v", name, err)
				errch <- err
				parent = root
			}
		}
		albumCache[name] = parent
		if err = parent.SetAttrs("title", title, "tag", name); err != nil {
			errch <- err
		}
		return parent
	}

	var workers sync.WaitGroup
	worker := func() {
		for img := range tbd {
			parent := getParentObj(img.albumName, img.albumTitle)

			fn := img.albumName + "/" + img.fileName
			log.Printf("importing %s", fn)
			fileRef, err := schema.WriteFileFromReader(im.host.Target(), fn, img.r)
			img.r.Close()
			if err != nil {
				// FIXME(tgulacsi): cannot download movies
				log.Printf("error downloading %s: %v", img.fileName, err)
				continue
			}
			// parent will have an attr camliPath:img.fileName set to this permanode
			obj, err := parent.ChildPathObject(img.fileName)
			if err != nil {
				errch <- err
			}

			if err = obj.SetAttrs(
				"camliContent", fileRef.String(),
				"album", img.albumTitle,
				"tag", img.albumName,
			); err != nil {
				errch <- err
			}
		}
		workers.Done()
	}

	workers.Add(parallelWorkers)
	for i := 0; i < parallelWorkers; i++ {
		go worker()
	}

	// decide whether we should import this image
	filter := func(img imageFile) (bool, error) {
		intrErr := func(e error) error {
			if e != nil {
				return e
			}
			if ctx.IsCanceled() {
				return context.ErrCanceled
			}
			return nil
		}
		parent := getParentObj(img.albumName, img.albumTitle)
		if parent != nil {
			pn := parent.Attr("camliPath:" + img.fileName)
			if pn != "" {
				ref, ok := blob.Parse(pn)
				if !ok {
					return true, fmt.Errorf("cannot parse %s as blobRef", pn)
				}
				obj, err := im.host.ObjectFromRef(ref)
				if err != nil {
					return false, err
				}
				if obj != nil {
					log.Printf("%s/%s already imported as %s.",
						img.albumName, img.fileName, obj.PermanodeRef())
					return false, intrErr(nil)
				}
			}
		}
		return true, intrErr(nil)
	}

	go iterItems(itemch, errch, filter, client, "default")
	for {
		select {
		case err = <-errch:
			close(tbd)
			if err == context.ErrCanceled {
				log.Printf("Picasa importer has been interrupted.")
			} else {
				log.Printf("Picasa importer error: %v", err)
				workers.Wait()
			}
			return err
		case <-ctx.Done():
			log.Printf("Picasa importer has been interrupted.")
			close(tbd)
			return context.ErrCanceled
		case img := <-itemch:
			tbd <- img
		}
	}
	close(tbd)
	workers.Wait()
	return nil
}

func (im *imp) getRootNode() (*importer.Object, error) {
	root, err := im.host.RootObject()
	if err != nil {
		return nil, err
	}

	if root.Attr("title") == "" {
		//FIXME(tgulacsi): we need the username, from somewhere
		title := fmt.Sprintf("Picasa (%s)", "default")
		if err := root.SetAttr("title", title); err != nil {
			return nil, err
		}
	}
	return root, nil
}

// getChildByPath searches for attribute camliPath:path and returns the object
// to which this permanode points.
// This is the reverse of imp.ChildPathObject.
func (im *imp) getChildByPath(path string) (obj *importer.Object, err error) {
	key := "camliPath:" + path
	defer func() {
		log.Printf("search for %s resulted in %v/%v", path, obj, err)
	}()
	res, e := im.host.Search().GetPermanodesWithAttr(&search.WithAttrRequest{
		N:    2, // only expect 1
		Attr: key,
	})
	log.Printf("searching for %s: %v, %v", key, res, e)
	if e != nil {
		err = e
		log.Printf("getChildByPath searching GetPermanodesWithAttr: %v", err)
		return nil, err
	}
	if len(res.WithAttr) == 0 {
		return nil, nil
	}
	if len(res.WithAttr) > 1 {
		err = fmt.Errorf("Found %d import roots for %q; want 1", len(res.WithAttr), path)
		return nil, err
	}
	pn := res.WithAttr[0].Permanode
	parent, e := im.host.ObjectFromRef(pn)
	if e != nil {
		err = e
		return nil, err
	}
	br := parent.Attr(key)
	pn, ok := blob.Parse(br)
	if !ok {
		err = fmt.Errorf("cannot parse %s (value of %s.%s) as blobRef",
			br, parent, key)
		return nil, err
	}
	obj, err = im.host.ObjectFromRef(pn)
	return obj, err
}

type imageFile struct {
	albumTitle, albumName string
	fileName              string
	ID                    string
	r                     io.ReadCloser
}

type filterFunc func(imageFile) (bool, error)

func iterItems(itemch chan<- imageFile, errch chan<- error,
	filter filterFunc, client *http.Client, username string) {

	defer close(itemch)

	albums, err := picago.GetAlbums(client, username)
	if err != nil {
		errch <- err
		return
	}
	gate := syncutil.NewGate(parallelAlbumRoutines)
	for _, album := range albums {
		photos, err := picago.GetPhotos(client, username, album.ID)
		if err != nil {
			select {
			case errch <- err:
			default:
				return
			}
			continue
		}
		gate.Start()
		go func(albumName, albumTitle string) {
			defer gate.Done()
			for _, photo := range photos {
				img := imageFile{
					albumTitle: albumTitle,
					albumName:  albumName,
					fileName:   photo.Filename(),
					ID:         photo.ID,
				}
				ok, err := filter(img)
				if err != nil {
					errch <- err
					return
				}
				if !ok {
					continue
				}

				img.r, err = picago.DownloadPhoto(client, photo.URL)
				if err != nil {
					select {
					case errch <- fmt.Errorf("Get(%s): %v", photo.URL, err):
					default:
						return
					}
					continue
				}
				itemch <- img
			}
		}(album.Name, album.Title)
	}
}
