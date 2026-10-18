/*
Copyright 2026 The Perkeep Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package nodeattr

// Fitness activity permanodes use the provider-independent attributes below.
// See doc/schema/fitness.md. Numeric values are decimal strings; an absent
// optional value is omitted, not represented by zero.
const (
	// FitnessActivity is a value of Type, not an attribute name.
	FitnessActivity = "fitnessActivity"

	// FitnessActivityType is a broad activity category: run, walk, hike, ride,
	// swim, row, ski, workout, or other.
	FitnessActivityType = "fitnessActivityType"

	// FitnessDistance is the distance traveled, in meters.
	FitnessDistance = "fitnessDistance"

	// FitnessMovingDuration is the moving duration, in seconds.
	FitnessMovingDuration = "fitnessMovingDuration"

	// FitnessElapsedDuration is the elapsed duration, in seconds.
	FitnessElapsedDuration = "fitnessElapsedDuration"

	// FitnessRecordedDuration is a provider's recorded duration, in seconds,
	// when its pause/moving-time semantics are not known.
	FitnessRecordedDuration = "fitnessRecordedDuration"

	// FitnessElevationGain is the total elevation gained, in meters.
	FitnessElevationGain = "fitnessElevationGain"

	// FitnessAverageHeartRate is the average heart rate, in beats per minute.
	FitnessAverageHeartRate = "fitnessAverageHeartRate"

	// FitnessMaxHeartRate is the maximum heart rate, in beats per minute.
	FitnessMaxHeartRate = "fitnessMaxHeartRate"

	// FitnessEnergy is the energy expended, in joules.
	FitnessEnergy = "fitnessEnergy"

	// FitnessTrack is the blobref of a file schema containing a GPX 1.1 track.
	FitnessTrack = "fitnessTrack"

	// FitnessSource identifies the source provider, for example "strava".
	FitnessSource = "fitnessSource"

	// FitnessSourceID is the provider's stable activity identifier.
	FitnessSourceID = "fitnessSourceID"
)
