// THIS FILE IS AUTO-GENERATED FROM camli.css
// DO NOT EDIT.
package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("camli.css", 2190, fileembed.String("/*\n"+
		"Copyright 2011 Google Inc.\n"+
		"\n"+
		"Licensed under the Apache License, Version 2.0 (the \"License\");\n"+
		"you may not use this file except in compliance with the License.\n"+
		"You may obtain a copy of the License at\n"+
		"\n"+
		"     http://www.apache.org/licenses/LICENSE-2.0\n"+
		"\n"+
		"Unless required by applicable law or agreed to in writing, software\n"+
		"distributed under the License is distributed on an \"AS IS\" BASIS,\n"+
		"WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.\n"+
		"See the License for the specific language governing permissions and\n"+
		"limitations under the License.\n"+
		"*/\n"+
		"\n"+
		"/* General CSS */\n"+
		"body {\n"+
		"  font: 16px/1.4 normal Arial, sans-serif;\n"+
		"}\n"+
		".camli-nav:before {\n"+
		"  content: \"[\";\n"+
		"}\n"+
		".camli-nav:after {\n"+
		"  content: \"]\";\n"+
		"}\n"+
		".camli-del {\n"+
		"  text-decoration: underline;\n"+
		"  cursor: pointer;\n"+
		"  color: darkred;\n"+
		"  margin-left: .4em;\n"+
		"  font-size: 80%;\n"+
		"}\n"+
		".camli-newp {\n"+
		"  text-decoration: underline;\n"+
		"  cursor: pointer;\n"+
		"  color: darkgreen;\n"+
		"  margin-left: .4em;\n"+
		"  font-size: 80%;\n"+
		"}\n"+
		".camli-tag-c {\n"+
		"  margin-right: .5em;\n"+
		"}\n"+
		".camli-tag {\n"+
		"  font-style: italic;\n"+
		"}\n"+
		".camli-dnd {\n"+
		"  border: 2px dashed black;\n"+
		"  min-height: 250px;\n"+
		"  padding: 10px;\n"+
		"}\n"+
		".camli-dnd-item {\n"+
		"  margin: 0.25em;\n"+
		"  border: 1px solid #888;\n"+
		"  padding: 0.25em;\n"+
		"}\n"+
		".camli-dnd-over {\n"+
		"  background: #eee;\n"+
		"}\n"+
		"\n"+
		"/* Bob info page */\n"+
		".camli-ui-blobinfo #blobdata {\n"+
		"  overflow: auto;\n"+
		"  max-width: 800px;\n"+
		"}\n"+
		"\n"+
		"/* Index page */\n"+
		".camli-ui-index #btnnew {\n"+
		"  font-size: 45px;\n"+
		"  font-family: sans-serif;\n"+
		"  padding: 0.25em;\n"+
		"  background: #008aff;\n"+
		"  color: #fff;\n"+
		"}\n"+
		"\n"+
		"/* tiled thumbnails */\n"+
		"#recent {\n"+
		"  padding: 0;\n"+
		"}\n"+
		"\n"+
		".camli-ui-thumb {\n"+
		"  margin: 0.25em;\n"+
		"  border: 1px solid gray;\n"+
		"  padding: 8px;\n"+
		"  width: 200px;\n"+
		"  height: 200px;\n"+
		"  max-width: 200px;\n"+
		"  max-height: 200px;\n"+
		"  float: left;\n"+
		"  overflow: hidden;\n"+
		"  text-align: center;\n"+
		"}\n"+
		"\n"+
		".camli-ui-thumb:hover {\n"+
		"  border: 1px solid black;\n"+
		"  background: #ccc;\n"+
		"}\n"+
		"\n"+
		".camli-ui-thumb.selected {\n"+
		"  border: 3px solid black;\n"+
		"  background: #ffc;\n"+
		"  padding: 6px;\n"+
		"}\n"+
		"\n"+
		".camli-ui-thumb.selected:hover {\n"+
		"  border: 3px solid black;\n"+
		"  padding: 6px;\n"+
		"  background: #e6e6b8;\n"+
		"}\n"+
		"\n"+
		".camli-ui-thumbtitle:hover {\n"+
		"  text-decoration: underline;\n"+
		"  background: #999;\n"+
		"}\n"+
		"\n"+
		"#plusdrop a.plusLink {\n"+
		"  text-decoration: none;\n"+
		"  font-size: 40pt;\n"+
		"  margin-top: 20px;\n"+
		"  padding: 80px;\n"+
		"}"), time.Unix(0, 1354842364693427527))
}
