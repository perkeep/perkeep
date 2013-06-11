// THIS FILE IS AUTO-GENERATED FROM blob_item_container.css
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("blob_item_container.css", 857, time.Unix(0, 1370942742232957700), fileembed.String(".cam-blobitemcontainer {\n"+
		"  outline: 0;  /* Do not show an outline when container has focus. */\n"+
		"  border-width: 5px;\n"+
		"  border-color: rgba(0, 0, 0, 0);\n"+
		"  border-style: dashed;\n"+
		"  position: relative;\n"+
		"  border-radius: 5px;\n"+
		"}\n"+
		".cam-blobitemcontainer-dropactive {\n"+
		"  border-color: #acf;\n"+
		"}\n"+
		".cam-blobitemcontainer-drag-indicator {\n"+
		"  position: absolute;\n"+
		"  left: 0;\n"+
		"  right: 0;\n"+
		"  bottom: 0;\n"+
		"  top: 0;\n"+
		"  display: none;\n"+
		"}\n"+
		".cam-blobitemcontainer-dropactive .cam-blobitemcontainer-drag-indicator {\n"+
		"  display: block;\n"+
		"}\n"+
		".cam-blobitemcontainer-drag-message {\n"+
		"  display: block;\n"+
		"  margin: 0 auto;\n"+
		"  border: 10px solid #acf;\n"+
		"  padding: 25px;\n"+
		"  background-color: #def;\n"+
		"  color: #000;\n"+
		"  font-size: 16px;\n"+
		"  font-family: sans-serif;\n"+
		"  width: 250px;\n"+
		"  opacity: 0.8;\n"+
		"  text-align: center;\n"+
		"  border-radius: 10px;\n"+
		"  margin-top: 30px;\n"+
		"}\n"+
		"\n"+
		".cam-blobitemcontainer-hidden {\n"+
		"  display: none;\n"+
		"}\n"+
		""))
}
