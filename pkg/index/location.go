package index

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/types/camtypes"
)

// LocationHelper queries permanode locations.
//
// A LocationHelper is not safe for concurrent use.
// Callers should use Lock or RLock on the underlying index instead.
type LocationHelper struct {
	index  *Index
	corpus *Corpus // may be nil
}

// NewLocationHelper returns a new location handler
// that uses ix to query blob attributes.
func NewLocationHelper(ix *Index) *LocationHelper {
	lh := &LocationHelper{index: ix}
	if ix.corpus != nil {
		lh.corpus = ix.corpus
	}
	return lh
}

// SetCorpus sets the corpus to be used
// for location lookups.
func (lh *LocationHelper) SetCorpus(corpus *Corpus) {
	lh.corpus = corpus
}

// altLocationRef maps camliNodeType to a slice of attributes
// whose values may refer to permanodes with location information.
var altLocationRef = map[string][]string{
	// TODO(mpl): twitter.
	"foursquare.com:checkin": {"foursquareVenuePermanode"},
}

// signerInfo groups all the info about the different representations of a claim signer.
type signerInfo struct {
	signer     blob.Ref     // the ref of some blob representation of the GPG key ID (sha1, sha224, etc)
	allSigners signerRefSet // the whole set of equivalent blob signers (different hashes)
	signerID   string       // the signer GPG ID (e.g. 2931A67C26F5ABDA)
}

// PermanodeLocation returns the location info for a permanode,
// from one of the following sources:
//  1. Permanode attributes "latitude" and "longitude"
//  2. Referenced permanode attributes (eg. for "foursquare.com:checkin"
//     its "foursquareVenuePermanode")
//  3. Location in permanode camliContent file metadata
// The sources are checked in this order, the location from
// the first source yielding a valid result is returned.
func (lh *LocationHelper) PermanodeLocation(ctx context.Context, permaNode blob.Ref,
	at time.Time, signer blob.Ref) (camtypes.Location, error) {
	si := signerInfo{
		signer: signer,
	}
	if lh.corpus == nil || !signer.Valid() {
		return lh.permanodeLocation(ctx, permaNode, at, si, true)
	}
	signerID, ok := lh.corpus.keyId[signer]
	if ok {
		si.signerID = signerID
		si.allSigners = lh.corpus.signerRefs[signerID]
	}
	return lh.permanodeLocation(ctx, permaNode, at, si, true)
}

func (lh *LocationHelper) permanodeLocation(ctx context.Context,
	pn blob.Ref, at time.Time, si signerInfo,
	useRef bool) (loc camtypes.Location, err error) {

	pa := permAttr{at: at}
	var signerID string
	if lh.corpus != nil {
		if si.signer.Valid() {
			if si.signerID == "" || len(si.allSigners) < 1 {
				return camtypes.Location{}, os.ErrNotExist
			}
			signerID = si.signerID
			// pa.signerFilter will only get used if pa.attrs == nil below.
			pa.signerFilter = si.allSigners
		}
		var claims []*camtypes.Claim
		pa.attrs, claims = lh.corpus.permanodeAttrsOrClaims(pn, at, signerID)
		if claims != nil {
			pa.claims = claimPtrSlice(claims)
		}
	} else {
		var claims []camtypes.Claim
		claims, err = lh.index.AppendClaims(ctx, nil, pn, si.signer, "")
		if err != nil {
			return camtypes.Location{}, err
		}
		pa.claims = claimSlice(claims)
		// no need for pa.signerFilter because AppendClaims already filtered by signer.
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

	if useRef {
		// Rule 2: referenced permanode attributes
		nodeType := pa.get(nodeattr.Type)
		if nodeType != "" {
			for _, a := range altLocationRef[nodeType] {
				refPn, hasRef := blob.Parse(pa.get(a))
				if !hasRef {
					continue
				}
				loc, err = lh.permanodeLocation(ctx, refPn, at, si, false)
				if err == nil {
					return loc, err
				}
			}
		}

		// Rule 3: location in permanode camliContent file metadata.
		// Use this only if pn was the argument passed to sh.getPermanodeLocation,
		// and is not something found through a reference via altLocationRef.
		if content, ok := blob.Parse(pa.get(nodeattr.CamliContent)); ok {
			return lh.index.GetFileLocation(ctx, content)
		}
	}

	return camtypes.Location{}, os.ErrNotExist
}

// permAttr represents attributes of a permanode
// for a given owner at a given time, either
// from a Corpus or Index/Interface.
type permAttr struct {
	// permanode attributes from Corpus.PermanodeAttrs
	// This may be nil if corpus has no cache for the permanode
	// for the given time, in that case claims must be used.
	attrs map[string][]string

	// claims of permanode
	// Populated only when attrs is not valid; using AppendClaims
	// of corpus, or of the index in the absence of a corpus.
	// Both attrs and claims may be nil if the permanode
	// does not exist, or all attributes of it are from
	// signer(s) other than the signerFilter.
	claims claimsIntf

	at           time.Time
	signerFilter signerRefSet
}

// get returns the value of attr.
func (pa permAttr) get(attr string) string {
	if pa.attrs != nil {
		v := pa.attrs[attr]
		if len(v) != 0 {
			return v[0]
		}
		return ""
	}

	if pa.claims != nil {
		return claimsIntfAttrValue(pa.claims, attr, pa.at, pa.signerFilter)
	}

	return ""
}
