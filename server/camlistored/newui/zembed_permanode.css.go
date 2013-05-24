// THIS FILE IS AUTO-GENERATED FROM permanode.css
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("permanode.css", 606, fileembed.String(".cam-permanode-page {\n"+
		"  font: 16px/1.4 normal Arial, sans-serif;\n"+
		"}\n"+
		".cam-permanode-nav:before {\n"+
		"  content: \"[\";\n"+
		"}\n"+
		".cam-permanode-nav:after {\n"+
		"  content: \"]\";\n"+
		"}\n"+
		".cam-permanode-del {\n"+
		"  text-decoration: underline;\n"+
		"  cursor: pointer;\n"+
		"  color: darkred;\n"+
		"  margin-left: .4em;\n"+
		"  font-size: 80%;\n"+
		"}\n"+
		".cam-permanode-tag-c {\n"+
		"  margin-right: .5em;\n"+
		"}\n"+
		".cam-permanode-tag {\n"+
		"  font-style: italic;\n"+
		"}\n"+
		".cam-permanode-dnd {\n"+
		"  border: 2px dashed black;\n"+
		"  min-height: 250px;\n"+
		"  padding: 10px;\n"+
		"}\n"+
		".cam-permanode-dnd-item {\n"+
		"  margin: 0.25em;\n"+
		"  border: 1px solid #888;\n"+
		"  padding: 0.25em;\n"+
		"}\n"+
		".cam-permanode-dnd-over {\n"+
		"  background: #eee;\n"+
		"}"), time.Unix(0, 1369665728944011422))
}
