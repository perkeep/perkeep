// THIS FILE IS AUTO-GENERATED FROM toolbar_test.html
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("toolbar_test.html", 912, time.Unix(0, 1370942742232957700), fileembed.String("<!doctype html>\n"+
		"<html>\n"+
		"  <head>\n"+
		"    <script src=\"closure/goog/base.js\"></script>\n"+
		"    <script src=\"./deps.js\"></script>\n"+
		"    <script>\n"+
		"      goog.require('goog.events');\n"+
		"      goog.require('camlistore.Toolbar');\n"+
		"      goog.require('camlistore.Toolbar.EventType');\n"+
		"    </script>\n"+
		"    <link rel=\"stylesheet\" href=\"toolbar.css\" type=\"text/css\">\n"+
		"    <link rel=\"stylesheet\" href=\"closure/goog/css/common.css\" type=\"text/css\">\n"+
		"    <link rel=\"stylesheet\" href=\"closure/goog/css/toolbar.css\" type=\"text/css\">\n"+
		"  </head>\n"+
		"  <body>\n"+
		"    <script>\n"+
		"      var x = new camlistore.Toolbar();\n"+
		"      x.render(document.body);\n"+
		"\n"+
		"      goog.events.listen(\n"+
		"          x, camlistore.Toolbar.EventType.BIGGER, function() {\n"+
		"            console.log('Bigger');\n"+
		"          });\n"+
		"\n"+
		"      goog.events.listen(\n"+
		"          x, camlistore.Toolbar.EventType.SMALLER, function() {\n"+
		"            console.log('Smaller');\n"+
		"          });\n"+
		"    </script>\n"+
		"  </body>\n"+
		"</html>\n"+
		""))
}
