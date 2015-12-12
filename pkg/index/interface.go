package index

import (
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/types/camtypes"
	"golang.org/x/net/context"
)

type Interface interface {
	// os.ErrNotExist should be returned if the blob isn't known
	GetBlobMeta(blob.Ref) (camtypes.BlobMeta, error)

	// Should return os.ErrNotExist if not found.
	GetFileInfo(fileRef blob.Ref) (camtypes.FileInfo, error)

	// Should return os.ErrNotExist if not found.
	GetImageInfo(fileRef blob.Ref) (camtypes.ImageInfo, error)

	// Should return os.ErrNotExist if not found.
	GetMediaTags(fileRef blob.Ref) (map[string]string, error)

	// KeyId returns the GPG keyid (e.g. "2931A67C26F5ABDA)
	// given the blobref of its ASCII-armored blobref.
	// The error is ErrNotFound if not found.
	KeyId(blob.Ref) (string, error)

	// AppendClaims appends to dst claims on the given permanode.
	// The signerFilter and attrFilter are both optional.  If non-zero,
	// they filter the return items to only claims made by the given signer
	// or claims about the given attribute, respectively.
	// Deleted claims are never returned.
	// The items may be appended in any order.
	//
	// TODO: this should take a context and a callback func
	// instead of a dst, then it can append to a channel instead,
	// and the context lets it be interrupted. The callback should
	// take the context too, so the channel send's select can read
	// from the Done channel.
	AppendClaims(dst []camtypes.Claim, permaNode blob.Ref,
		signerFilter blob.Ref,
		attrFilter string) ([]camtypes.Claim, error)

	// TODO(bradfitz): methods below this line are slated for a redesign
	// to work efficiently for the new in-memory index.

	// dest must be closed, even when returning an error.
	// limit <= 0 means unlimited.
	GetRecentPermanodes(dest chan<- camtypes.RecentPermanode,
		owner blob.Ref,
		limit int,
		before time.Time) error

	// SearchPermanodes finds permanodes matching the provided
	// request and sends unique permanode blobrefs to dest.
	// In particular, if request.FuzzyMatch is true, a fulltext
	// search is performed (if supported by the attribute(s))
	// instead of an exact match search.
	// If request.Query is blank, the permanodes which have
	// request.Attribute as an attribute (regardless of its value)
	// are searched.
	// Additionally, if request.Attribute is blank, all attributes
	// are searched (as fulltext), otherwise the search is
	// restricted  to the named attribute.
	//
	// dest is always closed, regardless of the error return value.
	SearchPermanodesWithAttr(dest chan<- blob.Ref,
		request *camtypes.PermanodeByAttrRequest) error

	// ExistingFileSchemas returns 0 or more blobrefs of "bytes"
	// (TODO(bradfitz): or file?) schema blobs that represent the
	// bytes of a file given in bytesRef.  The file schema blobs
	// returned are not guaranteed to reference chunks that still
	// exist on the blobservers, though.  It's purely a hint for
	// clients to avoid uploads if possible.  Before re-using any
	// returned blobref they should be checked.
	//
	// Use case: a user drag & drops a large file onto their
	// browser to upload.  (imagine that "large" means anything
	// larger than a blobserver's max blob size) JavaScript can
	// first SHA-1 the large file locally, then send the
	// wholeFileRef to this call and see if they'd previously
	// uploaded the same file in the past.  If so, the upload
	// can be avoided if at least one of the returned schemaRefs
	// can be validated (with a validating HEAD request) to still
	// all exist on the blob server.
	ExistingFileSchemas(wholeFileRef blob.Ref) (schemaRefs []blob.Ref, err error)

	// GetDirMembers sends on dest the children of the static
	// directory dirRef. It returns os.ErrNotExist if dirRef
	// is nil.
	// dest must be closed, even when returning an error.
	// limit <= 0 means unlimited.
	GetDirMembers(dirRef blob.Ref, dest chan<- blob.Ref, limit int) error

	// Given an owner key, a camliType 'claim', 'attribute' name,
	// and specific 'value', find the most recent permanode that has
	// a corresponding 'set-attribute' claim attached.
	// Returns os.ErrNotExist if none is found.
	// Only attributes white-listed by IsIndexedAttribute are valid.
	// TODO(bradfitz): ErrNotExist here is a weird error message ("file" not found). change.
	// TODO(bradfitz): use keyId instead of signer?
	PermanodeOfSignerAttrValue(signer blob.Ref, attr, val string) (blob.Ref, error)

	// PathsOfSignerTarget queries the index about "camliPath:"
	// URL-dispatch attributes.
	//
	// It returns a list of all the path claims that have been signed
	// by the provided signer and point at the given target.
	//
	// This is used when editing a permanode, to figure work up
	// the name resolution tree backwards ultimately to a
	// camliRoot permanode (which should know its base URL), and
	// then the complete URL(s) of a target can be found.
	PathsOfSignerTarget(signer, target blob.Ref) ([]*camtypes.Path, error)

	// All Path claims for (signer, base, suffix)
	PathsLookup(signer, base blob.Ref, suffix string) ([]*camtypes.Path, error)

	// Most recent Path claim for (signer, base, suffix) as of
	// provided time 'at', or most recent if 'at' is nil.
	PathLookup(signer, base blob.Ref, suffix string, at time.Time) (*camtypes.Path, error)

	// EdgesTo finds references to the provided ref.
	//
	// For instance, if ref is a permanode, it might find the parent permanodes
	// that have ref as a member.
	// Or, if ref is a static file, it might find static directories which contain
	// that file.
	// This is a way to go "up" or "back" in a hierarchy.
	//
	// opts may be nil to accept the defaults.
	EdgesTo(ref blob.Ref, opts *camtypes.EdgesToOpts) ([]*camtypes.Edge, error)

	// EnumerateBlobMeta sends ch information about all blobs
	// known to the indexer (which may be a subset of all total
	// blobs, since the indexer is typically configured to not see
	// non-metadata blobs) and then closes ch.  When it returns an
	// error, it also closes ch. The blobs may be sent in any order.
	// If the context finishes, the return error is ctx.Err().
	EnumerateBlobMeta(context.Context, chan<- camtypes.BlobMeta) error
}
