# Indexed EXIF

`GET <searchRoot>camli/search/exif?blobref=<fileRef>` returns the EXIF
metadata already indexed for a file schema. Discover `searchRoot` using
[discovery](discovery.md); for example, the usual configured URL is
`/my-search/camli/search/exif?blobref=sha224-…`.

This endpoint uses the search handler's normal authentication. EXIF can contain
private information, including GPS coordinates, camera serial numbers and
comments; it is not a public image endpoint.

## Response

A successful request returns HTTP 200 and a JSON object:

```json
{
  "fileRef": "sha224-0123456789abcdef0123456789abcdef0123456789abcdef01234567",
  "wholeRef": "sha224-abcdef0123456789abcdef0123456789abcdef0123456789abcdef01",
  "tags": [
    {
      "directory": "IFD0",
      "tag": "010f",
      "name": "Make",
      "type": "string",
      "values": ["Samsung"]
    },
    {
      "directory": "Exif",
      "tag": "829a",
      "name": "ExposureTime",
      "type": "rat",
      "values": ["1/125"]
    }
  ]
}
```

* `fileRef` identifies the file schema, not a permanode or raw image blob.
* `wholeRef` is the indexed digest of the complete file contents. It is `null`
  if no whole-file digest was indexed.
* `tags` is always an array. A known file with no indexed EXIF returns
  `"tags": []`, not an error.
* Each tag is identified by its `directory` and lowercase four-digit hexadecimal
  `tag`. Directories are `IFD0` (main image), `IFD1` (thumbnail), `Exif`, and `GPS`.
  Repeated tag numbers in different directories are separate entries.
* `name` is a directory-aware known field name, or `"Unknown tag"`. Unknown
  tag numbers remain visible.
* `type` is `string`, `int`, `rat`, or `float`. `values` is an array of strings,
  even for numbers, preserving indexed numeric precision and unreduced
  rationals such as `"100/1000"`. Strings are URL-decoded from the index.
  Values are not interpreted, reformatted, or stripped of padding.
* Tags are ordered first by directory (`IFD0`, `IFD1`, `Exif`, `GPS`), then by tag
  number. Components retain their indexed order.

Missing or invalid `blobref` parameters return HTTP 400. Unknown references and
references to anything other than a file schema return HTTP 404. Errors use
the search handler's JSON `error` and `errorType` fields.

Only indexed metadata is returned: this request never fetches or reparses the
original blobs. It does not recover tags that were not indexed. Malformed
optional EXIF rows are skipped. Full EXIF is read from index storage on demand,
even with the in-memory corpus enabled, and is not added to the corpus or to
default describe/search responses.
