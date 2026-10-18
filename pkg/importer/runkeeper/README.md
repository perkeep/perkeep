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
in `runkeeperLocalDate`. Only explicitly zoned GPX track/point times establish
UTC `startDate`; the server's local timezone is never used. Missing or unusable
tracks do not prevent activity metadata import.

Strictly valid, renderable GPX 1.1 originals are referenced directly by
`fitnessTrack`. Malformed XML, nested segments, invalid coordinates/times,
tracks above 16 MiB, or tracks above 200,000 points produce warnings and omit
`fitnessTrack`, preserving the original in `runkeeperSourceGPX`. No repairs or
invented points are performed. A GPX without timestamps can still render but
does not establish a start instant.

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
sensor rows. It is independent of ZIP ordering, container timestamps, unrelated
activities, and the overall ZIP hash. Even malformed GPX tail changes count.
`runkeeperImportVersion=1` marks completion. All attachments are uploaded first;
one atomic claim then publishes modeled attributes, removes now-absent optional
attributes, and publishes hash/version last. Failures retain the previous
complete activity; retry uses the same permanode. Unchanged activities create
no claims or attachment blobs. Run status/history claims are separate.

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
PERKEEP_RUNKEEPER_TEST_ARCHIVE=/absolute/path/to/private-export.zip \
  go test ./pkg/importer/runkeeper -run TestPersonalArchive -v
```

The opt-in private test expects the separately inspected 855-activity export;
it never commits or prints its personal rows. It checks source preservation,
855 distinct activities, 844 usable tracks, 850 per-activity original-track
references (three shared filenames), five photos, 11 unzoned activities, and
zero additional blobs on repeat. Ordinary tests use synthetic data, exercise
failed-upload/claim retries and incremental edits, and stream a generated
250,000-row sensor member while checking sampled live heap and spool bounds.
