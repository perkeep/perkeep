// THIS FILE IS AUTO-GENERATED FROM blob_item_container_test.html
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("blob_item_container_test.html", 4034, time.Unix(0, 1356370432000000000), fileembed.String("<!doctype html>\n"+
		"<html>\n"+
		"  <head>\n"+
		"		<script type=\"text/javascript\" src=\"all.js\"></script>\n"+
		"    \n"+
		"    \n"+
		"    <script>\n"+
		"      goog.require('goog.events');\n"+
		"      goog.require('goog.testing.net.XhrIo');\n"+
		"      goog.require('camlistore.ServerConnection');\n"+
		"      goog.require('camlistore.BlobItemContainer');\n"+
		"    </script>\n"+
		"    <link rel=\"stylesheet\" href=\"blob_item.css\" type=\"text/css\">\n"+
		"    <link rel=\"stylesheet\" href=\"blob_item_container.css\" type=\"text/css\">\n"+
		"  </head>\n"+
		"  <body>\n"+
		"    <script>\n"+
		"      // Mock response from:\n"+
		"      //   http://127.0.0.1:3179/my-search/camli/search/recent?thumbnails=100\n"+
		"      var recentBlobResponse = {\n"+
		"        \"recent\": [\n"+
		"          {\n"+
		"            \"blobref\": \"sha1-5660088af0aa0d4f2294088f41284002a1baaa29\",\n"+
		"            \"modtime\": \"2012-12-23T19:53:32Z\",\n"+
		"            \"owner\": \"sha1-f2b0b7da718b97ce8c31591d8ed4645c777f3ef4\"\n"+
		"          },\n"+
		"          {\n"+
		"            \"blobref\": \"sha1-3ced53f0a11115e98d6e40ca4558680f2768f23e\",\n"+
		"            \"modtime\": \"2012-12-23T19:19:37Z\",\n"+
		"            \"owner\": \"sha1-f2b0b7da718b97ce8c31591d8ed4645c777f3ef4\"\n"+
		"          },\n"+
		"          {\n"+
		"            \"blobref\": \"sha1-19236d4922116d03738f1c8c6d9f14debbab798b\",\n"+
		"            \"modtime\": \"2012-12-23T19:19:37Z\",\n"+
		"            \"owner\": \"sha1-f2b0b7da718b97ce8c31591d8ed4645c777f3ef4\"\n"+
		"          }\n"+
		"        ],\n"+
		"        \"sha1-19236d4922116d03738f1c8c6d9f14debbab798b\": {\n"+
		"          \"blobRef\": \"sha1-19236d4922116d03738f1c8c6d9f14debbab798b\",\n"+
		"          \"camliType\": \"permanode\",\n"+
		"          \"mimeType\": \"application/json; camliType=permanode\",\n"+
		"          \"permanode\": {\n"+
		"            \"attr\": {\n"+
		"              \"camliRoot\": [\n"+
		"                \"dev-pics-root\"\n"+
		"              ],\n"+
		"              \"title\": [\n"+
		"                \"Publish root node for dev-pics-root\"\n"+
		"              ]\n"+
		"            }\n"+
		"          },\n"+
		"          \"size\": 562,\n"+
		"          \"thumbnailHeight\": 100,\n"+
		"          \"thumbnailSrc\": \"node.png\",\n"+
		"          \"thumbnailWidth\": 100\n"+
		"        },\n"+
		"        \"sha1-3ced53f0a11115e98d6e40ca4558680f2768f23e\": {\n"+
		"          \"blobRef\": \"sha1-3ced53f0a11115e98d6e40ca4558680f2768f23e\",\n"+
		"          \"camliType\": \"permanode\",\n"+
		"          \"mimeType\": \"application/json; camliType=permanode\",\n"+
		"          \"permanode\": {\n"+
		"            \"attr\": {\n"+
		"              \"camliRoot\": [\n"+
		"                \"dev-blog-root\"\n"+
		"              ],\n"+
		"              \"title\": [\n"+
		"                \"Publish root node for dev-blog-root\"\n"+
		"              ]\n"+
		"            }\n"+
		"          },\n"+
		"          \"size\": 562,\n"+
		"          \"thumbnailHeight\": 100,\n"+
		"          \"thumbnailSrc\": \"node.png\",\n"+
		"          \"thumbnailWidth\": 100\n"+
		"        },\n"+
		"        \"sha1-5660088af0aa0d4f2294088f41284002a1baaa29\": {\n"+
		"          \"blobRef\": \"sha1-5660088af0aa0d4f2294088f41284002a1baaa29\",\n"+
		"          \"camliType\": \"permanode\",\n"+
		"          \"mimeType\": \"application/json; camliType=permanode\",\n"+
		"          \"permanode\": {\n"+
		"            \"attr\": {\n"+
		"              \"camliContent\": [\n"+
		"                \"sha1-c2379bcf77848c90d2c83709aaf7f628a21ff725\"\n"+
		"              ]\n"+
		"            }\n"+
		"          },\n"+
		"          \"size\": 556,\n"+
		"          \"thumbnailHeight\": 100,\n"+
		"          \"thumbnailSrc\": \"thumbnail/sha1-c2379bcf77848c90d2c83709aaf7f628a21ff72"+
		"5/leisure-suit-tony.gif?mw=100&mh=100\",\n"+
		"          \"thumbnailWidth\": 100\n"+
		"        },\n"+
		"        \"sha1-c2379bcf77848c90d2c83709aaf7f628a21ff725\": {\n"+
		"          \"blobRef\": \"sha1-c2379bcf77848c90d2c83709aaf7f628a21ff725\",\n"+
		"          \"camliType\": \"file\",\n"+
		"          \"file\": {\n"+
		"            \"size\": 37741,\n"+
		"            \"fileName\": \"leisure-suit-tony.gif\",\n"+
		"            \"mimeType\": \"image/gif\"\n"+
		"          },\n"+
		"          \"mimeType\": \"application/json; camliType=file\",\n"+
		"          \"size\": 198\n"+
		"        }\n"+
		"      };\n"+
		"\n"+
		"      var connection = new camlistore.ServerConnection(\n"+
		"        {\n"+
		"          'searchRoot': '/my/test/search'\n"+
		"        },\n"+
		"        goog.testing.net.XhrIo.send\n"+
		"      );\n"+
		"\n"+
		"      var container = new camlistore.BlobItemContainer(connection);\n"+
		"      container.render(document.body);\n"+
		"\n"+
		"      container.showRecent();\n"+
		"\n"+
		"      var request = goog.testing.net.XhrIo.getSendInstances().pop();\n"+
		"      request.simulateResponse(200, JSON.stringify(recentBlobResponse));\n"+
		"    </script>\n"+
		"  </body>\n"+
		"</html>\n"+
		""))
}
