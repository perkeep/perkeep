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

package strava

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"perkeep.org/pkg/importer"
)

const (
	attrImportThrough            = "stravaImportThrough"
	attrImportThroughVersion     = "stravaImportThroughVersion"
	attrBackfillCompletedVersion = "stravaBackfillCompletedVersion"

	progressCheckpointActivities = 100
	activityReconciliationWindow = 7 * 24 * time.Hour
)

// As with the OAuth locks, stripes do not retain state for deleted accounts.
// Runs and OAuth operations need distinct locks: a run calls client repeatedly.
// A token channel also lets a cancelled waiter leave before the active run ends.
var importRunGates = func() [64]chan struct{} {
	var gates [64]chan struct{}
	for i := range gates {
		gates[i] = make(chan struct{}, 1)
		gates[i] <- struct{}{}
	}
	return gates
}()

func importRunGate(acct *importer.Object) chan struct{} {
	return importRunGates[acct.PermanodeRef().Sum32()%uint32(len(importRunGates))]
}

// importProgress is authoritative account state, never a scratch-cache entry.
// through is an activity start time, not the wall clock or a pagination cursor.
// An absent through with backfillDone represents a successfully empty history.
type importProgress struct {
	through      time.Time
	backfillDone bool
}

func readImportProgress(acct *importer.Object) (importProgress, error) {
	readScalar := func(key string) (string, error) {
		vs := acct.Attrs(key)
		if len(vs) > 1 || len(vs) == 1 && vs[0] == "" {
			return "", fmt.Errorf("strava: invalid account progress attribute %s", key)
		}
		if len(vs) == 1 {
			return vs[0], nil
		}
		return "", nil
	}
	version, err := readScalar(attrImportThroughVersion)
	if err != nil {
		return importProgress{}, err
	}
	done, err := readScalar(attrBackfillCompletedVersion)
	if err != nil {
		return importProgress{}, err
	}
	// Never infer chronological progress from completedVersion: capped imports
	// also set it. Unknown schema versions require a full reconciliation, even
	// if some activity permanodes already have the current activity version.
	// Their timestamp payload is opaque: do not apply the current schema's
	// scalar/date validation before making this compatibility decision.
	if version != "" && version != runCompleteVersion || done != "" && done != runCompleteVersion {
		return importProgress{}, nil
	}
	raw, err := readScalar(attrImportThrough)
	if err != nil {
		return importProgress{}, err
	}
	if version == "" && (raw != "" || done != "") {
		return importProgress{}, fmt.Errorf("strava: account progress requires %s", attrImportThroughVersion)
	}
	var through time.Time
	if raw != "" {
		through, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil || through.IsZero() {
			return importProgress{}, fmt.Errorf("strava: invalid account progress attribute %s: expected nonzero RFC3339 activity time", attrImportThrough)
		}
		through = through.UTC()
	}
	return importProgress{through: through, backfillDone: done == runCompleteVersion}, nil
}

func (p importProgress) boundary() time.Time {
	if p.backfillDone && !p.through.IsZero() {
		// This is a local reconciliation policy, not a source edit guarantee.
		return p.through.Add(-activityReconciliationWindow)
	}
	return p.through
}

// writeImportProgress publishes the watermark and its schema binding together.
// A partial checkpoint also removes any stale backfill marker (notably during
// schema reconciliation); a final checkpoint marks completion atomically.
// SetAttrsWithDeletes skips unchanged operations, including steady-state polls.
func writeImportProgress(acct *importer.Object, p importProgress, complete bool) error {
	sets := []string{attrImportThroughVersion, runCompleteVersion}
	var deletes []string
	if p.through.IsZero() {
		deletes = append(deletes, attrImportThrough)
	} else {
		// An incompatible schema's opaque payload may not have been scalar.
		// Reset it atomically even if its first value equals our new timestamp:
		// SetAttrsWithDeletes otherwise skips a matching first value.
		if len(acct.Attrs(attrImportThrough)) > 1 {
			deletes = append(deletes, attrImportThrough)
		}
		sets = append(sets, attrImportThrough, p.through.UTC().Format(time.RFC3339Nano))
	}
	if p.backfillDone || complete {
		sets = append(sets, attrBackfillCompletedVersion, runCompleteVersion)
	} else {
		deletes = append(deletes, attrBackfillCompletedVersion)
	}
	if complete {
		sets = append(sets, importer.AcctAttrCompletedVersion, runCompleteVersion)
	}
	if err := acct.SetAttrsWithDeletes(deletes, sets...); err != nil {
		return fmt.Errorf("strava: saving chronological progress: %w", err)
	}
	return nil
}

// discoverActivities must finish before importing anything. A partially listed
// history cannot establish its oldest contiguous prefix. Ask the API to bound
// activity times, but do not assume its offset pages are globally time-ordered.
// Enumerate the entire selected range, restarting at page one and deduplicating
// shifted pages by ID, not by position. Filter locally too: the API's strict
// whole-second bound can include activities just before our inclusive boundary.
func (r *run) discoverActivities(boundary time.Time) ([]activity, error) {
	var summaries []activity
	seen := make(map[int64]string)
	for page := 1; ; page++ {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		client, err := r.client()
		if err != nil {
			return nil, err
		}
		listed, err := client.listActivitiesSince(r.Context(), page, 100, boundary)
		if err != nil {
			return nil, err
		}
		if len(listed) == 0 {
			break
		}
		for _, a := range listed {
			if a.ID <= 0 || a.StartDate.IsZero() {
				return nil, errors.New("strava: invalid activity identity or start date in list response")
			}
			hash := summaryHash(&a)
			if old, ok := seen[a.ID]; ok {
				if hash != old {
					return nil, fmt.Errorf("strava: activity %d changed during discovery; retry listing", a.ID)
				}
				continue
			}
			seen[a.ID] = hash
			if !boundary.IsZero() && a.StartDate.Before(boundary) {
				continue
			}
			summaries = append(summaries, a)
		}
	}
	// Equal timestamps can span pages and are a single checkpoint unit.
	sort.SliceStable(summaries, func(i, j int) bool {
		if summaries[i].StartDate.Equal(summaries[j].StartDate) {
			return summaries[i].ID < summaries[j].ID
		}
		return summaries[i].StartDate.Before(summaries[j].StartDate)
	})
	return summaries, nil
}

func (r *run) importChronologicalActivities(parent *importer.Object, progress importProgress) (err error) {
	summaries, err := r.discoverActivities(progress.boundary())
	if err != nil {
		return err
	}
	considered := 0
	checkpointFailed := false
	checkpoint := func(complete bool) error {
		if err := writeImportProgress(r.AccountNode(), progress, complete); err != nil {
			checkpointFailed = true
			return err
		}
		considered = 0
		return nil
	}
	defer func() {
		// Persist only whole, successfully considered timestamp groups. Failed
		// claims are not retried here or hidden behind a later successful write.
		if err != nil && considered > 0 && !checkpointFailed {
			err = errors.Join(err, checkpoint(false))
		}
	}()
	for first := 0; first < len(summaries); {
		last := first + 1
		for last < len(summaries) && summaries[first].StartDate.Equal(summaries[last].StartDate) {
			last++
		}
		for i := first; i < last; i++ {
			if err := r.importActivity(parent, &summaries[i]); err != nil {
				return fmt.Errorf("strava: activity %d: %w", summaries[i].ID, err)
			}
		}
		if err := r.Context().Err(); err != nil {
			return err
		}
		// Only a completed group can extend the contiguous prefix. In the
		// reconciliation overlap, already-persisted progress never regresses.
		if t := summaries[first].StartDate; t.After(progress.through) {
			progress.through = t
		}
		considered += last - first
		if considered >= progressCheckpointActivities && last < len(summaries) {
			if err := checkpoint(false); err != nil {
				return err
			}
		}
		first = last
	}
	if err := r.Context().Err(); err != nil {
		return err
	}
	return checkpoint(true)
}
