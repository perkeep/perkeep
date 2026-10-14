# Permanodes

Permanodes are how Perkeep models mutable data on top of an immutable,
content-addressable datastore. The data is modeled using nodes with two
camliTypes: `permanode` and `claim`.

## Permanode

A permanode is an anchor from which you build mutable objects.  To serve as a
reliable (consistently nameable) object, it must have no mutable state itself.
In fact, a permanode is really just a [signed](../json-signing/) random number.

    {"camliVersion": 1,
     "camliType": "permanode",

     // Required.  Any random string, to force the digest of this
     // node to be unique.  Note that the date in the ASCII-armored
     // GPG JSON signature will already help it be unique, so this
     // doesn't need to be a great random.
     "random": "615e05c68c8411df81a2001b639d041f"

    <REQUIRED-JSON-SIGNATURE>}

## Claim

A claim is any signed JSON schema blob. One common use is modifying
"attributes" on a permanode. The state of a permanode is the result
of combining all attribute-modifying claims which reference it, in
order. Claim nodes look something like this:


    {"camliVersion": 1,
     "camliType": "claim",
     "camliSigner": "....",
     "claimDate": "2010-07-10T17:20:03.9212Z", // redundant with data in ascii armored "camliSig",
                                               // but required. more legible. takes precedence over
                                               // any date inferred from camliSig
     "permaNode": "sha1-xxxxxxx",        // what is being modified
     "claimType": "set-attribute",
     "attribute": "camliContent",
     "value": "sha1-yyyyyyy",
     "camliSig": .........}

All claims must be [signed](../json-signing/).

The anagrammatical property `claimType` defines what the claim does, and is one
of the following:

* `add-attribute`: adds a value to a multi-valued attribute (e.g. "tag")

* `set-attribute`: set a single-valued attribute. equivalent to "del-attribute"
  of "attribute" and then add-attribute.

* `del-attribute`: deletes all values of "attribute", if no "value" given, or
  just the provided "value" if multi-valued

* `multi`: atomically do multiple add/set/del from above on potentially
  different permanodes (see [issue 110](https://perkeep.org/issue/110)):

        {"camliVersion": 1,
         "camliType": "claim",
         "claimType": "multi",
         "claimDate": "2013-02-24T17:20:03.9212Z",
         "claims": [
              {"claimType": "set-attribute",
               "permaNode": "sha1-xxxxxx",
               "attribute": "foo",
               "value": "fooValue"},
              {"claimType": "add-attribute",
               "permaNode": "sha1-yyyyy",
               "attribute": "tag",
               "value": "funny"}
         ],
         "camliSig": .........}

### Multi claims

A `multi` claim contains a `claims` array. Each entry must be an
`add-attribute`, `set-attribute`, or `del-attribute` operation. `share`, `delete`,
and nested `multi` operations are not allowed.

The enclosing claim supplies the `claimDate`, `camliSigner`, and `camliSig`.
These fields are prohibited in child entries. The signature covers the entire
envelope, including the array and any default `permaNode`.

Each entry may specify its own non-empty `permaNode`, overriding a default
`permaNode` on the enclosing claim. If a child's `permaNode` is absent or empty,
it uses the enclosing value. Every child must resolve to a non-empty, valid
permanode reference; an empty child value with no non-empty enclosing value
is invalid. Entries may modify the same permanode or different permanodes.

Entries are applied in array order, from first to last, using the enclosing
claim's `claimDate` for every entry.

Separate claim blobs with the same `claimDate` are ordered by the enclosing
blob reference's `Less` ordering. Children of one `multi` claim remain
contiguous in that order, retaining their array order.

For multiple mutations on one permanode, the enclosing claim can supply the
target once:

    {"camliVersion": 1,
     "camliType": "claim",
     "claimType": "multi",
     "permaNode": "sha1-xxxxxx",
     "claimDate": "2013-02-24T17:20:03.9212Z",
     "claims": [
          {"claimType": "set-attribute",
           "attribute": "title",
           "value": "Example"},
          {"claimType": "add-attribute",
           "attribute": "tag",
           "value": "funny"}
     ],
     <REQUIRED-JSON-SIGNATURE>}

Validation is all-or-nothing: if any child is invalid, none of the child
mutations are indexed or applied. This does not prevent storing the original
blob. If any required dependency is missing, indexing the entire batch is
deferred until all dependencies arrive; no child is applied early.

Dependency requirements follow the same rules as ordinary non-multi
attribute claims, using each child's resolved `permaNode` and the enclosing
signer. Multi claims do not introduce dependencies on blobs referenced by
attribute values. Any dependency required by those ordinary rules must be
available before the batch is applied.

Visibility is atomic across all affected permanodes:
one search or describe request sees either all of the batch's mutations
or none. Separate requests need not share a snapshot.

A delete claim can target the enclosing multi-claim blob, excluding all its
child mutations from state reconstruction. Children cannot be deleted
individually. Deleting that delete claim restores the envelope's eligibility
under the ordinary claim-deletion rules.

An empty array is a valid no-op: it has no mutations to apply.

#### Implementation considerations

The enclosing blob reference identifies the signed claim and is sufficient
for fetching or deleting it. Internally, separately indexed child operations
are distinguished by the tuple (enclosing blob reference, array index).
Array positions must be preserved for ordering and to distinguish multiple
operations from the same blob on the same permanode.

The existing claims API does not expose child indices or tuples. Child
operations use the enclosing blob reference in the API; multiple returned
operations can therefore share that reference. A variant API can be added
later if callers need explicit child positions.

Go callers can construct envelopes with `schema.NewMultiClaim`, supplying
ordinary attribute claim builders in array order. To use a shared target,
construct those children with a zero `blob.Ref` and set the envelope's default
with `SetMultiClaimPermanode` before signing.

The shared importer `SetAttrs`, `SetAttrs2`, and `SetAttrValues` methods use
multi claims for updates containing multiple mutations. Single-mutation
updates continue to use ordinary attribute claims.

Multi-claim indexing requires index schema version 7. Earlier indexes must
be reindexed, including any multi-claim blobs they previously ignored.

## Attributes

A permanode can have any attribute you like, but here are the ones that
currently mean something to Perkeep:

* `tag`: A set of zero or more keywords (or phrases) indexed completely, for
  searching by tag. No HTML.

* `title`: A name given to the permanode. No HTML.

* `description`: An account of the permanode. It may include but is not limited
  to: an abstract, a table of contents, or a free-text account of the resource.
  No HTML.

* `camliContent`: A reference to another blob. If a permanode has this
  attribute, it's considered a pointer to its camliContent value.

* `camliMember`: A reference to another permanode. This indicates that the
  referenced permanode is a dynamic set, and we're a part of it.

* `camliPath`: camliPath attributes are set on permanodes which represent
  dynamic directories. If a permanode has attributes:

        camliPath:dir2 = $blobref_dir2_permandode
        camliPath:bar.txt = $blobref_bartxt_permanode

    It will appear as a directory containing "dir2" and
    "bar.txt".

    These are used by a few things, including the web UI, the
    "publish" code (declaring you want a photo at a URL and then the
    HTTP front end resolving each directory link in
    <http://myhostname.com/pics/x/y/x/funny.jpg>), and the FUSE
    read/write filesystem code.

* `camliRoot`: A root name for the permanode. This will cause it to show up as a
  named folder in the FUSE filesystem under <code>roots/</code>.  Creating a
  directory in <code>roots/</code> will cause a new permanode to be created with
  this attr set. You can also browse roots in the web UI.
