# Permanodes

Permanodes are how Camlistore models mutable data on top of an immutable,
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
  different permanodes. looks like:

        {"camliVersion": 1,
         "camliType": "claim",
         "claimType": "multi",
         "claimDate": "2013-02-24T17:20:03.9212Z",
         "claims": [
              {"claimType": "set-attribute",
               "permanode": "sha1-xxxxxx",
               "attribute": "foo",
               "value": "fooValue"},
              {"claimType": "add-attribute",
               "permanode": "sha1-yyyyy",
               "attribute": "tag",
               "value": "funny"}
         ],
         "camliSig": .........}

## Attributes

A permanode can have any attribute you like, but here are the ones that
currently mean something to Camlistore:

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
