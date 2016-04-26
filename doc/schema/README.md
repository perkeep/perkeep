# Schema

At the lowest layer, Camlistore doesn't care what you put in it (everything is
just dumb bytes) and you're free to adopt your own data model.  However, the
upper layers of Camlistore standardize on a common schema to represent various
classes of data.

Schema blobs are JSON objects with at least two attributes always set:
`camliVersion`, which is always 1, and `camliType`, which tells you the type of
metadata the blob contains.

Here are some of the data types we've started to formalize a
[JSON](http://json.org/) schema for:

*   [Files](files/): traditional filesystems.  Files, directories, inodes,
    symlinks, etc. Uses the `file`, `directory`, `symlink`, and `inode`
    camliTypes.

*   [Permanodes](permanode.md): the immutable root "anchor" of mutable Camlistore
    objects (see [terminology](terms.md)). Users create signed
    [claim](permanode.md#claim) schema blobs which reference a permanode and
    define some mutation for the permanode.

    Permanodes are used to model many kinds of mutable data, including
    mutable files, dynamic directories, and more.

    Uses the `permanode` and `claim` camliTypes.

*   [Static Sets](objects/static-set.md): Immutable lists of other blobs by
    their refs. Indicated by the `static-set` camliType.

*   ["Keep" claims](objects/keep.md): Normally, any object that isn't referenced
    by a permanode could theoretically be garbage collected. Keep claims prevent
    that from happening. Indicated by the `keep` camliType.
