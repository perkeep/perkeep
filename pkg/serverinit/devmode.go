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

package serverinit

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/env"
	"camlistore.org/pkg/jsonsign/signhandler"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/server/app"
)

func (hl *handlerLoader) initPublisherRootNode(ah *app.Handler) error {
	if !env.IsDev() {
		return nil
	}

	h, err := hl.GetHandler("/my-search/")
	if err != nil {
		return err
	}
	sh := h.(*search.Handler)
	camliRootQuery := func(camliRoot string) (*search.SearchResult, error) {
		return sh.Query(&search.SearchQuery{
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
		return errors.New("publisher app handler has no AppConfig")
	}
	camliRoot, ok := appConfig["camliRoot"].(string)
	if !ok {
		return fmt.Errorf("camliRoot in publisher app handler appConfig is %T, want string", appConfig["camliRoot"])
	}
	result, err := camliRootQuery(camliRoot)
	if err == nil && len(result.Blobs) > 0 && result.Blobs[0].Blob.Valid() {
		// root node found, nothing more to do.
		log.Printf("Found %v camliRoot node for publisher: %v", camliRoot, result.Blobs[0].Blob.String())
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

	signUpload := func(bb *schema.Builder) (blob.Ref, error) {
		signed, err := sigh.Sign(bb)
		if err != nil {
			return blob.Ref{}, fmt.Errorf("could not sign blob: %v", err)
		}
		br := blob.SHA1FromString(signed)
		if _, err := blobserver.Receive(bs, br, strings.NewReader(signed)); err != nil {
			return blob.Ref{}, fmt.Errorf("could not upload %v: %v", br.String(), err)
		}
		return br, nil
	}

	pn, err := signUpload(schema.NewUnsignedPermanode())
	if err != nil {
		return fmt.Errorf("could not create new camliRoot node: %v", err)
	}
	if _, err := signUpload(schema.NewSetAttributeClaim(pn, "camliRoot", camliRoot)); err != nil {
		return fmt.Errorf("could not set camliRoot on new node %v: %v", pn, err)
	}
	if _, err := signUpload(schema.NewSetAttributeClaim(pn, "title", "Publish root node for "+camliRoot)); err != nil {
		return fmt.Errorf("could not set camliRoot on new node %v: %v", pn, err)
	}
	return nil
}
