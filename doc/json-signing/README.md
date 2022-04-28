# JSON Signing

JSON claim objects need to be signed.  If I want to distribute a Perkeep
blob object publicly, declaring that I "favorite" or "star" a named
entity, it should be verifiable.

## Background

The properties we want in the JSON file, ideally, include:

### GOAL #1) it's still a valid JSON file in its entirety

This means no non-JSON compliant header or footer.

This implies that the data structure to be signed and the signature
metadata be separate, in an outer JSON wrapper.

This has been discussed and implemented in various ways.  For example,
in jchris's canonical-json project, http://github.com/jchris/canonical-json,
the "signed-content" and the "signature" are parallel objects under the
same outer JSON object.

The problem then becomes that the verifier, after parsing the JSON
blob, needs to re-serialize the JSON "signed-content" object,
byte-for-byte as in the original, in order to verify the signature.

In jchris' strategy, the canonicalization is implemented by
referencing JavaScript code that serializes it.  This has the
advantage that the serialization could change over time, but the
disadvantage that you have to embed a Rhino, V8, SpiderMonkey, or
similar into your parser, which is somewhat heavy.  Considering that
canonical JSON serialization is something that should be relatively
static and could be defined once, I'm not sure that the flexibility is
worth the cost.

Overall, though, the jchris approach's structure of the JSON file is
good.

Notably, it satisfies on of my other goals:

### GOAL #2) The document still be human-readable

For instance, the laptop.org project is proposing this Canonical JSON format:
http://wiki.laptop.org/go/Canonical_JSON.  Unfortunately, all whitespace is
stripped.  It's not a deal breaker, but lacks human readableness.

You might say, "Bring your own serialization! Wrap the signed-content
in a string!"

But then you're back to the readable problem, because JSON strings
can't have embedded newline literals.

Further, the laptop.org proposal requires the use of a new JSON
serialization library and parser for each language which wants to
produce Perkeep documents.  This isn't a huge deal, but considering that
JSON libraries already exist and people are oddly passionate about
their favorites and inertia's not to be ignored, I state the next
goal:

### GOAL #3) Don't require a new JSON library for parsing/serialization

With the above goals in mind, Perkeep uses the following scheme to sign
and verify JSON documents:

## SIGNING

-  Start with a JSON object (not an array) to be encoded and signed.
   We'll call this data structure 'O'. While this signing technique
   could be used for applications other than Perkeep, this document
   is specifically about Perkeep, which requires that the JSON
   object 'O' contain the following two key/value pairs:

        "camliVersion": 1
        "camliSigner": "hashalg-xxxxxxxxxxx"  (blobref of ASCII-armored public key)

-  To find your camliSigner value, you could use GPG like:

        $ gpg --no-default-keyring --keyring=example/test-keyring.gpg --secret-keyring=example/test-secring.gpg \
              --export --armor 26F5ABDA > example/public-key.txt

        $ sha1sum example/public-key.txt
        8616ebc5143efe038528c2ab8fa6582353805a7a

    ... so the blobref value for camliSigner is "sha1-8616ebc5143efe038528c2ab8fa6582353805a7a".
    Clients will use this value in the future to find the public key to verify
    signtures.

-  Serialize in-memory JSON object 'O' with whatever JSON
   serialization library you have available.  internal or trailing
   whitespace doesn't matter. We'll call the JSON serialization of
   'O' (defined in earlier step) 'J'.
   (e.g. [signing-before-J.camli](./example/signing-before-J.camli))

-  Now remove any trailing whitespace and exactly and only one '}'
   character from the end of string 'J'. We'll call this truncated,
   trimmed string 'T'.
   (e.g. [signing-before.camli](./example/signing-before.camli))

-  Create an ASCII-armored detached signature of this document,
   e.g.:

        gpg --detach-sign --local-user=54F8A914 --armor \
            -o signing-before.camli.detachsig signing-before.camli

   (The output file is in [signing-before.camli.detachsig](./example/signing-before.camli.detachsig))

-  Take just the base64 part of that ASCII detached signature
   into a single line, and call that 'S'.

-  Append the following to 'T' above:

        ,"camliSig":"<S>"}\n

   ... where `<S>` is the single-line ASCII base64 detached signature.
   Note that there are exactly 13 bytes before `<S>` and exactly
   3 bytes after `<S>`.  Those must match exactly.

-  The resulting string is 'C', the camli-signed JSON document.

   (The output file is in [signing-after.camli](./example/signing-after.camli))

In review:

    O == the object to be signed
    J == any valid JSON serialization of O
    T == J, with 0+ trailing whitespace removed, and then 1 '}' character
         removed
    S == ascii-armored detached signature of T
    C == CONCAT(T, ',"camliSig":"', S, '"}', '\n')

(strictly, the trailing newline and the exact JSON serialization of
the camlisig element doesn't matter, but it'd be advised to follow
this recommendation for compatibility with other verification code)

## VERIFYING

-  start with a byte array representing the JSON to be verified.
   call this 'BA' ("bytes all")

-  given the byte array, find the last index in 'BA' of the 13 byte
   substring:

        ,"camliSig":"

   Let's call the bytes before that 'BP' ("bytes payload") and the bytes
   starting at that substring 'BS' ("bytes signature")

-  define 'BPJ' ("bytes payload JSON") as 'BP' + the single byte '}'.

-  parse 'BPJ', verifying that it's valid JSON object (dictionary).
   verify that the object has a 'camliSigner' key with a string key
   that's a valid blobref (e.g. "sha1-xxxxxxx") note the camliSigner.

-  replace the first byte of 'BS' (the ',') with an open brace ('{')
   and parse it as JSON. verify that it's a valid JSON object with
   exactly one key: "camliSig"

-  using 'camliSigner', a Perkeep blobref, find the blob (cached, via
   camli/web lookup, etc) that represents a GPG public key.

-  use GnuPG or equivalent libraries to verify that the ASCII-armored
   GPG signature in "camliSig" signs the bytes in 'BP' using the
   GPG public key found via the 'camliSigner' blobref

## Libraries

* [Go](/pkg/jsonsign)
