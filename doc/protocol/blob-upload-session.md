# Blob Upload Sessions

An upload session lets a client upload many blobs without waiting for
each upload to be durable. The server buffers the blobs a session
receives, and commits them to storage in groups, sharing the cost of
making them durable (like syncs to disk) among many blobs. The client
later flushes the session to learn which of its blobs are durable.

The server may commit a session's blobs whenever it likes, without a
flush; only a flush guarantees that they're committed. So a blob
uploaded in a session isn't necessarily visible (to stat, get,
enumerate or search) until the session is flushed, but the server never
makes one visible before it's durable. A client should flush its
session, and see the blobs reported durable, before it uploads anything
that refers to them, such as a file schema blob referring to the file's
chunks, or a claim referring to a file.

A server supports sessions at its blob root if discovery reports
`"uploadSessions": true`. See [discovery](discovery.md). Clients of
other servers should do plain [uploads](blob-upload.md).

Sessions are kept only in the server's memory. The server closes a
session once it has been idle for its idle timeout, and loses its
sessions when it restarts. Requests for a session the server doesn't
have get a 404 response; the client should then upload again any blobs
it hasn't seen reported durable, in a new session or not.

Below, `$BLOB_ROOT` is the blob root from discovery, and `$ID` is the
session's ID.

## Opening a session

    POST $BLOB_ROOT/camli/session

The request may have a form value `idleTimeout`: the idle timeout the
client would like, in seconds. The server bounds it, and uses a default
if it's missing. The response is like:

    {
      "session": "LEXVZ2ACZ5WB6LG7OCEWV5JXZF",
      "idleTimeoutSeconds": 300,
      "maxBufferedBytes": 67108864
    }

`maxBufferedBytes` is how many bytes of blobs the session can buffer
before uploads to it wait for commits.

The server may refuse to open a session (for instance, with a 503 when
it has too many open, or is read-only); the client should then do plain
uploads.

## Uploading in a session

Do a [batch upload](blob-upload.md) to

    POST $BLOB_ROOT/camli/upload?session=$ID

The server checks each blob as usual, and lists the ones it added to the
session under `accepted` in the response, rather than `received`:
they're not yet known to be durable. Blobs already in the session are
accepted again. Uploads in a session can't use `X-Camlistore-Vivify`.

A server that doesn't support sessions ignores the `session` parameter,
and lists the blobs it received, durable, under `received` as usual.

## Flushing a session

    POST $BLOB_ROOT/camli/session/$ID/flush

The server commits the blobs the session has buffered, waits for those
commits, and responds with the durable blobs the client hasn't yet
acknowledged, and the blobs that failed to commit since the session's
last flush or status:

    {
      "durable": [
        {"blobRef": "sha224-...", "size": 1234}
      ],
      "failed": [
        {"blobRef": "sha224-...", "error": "..."}
      ]
    }

A failed blob is no longer in the session; the client can upload it
again.

The request may have any number of `ack` form values, each a blob ref
the client acknowledges, having seen it reported durable. The server
forgets acknowledged blobs before responding, so they're not reported
again. A session can only have so many blobs buffered, or durable and
unacknowledged, at once.

## Session status

    POST $BLOB_ROOT/camli/session/$ID/status

is like a flush, but doesn't commit or wait for anything. As the server
may commit blobs without a flush, it can still report blobs durable.

## Closing a session

    DELETE $BLOB_ROOT/camli/session/$ID

closes the session. The server still commits the blobs it buffered, but
the client won't learn whether they're durable.
