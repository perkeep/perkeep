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

package server

import (
	"errors"
	"strings"
	"testing"

	"perkeep.org/pkg/health"
)

func TestHealthHTMLStates(t *testing.T) {
	mon := health.New()
	mon.Register("test.ok[x]", "x").OK()
	mon.Register("test.warn[x]", "x").Warn(errors.New("hmm"))
	mon.Register("test.fail[x]", "x").Fatal(errors.New("broken"))
	mon.Register("test.running[x]", "x").Start()
	mon.Register("test.pending[x]", "x")
	var buf strings.Builder
	(&StatusHandler{prefix: "/status/", health: mon}).writeHealthHTML(&buf, mon.Status())
	page := buf.String()
	for _, want := range []string{">ok</td>", ">⚠️ warning</td>", ">❌ failed (fatal)</td>", ">⏳ running</td>", ">pending</td>", "The server is read-only"} {
		if !strings.Contains(page, want) {
			t.Errorf("health HTML doesn't contain %q:\n%s", want, page)
		}
	}
}
