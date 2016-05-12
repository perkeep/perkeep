/*
Copyright 2016 The Camlistore AUTHORS

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

package search

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/schema/nodeattr"
	"camlistore.org/pkg/types/camtypes"

	"golang.org/x/net/context"
)

// TODO: make these pluggable, e.g. registered from an importer or something?
// How will that work when they're out-of-process?

// altLocationRef maps camliNodeType to a slice of attributes
// whose values may refer to permanodes with location information.
var altLocationRef = map[string][]string{
	"foursquare.com:checkin": {"foursquareVenuePermanode"},
}

// getPermanodeLocation returns the location info for a permanode,
// from one of the following sources:
//  1. Permanode attributes "latitude" and "longitude"
//  2. Referenced permanode attributes (eg. for "foursquare.com:checkin"
//     its "foursquareVenuePermanode")
//  3. Location in permanode camliContent file metadata
// The sources are checked in this order, the location from
// the first source yielding a valid result is returned.
func (sh *Handler) getPermanodeLocation(ctx context.Context, permaNode blob.Ref,
	at time.Time) (camtypes.Location, error) {

	return sh.permanodeLocation(ctx, permaNode, at, true, nil)
}

func (sh *Handler) permanodeLocation(ctx context.Context,
	pn blob.Ref, at time.Time,
	fromContent bool,
	done map[string]struct{}) (loc camtypes.Location, err error) {

	pa := permAttr{
		sh: sh,
		pn: pn,
		at: at,
	}
	if sh.corpus == nil {
		var err error
		pa.claims, err = sh.index.AppendClaims(ctx, nil, pn, sh.owner, "")
		if err != nil {
			return camtypes.Location{}, err
		}
	}

	// Rule 1: if permanode has an explicit latitude and longitude,
	// then this is its location.
	slat, slong := pa.get(nodeattr.Latitude), pa.get(nodeattr.Longitude)
	if slat != "" && slong != "" {
		lat, latErr := strconv.ParseFloat(slat, 64)
		long, longErr := strconv.ParseFloat(slong, 64)
		switch {
		case latErr != nil:
			err = fmt.Errorf("invalid latitude in %v: %v", pn, latErr)
		case longErr != nil:
			err = fmt.Errorf("invalid longitude in %v: %v", pn, longErr)
		default:
			err = nil
		}
		return camtypes.Location{Latitude: lat, Longitude: long}, err
	}

	// Rule 2: referenced permanode attributes
	nodeType := pa.get(nodeattr.Type)
	if nodeType != "" {
		for _, a := range altLocationRef[nodeType] {
			refPn, hasRef := blob.Parse(pa.get(a))
			if !hasRef {
				continue
			}
			if done == nil {
				done = make(map[string]struct{})
			}
			if _, refDone := done[refPn.String()]; refDone {
				// circular reference
				continue
			}
			done[pn.String()] = struct{}{}
			loc, err = sh.permanodeLocation(ctx, refPn, at, false, done)
			if err == nil {
				return loc, err
			}
		}
	}

	// Rule 3: location in permanode camliContent file metadata.
	// Use this only if pn was the argument passed to sh.getPermanodeLocation,
	// and is not something found through a reference via altLocationRef.
	if fromContent {
		if content, ok := blob.Parse(pa.get(nodeattr.CamliContent)); ok {
			return sh.index.GetFileLocation(ctx, content)
		}
	}

	return camtypes.Location{}, os.ErrNotExist
}

// permAttr returns attributes of pn at the given time from the corpus
// or from the cached slice of indexed attribute claims when sh.corpus is nil.
type permAttr struct {
	sh *Handler
	pn blob.Ref
	at time.Time

	claims []camtypes.Claim // only used when sh.corpus is nil
}

func (a permAttr) get(attr string) string {
	if a.sh.corpus != nil {
		return a.sh.corpus.PermanodeAttrValue(a.pn, attr, a.at, a.sh.owner)
	}

	return index.ClaimsAttrValue(a.claims, attr, a.at, a.sh.owner)
}
