# Runkeeper archive importer

Imports a Runkeeper account export ZIP locally, without API credentials or
requests to Runkeeper. Browser and command-line imports use the same archive
service and account matching.

## Browser

Open **Importers** in the Web UI and select **Runkeeper**:

1. Choose the account export ZIP and click **Upload ZIP**.
2. Check the detected username/email and whether an existing account will be
   reused, then click **Import archive**.
3. The account page shows progress, unchanged counts, warnings, and errors.

Reloading the account page resumes status monitoring, not the import itself.
After an error or server interruption, **Retry import of stored ZIP** reuses
the retained file. **Reimport ZIP** explicitly reconciles a completed input.
**Upload another ZIP** returns to identity inspection; it does not blindly
attach a different person's archive to the current account.

The browser sends the native file through the discovered streaming upload
helper, without reading the ZIP into JavaScript memory or parsing it locally.
It does not perform the CLI's whole-file hash precheck or resume a partially
transferred upload. Once uploading succeeds, inspection and import retries
reuse the file-schema reference without another transfer.

**Cancel upload** stops the browser transfer; **Cancel inspection** stops
inspection while retaining an already uploaded ZIP. Neither cancels an
accepted server import. Closing the page also leaves an accepted import
running; use **Pause Import** on the account page to request cancellation.
No OAuth login, empty-account setup, or automatic polling controls are needed.

## Command line

```
pk-import-runkeeper /path/to/runkeeper-data-export-1234-2026-10-15-141435.zip
pk-import-runkeeper --server=https://your-perkeep-server/ /path/to/export.zip
pk-import-runkeeper -inspect /path/to/export.zip
pk-import-runkeeper -fileref=<file-schema-blobref>
```

Build it with `go run make.go` or `go install ./cmd/pk-import-runkeeper`.
The standalone command keeps archive-command code out of `pk`;
the server importer and archive API are shared with the browser upload UI.

The CLI waits for completion and reports counts and warnings. The uploaded ZIP
is retained in the account's archive history, so disconnecting the CLI does not
lose an accepted input. Explicitly retry the same ZIP after an error; completed,
unchanged activities are skipped. The archive service serializes account
resolution and managed runs. OAuth and automatic API polling are not used.

## Identity and layout

The archive must contain `profile.json` and `cardioActivities.csv` at its root.
Identity comes from profile username **or** email, not its filename. Trim both
and unwrap the export's `Optional[...]` representation. Shared account matching
uses case-sensitive usernames and case-insensitive emails; matching both fields
on one account is one candidate. No candidate creates an account, one reuses it,
and multiple candidates fail unless the filename's advisory numeric ID matches
exactly one candidate's already stored ID. A renamed ZIP cannot overwrite a
conflicting stored ID or establish identity by itself.

Activities use `root/activities/<lowercase activity UUID>`, not dates or GPX
filenames. Shared GPX filenames therefore do not collapse distinct activities.
Absent activities in later exports are not deleted. Strava activities are not
merged with Runkeeper activities.

## Shared fitness attributes

Writes ordinary `fitnessActivity` permanodes with `fitnessSource=runkeeper`
and the original UUID as `fitnessSourceID`, consumed by the existing fitness
search, card, and route renderer. `Running`, `Walking`, `Cycling`, and `Hiking`
map to `run`, `walk`, `ride`, and `hike`; `Wheelchair` and unrecognized sports
map to `other`. The exact original type stays in `runkeeperActivityType`.

Distance columns explicitly identify miles, kilometres, or metres; climb
columns identify feet or metres. Values become SI metres, calories become
joules (×4,184), and heart rate remains bpm. Unknown optional values are absent;
known zeroes remain zeroes. Units are never inferred from profile preferences.

CSV `Duration` supports `minutes:seconds` and `hours:minutes:seconds`, including
more than 24 hours and fractional seconds. It becomes `fitnessRecordedDuration`:
the provider-reported timer, with unspecified pause handling. It is **not**
labeled moving or elapsed duration, and no duration is invented from GPX sample
bounds. The generic fitness card can use this duration as its fallback.

Route name becomes `title`, notes become `description`, and tagged friends stay
in `runkeeperFriendsTagged`. The source wall-clock date is retained verbatim
in `runkeeperLocalDate`; search continues to use the ordinary `startDate`
attribute, not a provider-specific date fallback.

`startDate` is populated from the best available source, in this order:

1. An explicitly zoned GPX track or point time, normalized to UTC, even when
   the route itself cannot be rendered.
2. An explicitly zoned RFC3339 CSV date, normalized to UTC.
3. An unzoned CSV `YYYY-MM-DD HH:MM:SS` or local ISO `YYYY-MM-DDTHH:MM:SS`
   (including fractional seconds), or a date-only `YYYY-MM-DD`. Its wall-clock
   fields are encoded as UTC for sorting; date-only values use midnight.

`runkeeperStartDateSource` records `gpx`, `csv`, or `csv-assumed-utc`,
respectively. **`csv-assumed-utc` is an approximate sorting placeholder, not
a known UTC instant or a discovered source timezone.** The server's local
timezone is never used. The importer warns when it needs this fallback.
If neither source supplies a usable date, it warns and imports an undated
activity rather than inventing today's date. Missing or unusable tracks do
not prevent activity metadata import.

Strictly valid, renderable GPX 1.1 originals are referenced directly by
`fitnessTrack`, without repair attributes. The importer automatically repairs
one known Runkeeper export defect for every user: a new `trkseg` starts while
the previous segment is still open at `gpx/trk/trkseg`. It inserts the missing
closing tag immediately before the new segment; it never merges segments,
invents points, or changes other source bytes. There is no opt-in setting or
private-archive-specific exception.

The repaired candidate must pass the same strict GPX namespace, coordinate,
time, point-count, and XML validation as an original. Only then does
`fitnessTrack` reference a **separate derived file**,
`runkeeper-repaired-v1.gpx`. `runkeeperSourceGPX` always retains the unmodified
original. `runkeeperGPXRepairVersion=1` identifies the repair algorithm and
`runkeeperGPXRepairCount` records the decimal number of inserted closes.
Successful repairs produce an explicit warning, not a silent source rewrite.

Balanced nested segments (which would leave an extra original close), other
XML damage, wrong namespaces on GPX structural elements, invalid
coordinates/times, and resource-limit violations remain unusable. Repair also
refuses to close a segment with its own namespace declarations: moving its
close could change the next segment's inherited bindings. GPX prefixes
declared on the root or track are supported. Rejected originals are still
attached, with a warning and no `fitnessTrack` or coordinates.

Repair uses a disposable private disk file, with at most 16 MiB each of input
and derived output, XML depth 64, 200,000 points, and 128-byte time text. GPX is
processed incrementally; only inspection metadata is cached, not repaired
files. Temporary files are removed on success or failure.

A valid GPX time encountered before a later XML or route-validation failure is
retained, including when a repaired route is available. When needed, a
separate recovery scan reads at most the first 256 KiB, requires a GPX 1.1
root/namespace, and accepts only explicitly zoned track/point times. It
tolerates bad coordinates or extra nested segments for date recovery only,
with a maximum XML depth of 64 and time text of 128 bytes. Date recovery alone
never makes an invalid route renderable. A GPX without timestamps can still
render; the CSV date policy then applies.

## Preserving source data

The retained original ZIP is the lossless baseline, including unrecognized
members, orphan records, original CSV formatting, and unknown profile fields.
Useful file-schema references supplement it:

| Location | Attribute | Source |
| --- | --- | --- |
| Account | `runkeeperProfileJSON` | Original `profile.json`. |
| Account | `runkeeperCardioCSV` | Original `cardioActivities.csv`. |
| Account | `runkeeperPhotosCSV` | Original `photos.csv`, when present. |
| Account | `runkeeperMeasurementsCSV` | Original `measurements.csv`, when present. |
| Account | `runkeeperStepsCSV`, `runkeeperHeartRateCSV` | Original sensor CSVs, including header-only tables. |
| Activity | `runkeeperActivityJSON` | All source activity columns and values, including unknown fields. |
| Activity | `runkeeperSourceGPX` | Unmodified original GPX, including unusable tracks. |
| Activity | `runkeeperPhotosJSON` | Photo source rows, content hashes, and original-image file-schema refs. |
| Activity | `runkeeperStepsJSONL`, `runkeeperHeartRateJSONL` | Associated sensor rows, one JSON object per line, preserving unknown columns. |

CSV header whitespace is normalized in derived JSON, not in original files.
Measurements remain raw: the export does not sufficiently establish their
units, even when profile preferences appear to suggest one. Photos are never
fetched from network URLs. There are no per-activity latest-ZIP attributes.

## Reconciliation and resource bounds

`runkeeperSourceHash` is a versioned SHA-256 fingerprint of the source activity
row plus its complete GPX bytes, associated photo rows/image bytes, and grouped
sensor rows, plus the applicable GPX repair algorithm version and derived
content hash. It is independent of ZIP ordering, container timestamps,
unrelated activities, and the overall ZIP hash. Even malformed GPX tail changes
count. `runkeeperImportVersion=3` marks completion. Explicit reimport of a
stored ZIP or importing the same ZIP again reconciles version-1 and version-2
activities on their existing UUID permanodes, adding dates or separate repaired
routes and provenance where possible. Future repair algorithm versions also
invalidate the affected fingerprints.
All attachments are uploaded first; one atomic claim then publishes modeled
attributes, including repair provenance, removes now-absent optional
attributes, and publishes hash/version last in that same claim. A failed derived
upload or claim retains the previous complete activity; retry uses the same
permanode and original attachments. Unchanged activities create no claims or
attachment blobs. When an input becomes valid, unrepairable, or loses its GPX,
stale repair-version/count attributes are removed with the modeled route update.
The date and its source marker are managed attributes: GPX removal can replace
them with a marked CSV fallback; if no date remains usable, both are removed.
Run status/history claims are separate.

CSV parsing streams with a 1 MiB logical-record limit, 128 columns, and 128 bytes
per header name. UTF-8 is required; modeled text fields are limited to 16 KiB.
Profile JSON is limited to 1 MiB, activity/photo/measurement CSVs to 64 MiB,
activity/photo counts to 100,000 each, original GPX/images to 64 MiB each, and
individual sensor CSVs to 4 GiB. The shared archive reader applies additional
entry, central-directory, and total actual-read budgets across reopened members.

Sensor grouping uses disposable private disk files, not an in-memory sample
collection, with at most 32 open buffered writers. **Actual encoded JSONL**
output is limited to 8 GiB total and 512 MiB per activity across both sensor
streams. Expanded photo metadata is budgeted before accumulation: at most
4 MiB per activity manifest and 32 MiB across the import, including allowance
for hashes/file refs. Header repetition and JSON escaping cannot bypass these
budgets. Temporary files are closed and removed on success, failure, or
cancellation; the retained ZIP is the durable retry input. Reaching a resource
limit fails the run rather than silently discarding associated source records.

At most 100 warning messages of at most 1,024 bytes are retained, with a separate
total count. Progress is batched every 100 activities and at start/completion.

## Tests and private validation

```
go test ./pkg/importer/runkeeper ./pkg/importer/allimporters
go test ./pkg/importer/runkeeper -run '^$' -bench BenchmarkRepairTrackRootAttributes
PERKEEP_RUNKEEPER_TEST_ARCHIVE=/absolute/path/to/private-export.zip \
  go test ./pkg/importer/runkeeper -run TestPersonalArchive -v
```

The opt-in private test expects the separately inspected 855-activity export;
it never commits or prints its personal rows. It checks source preservation,
855 distinct activities, 850 usable tracks (including six automatically
repaired routes), 850 per-activity original-track references (three shared
filenames), five photos, and 855 dated activities: 850 GPX-derived dates and
five marked CSV-assumed-UTC dates. It expects 11 warnings (six explicit repair
warnings and five CSV-date fallbacks) and zero additional blobs on repeat.
Ordinary tests use synthetic data, exercise strict repair and byte/segment
preservation, rejected repairs, date recovery, version-1/date and version-2/repair
migration, failed-derived-upload/atomic-claim retries and incremental edits, and
stream a generated 250,000-row sensor member while checking sampled live heap
and spool bounds.
The optional root-attribute benchmark holds point count fixed while increasing
unrelated GPX attributes; it exercises namespace lookup without a flaky timing
assertion.
