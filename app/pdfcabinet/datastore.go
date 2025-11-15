/*
Copyright 2017 The Perkeep Authors.

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
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/search"
)

func (h *handler) signAndSend(ctx context.Context, json string) error {
	// TODO(mpl): sign things ourselves if we can.
	scl, err := h.cl.Sign(ctx, h.server, strings.NewReader("json="+json))
	if err != nil {
		return fmt.Errorf("could not get signed claim %v: %v", json, err)
	}
	if _, err := h.cl.Upload(ctx, client.NewUploadHandleFromString(string(scl))); err != nil {
		return fmt.Errorf("could not upload signed claim %v: %v", json, err)
	}
	return nil
}

func (h *handler) setAttribute(ctx context.Context, pn blob.Ref, attr, val string) error {
	ucl, err := schema.NewSetAttributeClaim(pn, attr, val).SetSigner(h.signer).JSON()
	if err != nil {
		return fmt.Errorf("could not create claim to set %v:%v on %v: %v", attr, val, pn, err)
	}
	return h.signAndSend(ctx, ucl)
}

func (h *handler) delAttribute(ctx context.Context, pn blob.Ref, attr, val string) error {
	ucl, err := schema.NewDelAttributeClaim(pn, attr, val).SetSigner(h.signer).JSON()
	if err != nil {
		return fmt.Errorf("could not create claim to delete %v:%v on %v: %v", attr, val, pn, err)
	}
	return h.signAndSend(ctx, ucl)
}

func (h *handler) addAttribute(ctx context.Context, pn blob.Ref, attr, val string) error {
	ucl, err := schema.NewAddAttributeClaim(pn, attr, val).SetSigner(h.signer).JSON()
	if err != nil {
		return fmt.Errorf("could not create claim to add %v:%v on %v: %v", attr, val, pn, err)
	}
	return h.signAndSend(ctx, ucl)
}

func (h *handler) deleteNode(ctx context.Context, node blob.Ref) error {
	ucl, err := schema.NewDeleteClaim(node).SetSigner(h.signer).JSON()
	if err != nil {
		return fmt.Errorf("could not create delete claim for %v: %v", node, err)
	}
	return h.signAndSend(ctx, ucl)
}

// TODO(mpl): move that to client pkg, with a good API ?

func (h *handler) newPermanode() (blob.Ref, error) {
	// TODO(mpl): sign things ourselves if we can.
	var pn blob.Ref
	upn, err := schema.NewUnsignedPermanode().SetSigner(h.signer).JSON()
	if err != nil {
		return pn, fmt.Errorf("could not create unsigned permanode: %v", err)
	}
	spn, err := h.cl.Sign(context.TODO(), h.server, strings.NewReader("json="+upn))
	if err != nil {
		return pn, fmt.Errorf("could not get signed permanode: %v", err)
	}
	sbr, err := h.cl.Upload(context.TODO(), client.NewUploadHandleFromString(string(spn)))
	if err != nil {
		return pn, fmt.Errorf("could not upload permanode: %v", err)
	}
	return sbr.BlobRef, nil
}

func (h *handler) updateDocument(ctx context.Context, pn blob.Ref, new *document) error {
	old, err := h.fetchDocument(pn)
	if err != nil {
		return fmt.Errorf("document %v not found: %v", pn, err)
	}

	if old.title != new.title {
		if err := h.setAttribute(ctx, pn, nodeattr.Title, new.title); err != nil {
			return fmt.Errorf("could not set title for document %v: %v", pn, err)
		}
	}

	if !old.docDate.Equal(new.docDate) {
		if new.docDate.IsZero() {
			if err := h.delAttribute(ctx, pn, nodeattr.StartDate, ""); err != nil {
				return fmt.Errorf("could not delete document date for document %v: %v", pn, err)
			}
		} else {
			if err := h.setAttribute(ctx, pn, nodeattr.StartDate, new.docDate.UTC().Format(time.RFC3339)); err != nil {
				return fmt.Errorf("could not set document date for document %v: %v", pn, err)
			}
		}
	}

	if !old.dueDate.Equal(new.dueDate) {
		if new.dueDate.IsZero() {
			if err := h.delAttribute(ctx, pn, nodeattr.PaymentDueDate, ""); err != nil {
				return fmt.Errorf("could not delete due date for document %v: %v", pn, err)
			}
		} else {
			if err := h.setAttribute(ctx, pn, nodeattr.PaymentDueDate, new.dueDate.UTC().Format(time.RFC3339)); err != nil {
				return fmt.Errorf("could not set due date for document %v: %v", pn, err)
			}
		}
	}

	if !old.tags.equal(new.tags) {
		if err := h.updateTags(ctx, pn, old.tags, new.tags); err != nil {
			return fmt.Errorf("could not update tags for document %v: %v", pn, err)
		}
	}
	return nil
}

func (h *handler) updateTags(ctx context.Context, pn blob.Ref, old, new separatedString) error {
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
		if err := h.delAttribute(ctx, pn, "tag", o); err != nil {
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
		if err := h.addAttribute(ctx, pn, "tag", n); err != nil {
			return fmt.Errorf("could not add tag %v: %v", n, err)
		}
	}
	return nil
}

func (h *handler) createDocument(ctx context.Context, doc document) (blob.Ref, error) {
	pn, err := h.newPermanode()
	if err != nil {
		return pn, fmt.Errorf("could not create document: %v", err)
	}

	// make it a document
	if err := h.setAttribute(ctx, pn, nodeattr.Type, documentNodeType); err != nil {
		return pn, fmt.Errorf("could not set %v as a document: %v", pn, err)
	}

	// set creationTime
	if err := h.setAttribute(ctx, pn, nodeattr.DateCreated, doc.creation.UTC().Format(time.RFC3339)); err != nil {
		return pn, fmt.Errorf("could not set creationTime for document %v: %v", pn, err)
	}

	if err := h.setAttribute(ctx, pn, nodeattr.CamliContent, doc.pdf.String()); err != nil {
		return pn, fmt.Errorf("could not set camliContent for document %v: %v", pn, err)
	}

	if err := h.setAttribute(ctx, pn, nodeattr.Title, doc.title); err != nil {
		return pn, fmt.Errorf("could not set title for document %v: %v", pn, err)
	}

	return pn, nil
}

// persistDocAndPdf creates a new Document struct that represents
// the given pdfObject struct and stores it in the datastore,
// the pdfObject in the datastore with a reference back to the new Document struct
// and returns the key to the new document entity
func (h *handler) persistDocAndPdf(ctx context.Context, newDoc document) (blob.Ref, error) {
	br := blob.Ref{}
	pn, err := h.createDocument(ctx, newDoc)
	if err != nil {
		return br, err
	}
	return pn, nil
}

func (h *handler) searchDocument(ctx context.Context, pn blob.Ref) (*search.SearchResult, error) {
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
	res, err := h.sh.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (h *handler) searchDocumentByPDF(pdf blob.Ref) (*search.SearchResult, error) {
	B := &search.Constraint{Permanode: &search.PermanodeConstraint{
		SkipHidden: true,
		Attr:       nodeattr.Type,
		Value:      documentNodeType,
	}}
	B = &search.Constraint{Logical: &search.LogicalConstraint{
		Op: "and",
		A:  B,
		B: &search.Constraint{Permanode: &search.PermanodeConstraint{
			SkipHidden: true,
			Attr:       nodeattr.CamliContent,
			Value:      pdf.String(),
		}},
	}}

	q := &search.SearchQuery{
		Constraint: B,
		Describe:   &search.DescribeRequest{},
	}
	res, err := h.sh.Query(context.TODO(), q)
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
		// Perkeep in general). But I feel like we're getting lucky. For example, if
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

	res, err := h.sh.Query(context.TODO(), q)
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
func (h *handler) fetchDocumentByPDF(pdf blob.Ref) (*document, error) {
	return h.fetchDocumentFunc(pdf, h.searchDocumentByPDF)
}

// returns os.ErrNotExist when document was not found
func (h *handler) fetchDocument(pn blob.Ref) (*document, error) {
	return h.fetchDocumentFunc(pn, func(blob blob.Ref) (*search.SearchResult, error) {
		return h.searchDocument(context.TODO(), blob)
	})
}

// returns os.ErrNotExist when document was not found
func (h *handler) fetchDocumentFunc(blobs blob.Ref,
	searchFunc func(blob.Ref) (*search.SearchResult, error)) (*document, error) {
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

func (h *handler) describeDocument(b *search.DescribedBlob) (*document, error) {
	pdfRef := b.Permanode.Attr.Get(nodeattr.CamliContent)
	pdf, ok := blob.Parse(pdfRef)
	if !ok {
		return nil, fmt.Errorf("invalid blobref %q for camliContent of document %v", pdfRef, b.BlobRef)
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
		pdf:       pdf,
		permanode: b.BlobRef,
		docDate:   docDate,
		creation:  creationTime,
		title:     b.Permanode.Attr.Get(nodeattr.Title),
		tags:      newSeparatedString(strings.Join(b.Permanode.Attr["tag"], ",")),
		dueDate:   dueDate,
	}, nil
}

// breakAndDeleteDoc deletes the given document struct and marks its
// associated pdfObject as not being part of a document
func (h *handler) breakAndDeleteDoc(ctx context.Context, docRef blob.Ref) error {
	doc, err := h.fetchDocument(docRef)
	if err != nil {
		return fmt.Errorf("document %v not found: %v", docRef, err)
	}
	if err := h.deleteNode(ctx, docRef); err != nil {
		return fmt.Errorf("could not delete document %v: %v", docRef, err)
	}
	if err := h.delAttribute(ctx, doc.pdf, "document", ""); err != nil {
		return fmt.Errorf("could not unset document of pdf %v: %v", doc.pdf, err)
	}

	return nil
}

// deleteDocAndImages deletes the given document struct and marks all of its
// associated pdfObjects as not being part of a document
func (h *handler) deleteDocAndPDF(ctx context.Context, docRef blob.Ref) error {
	doc, err := h.fetchDocument(docRef)
	if err != nil {
		return fmt.Errorf("document %v not found: %v", docRef, err)
	}
	if err := h.deleteNode(ctx, docRef); err != nil {
		return fmt.Errorf("could not delete document %v: %v", docRef, err)
	}
	if err := h.deleteNode(ctx, doc.pdf); err != nil {
		return fmt.Errorf("could not delete pdf %v: %v", doc.pdf, err)
	}
	return nil
}
