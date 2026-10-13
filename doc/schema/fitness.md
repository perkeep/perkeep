# Fitness Activities

A fitness activity is a permanode with `camliNodeType` set to
`fitnessActivity`. This is a permanode type, not a new schema-blob
`camliType`: attributes are recorded using ordinary [claims](permanode.md#claim).
The constants are defined in [`pkg/schema/nodeattr/fitness.go`](/pkg/schema/nodeattr/fitness.go).

These attributes describe activities independently of their source service.
A provider's original activity type, identifiers, flags, and raw data can be
kept in additional provider-specific attributes.

## Attributes

All attributes below are single-valued. Optional attributes are omitted when
their values are unavailable. A known zero is stored as `"0"`, and is distinct
from an absent value. Numeric values are finite decimal strings without unit
suffixes; integers and fractional values are both allowed. No units are
inferred from a user's locale or source-service preferences.

| Attribute | Value |
| --- | --- |
| `camliNodeType` | `fitnessActivity` |
| `fitnessActivityType` | Broad category: `run`, `walk`, `hike`, `ride`, `swim`, `row`, `ski`, `workout`, or `other`. |
| `fitnessDistance` | Distance traveled, in meters. |
| `fitnessMovingDuration` | Moving duration, in seconds. |
| `fitnessElapsedDuration` | Total elapsed duration, in seconds, including stops. |
| `fitnessElevationGain` | Total elevation gained, in meters. |
| `fitnessAverageHeartRate` | Average heart rate, in beats per minute. |
| `fitnessMaxHeartRate` | Maximum heart rate, in beats per minute. |
| `fitnessEnergy` | Energy expended, in joules. |
| `fitnessTrack` | Blobref of a [file schema](file.md) containing a GPX 1.1 track; not a ref to its raw content blob. |
| `fitnessSource` | Source-provider name, for example `strava`. |
| `fitnessSourceID` | Stable activity identifier supplied by that provider. Numeric identifiers use decimal strings. |

The broad activity categories intentionally do not encode every sport or
variant. For example, a virtual or mountain-bike ride is a `ride`. Preserve the
provider's more specific classification separately. `other` means a known
activity outside these categories; omit `fitnessActivityType` if the category
is unknown.

Reuse the existing attributes rather than creating fitness-specific aliases:

| Attribute | Meaning for a fitness activity |
| --- | --- |
| `title` | Activity name. |
| `description` | Plain-text activity description. |
| `startDate` | Start instant in RFC 3339, normalized to UTC (`Z`). |
| `latitude`, `longitude` | Start coordinates in decimal degrees, when known; zero is a valid coordinate. |
| `url` | Original activity URL. |

Preserve a provider's local timezone separately when available. It does not
change the meaning of the UTC `startDate`. Importers must convert quantities
to the units above; for example, a nutritional kilocalorie is 4,184 joules.

## Track and source data

[`fitnessTrack` uses GPX 1.1](https://www.topografix.com/GPX/1/1/), with WGS84
latitude/longitude, optional elevation in meters, and optional UTC point
timestamps. An importer can derive timestamps by adding each sample's elapsed
time to `startDate`. Activities without usable location samples need not have
a track.

GPX is a portable location-track representation, not a lossless replacement
for a provider's detailed activity or sensor streams. Preserve available raw
source data alongside it when authorized: heart rate, cadence, power, sampling
metadata, and provider-specific fields may not appear in the generated GPX.
Do not invent missing positions, sensor values, or timestamps.

An activity's permanode is its stable identity. Its track or raw attachments
can change without creating a new activity permanode. Distinct providers'
identifiers do not by themselves establish that two activities are duplicates.
This schema does not require a particular importer's directory layout or
authorize collection or retention of a provider's data.

## Web UI rendering

The web UI recognizes `fitnessActivity` without consulting provider-specific
attributes. Its thumbnail handler reads the referenced GPX locally and draws
a red route with an activity-type badge at the bottom left. Track segments
remain separate; missing GPS data has a neutral preview instead of an invented
route. Generated images belong in the thumbnail cache, not in the archived
activity's claims or as new permanodes.

Cards retain the activity's title and display `fitnessDistance` in kilometres
to one decimal place, plus duration when available. They also show miles to
one decimal place when the activity's start coordinates fall inside the rough
contiguous-US rectangle **24–50 degrees north, 125–66 degrees west**. This is
an intentionally approximate display heuristic, not a country-boundary test:
it excludes Alaska and Hawaii and includes some neighbouring territory.
Moving duration is preferred, with elapsed duration used when moving duration
is unavailable. Display units do not change the SI values stored in claims.

The default preview is route-only: rendering does not fetch a basemap or send
the track to an external service. A future basemap integration should be
explicitly configured and respect its provider's caching, attribution, and
privacy requirements. OpenStreetMap's public tile service is not an unrestricted
bulk-thumbnail service; see its
[tile usage policy](https://operations.osmfoundation.org/policies/tiles/).
