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

import (
	"html"
	"strings"
)

type item interface {
	Title() string
	Content() string
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
	NoteTitle     string      `json:"title"`
	TextContent   string      `json:"textContent,omitempty"`
	ListContent   []*listItem `json:"listContent,omitempty"`
	EditTimestamp int64       `json:"userEditedTimestampUsec"`
	/* NAnnotations string `json:annotations`
	NTrashed     bool   `json:trashed`
	NArchived    bool   `json:archived`
	NPinned      bool   `json:pinned`
	NColor       string `json:color` */
}

func (i *noteItem) Title() string {
	return i.NoteTitle
}

func (i *noteItem) Content() string {
	if len(i.ListContent) > 0 {
		var sb strings.Builder

		for _, item := range i.ListContent {
			sb.WriteString("\n *")
			sb.WriteString(html.UnescapeString(item.Text))
			if item.Checked {
				sb.WriteString(" [x]")
			} else {
				sb.WriteString(" [ ]")
			}
		}

		return sb.String()
	}

	return html.UnescapeString(i.TextContent)
}
func (i *noteItem) Timestamp() int64 { return i.EditTimestamp / 1000000 }
func (i *noteItem) Service() string  { return "Google Keep" } //TODO official name? Formerly Google Keep, now Google Notizen in German

type listItem struct {
	Text    string `json:"text"`
	Checked bool   `json:"isChecked"`
}
