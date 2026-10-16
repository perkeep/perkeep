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

package s3

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"perkeep.org/pkg/health"
	"perkeep.org/pkg/test"

	"go4.org/jsonconfig"
)

func TestLayoutCheck(t *testing.T) {
	const (
		blob1 = "sha1-0000000a84aa1c10c44f0ca9365303d1b329d066"
		blob2 = "sha224-00000000000000000000000000000000000000000000000000000000"
	)
	tests := []struct {
		name      string
		bucket    string // the storage's "bucket" config, with any directory
		keys      []string
		wantState health.State
		wantMsg   string
	}{
		{
			name:      "root_only",
			bucket:    "b",
			keys:      []string{blob1, blob2},
			wantState: health.StateOK,
		},
		{
			name:      "root_with_loose",
			bucket:    "b",
			keys:      []string{blob1, "loose/" + blob2},
			wantState: health.StateFailed,
			wantMsg:   `doesn't read the blobs under "/loose/"`,
		},
		{
			name:      "root_with_only_packed",
			bucket:    "b",
			keys:      []string{"packed/" + blob2},
			wantState: health.StateFailed,
			wantMsg:   `doesn't read the blobs under "/packed/"`,
		},
		{
			name:      "loose_only",
			bucket:    "b/loose",
			keys:      []string{"loose/" + blob1, "packed/" + blob2},
			wantState: health.StateOK,
		},
		{
			name:      "loose_with_root",
			bucket:    "b/loose",
			keys:      []string{blob1, "loose/" + blob2},
			wantState: health.StateFailed,
			wantMsg:   `doesn't read the loose blobs at "/"`,
		},
		{
			name:      "packed_with_root",
			bucket:    "b/packed",
			keys:      []string{blob1, "packed/" + blob2},
			wantState: health.StateFailed,
			wantMsg:   `doesn't read the loose blobs at "/"`,
		},
		{
			name:      "loose_with_other_root_files",
			bucket:    "b/loose",
			keys:      []string{"README", "loose/" + blob1},
			wantState: health.StateOK,
		},
		{
			name:      "loose_in_dir_with_blobs_above_dir",
			bucket:    "b/blobs/loose",
			keys:      []string{blob1, "blobs/loose/" + blob2},
			wantState: health.StateOK,
		},
		{
			name:      "loose_in_dir_with_dir_blobs",
			bucket:    "b/blobs/loose",
			keys:      []string{"blobs/" + blob1, "blobs/loose/" + blob2},
			wantState: health.StateFailed,
			wantMsg:   `doesn't read the loose blobs at "/blobs/"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				keys := slices.Clone(tt.keys)
				slices.Sort(keys)
				mon := health.New()
				_, err := newFromConfigWithTransport(&test.Loader{HealthMonitor: mon}, jsonconfig.Obj{
					"aws_access_key":        "key",
					"aws_secret_access_key": "secret",
					"bucket":                tt.bucket,
					"hostname":              "s3.example",
					"cacheSize":             float64(0),
				}, &listingS3{keys: keys})
				if err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				st := mon.Status()
				if len(st.Checks) != 1 || st.Checks[0].ID != "storage.s3.layout[lies]" {
					t.Fatalf("checks = %+v; want one, storage.s3.layout[lies]", st.Checks)
				}
				c := st.Checks[0]
				if c.State != tt.wantState || !strings.Contains(c.Message, tt.wantMsg) {
					t.Errorf("check = %v %q; want %v containing %q", c.State, c.Message, tt.wantState, tt.wantMsg)
				}
				if wantRO := tt.wantState == health.StateFailed; st.ReadOnly != wantRO {
					t.Errorf("read-only = %v; want %v", st.ReadOnly, wantRO)
				}
			})
		})
	}
}

func TestLayoutCheckSkipped(t *testing.T) {
	mon := health.New()
	_, err := newFromConfigWithTransport(&test.Loader{HealthMonitor: mon}, jsonconfig.Obj{
		"aws_access_key":        "key",
		"aws_secret_access_key": "secret",
		"bucket":                "b",
		"hostname":              "s3.example",
		"cacheSize":             float64(0),
		"skipStartupCheck":      true,
	}, &listingS3{})
	if err != nil {
		t.Fatal(err)
	}
	if st := mon.Status(); len(st.Checks) != 0 {
		t.Errorf("checks = %+v; want none with skipStartupCheck", st.Checks)
	}
}
