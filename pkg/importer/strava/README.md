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

## Stored data

Each account's import root has an `activities` child. Its `camliPath:<decimal ID>`
attributes point to stable activity permanodes, so the layout is
`root/activities/<decimal ID>`. Reimports reuse the same activity permanode.

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
| `stravaImportVersion` | `"1"` only after that activity's attributes and attachments are complete. |

Attachments are also browsable as files in the directory UI and FUSE:
`camliPath:activity.json`, `camliPath:streams.json`, and `camliPath:track.gpx`
point to stable child permanodes whose `title` is the filename and whose
`camliContent` is the file-schema blobref. The activity's attachment attributes
above and `fitnessTrack` still point directly to file schemas, not those children.

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
activity has `stravaImportVersion="1"` **and** its `stravaSummaryHash` matches
the newly listed modeled summary. New or changed summaries trigger an import
into the stable activity permanode. The completion version is written last,
after all attachments and attributes succeed. The account's
`completedVersion="1"` marks completion of an entire enumeration, not a reason
to stop future enumeration.

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

Strava applies [short-window and daily rate limits](https://developers.strava.com/docs/rate-limits/).
An HTTP 429 or another request/import error safely returns an error; the importer
does not automatically retry or sleep through a limit. Rerun after resolving
the error or waiting for the relevant window. Successfully completed activities
remain skippable; an incomplete activity is retried.

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
