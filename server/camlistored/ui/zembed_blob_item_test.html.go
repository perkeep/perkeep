// THIS FILE IS AUTO-GENERATED FROM blob_item_test.html
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("blob_item_test.html", 1505, time.Unix(0, 1370942742232957700), fileembed.String("<!doctype html>\n"+
		"<html>\n"+
		"  <head>\n"+
		"    <script src=\"closure/goog/base.js\"></script>\n"+
		"    <script src=\"./deps.js\"></script>\n"+
		"    <script>\n"+
		"      goog.require('camlistore.BlobItem');\n"+
		"    </script>\n"+
		"    <link rel=\"stylesheet\" href=\"blob_item.css\" type=\"text/css\">\n"+
		"  </head>\n"+
		"  <body>\n"+
		"    <script>\n"+
		"      var blobRef = 'sha1-5660088af0aa0d4f2294088f41284002a1baaa29';\n"+
		"      var metaBag = {\n"+
		"        'sha1-5660088af0aa0d4f2294088f41284002a1baaa29': {\n"+
		"          'blobRef': 'sha1-5660088af0aa0d4f2294088f41284002a1baaa29',\n"+
		"          'camliType': 'permanode',\n"+
		"          'mimeType': 'application/json; camliType=permanode',\n"+
		"          'permanode': {\n"+
		"            'attr': {\n"+
		"              'camliContent': ['sha1-c2379bcf77848c90d2c83709aaf7f628a21ff725']\n"+
		"            }\n"+
		"          },\n"+
		"          'size': 556,\n"+
		"          'thumbnailHeight': 100,\n"+
		"          'thumbnailSrc': 'thumbnail/sha1-c2379bcf77848c90d2c83709aaf7f628a21ff72"+
		"5/leisure-suit-tony.gif?mw=100&mh=100',\n"+
		"          'thumbnailWidth': 100\n"+
		"        },\n"+
		"        'sha1-c2379bcf77848c90d2c83709aaf7f628a21ff725': {\n"+
		"          'blobRef': 'sha1-c2379bcf77848c90d2c83709aaf7f628a21ff725',\n"+
		"          'camliType': 'file',\n"+
		"          'file': {\n"+
		"            'size': 37741,\n"+
		"            'fileName': 'leisure-suit-tony.gif',\n"+
		"            'mimeType': 'image/gif'\n"+
		"          },\n"+
		"          'mimeType': 'application/json; camliType=file',\n"+
		"          'size': 198\n"+
		"        }\n"+
		"      };\n"+
		"\n"+
		"      var x = new camlistore.BlobItem(blobRef, metaBag);\n"+
		"      x.render(document.body);\n"+
		"    </script>\n"+
		"  </body>\n"+
		"</html>\n"+
		""))
}
