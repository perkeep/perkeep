# Runkeeper archive importer plan

The CLI and browser upload implementations follow this plan; Twitter
adaptation remains future work. See the
[Runkeeper importer instructions](/pkg/importer/runkeeper/README.md) and
[archive import protocol](protocol/archive-import.md) for the implemented
interface. Runkeeper account exports are imported without Runkeeper API
credentials or calls to Runkeeper.

## Reuse the fitness model

The [fitness activity schema](schema/fitness.md) already provides the shared
model. The Strava importer writes it; the search predicates, server thumbnail
renderer, and web cards consume it without requiring Strava as the source.
Runkeeper should write the same attributes, including:

- `camliNodeType=fitnessActivity`, `fitnessSource=runkeeper`, and the activity
  UUID as `fitnessSourceID`.
- Generic activity categories (`Running` to `run`, `Cycling` to `ride`, etc.),
  with the original classification preserved as `runkeeperActivityType`.
- SI measurements, title/notes, start coordinates, and a file-schema reference
  in `fitnessTrack` for usable GPX.

That enables the same red routes, activity badges, configured OSM-derived
basemaps, `is:run`, `is:bike`, and distance predicates. OSM tiles remain the
existing explicit server option. Do not add provider-specific rendering.
Preserve classifications such as `Wheelchair` even if their generic category
is initially `other`. The existing `is:ebike` predicate is a special case
based on Strava-specific fields, not a generic electric-bike classification.

Keep provider parsing and sport-name mapping separate. Strava's API client,
OAuth, rate limiting, stream-to-GPX generation, and summary hashing are not
Runkeeper dependencies. Extract small shared validation or attachment helpers
only when implementing the second caller demonstrates a useful abstraction;
do not introduce a second generic fitness model alongside the schema.

## First-class archive uploads

Twitter has a ZIP reader, but its user workflow requires `pk-put file` and
manually setting `twitterArchiveZipFileRef` on an already configured account.
Its run still checks API credentials and normally imports from the API first.
It is not a standalone browser-upload pattern.

Add an optional archive-import capability to the existing importer framework,
without requiring API importers to implement a new mandatory interface:

1. Choose a ZIP on the importer's landing page.
2. Upload it through the ordinary file-upload path, producing a `file` schema
   blobref, then submit that reference to the archive-inspection API. Validate
   format and identity before creating an account.
3. Show the detected identity and proposed existing/new account. Report
   ambiguity before starting an import.
4. Attach the already uploaded archive to the chosen account's archive history
   and start a normal managed importer run.
5. Show progress, completion counts, warnings, and errors. Allow cancellation
   and retry using the archived file, without uploading it again.

An account page should also offer another upload, with the same identity
checks. A ZIP must not silently populate whichever account page received it.
Archive-only accounts should not present OAuth login or automatic API polling
controls. Reuse the existing run lifecycle rather than adding another worker
system. Start with one active archive per account and explicit handling of an
upload arriving during a run; a general job queue is unnecessary initially.

File transfer and archive import are separate operations. Reuse the ordinary
file-upload machinery, with bounded multipart handling and temporary-file
cleanup where relevant, rather than sending the entire ZIP to an import action.
The import API accepts a file-schema blobref, not raw ZIP bytes or a raw content
blobref. It owns request-forgery protection, authorized reference validation,
archive opening, and durable input references. Reuse the streaming-to-file-schema
approach in `pkg/server/uploadhelper.go` and the blob-backed
`schema.NewFileReader`/`zip.NewReader` combination rather than coupling importers
to the UI handler or requiring archive extraction.
Use limits on compressed uploads, entry counts, decompressed bytes, and actual
reads. Reject ambiguous duplicate member names and unsafe paths; avoid
extracting ZIP paths onto the filesystem. Do not use filenames as proof of
identity. A scratch cache must not be the only record needed to resume.

Twitter can then use the same upload/input mechanism while preserving its
existing attribute-based input. Its smallest adapter can target configured
accounts and retain existing API behavior. Separating its archive run from
OAuth/API requirements and updating its archive parser are additional tasks,
not prerequisites for Runkeeper.
The current parser recognizes the historical `data/js/tweets/2*.js` layout;
a user's real Twitter ZIP would help establish which additional layouts and
media conventions to support. It is not needed to build the shared upload
mechanism or the Runkeeper importer.

Before exposing that parser through the new flow, reject unsupported layouts
instead of treating zero recognized files as success, validate the archive's
account identity, and make archive processing genuinely offline. The existing
code can fetch media URLs supplied by archive records; do not carry arbitrary
network fetching into the shared upload pattern. Ensure parsing errors cancel
and join already started workers.

## CLI and web share one import workflow

The initial CLI is standalone:

```text
pk-import-runkeeper foo.zip
pk-import-runkeeper --server=myserver foo.zip
```

The standalone `cmd/pk-import-runkeeper` command uses normal client server
selection and authentication. Keep this archive command separate from `pk`
while its client dependencies are being slimmed.

The CLI is a thin authenticated client of the shared archive-import service,
also used by the web UI, not a second implementation that directly
creates permanodes. Keep the service's parser, identity resolution, incremental
writes, and run orchestration independent of HTTP and HTML. The shared
layering is:

```text
CLI / web --> ordinary file upload --> file-schema blobref
                                             |
                                             v
                           authenticated archive-import API
                                             |
                                  shared importer service
                                             |
                                    provider adapter
                                             |
                                 existing managed runs
```

Add typed `pkg/client` methods for submitting an existing file-schema blobref,
inspection, starting a run, and observing its result. Discover the importer root
(already advertised as `importerRoot`) rather than hard-coding `/importer/`.
Preserve the original
basename as advisory metadata so the same filename-ID tie-break works for
CLI and browser uploads.

Both frontends must apply identical account matching, validation, resource
limits, and no-duplicate behavior. Keep atomic account resolution and write
serialization on the server so concurrent CLI and web imports cannot race.
The CLI should report detected identity and account selection, wait for the
result by default, print progress/warnings, and exit nonzero on failure.
Ambiguity must fail rather than choosing an account to suit noninteractive
execution. A lost client connection must not lose the archived input or the
ability to retry; define interruption and explicit cancellation separately.

A serverless local import mode is not required initially. Keeping provider
parsing transport-independent leaves that option open without introducing a
second account-resolution or persistence path now.

## Multi-gigabyte archives and bounded memory

The user's 2022 Twitter export is approximately 1.5 GB. Treat archives of that
size as normal inputs, not exceptional files that require loading into memory.
Apply the upload-first, import-by-reference contract to both CLI and browser:

- Stream file chunks to ordinary blob storage and obtain the file-schema ref.
  Retrying inspection or import must not require retransmitting the ZIP.
- Before uploading a local seekable file, compute SHA-1 and SHA-256 whole-file
  digests in one bounded-memory pass, along with Perkeep's current canonical
  SHA-224 digest so lookup works against the existing index. Query the server
  for matching whole-file content first. On a verified hit, reuse its file
  schema or existing parts without sending content chunks; on a miss, rewind
  the local file and stream the normal chunked upload. Reuse
  `pkg/client.SearchExistingFileSchema`, which accepts multiple digests and
  verifies matching content is still available. Do not confuse a whole-file
  content digest with the file-schema blobref submitted to the importer.
- Preserve the submitted basename when reusing content: if existing file
  metadata differs, create only a new small file schema referencing the same
  parts, as `fileMapFromDuplicate` already does. This retains filename hints
  without retransmitting the archive. Detect local-file changes between hashing
  and uploading; do not trust a stale precomputed digest.
- Open the stored file with `schema.NewFileReader`, which implements `ReadAt`,
  `Seek`, and `Size`, and pass it to `zip.NewReader`. Fetch archive regions on
  demand; do not create an archive-sized `[]byte` or memory-backed copy.
- ZIP metadata also consumes memory. Bound central-directory size and entry
  counts, checking limits before excessive metadata allocation where possible.
- Stream each selected member through CSV/JSON decoders or into file storage.
  A single tweets JSON/JS member can itself be huge: replacing whole-ZIP
  buffering with `io.ReadAll` on that member is not sufficient. Consume wrapper
  prefixes with bounded parsing and process records incrementally.
- Bound individual record sizes, open readers, concurrent workers, and queued
  records. Large step/heart-rate CSVs must not require keeping all samples in
  memory. Where grouping needs spill storage, use bounded, disposable local
  disk state that can be rebuilt from the retained ZIP.
- Enforce actual decompressed-byte budgets as well as declared ZIP sizes,
  with limits suitable for legitimate multi-gigabyte exports. Random access
  skips unrelated members; compressed members still decode sequentially.

The existing `pkg/client.UploadFile` has an important caveat: without
`FileUploadOptions.WholeRef`, it hashes through an archive-sized `bytes.Buffer`.
Do not use that path unchanged. For a local seekable file, hash with a bounded
streaming pass and look up all computed digests before rewinding for upload.
`FileUploadOptions.WholeRef` currently accepts only one digest; share or extend
the existing duplicate-file lookup/parts-reuse path rather than dropping
multi-digest lookup or reintroducing buffering. Browser upload handling must
also stay streaming; do not materialize the whole file in JavaScript memory.

Validate the submitted ref as an accessible `file` schema and derive its
filename and size server-side. Never accept an arbitrary URL to fetch an
archive. Test with large generated archives and a large single member, checking
bounded memory on both upload and import; small fixtures alone cannot establish
this property.

## Resolve Runkeeper accounts before importing

Search only active Runkeeper importer accounts:

- Match either the profile username or email against existing account
  attributes; the username is the preferred human-facing identity.
- Unwrap the export's observed `Optional[...]` email encoding and trim input.
  Specify case-comparison rules explicitly rather than inventing aliases.
- Deduplicate matches by account permanode. One account matching both fields
  is one candidate, not an ambiguity.
- With no candidates, create an account; with one, reuse it.
- With multiple candidates, use the filename's numeric ID only if it matches
  an already stored account ID or explicitly advisory filename-ID attribute on
  exactly one candidate. Otherwise fail with a useful ambiguity error. Do not
  merge two existing account graphs.
- Serialize resolution and account creation so simultaneous uploads cannot
  both create an account for the same identity. Revalidate the selected
  account when confirming a preview.

The inspected profile includes username and email but no numeric account ID.
The filename contains an apparent account ID; retain its provenance and do not
silently replace a conflicting stored ID from a renamed ZIP.

## Incremental activity imports

Use `root/activities/<activity UUID>` paths and existing importer object
setters, which avoid claims for unchanged values. Serialize runs for an account;
`ChildPathObject` alone is not an atomic concurrent create-if-absent primitive.

Use a versioned per-activity fingerprint covering the parsed source row and
all associated track, photo, step, and heart-rate inputs, not just the ZIP
file's hash or CSV row. ZIP ordering and container timestamps should not
change that fingerprint. Mark an activity complete only after its attributes
and attachments succeed; retry incomplete activities on the same permanode.
Avoid deleting and resetting a completion marker for unchanged activities.

Preserve original source data, including unknown columns, notes, tagged
friends, photos, measurements, and sensor samples. Retaining the original ZIP
provides a lossless baseline; add useful modeled fields and stable attachment
references without copying an entire large sensor CSV into every activity.
Keep archive history at account level: changing a per-activity "latest ZIP"
attribute on every upload would cause unnecessary claims for unchanged data.

Absent activities in a later export do not imply deletion. Do not deduplicate
Strava and Runkeeper activities by date, distance, or identical track content:
provider identifiers do not establish cross-provider identity.

## Observed export edge cases

The inspected export has 855 distinct activity UUIDs and 847 GPX files:

- Three GPX filenames are each referenced by two different activity UUIDs.
  Never use GPX filenames or start times as activity identity.
- Five activities have no GPX. CSV dates have no timezone; GPX timestamps have
  UTC instants. Preserve the source local date, and do not label an unknown
  timezone as UTC. Decide whether to offer a user-specified fallback timezone.
- Six GPX files fail strict XML parsing, with an unmatched segment opening.
  Preserve the original files in source attachments and omit `fitnessTrack`
  when a track cannot be safely rendered. Report warnings without blocking
  unrelated activities; any targeted repair must be explicit, tested, and stored
  as a derived track rather than overwriting the original.
- The profile's email is encoded as `Optional[address]`.
- There are five photos, two weight measurements, and 462,060 step samples.
  Heart-rate CSV contains only a header.
- Durations include values greater than 24 hours. Parse durations as durations,
  not clock times. The CSV does not establish separate moving/elapsed times;
  choose documented semantics without manufacturing both from one value.

## Suggested implementation sequence

Deliver a CLI-first Runkeeper vertical slice before adding browser polish or
modernizing Twitter. Keep each step separately reviewable and test it as it
lands; do not defer correctness and memory tests to a final phase.

1. **Bounded-memory ordinary uploads.** Share the streaming prehash,
   multi-digest existing-content lookup, and parts-reuse/upload path through
   `pkg/client`. Verify large files do not buffer in memory, lookup hits send
   no content chunks (including renamed files), misses stream correctly, and
   hashing/upload races are detected. Leave SHA-1 acceptance policy and hash
   migration outside this work.
2. **Minimal archive-import service and API.** Accept existing file-schema
   refs, open ZIPs with blob-backed random access, and add optional provider
   inspection/account-resolution hooks. Use Runkeeper profile inspection as
   the first real caller. Add protected API/client methods, durable source
   references, and existing managed-run integration, not a general job queue.
   Test limits, unsupported inputs, identity ambiguity, concurrent account
   resolution, and failure/cancellation behavior.
3. **Runkeeper end to end through `pk-import-runkeeper`.** Implement streaming
   parsing, UUID-keyed fitness permanodes, raw/source attachments, useful optional
   data, and versioned per-activity fingerprints. Wire up the thin CLI and verify
   no-op repeats, changed/added activities, failed-run retries, missing or
   malformed tracks, unzoned dates, and source preservation. Verify existing
   search/thumbnail behavior, then validate locally against the supplied ZIP.
   This is the first usable end-to-end milestone. Extract only helpers that
   demonstrably simplify both Runkeeper and Strava.
4. **Web upload and account UX.** Reuse ordinary streaming uploads and the
   same import-by-reference API for preview, identity confirmation, progress,
   warnings, and retry. Hide irrelevant OAuth/polling controls for archive-only
   accounts. Test the browser flow and simultaneous CLI/browser submissions.
5. **Twitter adapter and hardening, separately scoped.** Reuse the proven
   archive mechanism while retaining old input compatibility. Validate legacy
   layout/identity, reject unsupported archives, and address unsafe media URLs
   and worker cleanup before expanding exposure. Supporting the user's 2022
   layout or making Twitter fully offline requires representative fixtures and
   is not a prerequisite for Runkeeper.

Include large-archive, large-member, and bounded-memory regression checks in
the relevant steps above, not only small happy-path fixtures.

Use small synthetic fixtures in the repository. Keep personal exports outside
Git; use the supplied archive for a separate local validation pass.
