// THIS FILE IS AUTO-GENERATED FROM index-compiled.html
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("index-compiled.html", 905, fileembed.String("<!doctype html>\n"+
		"<html>\n"+
		"  <head>\n"+
		"		<script type=\"text/javascript\" src=\"all.js\"></script>\n"+
		"    <script src=\"?camli.mode=config&var=CAMLISTORE_CONFIG\"></script>\n"+
		"    \n"+
		"    <!-- easier css handling: https://code.google.com/p/camlistore/issues/detail?"+
		"id=98 -->\n"+
		"    <link rel=\"stylesheet\" href=\"blob_item.css\" type=\"text/css\">\n"+
		"    <link rel=\"stylesheet\" href=\"blob_item_container.css\" type=\"text/css\">\n"+
		"    <link rel=\"stylesheet\" href=\"create_item.css\" type=\"text/css\">\n"+
		"    <link rel=\"stylesheet\" href=\"index.css\" type=\"text/css\">\n"+
		"    <link rel=\"stylesheet\" href=\"toolbar.css\" type=\"text/css\">\n"+
		"    <link rel=\"stylesheet\" href=\"closure/goog/css/common.css\" type=\"text/css\">\n"+
		"    <link rel=\"stylesheet\" href=\"closure/goog/css/toolbar.css\" type=\"text/css\">\n"+
		"  </head>\n"+
		"  <body>\n"+
		"    <script>\n"+
		"      var page = new camlistore.IndexPage(CAMLISTORE_CONFIG);\n"+
		"      page.decorate(document.body);\n"+
		"    </script>\n"+
		"  </body>\n"+
		"</html>\n"+
		""), time.Unix(0, 1369665728944011422))
}
