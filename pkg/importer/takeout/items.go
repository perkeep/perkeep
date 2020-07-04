/*
Copyright 2020 The Perkeep Authors

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

package takeout

import "html"

type item interface {
	Title() string
	TextContent() string
	Timestamp() int64
	Service() string
}

type annotation interface {
	URL() string
	Description() string
	Title() string
	Source() string
}

// Schema for notes
type noteItem struct {
	NTitle       string `json:"title"`
	NTextContent string `json:"textContent"`
	NTimestamp   int64  `json:"userEditedTimestampUsec"`
	/* NAnnotations string `json:annotations`
	NTrashed     bool   `json:trashed`
	NArchived    bool   `json:archived`
	NPinned      bool   `json:pinned`
	NColor       string `json:color` */
}

func (i *noteItem) Title() string {
	return i.NTitle
}

func (i *noteItem) TextContent() string { return html.UnescapeString(i.NTextContent) }
func (i *noteItem) Timestamp() int64    { return i.NTimestamp / 1000000 }
func (i *noteItem) Service() string     { return "Google Keep" } //TODO official name? Formerly Google Keep, now Google Notizen in German
