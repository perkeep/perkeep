/*
Copyright 2026 The Perkeep Authors.

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

package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/test"
)

// archiveWorld adds an importer at a non-default prefix without changing the
// configuration shared by the other integration tests.
func archiveWorld(t *testing.T) *test.World {
	t.Helper()
	base, err := test.NewWorld()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(base.SourceRoot(), "pkg/test/testdata/server-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	// Browser requests must authenticate even on localhost: otherwise a bad
	// discovery token could accidentally pass through the local-user bypass.
	config["auth"] = "userpass:testuser:passTestWorld"
	prefixes := config["prefixes"].(map[string]any)
	// Archive tests need only perkeepd and pk-import-runkeeper, not the
	// unrelated WebDAV app.
	delete(prefixes, "/webdav/")
	prefixes["/manual-imports/"] = map[string]any{"handler": "importer", "handlerArgs": map[string]any{}}
	prefixes["/browser-files/"] = map[string]any{"handler": "ui", "handlerArgs": map[string]any{}}
	data, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "archive-server.json")
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(filepath.Join(base.SourceRoot(), "pkg/test/testdata"), name)
	if err != nil {
		t.Fatal(err)
	}
	w, err := test.WorldFromConfig(rel)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Stop)
	return w
}

func runkeeperFixture(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	files := map[string]string{
		"profile.json": `{"username":"runner","email":"Optional[runner@example.com]","name":"Test Runner"}`,
		"cardioActivities.csv": "Activity Id,Date,Type,Route Name,Distance (mi),Duration,Average Pace,Average Speed (mph),Calories Burned,Climb (ft),Average Heart Rate (bpm),Friend's Tagged,Notes,GPX File\n" +
			"11111111-1111-4111-8111-111111111111,2023-09-20 13:50:08,Running,,1.00,10:00,,,100,10,,,,track.gpx\n" +
			"22222222-2222-4222-8222-222222222222,2023-09-19 13:50:08,Cycling,,2.00,10:00,,,100,10,,,,track.gpx\n" +
			"33333333-3333-4333-8333-333333333333,2023-09-18 13:50:08,Running,,1.00,10:00,,,100,10,,,,\n" +
			"44444444-4444-4444-8444-444444444444,2023-09-17 13:50:08,Walking,,1.00,10:00,,,100,10,,,,broken.gpx\n",
		"track.gpx":  `<?xml version="1.0"?><gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><time>2023-09-20T20:50:08Z</time><trkseg><trkpt lat="47.6" lon="-122.3"><time>2023-09-20T20:50:08Z</time></trkpt><trkpt lat="47.61" lon="-122.31"/></trkseg></trk></gpx>`,
		"broken.gpx": `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><trkseg><trkseg></trk></gpx>`,
		"steps.csv":  "Activity Id, Timestamp, Steps\n11111111-1111-4111-8111-111111111111,0,5\n",
	}
	for name, data := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "runkeeper-data-export-123.zip")
	if err := os.WriteFile(name, out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestRunkeeperCLI(t *testing.T) {
	testRunkeeperCLI(t, runkeeperFixture(t), 4)
}

// Personal exports stay outside the repository. This optional check runs the
// actual CLI and server against a supplied export, including a second import.
func TestRunkeeperPersonalCLI(t *testing.T) {
	name := os.Getenv("PERKEEP_RUNKEEPER_TEST_ARCHIVE")
	if name == "" {
		t.Skip("set PERKEEP_RUNKEEPER_TEST_ARCHIVE to validate a personal export")
	}
	z, err := zip.OpenReader(name)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	f, err := z.Open("cardioActivities.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows := csv.NewReader(f)
	if _, err := rows.Read(); err != nil {
		t.Fatal(err)
	}
	count := 0
	for {
		if _, err := rows.Read(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		count++
	}
	testRunkeeperCLI(t, name, count)
}

func testRunkeeperCLI(t *testing.T, filename string, want int) {
	t.Helper()
	w := archiveWorld(t)
	c, err := client.New(client.OptionServer(w.ServerBaseURL()), client.OptionNoExternalConfig(),
		client.OptionAuthMode(auth.NewBasicAuth("testuser", "passTestWorld")))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	query := func(expr string) *search.SearchResult {
		t.Helper()
		result, err := c.Query(ctx, &search.SearchQuery{Expression: expr, Limit: -1})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := w.Cmd("pk-import-runkeeper", args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("pk-import-runkeeper: %v\n%s\n%s", err, out, &stderr)
		}
		t.Logf("pk-import-runkeeper: %s%s", out, &stderr)
		return string(out)
	}
	run("-inspect", filename)
	if got := len(query("attr:camliNodeType:importerAccount").Blobs); got != 0 {
		t.Fatalf("inspection created %d accounts", got)
	}
	run(filename)
	activities := query("is:fitness").Blobs
	if len(activities) != want {
		t.Fatalf("activities = %d, want %d", len(activities), want)
	}
	if got := len(query("is:run").Blobs); got == 0 {
		t.Fatal("is:run did not match imported activities")
	}
	if got := len(query("is:bike").Blobs); got == 0 {
		t.Fatal("is:bike did not match imported activities")
	}
	claimCounts := make(map[blob.Ref]int)
	for _, b := range activities {
		claims, err := c.GetClaims(ctx, &search.ClaimsRequest{Permanode: b.Blob})
		if err != nil {
			t.Fatal(err)
		}
		claimCounts[b.Blob] = len(claims.Claims)
	}
	accounts := query("attr:camliNodeType:importerAccount").Blobs
	if len(accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(accounts))
	}
	status, err := c.ArchiveImportStatus(ctx, "runkeeper", accounts[0].Blob)
	if err != nil {
		t.Fatal(err)
	}
	// Repeat the ordinary local-file path as well as the retained-ref path.
	// Whole-file reuse must preserve identity and must not disturb activities.
	run(filename)
	run("-fileref=" + status.FileRef.String())
	status, err = c.ArchiveImportStatus(ctx, "runkeeper", accounts[0].Blob)
	if err != nil || status.Progress.ItemsUnchanged != want {
		t.Fatalf("second import status = %v, %v; want %d unchanged", status, err, want)
	}
	if got := len(query("is:fitness").Blobs); got != want {
		t.Fatalf("repeat import created duplicates: %d activities", got)
	}
	for ref, n := range claimCounts {
		claims, err := c.GetClaims(ctx, &search.ClaimsRequest{Permanode: ref})
		if err != nil {
			t.Fatal(err)
		}
		if len(claims.Claims) != n {
			t.Errorf("repeat import added claims on %s: %d -> %d", ref, n, len(claims.Claims))
		}
	}
	root, err := c.ImporterRoot()
	if err != nil || !strings.HasSuffix(root, "/manual-imports/") {
		t.Fatalf("importer discovery = %q, %v", root, err)
	}
}
