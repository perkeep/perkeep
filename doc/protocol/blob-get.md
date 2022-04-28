# Blob Get Protocol

The `/camli/<blobref>` endpoint returns a blob the server knows about.

A request with the GET verb will return 200 and the blob contents if
present, 404 if not. A request with the HEAD verb will return 200 and
the blob meta data (i.e., content-length), or 404 if the blob is not
present.

The response must include an explicit Content-Length, even with HTTP/1.1.
(The one piece of metadata a blobserver keeps on a blob is its length,
 which is used in both enumerate-blobs bodies and responses to blob GETs.)

## Get a blob

Request:

    GET /camli/sha1-126249fd8c18cbb5312a5705746a2af87fba9538 HTTP/1.1
    Host: example.com

Response:

    HTTP/1.1 200 OK
    Content-Type: application/octet-stream
    Content-Length: <the blob length in bytes>

    <the blob contents>

## Existence check

Request:

    HEAD /camli/sha1-126249fd8c18cbb5312a5705746a2af87fba9538 HTTP/1.1
    Host: example.com

Response:

    HTTP/1.1 200 OK
    Content-Type: application/octet-stream
    Content-Length: <the blob length in bytes>

## Does not exist

Request:

    GET /camli/sha1-126249fd8c18cbb5312a5705746a2af87fba9538 HTTP/1.1
    Host: example.com

Response:

    HTTP/1.1 404 Not Found

