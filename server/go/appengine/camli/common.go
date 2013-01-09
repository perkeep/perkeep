// +build appengine

/*
Copyright 2011 Google Inc.

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

package appengine

import (
	"fmt"
	"strings"
)

func sanitizeNamespace(ns string) (outns string, err error) {
	outns = ns
	switch {
	case strings.Contains(ns, "|"):
		err = fmt.Errorf("no pipe allowed in namespace %q", ns)
	case strings.Contains(ns, "\x00"):
		err = fmt.Errorf("no zero byte allowed in namespace %q", ns)
	case ns == "-":
		err = fmt.Errorf("reserved namespace %q", ns)
	case ns == "":
		outns = "-"
	}
	return
}
