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
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/schema/nodeattr"
	"camlistore.org/pkg/search"
)

const (
	mediaObjectKind = "MediaObject"
	userInfoKind    = "UserInfo"
	documentKind    = "Document"
)

func (h *handler) searchScans(limit int) (*search.SearchResult, error) {
	q := &search.SearchQuery{
		Limit: limit,
		Constraint: &search.Constraint{
			Logical: &search.LogicalConstraint{
				Op: "and",
				A: &search.Constraint{Permanode: &search.PermanodeConstraint{
					SkipHidden: true,
					Attr:       nodeattr.Type,
					Value:      scanNodeType,
				}},
				B: &search.Constraint{Logical: &search.LogicalConstraint{
					Op: "not",
					A: &search.Constraint{Permanode: &search.PermanodeConstraint{
						SkipHidden: true,
						Attr:       "document",
						ValueMatches: &search.StringConstraint{
							ByteLength: &search.IntConstraint{
								Min: 1,
							},
						},
					}},
				}},
			},
		},
		Describe: &search.DescribeRequest{
			Depth: 1,
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliContent"},
				},
			},
		},
	}

	res, err := h.sh.Query(q)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (h *handler) searchScan(pn blob.Ref) (*search.SearchResult, error) {
	q := &search.SearchQuery{
		Constraint: &search.Constraint{
			Logical: &search.LogicalConstraint{
				Op: "and",
				A: &search.Constraint{Permanode: &search.PermanodeConstraint{
					SkipHidden: true,
					Attr:       nodeattr.Type,
					Value:      scanNodeType,
				}},
				B: &search.Constraint{
					BlobRefPrefix: pn.String(),
				},
			},
		},
		Describe: &search.DescribeRequest{
			Depth: 1,
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliContent"},
				},
			},
		},
	}
	res, err := h.sh.Query(q)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (h *handler) searchScanByContent(contentRef blob.Ref) (*search.SearchResult, error) {
	q := &search.SearchQuery{
		Constraint: &search.Constraint{
			Logical: &search.LogicalConstraint{
				Op: "and",
				A: &search.Constraint{Permanode: &search.PermanodeConstraint{
					SkipHidden: true,
					Attr:       nodeattr.Type,
					Value:      scanNodeType,
				}},
				B: &search.Constraint{Permanode: &search.PermanodeConstraint{
					SkipHidden: true,
					Attr:       "camliContent",
					Value:      contentRef.String(),
				}},
			},
		},
		Describe: &search.DescribeRequest{
			Depth: 1,
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliContent"},
				},
			},
		},
	}
	res, err := h.sh.Query(q)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (h *handler) describeScan(b *search.DescribedBlob) (mediaObject, error) {
	var scan mediaObject
	attrs := b.Permanode.Attr
	creationTime, err := time.Parse(time.RFC3339, attrs.Get(nodeattr.DateCreated)) // TODO: or types.Time3339 ?
	if err != nil {
		return scan, err
	}
	var document blob.Ref
	documentRef := attrs.Get("document")
	if documentRef != "" {
		var ok bool
		document, ok = blob.Parse(documentRef)
		if ok {
			// TODO(mpl): more to be done here ? Do we ever want to display something about the document of a scan ?
		}
	}
	content, ok := blob.Parse(attrs.Get("camliContent"))
	if !ok {
		return scan, fmt.Errorf("scan permanode has invalid content blobref: %q", attrs.Get("camliContent"))
	}
	return mediaObject{
		permanode:   b.BlobRef,
		contentRef:  content,
		creation:    creationTime,
		documentRef: document,
	}, nil
}

func (h *handler) fetchScans(limit int) ([]mediaObject, error) {
	res, err := h.searchScans(limit)
	if err != nil {
		return nil, err
	}
	if len(res.Blobs) == 0 {
		return nil, nil
	}
	if res.Describe == nil || len(res.Describe.Meta) == 0 {
		return nil, errors.New("scan permanodes were not described")
	}
	scans := make([]mediaObject, 0)
	for _, sbr := range res.Blobs {
		br := sbr.Blob
		des, ok := res.Describe.Meta[br.String()]
		if !ok || des == nil || des.Permanode == nil {
			continue
		}
		scan, err := h.describeScan(des)
		if err != nil {
			return nil, fmt.Errorf("error describing scan %v: %v", br, err)
		}
		scans = append(scans, scan)
	}
	return scans, nil
}

// returns os.ErrNotExist when scan was not found
func (h *handler) fetchScanByContent(contentRef blob.Ref) (mediaObject, error) {
	return h.fetchScanFunc(contentRef, h.searchScanByContent)
}

// returns os.ErrNotExist when scan was not found
func (h *handler) fetchScan(pn blob.Ref) (mediaObject, error) {
	return h.fetchScanFunc(pn, h.searchScan)
}

// returns os.ErrNotExist when scan was not found
func (h *handler) fetchScanFunc(br blob.Ref, searchFunc func(blob.Ref) (*search.SearchResult, error)) (mediaObject, error) {
	var mo mediaObject
	res, err := searchFunc(br)
	if err != nil {
		return mo, err
	}
	if len(res.Blobs) != 1 {
		return mo, os.ErrNotExist
	}
	if res.Describe == nil || len(res.Describe.Meta) == 0 {
		return mo, errors.New("scan permanode was not described")
	}
	for _, des := range res.Describe.Meta {
		if des.Permanode == nil {
			continue
		}
		scan, err := h.describeScan(des)
		if err != nil {
			return mo, fmt.Errorf("error describing scan %v: %v", des.BlobRef, err)
		}
		return scan, nil
	}
	return mo, os.ErrNotExist
}

// TODO(mpl): move that to client pkg, with a good API ?

func (h *handler) signAndSend(json string) error {
	// TODO(mpl): sign things ourselves if we can.
	scl, err := h.cl.Sign(h.server, strings.NewReader("json="+json))
	if err != nil {
		return fmt.Errorf("could not get signed claim %v: %v", json, err)
	}
	if _, err := h.cl.Upload(client.NewUploadHandleFromString(string(scl))); err != nil {
		return fmt.Errorf("could not upload signed claim %v: %v", json, err)
	}
	return nil
}

func (h *handler) setAttribute(pn blob.Ref, attr, val string) error {
	ucl, err := schema.NewSetAttributeClaim(pn, attr, val).SetSigner(h.signer).JSON()
	if err != nil {
		return fmt.Errorf("could not create claim to set %v:%v on %v: %v", attr, val, pn, err)
	}
	return h.signAndSend(ucl)
}

func (h *handler) delAttribute(pn blob.Ref, attr, val string) error {
	ucl, err := schema.NewDelAttributeClaim(pn, attr, val).SetSigner(h.signer).JSON()
	if err != nil {
		return fmt.Errorf("could not create claim to delete %v:%v on %v: %v", attr, val, pn, err)
	}
	return h.signAndSend(ucl)
}

func (h *handler) addAttribute(pn blob.Ref, attr, val string) error {
	ucl, err := schema.NewAddAttributeClaim(pn, attr, val).SetSigner(h.signer).JSON()
	if err != nil {
		return fmt.Errorf("could not create claim to add %v:%v on %v: %v", attr, val, pn, err)
	}
	return h.signAndSend(ucl)
}

func (h *handler) deleteNode(node blob.Ref) error {
	ucl, err := schema.NewDeleteClaim(node).SetSigner(h.signer).JSON()
	if err != nil {
		return fmt.Errorf("could not create delete claim for %v: %v", node, err)
	}
	return h.signAndSend(ucl)
}

// TODO(mpl): move that to client pkg, with a good API ?

func (h *handler) newPermanode() (blob.Ref, error) {
	// TODO(mpl): sign things ourselves if we can.
	var pn blob.Ref
	upn, err := schema.NewUnsignedPermanode().SetSigner(h.signer).JSON()
	if err != nil {
		return pn, fmt.Errorf("could not create unsigned permanode: %v", err)
	}
	spn, err := h.cl.Sign(h.server, strings.NewReader("json="+upn))
	if err != nil {
		return pn, fmt.Errorf("could not get signed permanode: %v", err)
	}
	sbr, err := h.cl.Upload(client.NewUploadHandleFromString(string(spn)))
	if err != nil {
		return pn, fmt.Errorf("could not upload permanode: %v", err)
	}
	return sbr.BlobRef, nil
}

func (h *handler) createScan(mo mediaObject) (blob.Ref, error) {
	pn, err := h.newPermanode()
	if err != nil {
		return pn, fmt.Errorf("could not create scan: %v", err)
	}

	// make it a scan
	if err := h.setAttribute(pn, nodeattr.Type, scanNodeType); err != nil {
		return pn, fmt.Errorf("could not set %v as a scan: %v", pn, err)
	}

	// give it content
	if err := h.setAttribute(pn, nodeattr.CamliContent, mo.contentRef.String()); err != nil {
		return pn, fmt.Errorf("could not set content for scan %v: %v", pn, err)
	}

	// set creationTime
	if err := h.setAttribute(pn, nodeattr.DateCreated, mo.creation.UTC().Format(time.RFC3339)); err != nil {
		return pn, fmt.Errorf("could not set creationTime for scan %v: %v", pn, err)
	}

	return pn, nil
}

// old is an optimization, in case the caller already had fetched the old scan.
// If nil, the current scan will be fetched for comparison with new.
func (h *handler) updateScan(pn blob.Ref, new, old *mediaObject) error {
	if old == nil {
		mo, err := h.fetchScan(pn)
		if err != nil {
			return fmt.Errorf("scan %v not found: %v", pn, err)
		}
		old = &mo
	}
	if new.contentRef.Valid() && old.contentRef != new.contentRef {
		if err := h.setAttribute(pn, "camliContent", new.contentRef.String()); err != nil {
			return fmt.Errorf("could not set contentRef for scan %v: %v", pn, err)
		}
	}
	if new.documentRef.Valid() && old.documentRef != new.documentRef {
		if err := h.setAttribute(pn, "document", new.documentRef.String()); err != nil {
			return fmt.Errorf("could not set documentRef for scan %v: %v", pn, err)
		}
	}
	if !old.creation.Equal(new.creation) {
		if new.creation.IsZero() {
			if err := h.delAttribute(pn, nodeattr.DateCreated, ""); err != nil {
				return fmt.Errorf("could not delete creation date for scan %v: %v", pn, err)
			}
		} else {
			if err := h.setAttribute(pn, nodeattr.DateCreated, new.creation.UTC().Format(time.RFC3339)); err != nil {
				return fmt.Errorf("could not set creation date for scan %v: %v", pn, err)
			}
		}
	}
	return nil
}

func (h *handler) updateDocument(pn blob.Ref, new *document) error {
	old, err := h.fetchDocument(pn)
	if err != nil {
		return fmt.Errorf("document %v not found: %v", pn, err)
	}
	if old.physicalLocation != new.physicalLocation {
		if err := h.setAttribute(pn, nodeattr.LocationText, new.physicalLocation); err != nil {
			return fmt.Errorf("could not set physicalLocation for document %v: %v", pn, err)
		}
	}

	if old.title != new.title {
		if err := h.setAttribute(pn, nodeattr.Title, new.title); err != nil {
			return fmt.Errorf("could not set title for document %v: %v", pn, err)
		}
	}

	if !old.docDate.Equal(new.docDate) {
		if new.docDate.IsZero() {
			if err := h.delAttribute(pn, nodeattr.StartDate, ""); err != nil {
				return fmt.Errorf("could not delete document date for document %v: %v", pn, err)
			}
		} else {
			if err := h.setAttribute(pn, nodeattr.StartDate, new.docDate.UTC().Format(time.RFC3339)); err != nil {
				return fmt.Errorf("could not set document date for document %v: %v", pn, err)
			}
		}
	}

	if !old.dueDate.Equal(new.dueDate) {
		if new.dueDate.IsZero() {
			if err := h.delAttribute(pn, nodeattr.PaymentDueDate, ""); err != nil {
				return fmt.Errorf("could not delete due date for document %v: %v", pn, err)
			}
		} else {
			if err := h.setAttribute(pn, nodeattr.PaymentDueDate, new.dueDate.UTC().Format(time.RFC3339)); err != nil {
				return fmt.Errorf("could not set due date for document %v: %v", pn, err)
			}
		}
	}

	if !old.tags.equal(new.tags) {
		if err := h.updateTags(pn, old.tags, new.tags); err != nil {
			return fmt.Errorf("could not update tags for document %v: %v", pn, err)
		}
	}
	return nil
}

func (h *handler) updateTags(pn blob.Ref, old, new separatedString) error {
	// first, delete the ones that are supposed to be gone
	for _, o := range old {
		found := false
		for _, n := range new {
			if o == n {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if err := h.delAttribute(pn, "tag", o); err != nil {
			return fmt.Errorf("could not delete tag %v: %v", o, err)
		}
	}
	// then, add the ones that previously didn't  exist
	for _, n := range new {
		found := false
		for _, o := range old {
			if o == n {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if err := h.addAttribute(pn, "tag", n); err != nil {
			return fmt.Errorf("could not add tag %v: %v", n, err)
		}
	}
	return nil
}

func (h *handler) createDocument(doc document) (blob.Ref, error) {
	pn, err := h.newPermanode()
	if err != nil {
		return pn, fmt.Errorf("could not create document: %v", err)
	}

	// make it a document
	if err := h.setAttribute(pn, nodeattr.Type, documentNodeType); err != nil {
		return pn, fmt.Errorf("could not set %v as a document: %v", pn, err)
	}

	// set creationTime
	if err := h.setAttribute(pn, nodeattr.DateCreated, doc.creation.UTC().Format(time.RFC3339)); err != nil {
		return pn, fmt.Errorf("could not set creationTime for document %v: %v", pn, err)
	}

	// set its pages
	// TODO(mpl,bradfitz): camliPath vs camliMember vs camliPathOrder vs something else ?
	// https://groups.google.com/d/msg/camlistore/xApHFjJKn3M/9Q5BfNbbptkJ
	for pageNumber, pageRef := range doc.pages {
		if err := h.setAttribute(pn, fmt.Sprintf("camliPath:%d", pageNumber), pageRef.String()); err != nil {
			return pn, fmt.Errorf("could not set document %v page %d to scan %q: %v", doc.permanode, pageNumber, pageRef, err)
		}
	}

	return pn, nil
}

// persistDocAndPages creates a new Document struct that represents
// the given mediaObject structs and stores it in the datastore, updates each of
// these mediaObject in the datastore with references back to the new Document struct
// and returns the key to the new document entity
func (h *handler) persistDocAndPages(newDoc document) (blob.Ref, error) {
	br := blob.Ref{}
	pn, err := h.createDocument(newDoc)
	if err != nil {
		return br, err
	}
	for _, page := range newDoc.pages {
		if err := h.setAttribute(page, "document", pn.String()); err != nil {
			return br, fmt.Errorf("could not update scan %v with %v:%v", page, "document", pn)

		}
	}
	return pn, nil
}

func (h *handler) searchDocument(pn blob.Ref) (*search.SearchResult, error) {
	q := &search.SearchQuery{
		Constraint: &search.Constraint{
			Logical: &search.LogicalConstraint{
				Op: "and",
				A: &search.Constraint{Permanode: &search.PermanodeConstraint{
					SkipHidden: true,
					Attr:       nodeattr.Type,
					Value:      documentNodeType,
				}},
				B: &search.Constraint{
					BlobRefPrefix: pn.String(),
				},
			},
		},
		Describe: &search.DescribeRequest{},
	}
	res, err := h.sh.Query(q)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (h *handler) searchDocumentByPages(pages []blob.Ref) (*search.SearchResult, error) {
	B := &search.Constraint{Permanode: &search.PermanodeConstraint{
		SkipHidden: true,
		Attr:       nodeattr.Type,
		Value:      documentNodeType,
	}}
	for i, page := range pages {
		B = &search.Constraint{Logical: &search.LogicalConstraint{
			Op: "and",
			A:  B,
			B: &search.Constraint{Permanode: &search.PermanodeConstraint{
				SkipHidden: true,
				Attr:       fmt.Sprintf("camliPath:%d", i),
				Value:      page.String(),
			}},
		}}
	}

	q := &search.SearchQuery{
		Constraint: B,
		Describe:   &search.DescribeRequest{},
	}
	res, err := h.sh.Query(q)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (h *handler) searchDocuments(limit int, opts searchOpts) (*search.SearchResult, error) {
	constraint := &search.Constraint{
		Permanode: &search.PermanodeConstraint{
			SkipHidden: true,
			Attr:       nodeattr.Type,
			Value:      documentNodeType,
		},
	}
	tags := opts.tags
	sort := search.CreatedDesc
	switch {
	case len(tags) > 0:
		B := &search.Constraint{
			Permanode: &search.PermanodeConstraint{
				Attr:       "tag",
				SkipHidden: true,
				Value:      tags[0],
			},
		}
		for _, tag := range tags[1:] {
			B = &search.Constraint{
				Logical: &search.LogicalConstraint{
					Op: "or",
					A:  B,
					B: &search.Constraint{
						Permanode: &search.PermanodeConstraint{
							Attr:       "tag",
							SkipHidden: true,
							Value:      tag,
						},
					},
				},
			}
		}
		constraint = &search.Constraint{
			Logical: &search.LogicalConstraint{
				Op: "and",
				A:  constraint,
				B:  B,
			},
		}
	case opts.due:
		sort = search.CreatedAsc
		// TODO(mpl): having added nodeattr.PaymentDueDate at the top of the list for what
		// "counts" as a creation time in the corpus, we're getting the Due Documents results
		// sorted for free, without apparently disturbing anything else (here or in the rest of
		// Camlistore in general). But I feel like we're getting lucky. For example, if
		// somewhere we specifically wanted the list of documents strictly sorted by their
		// creation date, we couldn't have it because any document with a due date would use it
		// for the sort instead of its creation date. Anyway, I think sometime we'll have to
		// make something server-side that allows attribute-defined sorting.
		constraint = &search.Constraint{
			Logical: &search.LogicalConstraint{
				Op: "and",
				A:  constraint,
				// We can't just use a TimeConstraint, because it would also match for any other
				// permanode time (startDate, dateCreated, etc).
				B: &search.Constraint{
					Permanode: &search.PermanodeConstraint{
						Attr:       nodeattr.PaymentDueDate,
						SkipHidden: true,
						ValueMatches: &search.StringConstraint{
							ByteLength: &search.IntConstraint{
								Min: 1,
							},
						},
					},
				},
			},
		}
	case opts.untagged:
		constraint = &search.Constraint{
			Logical: &search.LogicalConstraint{
				Op: "and",
				A:  constraint,
				B: &search.Constraint{
					// Note: we can't just match the Empty string constraint for the tag attribute,
					// because we actually want to match the absence of any tag attribute, hence below.
					Logical: &search.LogicalConstraint{
						Op: "not",
						A: &search.Constraint{
							Permanode: &search.PermanodeConstraint{
								Attr:       "tag",
								SkipHidden: true,
								ValueMatches: &search.StringConstraint{
									ByteLength: &search.IntConstraint{
										Min: 1,
									},
								},
							},
						},
					},
				},
			},
		}
	case opts.tagged:
		constraint = &search.Constraint{
			Logical: &search.LogicalConstraint{
				Op: "and",
				A:  constraint,
				B: &search.Constraint{
					Permanode: &search.PermanodeConstraint{
						Attr:       "tag",
						SkipHidden: true,
						ValueMatches: &search.StringConstraint{
							ByteLength: &search.IntConstraint{
								Min: 1,
							},
						},
					},
				},
			},
		}
	}
	q := &search.SearchQuery{
		Limit:      limit,
		Constraint: constraint,
		Describe:   &search.DescribeRequest{},
		Sort:       sort,
	}

	res, err := h.sh.Query(q)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// any one of them mutually exclusive will all the others
type searchOpts struct {
	tags     separatedString
	due      bool
	untagged bool
	tagged   bool
}

func (h *handler) fetchDocuments(limit int, opts searchOpts) ([]*document, error) {
	res, err := h.searchDocuments(limit, opts)
	if err != nil {
		return nil, err
	}
	if len(res.Blobs) == 0 {
		return nil, nil
	}
	if res.Describe == nil || len(res.Describe.Meta) == 0 {
		return nil, errors.New("documents permanodes were not described")
	}
	documents := make([]*document, 0)
	for _, sbr := range res.Blobs {
		br := sbr.Blob
		des, ok := res.Describe.Meta[br.String()]
		if !ok || des == nil || des.Permanode == nil {
			continue
		}
		doc, err := h.describeDocument(des)
		if err != nil {
			return nil, fmt.Errorf("error describing document %v: %v", br, err)
		}
		documents = append(documents, doc)
	}
	return documents, nil
}

func (h *handler) fetchTags() (map[string]int, error) {
	// TODO(mpl): Cache this result before returning it, since we only need to wipe
	// it out when a document adds or removes a tag.
	docs, err := h.fetchDocuments(-1, searchOpts{tagged: true})
	if err != nil {
		return nil, fmt.Errorf("could not fetch all tagged documents: %v", err)
	}
	// Dedupe tags
	count := make(map[string]int)
	for _, doc := range docs {
		for _, tag := range doc.tags {
			count[tag]++
		}
	}
	return count, nil
}

// returns os.ErrNotExist when document was not found
func (h *handler) fetchDocumentByPages(pages []blob.Ref) (*document, error) {
	return h.fetchDocumentFunc(pages, h.searchDocumentByPages)
}

// returns os.ErrNotExist when document was not found
func (h *handler) fetchDocument(pn blob.Ref) (*document, error) {
	return h.fetchDocumentFunc([]blob.Ref{pn}, func(blobs []blob.Ref) (*search.SearchResult, error) {
		return h.searchDocument(blobs[0])
	})
}

// returns os.ErrNotExist when document was not found
func (h *handler) fetchDocumentFunc(blobs []blob.Ref,
	searchFunc func([]blob.Ref) (*search.SearchResult, error)) (*document, error) {
	res, err := searchFunc(blobs)
	if err != nil {
		return nil, err
	}
	if len(res.Blobs) < 1 {
		return nil, os.ErrNotExist
	}
	if res.Describe == nil || len(res.Describe.Meta) == 0 {
		return nil, errors.New("document permanode was not described")
	}
	for _, des := range res.Describe.Meta {
		if des.Permanode == nil {
			continue
		}
		doc, err := h.describeDocument(des)
		if err != nil {
			return nil, fmt.Errorf("error describing document %v: %v", des.BlobRef, err)
		}
		return doc, nil
	}
	return nil, os.ErrNotExist
}

// dateOrZero parses datestr with the given format and returns the resulting
// time and error. An empty datestr is not an error and yields a zero time. format
// defaults to time.RFC3339 if empty.
func dateOrZero(datestr, format string) (time.Time, error) {
	if datestr == "" {
		return time.Time{}, nil
	}
	if format == "" {
		format = time.RFC3339
	}
	return time.Parse(format, datestr)
}

type numberedPage struct {
	nb   int
	page blob.Ref
}

type numberedPages []numberedPage

func (np numberedPages) Len() int           { return len(np) }
func (np numberedPages) Swap(i, j int)      { np[i], np[j] = np[j], np[i] }
func (np numberedPages) Less(i, j int) bool { return np[i].nb < np[j].nb }

func (h *handler) describeDocument(b *search.DescribedBlob) (*document, error) {
	var sortedPages numberedPages
	for key, val := range b.Permanode.Attr {
		if strings.HasPrefix(key, "camliPath:") {
			pageNumber, err := strconv.ParseInt(strings.TrimPrefix(key, "camliPath:"), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid page number %q in document %v: %v", key, b.BlobRef, err)
			}
			pageRef := val[0]
			br, ok := blob.Parse(pageRef)
			if !ok {
				return nil, fmt.Errorf("invalid blobref %q for page %d of document %v", pageRef, pageNumber, b.BlobRef)
			}
			sortedPages = append(sortedPages, numberedPage{
				nb:   int(pageNumber),
				page: br,
			})
		}
	}
	sort.Sort(sortedPages)
	pages := make([]blob.Ref, len(sortedPages))
	for i, v := range sortedPages {
		pages[i] = v.page
	}
	creationTime, err := time.Parse(time.RFC3339, b.Permanode.Attr.Get(nodeattr.DateCreated)) // TODO: or types.Time3339 ?
	if err != nil {
		return nil, err
	}
	docDate, err := dateOrZero(b.Permanode.Attr.Get(nodeattr.StartDate), "") // TODO: or types.Time3339 ?
	if err != nil {
		return nil, err
	}
	dueDate, err := dateOrZero(b.Permanode.Attr.Get(nodeattr.PaymentDueDate), "") // TODO: or types.Time3339 ?
	if err != nil {
		return nil, err
	}
	return &document{
		pages:            pages,
		permanode:        b.BlobRef,
		docDate:          docDate,
		creation:         creationTime,
		title:            b.Permanode.Attr.Get(nodeattr.Title),
		tags:             newSeparatedString(strings.Join(b.Permanode.Attr["tag"], ",")),
		physicalLocation: b.Permanode.Attr.Get(nodeattr.LocationText),
		dueDate:          dueDate,
	}, nil
}

// breakAndDeleteDoc deletes the given document struct and marks all of its
// associated mediaObject as not being part of a document
func (h *handler) breakAndDeleteDoc(docRef blob.Ref) error {
	doc, err := h.fetchDocument(docRef)
	if err != nil {
		return fmt.Errorf("document %v not found: %v", docRef, err)
	}
	if err := h.deleteNode(docRef); err != nil {
		return fmt.Errorf("could not delete document %v: %v", docRef, err)
	}
	for _, page := range doc.pages {
		if err := h.delAttribute(page, "document", ""); err != nil {
			return fmt.Errorf("could not unset document of scan %v: %v", page, err)
		}
	}
	return nil
}

// deleteDocAndImages deletes the given document struct and marks all of its
// associated mediaObjects as not being part of a document
func (h *handler) deleteDocAndImages(docRef blob.Ref) error {
	doc, err := h.fetchDocument(docRef)
	if err != nil {
		return fmt.Errorf("document %v not found: %v", docRef, err)
	}
	if err := h.deleteNode(docRef); err != nil {
		return fmt.Errorf("could not delete document %v: %v", docRef, err)
	}
	for _, page := range doc.pages {
		if err := h.deleteNode(page); err != nil {
			return fmt.Errorf("could not delete scan %v: %v", page, err)
		}
	}
	return nil
}
