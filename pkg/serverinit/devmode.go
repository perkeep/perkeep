/*
Copyright 2014 The Perkeep Authors

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

package serverinit

import (
	"context"
	"fmt"
	"log"
	"strings"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/jsonsign/signhandler"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/server/app"
)

func (hl *handlerLoader) initPublisherRootNode(ah *app.Handler) error {
	return hl.initAppRootNode(ah, "publisher")
}

func (hl *handlerLoader) initWebDAVRootNode(ah *app.Handler) error {
	return hl.initAppRootNode(ah, "webdav")
}

// initAppRootNode creates a camliRoot permanode for an app if it doesn't exist.
// This is only done in dev mode.
func (hl *handlerLoader) initAppRootNode(ah *app.Handler, appName string) error {
	if !env.IsDev() {
		return nil
	}

	h, err := hl.GetHandler("/my-search/")
	if err != nil {
		return err
	}
	sh := h.(*search.Handler)
	camliRootQuery := func(camliRoot string) (*search.SearchResult, error) {
		return sh.Query(context.TODO(), &search.SearchQuery{
			Limit: 1,
			Constraint: &search.Constraint{
				Permanode: &search.PermanodeConstraint{
					Attr:  "camliRoot",
					Value: camliRoot,
				},
			},
		})
	}

	appConfig := ah.AppConfig()
	if appConfig == nil {
		return fmt.Errorf("%s app handler has no AppConfig", appName)
	}
	camliRoot, ok := appConfig["camliRoot"].(string)
	if !ok {
		// camliRoot not configured, skip initialization
		return nil
	}
	result, err := camliRootQuery(camliRoot)
	if err == nil && len(result.Blobs) > 0 && result.Blobs[0].Blob.Valid() {
		// root node found, nothing more to do.
		log.Printf("Found %v camliRoot node for %s: %v", camliRoot, appName, result.Blobs[0].Blob.String())
		return nil
	}

	log.Printf("No %v camliRoot node found, creating one from scratch now.", camliRoot)

	bs, err := hl.GetStorage("/bs-recv/")
	if err != nil {
		return err
	}
	h, err = hl.GetHandler("/sighelper/")
	if err != nil {
		return err
	}
	sigh := h.(*signhandler.Handler)

	ctx := context.TODO()
	signUpload := func(bb *schema.Builder) (blob.Ref, error) {
		signed, err := sigh.Sign(ctx, bb)
		if err != nil {
			return blob.Ref{}, fmt.Errorf("could not sign blob: %w", err)
		}
		br := blob.RefFromString(signed)
		if _, err := blobserver.Receive(ctx, bs, br, strings.NewReader(signed)); err != nil {
			return blob.Ref{}, fmt.Errorf("could not upload %v: %w", br.String(), err)
		}
		return br, nil
	}

	pn, err := signUpload(schema.NewUnsignedPermanode())
	if err != nil {
		return fmt.Errorf("could not create new camliRoot node: %w", err)
	}
	if _, err := signUpload(schema.NewSetAttributeClaim(pn, "camliRoot", camliRoot)); err != nil {
		return fmt.Errorf("could not set camliRoot on new node %v: %w", pn, err)
	}
	if _, err := signUpload(schema.NewSetAttributeClaim(pn, "title", fmt.Sprintf("Root node for %s (%s)", appName, camliRoot))); err != nil {
		return fmt.Errorf("could not set title on new node %v: %w", pn, err)
	}
	// Mark it as a directory for webdav
	if appName == "webdav" {
		if _, err := signUpload(schema.NewSetAttributeClaim(pn, "camliNodeType", "directory")); err != nil {
			return fmt.Errorf("could not set camliNodeType on new node %v: %w", pn, err)
		}
	}
	return nil
}
