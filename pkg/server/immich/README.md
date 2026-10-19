# Immich mobile uploads to Perkeep

Perkeep can optionally serve the upload-oriented subset of the
[Immich mobile API](https://docs.immich.app/features/mobile-app/). This lets the
existing iOS and Android apps back up photos and videos without a separate
Perkeep app.

This is an experimental, single-owner compatibility adapter, not an Immich
server. The protocol target is **Immich v3.3.1**, based on the mobile and server
sources at that tag. HTTP integration tests exercise login, ingestion and
incremental sync. Physical-device and background-backup behavior still needs
broader testing. Future app versions may require protocol updates.

**The stock mobile app does not guarantee unmodified camera originals.**
See [originals and edited resources](#originals-and-edited-resources) before
relying on it for that purpose.

## Enable it

Add this object to your existing high-level `server-config.json`, keeping your
normal blob storage, signing identity and Perkeep authentication settings:

```json
"immich": {}
```

No separate metadata database or filesystem path is needed. Authoritative API
state lives in normal Perkeep blobs and signed claims; the adapter rebuilds its
in-memory view at startup. The API is disabled when the object is absent.
A signing identity and secret ring are required; the native Perkeep index and
search handler are also required. Keep `runIndex` enabled (the default):
`runIndex: false` is rejected when Immich is
enabled. Deduplication works with or without the in-memory search corpus
(`copyIndexToMemory`).

After restarting Perkeep, enter this server address in the Immich mobile app:

```text
https://your-perkeep-server.example/immich/api
```

Include `/api` in the address: the adapter does not install Immich's well-known
discovery document, and the v3.3.1 client otherwise only appends `/api` during
its connection check, not for subsequent requests. Enter **any email and
password** in the app: with the configuration above these values are ignored,
not checked against Perkeep credentials. All logins use the same Perkeep owner,
displayed as `owner@perkeep.invalid`; different app credentials do not create
separate accounts. Select the albums to back up and enable backup using the
app's normal settings.

### Normal Perkeep and Tailscale authentication

With both `immich.email` and `immich.password` omitted, the adapter requires
the server's normal Perkeep authentication with full access on **every
request**, including login, discovery and requests carrying an Immich session
cookie or token. Immich sessions remain in use for app compatibility, but
never bypass this check. The arbitrary credentials entered in the app do not
satisfy the normal Perkeep authentication check.

For a server using Tailscale authentication, keep its existing `auth` setting:
its Tailscale identity policy is unchanged. The phone must be connected to the
Tailnet and use a route to the server on which Perkeep can authenticate its
Tailscale identity. A public URL that does not provide that authenticated
Tailscale route is not sufficient. Devices excluded by the existing policy
remain excluded, even if they have an Immich session.

Other Perkeep auth modes likewise require the phone's requests to satisfy
the configured authentication independently of the app's login form.
**With `auth: "none"`, the Immich API is public to anyone who can reach it**:
the app's arbitrary email and password do not protect it.

### Optional standalone credentials

To deliberately use separate Immich-only authentication instead of normal
Perkeep authentication, configure **both** credentials:

```json
"immich": {
    "email": "photos@example.com",
    "password": "use-a-long-separate-random-password"
}
```

Then log in using that exact pair. Setting only one of `email` or `password`
is a configuration error. Protect the server config, which contains this
password. These credentials grant access only to the Immich handler, not other
Perkeep handlers.

**This is a separate authentication mode that bypasses normal Perkeep auth,
including its Tailscale identity policy, for the Immich API.** Choose it
deliberately on a Tailscale-authenticated server: anyone who can reach this
endpoint and has these credentials can use it without passing the server's
normal identity check.

### Transport and upload limits

Use HTTPS with a certificate trusted by the phone. A reverse proxy must allow
large multipart bodies and long uploads, forward cookies, preserve the path,
and allow the finite NDJSON sync stream. When TLS terminates at a trusted
proxy, pass `X-Forwarded-Proto: https` so login cookies are marked Secure.
An additional interactive proxy login is generally unsuitable for unattended
mobile backup.

The default request limit is **32 GiB including the entire multipart body**.
To lower it, set `maxUploadBytes` inside `immich`, for example `1073741824` for
1 GiB. File parts above 8 MiB spill to temporary disk while the request is
parsed; provision temporary space for concurrent large videos. The server
validates metadata before importing the file, and removes multipart temporary
files when the request finishes.

## What is implemented

* Server discovery/version/features/config, password login, token validation,
  logout, owner profile and preferences.
* Cookie authentication for native foreground/background upload workers, plus
  Bearer and Immich session-token headers. In normal Perkeep auth mode, each
  request must also pass Perkeep authentication.
* Multipart photo/video uploads, original timestamps, favorites, visibility,
  duration, mobile metadata, XMP sidecars, and two-upload Live Photo pairing
  (hidden video first, still image referencing its returned asset ID).
* Server-computed SHA-1 checksums in Immich's encoding, optional checksum-header
  verification, bulk duplicate checks, idempotent upload receipts, and
  concurrent duplicate-upload reconciliation.
* Finite `/sync/stream` responses with the owner, assets, minimal file metadata,
  mobile metadata and completion events. Per-session acknowledgements advance
  in memory only when the app explicitly acknowledges them, with daily durable
  checkpoints and a best-effort checkpoint on graceful shutdown.
  Interrupted/unacknowledged data is replayed; new uploads use a sequence-indexed
  change log rather than a full collection scan.
* Original downloads with byte ranges, asset details and Perkeep-generated
  image thumbnails where the existing decoder supports the format.

Originals are written through the configured primary Perkeep blob storage as
ordinary chunked file schemas, preserving the uploaded `assetData` bytes
without transcoding. Upload ingestion does not generate or store thumbnail
derivatives or accept uploaded thumbnails: file parts other than `assetData`
and the optional XMP `sidecarData` are rejected. Thumbnails served on demand
are separate from the stored original. The server cannot determine whether
the client's `assetData` is the device's original capture or an edited or
otherwise derived resource.

### Originals and edited resources

The v3.3.1 app's foreground and background upload paths use the same
`StorageRepository` and pinned `photo_manager` 3.9.0 dependency. Although the
API is called `originFile`, its iOS implementation selects
`getCurrentResource`, which can prefer an edited `fullSizePhoto` or
`fullSizeVideo`; Live Photo motion selection can prefer
`fullSizePairedVideo`. It does not always select the unmodified camera capture
or all original resources in a RAW+JPEG pair. Downloading an iCloud-backed
resource avoids using a local display thumbnail, but does not change this
resource-selection policy.

There is no upload-protocol switch or inspected backup setting that forces
unmodified resources. An edited photo sent as `assetData` is indistinguishable
to the server from an original photo with those bytes. Android likewise
uploads the selected MediaStore file, not its pre-edit history. Filenames,
dimensions and hashes cannot establish camera-original provenance.

Consequently this server adapter alone **cannot enforce an unmodified-originals
only policy with the stock app**. That requires an uploader verified to select
the original resources explicitly. The adapter preserves exactly the bytes it
receives; whole-file deduplication identifies identical bytes, not different
edits or encodings of a logical photo. Do not delete phone originals based on
an adapter backup receipt without independently verifying what was uploaded.

### Native content identity

New mobile asset IDs are deterministic functions of the signing owner's
public-key blobref and the original's `wholeRef`, not its filename, timestamps,
file-schema blobref, login credentials, API mount prefix or randomly assigned
Immich user ID. The versioned derivation hashes
`"perkeep.org/immich/asset-id/v1\0" + ownerBlobRef + "\0" + wholeRef` with SHA-256,
takes the first 16 bytes, and sets the UUID version-4 and RFC variant bits.
Both blobrefs include their hash-algorithm prefixes. The result has the
UUIDv4 shape Immich expects, but is deterministic, not a random UUID or a
secret. Different signing owners get different IDs for the same original.
Previously published or imported receipts keep their recorded IDs.

Deduplication uses the native whole-file content hash (`wholeRef`), not just
the adapter's metadata. The adapter asks the native index for file schemas
with the same `wholeRef`, then searches for an owner's permanode with an exact
`camliContent` match to one of those schemas. It reuses that permanode,
including one uploaded outside this adapter. It preserves the existing title,
content and user attributes, but appends namespaced adapter claims for the
mobile asset ID, checksum and upload-receipt link. A retry keeps the same
permanode and mobile asset identity; it does not create a second catalog object.

Existing duplicate permanodes are not merged or deleted. The adapter serializes
its own lookup/publication, but does not impose a global uniqueness constraint
on other Perkeep writers. Run only one adapter writer against a given account.
The native index must be complete and caught up.

If no matching permanode exists, the adapter creates a content-keyed permanode
signed at the fixed Unix epoch, using the same scheme as
`pk-put file --filenodes`. New permanodes get `camliContent`, `title`,
`dateCreated`, `immichAssetId`, and `immichChecksum` attributes.
`immichRecord` links to the immutable asset JSON blob, and `immichState` points
to the account permanode. Optional `immichSidecar`,
`immichLivePhotoVideo`, and `immichMetadata` attributes preserve sidecars,
motion-video file references and mobile metadata on newly created permanodes.
The adapter's asset attributes and receipt link are published together in one
signed multi-claim, including when adopting an existing photo permanode.
If an existing content-planned node has been repointed or cleared, the adapter
fails rather than overwriting that object's attributes.
Perkeep keeps its usual blob hash; SHA-1 is used only for Immich compatibility.
Successful upload acknowledgements are emitted only after the original and
receipt blobs are stored and atomic native claim publication succeeds.

### State, backups and migration

The photo permanodes form the mobile asset catalog. A small account/device
permanode titled **Immich Upload State** stores the remaining API state through
signed claims and linked blobs: the owner identity, sessions, counter snapshots
and the last checkpoint of per-device sync acknowledgements. The runtime
key/value view is in memory, rebuildable from this native data, not an extra
on-disk database.
The account's planned identity is tied to the signing owner, not the API URL
prefix, so mounting the adapter at another prefix does not create a new account.

Registering a new photo publishes its adapter attributes and immutable receipt
in one signed multi-claim on that photo's permanode, without mutating the account
permanode. Each receipt contains its assigned sync sequence. The allocator,
total asset count and byte usage are kept in memory and rebuilt at startup;
the next sequence follows the numeric maximum of all photo receipts and the
infrequent account high-water mark. This includes preference-only sequence gaps
and is independent of camera dates, filenames and search-result ordering.
The durable receipt is published before an upload succeeds or its sequence
becomes visible to sync, so an ordinary crash cannot rewind past a committed
photo.

Account changes such as login/logout, changed preferences and ACK checkpoints
also snapshot the then-current sequence, count and usage. Recovery checks the
exact photo prefix covered by that snapshot while permitting later photo-only
receipts. A missing receipt or partial claim within that covered prefix is an
error, not permission to reuse a lower sequence. These snapshots are not a
second per-upload catalog: a completely missing, uncheckpointed tail cannot be
detected from an incomplete index alone. Keep the native index complete and
caught up; rebuild an uncertain or lost index from the normal Perkeep blobs
before resuming uploads. Do not treat a partial restore as an empty catalog.
Startup scans native indexed metadata and receipt claims, not all original
file bytes or a separate database. Recovery rejects native deletion of an
adapter account, photo receipt or its reserved attributes; those unsupported
changes must not silently rewind the sequence allocator.

ACK progress is a replay optimization, not the evidence that an original was
backed up. Normal ACK advances are immediately visible to later sync requests
in the same process, but do not publish claims individually. If progress is
dirty, one atomic checkpoint publishes the pending ACKs at most once every
24 hours across all sessions; the last checkpoint time survives restarts.
Idle polls and unchanged ACKs do not write claims. Graceful handler/server
shutdown attempts a final dirty checkpoint even inside that interval, but
shutdown or a successful checkpoint is not required for photo durability.
A crash may replay metadata since the last checkpoint; stable asset IDs let
the app upsert it without creating new photo objects or requiring another
upload of the original bytes. Failed checkpoints retain dirty progress for
a later attempt and are reported in server logs or shutdown errors.

Explicit checkpoint deletion or a sync reset remains immediately durable,
not delayed by the daily policy: restoring an old, higher ACK after the app
clears its cache could incorrectly skip metadata. Login/logout, photo receipts
and preference publication also retain their existing immediate durability.
Reapplying unchanged preferences does not allocate a sequence or write claims.

**Back up the normal Perkeep blob store, including schema blobs and signed
claims, and preserve the signing identity as usual.** Restoring those blobs
and fully rebuilding the native index reconstructs the same mobile asset IDs,
sessions and last-checkpointed sync positions. This does not export unrelated
Perkeep photos into the app. An incomplete index or missing native state must
be repaired before accepting uploads; startup requires a healthy, complete
native index.

For an installation using the adapter's old dedicated LevelDB directory, stop
the old writer and back up its directory before upgrading. Temporarily set
the deprecated high-level `immich.metadataFile` to that **existing directory**
(not the Perkeep index and not a single file). It is only a one-time import
source: the adapter never creates, modifies or deletes it. Migration publishes
its assets and account/device state as native blobs and claims. Once native
state is initialized, the legacy source is not accessed again, including on
restart. After a successful migration, remove `metadataFile`; subsequent
operation and recovery need only the normal Perkeep data, not that directory.

## Limitations and data safety

* Only uploads made through this adapter appear in the mobile asset index.
  Existing Perkeep photos are not automatically exported into Immich.
* No Immich web UI, socket.io/realtime notifications, sharing, partner accounts,
  album synchronization, editing, trash/delete, facial recognition, smart
  search, or video transcoding/thumbnails. Unsupported mutations fail rather
  than reporting success. Incremental sync happens when the app requests it,
  not through realtime push.
* Synced EXIF details are minimal; the original EXIF bytes are preserved in the
  file and Perkeep can index them normally. Photos that the existing Perkeep
  image decoder cannot decode remain uploadable/downloadable, but have no
  generated thumbnail. To bound decoder memory, thumbnail generation accepts
  originals up to 64 MiB, with headers identifiable within 16 MiB and a decoded
  pixel estimate fitting the shared 256-MiB resize budget. Larger originals
  remain backed up and downloadable.
* Android motion photos with video embedded in a single JPEG/HEIC are preserved
  byte-for-byte, but the adapter does not extract that video or automatically
  discover Live Photo pairs from EXIF identifiers. Pairing requires the client's
  explicit two-upload `livePhotoVideoId` association.
* Duplicate original bytes retain the first upload's asset metadata, filename
  and associations. Different edits/original encodings have different checksums.
* The API exposes one owner. Sessions persist across restarts; in standalone
  credential mode, changing the configured email or password invalidates them.
  In normal Perkeep auth mode, changing the app's ignored credentials does not
  change the owner or revoke sessions. Log out to revoke a session.
  Finish or cancel active sync/acknowledgement requests before manually resetting
  checkpoints: reset does not invalidate older in-flight acknowledgement tokens.
* **The native Perkeep index is required for safe deduplication.** If it is
  lost, rebuild it from the surviving blob store before accepting uploads.
  The adapter does not automatically rebuild it. An index entry alone is not
  a backup: adopting a native object also requires its permanode/content
  claims and matching file bytes to be present in blob storage. Missing blobs
  or a partial replica write fail rather than producing a successful receipt;
  repair the storage/index before retrying. This conservatively includes
  non-deleted historical content-claim envelopes, not just the newest claim.
* Do not delete originals from a phone solely because an initial experimental
  backup appears complete. First verify downloaded files, restart persistence,
  repeat/incremental backup, and Live Photo/video contents with your actual app.

## Low-level configuration

Advanced configurations can mount the handler on another prefix ending in
`/api/`. It performs its authentication checks internally and must not be
wrapped in Perkeep's normal auth handler. With no `email` or `password`, it
checks normal Perkeep authentication on every request, as in the high-level
configuration above:

```json
"/immich/api/": {
    "handler": "immich",
    "handlerArgs": {
        "blobRoot": "/bs/",
        "jsonSignRoot": "/sighelper/",
        "searchRoot": "/my-search/"
    }
}
```

The referenced blob storage, signing handler and native search handler must
exist. `searchRoot` is required and must name a `search.Handler` backed by the
native Perkeep index; an in-memory corpus is not required. Do not configure a
runtime `metadata` database. For one-time legacy import only, add
`"legacyMetadata": {"type": "leveldb", "file": "/path/to/existing/immich.leveldb"}`
to `handlerArgs`, then remove it after migration. Adding both `email` and
`password` to `handlerArgs` selects the same standalone
authentication mode and bypasses normal Perkeep auth; adding only one is an
error. The old low-level `metadata` object is accepted only as a deprecated
alias for `legacyMetadata`, never as a runtime database; do not set both.

## Verification

```sh
go test ./pkg/server/immich ./pkg/server ./pkg/serverinit ./pkg/types/serverconfig
go test -race ./pkg/server/immich ./pkg/server ./pkg/serverinit ./pkg/types/serverconfig
```

Before relying on a new mobile release, compare its login/cookie handling,
upload form fields and sync request/entity versions with this implementation.
The principal upstream references used for the v3.3.1 contract are:

* `mobile/lib/services/api.service.dart`
* `mobile/lib/services/auth.service.dart`
* `mobile/lib/domain/services/sync_stream.service.dart`
* `mobile/lib/infrastructure/repositories/{sync_api,sync_stream}.repository.dart`
* The native iOS/Android networking and backup workers under `mobile/`
* `server/src/dtos/{auth,server,user,user-preferences,asset-media,sync}.dto.ts`
* `server/src/controllers/{asset-media,sync}.controller.ts`
