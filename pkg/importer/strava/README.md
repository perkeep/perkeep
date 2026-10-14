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
newest-first listing. The server requests a page size of ten, imports at
most those ten activities, and stops successfully without probing another
page. This sets the normal completion marker; it is not an error or an
incomplete run.

The cap counts already imported activities too. Repeated runs check the
same recent window, rather than importing the next ten older activities.
Previously stored older activities are not deleted. If fewer activities
exist, reaching the end of the listing also succeeds.

Unset or empty means no cap. Other values must be positive decimal integers;
invalid, zero, negative, and overflowing values fail before any API request.
Remove the variable to enable a full backfill. Completion of a capped run
does not prevent that later backfill.

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
historical wrapper permanodes.

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

## Pagination, updates, and resuming

Every invocation enumerates the authenticated athlete's activity summaries
starting at **page 1**. There is no persisted page cursor: offset pagination
can shift when activities are added or deleted. An interrupted run restarts
enumeration and skips completed, unchanged activities.

Detailed activity and streams requests are skipped only when the existing
activity has `stravaImportVersion="2"` **and** its `stravaSummaryHash` matches
the newly listed modeled summary. New or changed summaries trigger an import
into the stable activity permanode. The completion version is written last,
after all attachments and attributes succeed. The account's
`completedVersion="2"` marks completion of the selected enumeration window
(capped or complete), not a reason to stop future enumeration.

`SupportsIncremental=true` describes incremental **detail/stream fetching**,
not a server-side modified-since listing API or a complete synchronization
guarantee. Unchanged completed nodes are not routinely refreshed. Edits that
do not alter modeled summary fields, such as a description-only edit, may be
missed. Source deletions and older track content are not automatically removed;
Perkeep may retain historical blobs and claims even after an attachment changes.
Paging during concurrent source changes can also miss an activity until a
later run.

To force one activity to fetch its current detail and streams on the next run:

```
pk-put attr --del <activity-permanode-ref> stravaImportVersion
```

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

A fresh ten-activity test import normally needs **21 REST requests**:
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
