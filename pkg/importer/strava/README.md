# Strava Importer

Imports the authenticated athlete's activities into stable, provider-independent
[fitness activity permanodes](/doc/schema/fitness.md), with raw API JSON and
generated GPX tracks.

## API policy and personal exports

Strava's [API Agreement](https://www.strava.com/legal/api) and
[API Policy](https://www.strava.com/legal/api_policy), effective June 1, 2026,
include archive/persistent-storage restrictions and a seven-day cache limit
that may affect this use. Clarify permission with Strava; do not assume
self-hosting creates an exemption.

For personal archiving, Strava's
[downloadable account archive](https://support.strava.com/hc/en-us/articles/216918437-Exporting-your-Data-and-Bulk-Export)
is an alternative that does not require this API importer.
A local download-archive importer is **not implemented** here.

## Setup

1. Register a Strava [API application](https://www.strava.com/settings/api).
   Strava's current setup guide requires a subscription to create an application.
   Set its **Authorization Callback Domain** to the hostname of your externally
   reachable Perkeep server, not a URL with a path. Use HTTPS outside local
   development. The importer supplies its own callback URL.
2. Open your server's `/importer/strava` page and configure the application's
   Client ID and Client Secret using Perkeep's importer credential form.
   Keep the secret and account tokens private.
3. Create the importer account, start OAuth setup, and grant **`activity:read_all`**.
   This scope includes activities visible only to the athlete. Denying it
   prevents setup completion.
   The importer uses a nonce-bearing OAuth `state` to bind the callback to the
   account, and persists access tokens, expiry, and rotating refresh tokens.
4. Start the import from the account page.

See Strava's [getting-started guide](https://developers.strava.com/docs/getting-started/)
and [OAuth documentation](https://developers.strava.com/docs/authentication/)
for application setup, scopes, and refresh-token behavior.

### Bounded test imports

Set `PERKEEP_STRAVA_MAX_ACTIVITIES=10` in the **server's environment** to
consider only the first ten activity summaries returned by Strava's
usual newest-first listing. The server requests a page size of ten, imports at
most those ten activities, and stops successfully without probing another
page. This sets generic `completedVersion="2"`; it is not an error or an
incomplete test run. It does **not** advance the chronological watermark or
set the separate backfill-done marker described below.

The cap counts already imported activities too. Repeated runs check the
same recent window, rather than importing the next ten older activities.
Previously stored older activities are not deleted. If fewer activities
exist, reaching the end of the listing also succeeds.

Unset or empty means no cap. Other values must be positive decimal integers;
invalid, zero, negative, and overflowing values fail before any API request.
Remove the variable to enable chronological backfill. Completion of a capped
run does not prevent that later backfill. The cap always selects the
**first N listed summaries** (the existing "newest N" test window), not the
next N after a chronological checkpoint. The API reference does not guarantee
timestamp ordering.

## Stored data

Each account's import root, titled `Strava workouts`, has an `activities` child
titled `Activities`. The internal root has `camliDefVis="hide"` so it is excluded
from the default home view, but remains accessible through the importer account.
The `Activities` collection and activity permanodes remain visible.
The collection's `camliPath:<decimal ID>` attributes point to stable activity
permanodes, so the layout is `root/activities/<decimal ID>`. Reimports reuse the
same activity permanode.

Generic attributes use SI units: distance/elevation in meters, durations in
seconds, energy in joules, heart rate in beats per minute, and UTC `startDate`.
Energy is converted from Strava's kilocalories by multiplying by 4,184.
The importer reuses `title`, `description`, `latitude`, `longitude`, and `url`;
sets `fitnessSource` to `strava` and `fitnessSourceID` to the decimal activity ID;
and maps the sport to a broad `fitnessActivityType`. Unknown optional values
are absent rather than fabricated as zero.

Additional single-valued attributes retain provider-specific data:

| Attribute | Value |
| --- | --- |
| `stravaActivityJSON` | File-schema blobref for `activity.json`, the raw detailed-activity API response. |
| `stravaStreamsJSON` | File-schema blobref for `streams.json`, the raw response containing returned requested streams. |
| `stravaSportType` | Strava's specific `sport_type`. |
| `stravaActivityType` | Strava's legacy `type`, not the generic fitness category. |
| `stravaTimezone` | Provider timezone description. |
| `stravaExternalID`, `stravaGearID` | Provider identifiers, when supplied. |
| `stravaTrainer`, `stravaCommute`, `stravaManual`, `stravaPrivate` | Boolean strings, `"true"` or `"false"`. |
| `stravaSummaryHash` | Hash of the modeled activity summary used to detect changes. |
| `stravaImportVersion` | `"2"` only after that activity's attributes and attachments are complete. |

The activity's `stravaActivityJSON`, `stravaStreamsJSON`, and `fitnessTrack`
attributes reference named file-schema blobs directly. Attachments do not create
wrapper permanodes or `camliPath:<filename>` links. Refreshing an activity removes
any filename paths left by the previous layout; it does not modify or delete
historical wrapper permanodes. These path deletions, missing optional-attribute
deletions, modeled attributes, attachment refs, and completion markers are
published together in one signed activity claim after every file upload succeeds.

The client requests `time`, `latlng`, `altitude`, `distance`, `heartrate`,
`cadence`, `watts`, `temp`, `moving`, and `velocity_smooth` streams. Strava may
return only a subset; unavailable streams are not invented. A missing streams
endpoint can leave an activity without stream or track attachments.
The raw JSON preserves the fields actually returned, including sensor values
and sampling metadata; it is not a substitute for an original uploaded file.

When usable `latlng` samples exist, `fitnessTrack` references a file schema for
the generated GPX 1.1 track. Aligned `time` offsets yield UTC timestamps relative
to the activity start; aligned `altitude` samples yield elevation. Activities
without coordinates, including many manual or indoor activities, have no GPX.
Malformed or misaligned streams fail the activity rather than creating a
misleading track.

## Chronological backfill, updates, and resuming

Uncapped imports discover the complete selected activity-time range, starting
at page 1 and continuing until an empty page, then sort the discovered summaries
and import **oldest first**. The initial run omits the time filter and discovers
the complete history before importing it. This separates discovery order from
publication order: progress means a completed contiguous prefix, not the
newest activity seen.

The account permanode holds the authoritative progress:

| Attribute | Value |
| --- | --- |
| `stravaImportThrough` | UTC RFC3339 activity-start timestamp through which the discovered chronological prefix is complete. |
| `stravaImportThroughVersion` | `"2"`, binding the checkpoint to the current `runCompleteVersion`. |
| `stravaBackfillCompletedVersion` | `"2"` only after the uncapped historical backfill is complete. |

An interrupted backfill restarts discovery at page 1 using the durable
watermark as an inclusive lower bound. It enumerates the entire filtered range,
not just pages that appear to be above the boundary. Activities equal to the
boundary remain included, including ties across listing pages. Stable activity
IDs identify individual activities; a whole timestamp group must complete before
the timestamp-only watermark advances. A failure in a group leaves the
checkpoint at the preceding completed group.

Checkpoint claims are batched after 50 activities or 30 elapsed seconds,
checked at completed timestamp-group boundaries, at the end, and on graceful
failures when possible. A slow activity or a large same-time group can extend
the elapsed-time interval; there is no background checkpoint timer.
Each checkpoint atomically publishes
the related account attributes. There is no per-activity cursor write, and an
unchanged poll does not rewrite progress. A crash can lose the latest
uncheckpointed progress but not already published activities: their individual
hash/version markers make replay safe.

The separate backfill-done marker distinguishes interrupted backfill from
normal polling. An empty history sets completion without inventing a
watermark; later activities can still be discovered. The generic account
`completedVersion="2"` can also be set by a capped test run, so it is **not**
evidence that the historical backfill finished.

Accounts without the new progress state require full discovery, even if generic
`completedVersion` is already current; this includes legacy accounts.
After validating the scalar version fields, an incompatible nonempty checkpoint
or backfill version requires a safe full scan. Its timestamp payload is opaque:
it is ignored, not parsed using the current RFC3339 format. An unknown backfill
version triggers this scan even without a checkpoint version binding.
For current-schema progress, incomplete or malformed
authoritative state fails the run instead of silently skipping history;
in particular, a watermark or current-version done marker requires its binding.
The completed-empty-history case above is valid without a timestamp.

### Discovery limits

Strava's [activity-list API reference](https://developers.strava.com/docs/reference/#api-Activities-getLoggedInAthleteActivities)
documents an `after` epoch-time filter, but no guaranteed timestamp ordering.
For a nonzero inclusive boundary, the client sends
`after=floor(boundary Unix seconds)-1` and locally retains only summaries at
or after the exact boundary. Including the preceding whole second preserves
equal timestamps and fractional seconds without importing earlier summaries.
During backfill and new-activity catchup the boundary is the watermark.
Daily reconciliation instead uses the current time minus seven days. With no
watermark, no `after` filter is sent.

Discovery collects **all** filtered result pages until an empty page, without
assuming timestamp order or stopping when a listed activity is below the
boundary. Out-of-order unique summaries are accepted and sorted locally.
The selected range is held in memory before any of its activities are imported
or progress advances. Failed or quota-incomplete discovery is discarded; it
cannot establish a safe new watermark.

Consequently, a sufficiently large history can exhaust the listing quota
before discovery finishes and repeat that work on the next run. This design
fits smaller histories, but does not promise quota-bounded discovery for every
account. The `after` filter reduces later discovery ranges, but cannot shrink
an initial full-history scan. Stable API cursors, bounded resumable discovery
windows, or a durable discovery manifest would be preferable for larger
histories.

There is no persisted page cursor or transactional source snapshot. Concurrent
inserts and deletes can shift offset pages and leave activities undiscovered.
Identical summaries repeated across pages are safely deduplicated by activity
ID; conflicting summaries for the same ID fail discovery so a later run can
retry. Neither deduplication nor sorting recovers items never returned.
A later full reconciliation may be needed for unobserved source changes.

### Hourly discovery and daily reconciliation

Only after historical backfill is complete do invocations run two kinds of
incremental checks:

- **Hourly discovery:** check the most recent activity summary. An unchanged
  summary needs one listing request and no activity writes. A newer activity
  triggers chronological catchup from the durable watermark, including
  activities accumulated during a long period offline. A changed most-recent
  summary triggers a refresh if its activity date is within the last seven days.
- **Daily reconciliation:** discover activities dated within the last seven
  days and fetch detailed JSON and streams even when summary hashes are
  unchanged. This catches description-only and track edits. Successful full
  fetches within the previous 24 hours are skipped, including activities just
  imported by discovery or an interrupted daily sweep.

Scheduling and per-activity full-refresh timestamps use the account's mutable
scratch cache, not a second authoritative checkpoint format. Routine checks
therefore do not create scheduling claim blobs. Cache loss or failures can
repeat recent checks, but never rewind the durable historical watermark.
Failed daily work remains due; only a successful sweep postpones the next one.

These intervals gate work when the importer is invoked; they do not silently
enable automatic imports or create a separate scheduler. Enable automatic
imports in the account UI. Its default invocation interval is 30 minutes;
Strava discovery is still limited to once an hour. Set the account's
`importAuto` attribute to `1h` for an hourly invocation interval.

The seven-day window is a local importer policy based on **activity date**,
not a source guarantee about edit or upload time, nor the unrelated API
cache-policy limit mentioned above. After backfill, an edit made today to a
month-old activity, or a newly uploaded month-old activity, can be ignored
intentionally. Backdated uploads within the recent window, and same-time
additions not visible in the one-summary hourly probe, are picked up daily.
Source deletions are not mirrored; historical blobs and claims are retained.

### Activity publication

Detailed activity and streams requests are skipped only when the existing
activity has `stravaImportVersion="2"` **and** its `stravaSummaryHash` matches
the newly listed modeled summary, unless a daily full refresh is due.
New or changed summaries trigger an import into the stable activity permanode.
Detail and streams are fetched and validated, GPX is generated, and all file content and
schemas are uploaded before publishing any activity attributes. One atomic
signed claim publishes the modeled values, attachment refs, removal of absent
optional values and legacy filename paths,
summary hash, and finally the completion version. A new successful activity needs
exactly one activity-specific claim blob; an unchanged activity needs none.

A failed fetch, file upload, or claim publication leaves an existing activity's
previous attributes, file refs, summary hash, and completion version intact. Its
old hash still differs from the changed summary, so the next run retries it.
A new failed activity may already have a collection path to its blank, untyped
permanode, but no activity attributes or attachment refs are published. Files
uploaded before a later failure can remain unreferenced; retries reuse identical
content-addressed blobs rather than deleting historical data.

An unchanged full refresh writes no new activity claim or attachment blobs.
`SupportsIncremental=true` does not imply a modified-since API or complete
source mirroring. Edits outside the recent window require explicit full
reconciliation. Offset-page changes can leave unobserved source activities;
see the discovery limitations above.

To force one activity **inside the next discovery window** to fetch its current
detail and streams, remove its completion marker:

```
pk-put attr --del <activity-permanode-ref> stravaImportVersion
```

For a full reconciliation, stop any running import, unset
`PERKEEP_STRAVA_MAX_ACTIVITIES`, and clear all three new progress attributes
on the **account** permanode before running again:

```
pk-put attr --del <account-permanode-ref> stravaImportThrough
pk-put attr --del <account-permanode-ref> stravaImportThroughVersion
pk-put attr --del <account-permanode-ref> stravaBackfillCompletedVersion
```

This relists all summaries, but still skips unchanged, completed activities.
For a known older description-only or stream-only change, also remove that
activity's `stravaImportVersion` before the full reconciliation.

Progress and backfill phase live in signed account claims in the main blob
store, not in the disposable quota cache. The importer's last-run status is
runtime information, not an authoritative checkpoint.

### Rate limits

Strava applies separate application-wide **overall** and **read** limits
over natural 15-minute windows and UTC days. The importer checks both
budgets before each REST request. Until response headers announce limits,
it conservatively uses:

| Budget | Per 15 minutes | Per UTC day |
| --- | --- | --- |
| Overall | 200 | 2,000 |
| Read | 100 | 1,000 |

It learns limits and observed usage from `X-RateLimit-Limit`,
`X-RateLimit-Usage`, `X-ReadRateLimit-Limit`, and `X-ReadRateLimit-Usage`.
Usage reported by other clients of the same application is included.
Accounts and runs sharing a Client ID share one in-process budget. A
snapshot keyed to that Client ID is overwritten in the importer host's
automatically scoped `ImporterCache` after each run, including failed runs
and polls that find no changed activities. This is shared across Strava
accounts and isolated from other importers and account-private caches.
No quota bookkeeping blobs or permanode mutation claims are written.
A valid file-backed snapshot restores the application-wide budget after a
restart, regardless of which account runs first.

With a local `blobPath`, the shared store is under
`<blobPath>/importer-cache/strava/`, using the configured file backend, such
as `cache.sqlite` or `cache.leveldb/`. Without a local cache root, snapshots
are memory-only. See [importer scratch caches](/doc/server-config.md#importer-scratch-caches)
for `cacheRoot` and `cacheType` configuration.

This is disposable optimization state, not synced with your blobs and not
required for correctness. Cache opening, read, validation, and write
failures are logged but do not fail an import or replace its actual error.
A missing, deleted, or invalid snapshot leaves the current in-process
budget intact; after a restart it starts with fresh conservative defaults.
The next API response rediscovers Strava's observed usage, and Strava still
enforces its limits. The old `stravaRateLimitState` permanode attribute is
left untouched and ignored, including on upgrade.

Access tokens are also bound to the Client ID that issued them. Changing
application credentials requires a validated OAuth refresh or reconnect
before REST requests can use the new application's budget.

When either budget is exhausted, the next request is blocked locally with
an error identifying the reset time. A successful response consuming the
last available request is still processed. Quarter-hour usage resets at
`:00`, `:15`, `:30`, and `:45`; daily usage resets at midnight UTC. An HTTP
429 stops the run and records a cooldown, including `Retry-After` when
supplied. There are no automatic retries or sleeps through a limit.

Unobserved external usage or an unexpected process termination can leave
local state stale; the next response refreshes observed usage, and a 429 is
never retried automatically. This is not a distributed quota coordinator
for other applications or separately running Perkeep processes.

With the testing cap enabled, a fresh ten-activity import normally needs
**21 REST requests**:
one listing and two requests per activity (detail and streams). OAuth
setup/refresh is separate; missing activities, fewer returned summaries,
or errors may change the request count. Rerun after resolving an error or
waiting for the relevant window. Successfully completed activities remain
skippable; an incomplete activity is retried.

## Formats and scope

**GPX plus raw JSON** is the chosen representation: portable location tracks
without discarding the returned provider/sensor data.

* [GPX 1.1](https://www.topografix.com/GPX/1/1/) is simple XML for location,
  elevation, and time. This importer does not add sensor extensions.
* [TCX](https://www8.garmin.com/xmlschemas/TrainingCenterDatabasev2.xsd) has
  training-specific structures such as laps and heart rate; it is not generated.
* [FIT](https://developer.garmin.com/fit/overview/) is a compact binary format
  with rich fitness messages; reconstructing it would not recover the original
  device recording.
* [KML](https://developers.google.com/kml/documentation/kmlreference) targets
  geographic visualization; [GeoJSON](https://www.rfc-editor.org/rfc/rfc7946)
  targets JSON geometry/features. Neither is the fitness-track attachment
  convention used here.

There is no photo import, original FIT/TCX download, route-library import, or
webhook/deletion synchronization. Only the authorized athlete's activities
are imported. This is not a live mirror.

The implementation uses a small private HTTP client with no additional
Strava-client dependency. The current official
[developer documentation](https://developers.strava.com/docs/) recommends
Swagger-generated clients; it does not establish a maintained official Go
replacement to adopt. The private client covers only the endpoints needed here.

## Web UI

The generic fitness renderer uses the archived GPX to show a red route preview
with an activity icon, distance, and duration. It works from `fitnessActivity`
attributes, not Strava-specific fields, and makes no additional Strava requests.
The GPX route is rendered locally and is never uploaded to a map service.
OSM-derived basemap backgrounds are an explicit server option; they request
only tiles for the displayed map area and cache them separately from activity
blobs. They make no additional Strava requests.
See [fitness activity rendering](/doc/schema/fitness.md#web-ui-rendering) for
display units, the rough US miles heuristic, and basemap considerations.
