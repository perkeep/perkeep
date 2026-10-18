# Archive imports

Archive import is an optional importer capability. Runkeeper implements it;
this endpoint does not add archive support to other importers such as Twitter.
The CLI entry point is:

```text
pk-import-runkeeper export.zip
pk-import-runkeeper -inspect export.zip
pk-import-runkeeper -fileref=<file-schema-blobref>
```

This standalone command keeps archive-command code out of `pk`.
The server-side importer and archive API remain shared with future web clients.

## Upload first, import by reference

Upload the archive through the ordinary [file upload](blob-upload.md)
mechanism. The input to archive import is the resulting **file-schema blobref**,
not a raw ZIP-content digest, local server pathname, or URL.

The CLI streams a local seekable file through SHA-224, SHA-1, and SHA-256
hashers, queries for already stored whole-file content, and reuses existing
parts on a verified hit. A miss rewinds the file for chunked upload. Neither
path buffers the whole archive. The filename remains advisory metadata;
renaming an already stored archive can require only another small file schema.

The server opens the file with `schema.FileReader` and `archive/zip` random
access. Large selected members are decoded incrementally. Retrying import or
inspection does not require uploading the file again.

## Endpoint

Discover `importerRoot` in [discovery](discovery.md). Append
`<provider>/archive`, for example `/importer/runkeeper/archive`; do not
hard-code the importer prefix.

All operations require normal importer-handler authentication. POST requires
`Content-Type: application/json`; cross-site browser requests are rejected.
The request body is limited to 16 KiB and must contain exactly one JSON object.
Responses are JSON with `Cache-Control: no-store`. Error responses contain
an `error` string; ambiguous accounts or conflicting active inputs return 409.

### Inspect or start

POST:

```json
{
  "action": "inspect",
  "fileRef": "sha224-01234567890123456789012345678901234567890123456789012345"
}
```

`inspect` validates the archive and resolves its identity without creating an
account or starting a run. `start` performs the same inspection, creates or
reuses the account, retains the input, and starts a managed asynchronous run.

The result has:

- `identity`: `userName`, `email`, `name`, and optional advisory `filenameUserID`.
- `accountRef`: the existing or newly created importer-account permanode,
  omitted when inspection finds no account.
- `created`: true if this request created an account.
- `status`: current or previous status for this input, when available.

The framework matches username exactly after trimming, or email after trimming
and case folding, only within the selected importer. Multiple matches are an
error unless the advisory filename ID matches an already stored `userID` or
advisory filename-ID hint on exactly one candidate. A filename-derived hint is
stored separately with its source file ref, never as a verified `userID`, and
later conflicting filenames do not replace it. An existing `userID` takes
precedence over the advisory hint. The filename alone cannot identify an
account.

Starting the same file while it is running returns the active run. A different
input for the same active account is rejected; it is not silently queued or
substituted. Explicitly starting a completed file reconciles it again so
importer upgrades can refresh older imports.

### Follow completion

GET `<provider>/archive?account=<account-permanode-ref>` returns a status:

- `accountRef`, `fileRef`: the account and retained input.
- `state`: `running`, `complete`, `error`, or `interrupted`.
- `started`, `finished`: run timestamps.
- `error`: failure details, when present.
- `progress`: `itemsTotal`, `itemsDone`, `itemsUnchanged`, `warningCount`,
  and a bounded `warnings` list.

Run status and archive history are stored on the account, not solely in a
scratch cache. A stored running status without an active process is reported
as interrupted. Retry by submitting its file ref again. This is a single active
run per account, not a durable queue or automatic resume scheduler.

`pk-import-runkeeper` waits by default and exits nonzero on failure. `-detach`
returns after the server accepts the run. Interrupting the CLI stops waiting,
not the server's managed import; the existing importer account's Pause control
can request cancellation. `-json` emits the inspection/start result when
inspecting or detaching, or the final status when waiting; progress goes to
stderr.

## Limits

ZIP inputs are limited to 32 GiB logical file size, 100,000 entries, 32 MiB
central-directory metadata, 4 GiB declared or actually read per entry, and
64 GiB cumulative uncompressed data. Providers may impose tighter limits for
individual metadata files.
Repeated member reads count against the cumulative budget. Warning lists are
limited to 100 entries with 1,024 bytes each; `warningCount` retains the total.

ZIP64 is supported. Multi-disk, self-extracting, duplicate-name, unsafe-path,
and non-regular/non-directory entries are rejected. The archive is not extracted
onto the server filesystem. Runkeeper uses private disposable disk files to
group sensor samples without keeping the entire sensor CSV in memory.

These limits bound processing but do not make scratch storage free: large
imports may require significant temporary disk space and archived blob storage.
