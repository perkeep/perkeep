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

/**
 * @fileoverview Blob description page.
 *
 */
goog.provide('camlistore.BlobPage');

goog.require('goog.dom');
goog.require('goog.ui.Component');
goog.require('camlistore.ServerConnection');

/**
 * @param {camlistore.ServerType.DiscoveryDocument} config Global config
 *   of the current server this page is being rendered for.
 * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.
 *
 * @extends {goog.ui.Component}
 * @constructor
 */
camlistore.BlobPage = function(config, opt_domHelper) {
	goog.base(this, opt_domHelper);

	/**
	 * @type {Object}
	 * @private
	 */
	this.config_ = config;

	/**
	 * @type {camlistore.ServerConnection}
	 * @private
	 */
	this.connection_ = new camlistore.ServerConnection(config);
};
goog.inherits(camlistore.BlobPage, goog.ui.Component);

/**
 * @type {number}
 * @private
 */
camlistore.BlobPage.prototype.thumbnailSize_ = 200;

/**
 * Called when component's element is known to be in the document.
 */
camlistore.BlobPage.prototype.enterDocument = function() {
	var blobref = getBlobParam();
	if (!blobref) {
		alert("missing blob param in url");
		return;
	}
	var blobmeta = goog.dom.getElement('blobmeta');
	blobmeta.innerText = "(loading)";

	var blobdescribe = goog.dom.getElement('blobdescribe');
	blobdescribe.innerHTML = "<a href='" +
		goog.uri.utils.appendPath(this.config_.searchRoot,
			'camli/search/describe?blobref=' + blobref) +
		 "'>describe</a>";
	this.describeBlob_(blobref);

}

/**
 * @param {string} blobRef BlobRef for the uploaded file.
 * @private
 */
camlistore.BlobPage.prototype.describeBlob_ =
function(blobRef) {
	this.connection_.describeWithThumbnails(
		blobRef,
		0,
		goog.bind(this.handleDescribeBlob_, this),
		function(msg) {
			alert("Error describing blob " + blobRef + ": " + msg);
		}
	);
};


// TODO(mpl): improve blob_item and redo the following based on it.
/**
 * @param {Object} bmap Describe request response.
 * @private
 */
camlistore.BlobPage.prototype.handleDescribeBlob_ =
function(bmap) {
	var blobmeta = goog.dom.getElement('blobmeta');
	var bd = goog.dom.getElement("blobdownload");
	bd.innerHTML = "";
	var blobref = getBlobParam();
	if (!blobref) {
		alert("no blobref?");
		return;
	}
	var binfo = bmap.meta[blobref];
	if (!binfo) {
		blobmeta.innerHTML = "(not found)";
		return;
	}
	blobmeta.innerHTML = htmlEscape(JSON.stringify(binfo, null, 2));
	if (binfo.camliType || (binfo.type && binfo.type.indexOf("text/") == 0)) {
		this.connection_.getBlobContents(blobref,
			goog.bind(function(data) {
				goog.dom.getElement("blobdata").innerHTML = linkifyBlobRefs(data);
				var bb = goog.dom.getElement('blobbrowse');
				if (binfo.camliType != "directory") {
					bb.style.visibility = 'hidden';
				} else {
					bb.innerHTML = "<a href='?d=" + blobref + "'>browse</a>";
				}
				if (binfo.camliType == "file") {
					// TODO(mpl): we can't get the thumnails url in a describe
					// response because the server only gives it for a permanode.
					// That's why we do this messy business here. Fix it server side.
					finfo = JSON.parse(data);
					bd.innerHTML = "<a href=''></a>";
					var fileName = htmlEscape(finfo.fileName) || blobref;
					bd.firstChild.href = "./download/" + blobref + "/" + fileName;
					// If the mime type was not detected by magic pkg, we end up
					// with an empty mimetype value in the indexer's fileinfo,
					// hence no mimeType in the returned JSON.
					if (!!binfo.file.mimeType &&
						binfo.file.mimeType.indexOf("image/") == 0) {
						var thumbURL = "<img src='./thumbnail/" + blobref + "/" +
							fileName + "?mw=" + this.thumbnailSize_ +
							"&mh=" + this.thumbnailSize_ + "'>";
						goog.dom.getElement("thumbnail").innerHTML = thumbURL;
					} else {
						goog.dom.getElement("thumbnail").innerHTML = "";
					}
					goog.dom.setTextContent(bd.firstChild, fileName);
					bd.innerHTML = "download: " + bd.innerHTML;
				}
			}, this),
			alert
		);
	} else {
		goog.dom.getElement("blobdata").innerHTML = "<em>Unknown/binary data</em>";
	}
	bd.innerHTML = "<a href='" +
		goog.uri.utils.appendPath(
			this.config_.blobRoot,
			"camli/" + blobref)
		+ "'>download</a>";

	if (binfo.camliType && binfo.camliType == "permanode") {
		goog.dom.getElement("editspan").style.display = "inline";
		goog.dom.getElement("editlink").href = "./?p=" + blobref;

		var claims = goog.dom.getElement("claimsdiv");
		claims.style.visibility = "";
		this.connection_.permanodeClaims(blobref,
			function(data) {
				goog.dom.getElement("claims").innerHTML = linkifyBlobRefs(JSON.stringify(data, null, 2));
			},
			function(msg) {
				alert(msg);
			}
		);
	}
}

function htmlEscape(data) {
	return goog.string.htmlEscape(data);
}

function linkifyBlobRefs(schemaBlob) {
	var re = /(\w{3,6}-[a-f0-9]{30,})/g;
	return htmlEscape(schemaBlob).replace(re, "<a href='./?b=$1'>$1</a>");
};

// Gets the |p| query parameter, assuming that it looks like a blobref.
function getBlobParam() {
	var blobRef = getQueryParam('b');
	return (blobRef && isPlausibleBlobRef(blobRef)) ? blobRef : null;
}

// TODO(mpl): move it to a common place (used by permanode.js too).
// I suppose we could go back to depending on camli.js for these little helpers only.
// Returns the first value from the query string corresponding to |key|.
// Returns null if the key isn't present.
getQueryParam = function(key) {
	var params = document.location.search.substring(1).split('&');
	for (var i = 0; i < params.length; ++i) {
		var parts = params[i].split('=');
		if (parts.length == 2 && decodeURIComponent(parts[0]) == key)
			return decodeURIComponent(parts[1]);
	}
	return null;
};

// Returns true if the passed-in string might be a blobref.
isPlausibleBlobRef = function(blobRef) {
	return /^\w+-[a-f0-9]+$/.test(blobRef);
};
