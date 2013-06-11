// THIS FILE IS AUTO-GENERATED FROM server_connection.js
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("server_connection.js", 22757, time.Unix(0, 1370942742232957700), fileembed.String("/**\n"+
		" * @fileoverview Connection to the blob server and API for the RPCs it\n"+
		" * provides. All blob index UI code should use this connection to contact\n"+
		" * the server.\n"+
		" *\n"+
		" */\n"+
		"goog.provide('camlistore.ServerConnection');\n"+
		"\n"+
		"goog.require('camlistore.base64');\n"+
		"goog.require('camlistore.SHA1');\n"+
		"goog.require('goog.net.XhrIo');\n"+
		"goog.require('goog.Uri'); // because goog.net.XhrIo forgot to include it.\n"+
		"goog.require('goog.debug.ErrorHandler'); // because goog.net.Xhrio forgot to incl"+
		"ude it.\n"+
		"goog.require('goog.uri.utils');\n"+
		"goog.require('camlistore.ServerType');\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {camlistore.ServerType.DiscoveryDocument} config Discovery document\n"+
		" *   for the current server.\n"+
		" * @param {Function=} opt_sendXhr Function for sending XHRs for testing.\n"+
		" * @constructor\n"+
		" */\n"+
		"camlistore.ServerConnection = function(config, opt_sendXhr) {\n"+
		"  /**\n"+
		"   * @type {camlistore.ServerType.DiscoveryDocument}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.config_ = config;\n"+
		"\n"+
		"  /**\n"+
		"   * @type {Function}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.sendXhr_ = opt_sendXhr || goog.net.XhrIo.send;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {?Function|undefined} fail Fail func to call if exists.\n"+
		" * @return {Function}\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.safeFail_ = function(fail) {\n"+
		"	if (typeof fail === 'undefined') {\n"+
		"		return alert;\n"+
		"	}\n"+
		"	if (fail === null) {\n"+
		"		return alert;\n"+
		"	}\n"+
		"	return fail;\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {?Function} fail Optional fail callback.\n"+
		" * @param {goog.events.Event} e Event that triggered this\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.handleXhrResponseText_ =\n"+
		"function(success, fail, e) {\n"+
		"	var xhr = e.target;\n"+
		"	var error = !xhr.isSuccess();\n"+
		"	var result = null;\n"+
		"	if (!error) {\n"+
		"		result = xhr.getResponseText();\n"+
		"		error = !result;\n"+
		"	}\n"+
		"	if (error) {\n"+
		"		if (fail) {\n"+
		"			fail()\n"+
		"		} else {\n"+
		"			// TODO(bslatkin): Add a default failure event handler to this class.\n"+
		"			console.log('Failed XHR (text) in ServerConnection');\n"+
		"		}\n"+
		"		return;\n"+
		"	}\n"+
		"	success(result);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {string} blobref blobref whose contents we want.\n"+
		" * @param {Function} success callback with data.\n"+
		" * @param {?Function} opt_fail optional failure calback\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.getBlobContents =\n"+
		"function(blobref, success, opt_fail) {\n"+
		"	var path = goog.uri.utils.appendPath(\n"+
		"		this.config_.blobRoot, 'camli/' + blobref\n"+
		"	);\n"+
		"\n"+
		"	this.sendXhr_(path,\n"+
		"		goog.bind(this.handleXhrResponseText_, this,\n"+
		"			success, this.safeFail_(opt_fail)\n"+
		"		)\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"// TODO(mpl): set a global timeout ?\n"+
		"// Brett, would it be worth to use the XhrIo send instance method, with listeners"+
		",\n"+
		"// instead of the send() utility function ?\n"+
		"/**\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {?Function} fail Optional fail callback.\n"+
		" * @param {goog.events.Event} e Event that triggered this\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.handleXhrResponseJson_ =\n"+
		"function(success, fail, e) {\n"+
		"	var xhr = e.target;\n"+
		"	var error = !xhr.isSuccess();\n"+
		"	var result = null;\n"+
		"	if (!error) {\n"+
		"		result = xhr.getResponseJson();\n"+
		"		error = !result;\n"+
		"	}\n"+
		"	if (error) {\n"+
		"		if (fail) {\n"+
		"			fail()\n"+
		"		} else {\n"+
		"			// TODO(bslatkin): Add a default failure event handler to this class.\n"+
		"			console.log('Failed XHR (GET) in ServerConnection');\n"+
		"		}\n"+
		"		return;\n"+
		"	}\n"+
		"	success(result);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {Function} success callback with data.\n"+
		" * @param {?Function} opt_fail optional failure calback\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.discoSignRoot =\n"+
		"function(success, opt_fail) {\n"+
		"	var path = goog.uri.utils.appendPath(\n"+
		"		this.config_.jsonSignRoot, '/camli/sig/discovery'\n"+
		"	);\n"+
		"\n"+
		"	this.sendXhr_(path,\n"+
		"		goog.bind(this.handleXhrResponseJson_, this,\n"+
		"			success, this.safeFail_(opt_fail)\n"+
		"		)\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {function(camlistore.ServerType.StatusResponse)} success.\n"+
		" * @param {?Function} opt_fail optional failure calback\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.serverStatus =\n"+
		"function(success, opt_fail) {\n"+
		"	var path = goog.uri.utils.appendPath(\n"+
		"		this.config_.statusRoot, 'status.json'\n"+
		"	);\n"+
		"\n"+
		"	this.sendXhr_(path,\n"+
		"		goog.bind(this.handleXhrResponseJson_, this,\n"+
		"			success, this.safeFail_(opt_fail)\n"+
		"		)\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" * @param {goog.events.Event} e Event that triggered this\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.genericHandleSearch_ =\n"+
		"function(success, opt_fail, e) {\n"+
		"	this.handleXhrResponseJson_(success, this.safeFail_(opt_fail), e);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {string} blobref root of the tree\n"+
		" * @param {Function} success callback with data.\n"+
		" * @param {?Function} opt_fail optional failure calback\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.getFileTree =\n"+
		"function(blobref, success, opt_fail) {\n"+
		"\n"+
		"	// TODO(mpl): do it relatively to a discovered root?\n"+
		"	var path = \"./tree/\" + blobref;\n"+
		"\n"+
		"	this.sendXhr_(\n"+
		"		path,\n"+
		"		goog.bind(this.genericHandleSearch_, this,\n"+
		"			success, this.safeFail_(opt_fail)\n"+
		"		)\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {function(camlistore.ServerType.SearchRecentResponse)} success callback"+
		" with data.\n"+
		" * @param {number=} opt_thumbnailSize\n"+
		" * @param {?Function} opt_fail optional failure calback\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.getRecentlyUpdatedPermanodes =\n"+
		"    function(success, opt_thumbnailSize, opt_fail) {\n"+
		"\n"+
		"  var path = goog.uri.utils.appendPath(\n"+
		"      this.config_.searchRoot, 'camli/search/recent');\n"+
		"  if (!!opt_thumbnailSize) {\n"+
		"    path = goog.uri.utils.appendParam(path, 'thumbnails', opt_thumbnailSize);\n"+
		"  }\n"+
		"\n"+
		"  this.sendXhr_(\n"+
		"      path,\n"+
		"      goog.bind(this.genericHandleSearch_, this,\n"+
		"                success, this.safeFail_(opt_fail)));\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {string} blobref Permanode blobref.\n"+
		" * @param {number} thumbnailSize\n"+
		" * @param {function(camlistore.ServerType.DescribeResponse)} success.\n"+
		" * @param {Function=} opt_fail Optional fail callback.\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.describeWithThumbnails =\n"+
		"function(blobref, thumbnailSize, success, opt_fail) {\n"+
		"	var path = goog.uri.utils.appendPath(\n"+
		"		this.config_.searchRoot, 'camli/search/describe?blobref=' + blobref\n"+
		"	);\n"+
		"	// TODO(mpl): should we URI encode the value? doc does not say...\n"+
		"	path = goog.uri.utils.appendParam(path, 'thumbnails', thumbnailSize);\n"+
		"\n"+
		"	this.sendXhr_(\n"+
		"		path,\n"+
		"		goog.bind(this.genericHandleSearch_, this,\n"+
		"			success, this.safeFail_(opt_fail)\n"+
		"		)\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {string} signer permanode must belong to signer.\n"+
		" * @param {string} attr searched attribute.\n"+
		" * @param {string} value value of the searched attribute.\n"+
		" * @param {Function} success.\n"+
		" * @param {Function=} opt_fail Optional fail callback.\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.permanodeOfSignerAttrValue =\n"+
		"function(signer, attr, value, success, opt_fail) {\n"+
		"	var path = goog.uri.utils.appendPath(\n"+
		"		this.config_.searchRoot, 'camli/search/signerattrvalue'\n"+
		"	);\n"+
		"	path = goog.uri.utils.appendParams(path,\n"+
		"		'signer', signer, 'attr', attr, 'value', value\n"+
		"	);\n"+
		"\n"+
		"	this.sendXhr_(\n"+
		"		path,\n"+
		"		goog.bind(this.genericHandleSearch_, this,\n"+
		"			success, this.safeFail_(opt_fail)\n"+
		"		)\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {string} signer permanode must belong to signer.\n"+
		" * @param {string} attr searched attribute.\n"+
		" * @param {string} value value of the searched attribute.\n"+
		" * @param {boolean} fuzzy fuzzy search.\n"+
		" * @param {number} max max number of results.\n"+
		" * @param {number} thumbsize thumbnails size, 0 for no thumbnails.\n"+
		" * @param {function(camlistore.ServerType.SearchWithAttrResponse)} success.\n"+
		" * @param {Function=} opt_fail Optional fail callback.\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.permanodesWithAttr =\n"+
		"function(signer, attr, value, fuzzy, max, thumbsize, success, opt_fail) {\n"+
		"	var path = goog.uri.utils.appendPath(\n"+
		"		this.config_.searchRoot, 'camli/search/permanodeattr'\n"+
		"	);\n"+
		"	path = goog.uri.utils.appendParams(path,\n"+
		"		'signer', signer, 'attr', attr, 'value', value,\n"+
		"		'fuzzy', fuzzy, 'max', max, 'thumbnails', thumbsize\n"+
		"	);\n"+
		"\n"+
		"	this.sendXhr_(\n"+
		"		path,\n"+
		"		goog.bind(this.genericHandleSearch_, this,\n"+
		"			success, this.safeFail_(opt_fail)\n"+
		"		)\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"// Where is the target accessed via? (paths it's at)\n"+
		"/**\n"+
		" * @param {string} signer owner of permanode.\n"+
		" * @param {string} target blobref of permanode we want to find paths to\n"+
		" * @param {Function} success.\n"+
		" * @param {Function=} opt_fail Optional fail callback.\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.pathsOfSignerTarget =\n"+
		"function(signer, target, success, opt_fail) {\n"+
		"	var path = goog.uri.utils.appendPath(\n"+
		"		this.config_.searchRoot, 'camli/search/signerpaths'\n"+
		"	);\n"+
		"	path = goog.uri.utils.appendParams(path, 'signer', signer, 'target', target);\n"+
		"\n"+
		"	this.sendXhr_(\n"+
		"		path,\n"+
		"		goog.bind(this.genericHandleSearch_, this,\n"+
		"			success, this.safeFail_(opt_fail)\n"+
		"		)\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {string} permanode Permanode blobref.\n"+
		" * @param {Function} success.\n"+
		" * @param {Function=} opt_fail Optional fail callback.\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.permanodeClaims =\n"+
		"function(permanode, success, opt_fail) {\n"+
		"	var path = goog.uri.utils.appendPath(\n"+
		"		this.config_.searchRoot, 'camli/search/claims?permanode=' + permanode\n"+
		"	);\n"+
		"\n"+
		"	this.sendXhr_(\n"+
		"		path,\n"+
		"		goog.bind(this.genericHandleSearch_, this,\n"+
		"			success, this.safeFail_(opt_fail)\n"+
		"		)\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {Object} clearObj Unsigned object.\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.sign_ =\n"+
		"function(clearObj, success, opt_fail) {\n"+
		"	var sigConf = this.config_.signing;\n"+
		"	if (!sigConf || !sigConf.publicKeyBlobRef) {\n"+
		"		this.safeFail_(opt_fail)(\"Missing Camli.config.signing.publicKeyBlobRef\");\n"+
		"		return;\n"+
		"	}\n"+
		"\n"+
		"    clearObj.camliSigner = sigConf.publicKeyBlobRef;\n"+
		"    clearText = JSON.stringify(clearObj);\n"+
		"\n"+
		"	this.sendXhr_(\n"+
		"		sigConf.signHandler,\n"+
		"		goog.bind(this.handlePost_, this,\n"+
		"			success, this.safeFail_(opt_fail)),\n"+
		"		\"POST\",\n"+
		"		\"json=\" + encodeURIComponent(clearText),\n"+
		"		{\"Content-Type\": \"application/x-www-form-urlencoded\"}\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {Object} sObj Signed object.\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.verify_ =\n"+
		"function(sObj, success, opt_fail) {\n"+
		"	var sigConf = this.config_.signing;\n"+
		"	if (!sigConf || !sigConf.publicKeyBlobRef) {\n"+
		"		this.safeFail_(opt_fail)(\"Missing Camli.config.signing.publicKeyBlobRef\");\n"+
		"		return;\n"+
		"	}\n"+
		"\n"+
		"    var clearText = JSON.stringify(sObj);\n"+
		"\n"+
		"	this.sendXhr_(\n"+
		"		sigConf.verifyHandler,\n"+
		"		goog.bind(this.handlePost_, this,\n"+
		"			success, this.safeFail_(opt_fail)),\n"+
		"		\"POST\",\n"+
		"		\"sjson=\" + encodeURIComponent(clearText),\n"+
		"		{\"Content-Type\": \"application/x-www-form-urlencoded\"}\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" * @param {goog.events.Event} e Event that triggered this\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.handlePost_ =\n"+
		"function(success, opt_fail, e) {\n"+
		"	this.handleXhrResponseText_(success, opt_fail, e);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {string} s String to upload.\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.uploadString_ =\n"+
		"function(s, success, opt_fail) {\n"+
		"	var blobref = \"sha1-\" + Crypto.SHA1(s);\n"+
		"	var parts = [s];\n"+
		"	var bb = new Blob(parts);\n"+
		"	var fd = new FormData();\n"+
		"	fd.append(blobref, bb);\n"+
		"\n"+
		"	// TODO: hack, hard-coding the upload URL here.\n"+
		"	// Change the spec now that App Engine permits 32 MB requests\n"+
		"	// and permit a PUT request on the sha1?  Or at least let us\n"+
		"	// specify the well-known upload URL?  In cases like this, uploading\n"+
		"	// a new permanode, it's silly to even stat.\n"+
		"	this.sendXhr_(\n"+
		"		this.config_.blobRoot + \"camli/upload\",\n"+
		"		goog.bind(this.handleUploadString_, this,\n"+
		"			blobref,\n"+
		"			success,\n"+
		"			this.safeFail_(opt_fail)\n"+
		"		),\n"+
		"		\"POST\",\n"+
		"		fd\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {string} blobref Uploaded blobRef.\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" * @param {goog.events.Event} e Event that triggered this\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.handleUploadString_ =\n"+
		"function(blobref, success, opt_fail, e) {\n"+
		"	this.handlePost_(\n"+
		"		function(resj) {\n"+
		"			if (!resj) {\n"+
		"				alert(\"upload permanode fail; no response\");\n"+
		"				return;\n"+
		"			}\n"+
		"			var resObj = JSON.parse(resj);\n"+
		"			if (!resObj.received || !resObj.received[0] || !resObj.received[0].blobRef) {\n"+
		"				alert(\"upload permanode fail, expected blobRef not in response\");\n"+
		"				return;\n"+
		"			}\n"+
		"			success(blobref);\n"+
		"		},\n"+
		"		this.safeFail_(opt_fail),\n"+
		"		e\n"+
		"	)\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.createPermanode =\n"+
		"function(success, opt_fail) {\n"+
		"	var json = {\n"+
		"		\"camliVersion\": 1,\n"+
		"		\"camliType\": \"permanode\",\n"+
		"		\"random\": \"\"+Math.random()\n"+
		"	};\n"+
		"	this.sign_(json,\n"+
		"		goog.bind(this.handleSignPermanode_, this, success, this.safeFail_(opt_fail)),\n"+
		"		function(msg) {\n"+
		"			this.safeFail_(opt_fail)(\"sign permanode fail: \" + msg);\n"+
		"		}\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" * @param {string} signed Signed string to upload\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.handleSignPermanode_ =\n"+
		"function(success, opt_fail, signed) {\n"+
		"	this.uploadString_(\n"+
		"		signed,\n"+
		"		success,\n"+
		"		function(msg) {\n"+
		"			this.safeFail_(opt_fail)(\"upload permanode fail: \" + msg);\n"+
		"		}\n"+
		"	)\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {string} permanode Permanode to change.\n"+
		" * @param {string} claimType What kind of claim: \"add-attribute\", \"set-attribute\""+
		"...\n"+
		" * @param {string} attribute What attribute the claim applies to.\n"+
		" * @param {string} value Attribute value.\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.changeAttribute_ =\n"+
		"function(permanode, claimType, attribute, value, success, opt_fail) {\n"+
		"	var json = {\n"+
		"		\"camliVersion\": 1,\n"+
		"		\"camliType\": \"claim\",\n"+
		"		\"permaNode\": permanode,\n"+
		"		\"claimType\": claimType,\n"+
		"		// TODO(mpl): to (im)port.\n"+
		"		\"claimDate\": dateToRfc3339String(new Date()),\n"+
		"		\"attribute\": attribute,\n"+
		"		\"value\": value\n"+
		"	};\n"+
		"	this.sign_(json,\n"+
		"		goog.bind(this.handleSignClaim_, this, success, this.safeFail_(opt_fail)),\n"+
		"		function(msg) {\n"+
		"			this.safeFail_(opt_fail)(\"sign \" + claimType + \" fail: \" + msg);\n"+
		"		}\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" * @param {string} signed Signed string to upload\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.handleSignClaim_ =\n"+
		"function(success, opt_fail, signed) {\n"+
		"	this.uploadString_(\n"+
		"		signed,\n"+
		"		success,\n"+
		"		function(msg) {\n"+
		"			this.safeFail_(opt_fail)(\"upload \" + claimType + \" fail: \" + msg);\n"+
		"		}\n"+
		"	)\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {string} permanode Permanode blobref.\n"+
		" * @param {string} attribute Name of the attribute to set.\n"+
		" * @param {string} value Value to set the attribute to.\n"+
		" * @param {function(string)} success Success callback, called with blobref of\n"+
		" *   uploaded file.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.newSetAttributeClaim =\n"+
		"function(permanode, attribute, value, success, opt_fail) {\n"+
		"	this.changeAttribute_(permanode, \"set-attribute\", attribute, value,\n"+
		"		success, this.safeFail_(opt_fail)\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {string} permanode Permanode blobref.\n"+
		" * @param {string} attribute Name of the attribute to add.\n"+
		" * @param {string} value Value of the added attribute.\n"+
		" * @param {function(string)} success Success callback, called with blobref of\n"+
		" *   uploaded file.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.newAddAttributeClaim =\n"+
		"function(permanode, attribute, value, success, opt_fail) {\n"+
		"	this.changeAttribute_(permanode, \"add-attribute\", attribute, value,\n"+
		"		success, this.safeFail_(opt_fail)\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {string} permanode Permanode blobref.\n"+
		" * @param {string} attribute Name of the attribute to delete.\n"+
		" * @param {string} value Value of the attribute to delete.\n"+
		" * @param {function(string)} success Success callback, called with blobref of\n"+
		" *   uploaded file.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.newDelAttributeClaim =\n"+
		"function(permanode, attribute, value, success, opt_fail) {\n"+
		"	this.changeAttribute_(permanode, \"del-attribute\", attribute, value,\n"+
		"		success, this.safeFail_(opt_fail)\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {File} file File to be uploaded.\n"+
		" * @param {function(string)} success Success callback, called with blobref of\n"+
		" * uploaded file.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" * @param {?Function} opt_onContentsRef Optional callback to set contents during "+
		"upload.\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.uploadFile =\n"+
		"function(file, success, opt_fail, opt_onContentsRef) {\n"+
		"	var fr = new FileReader();\n"+
		"	var onload = function() {\n"+
		"		var dataurl = fr.result;\n"+
		"		var comma = dataurl.indexOf(\",\");\n"+
		"		if (comma != -1) {\n"+
		"			var b64 = dataurl.substring(comma + 1);\n"+
		"			var arrayBuffer = Base64.decode(b64).buffer;\n"+
		"			var hash = Crypto.SHA1(new Uint8Array(arrayBuffer, 0));\n"+
		"\n"+
		"			var contentsRef = \"sha1-\" + hash;\n"+
		"			if (opt_onContentsRef) {\n"+
		"				opt_onContentsRef(contentsRef);\n"+
		"			}\n"+
		"			this.camliUploadFileHelper_(file, contentsRef, success, this.safeFail_(opt_fai"+
		"l));\n"+
		"		}\n"+
		"	};\n"+
		"	fr.onload = goog.bind(onload, this);\n"+
		"	fr.onerror = function() {\n"+
		"		console.log(\"FileReader onerror: \" + fr.error + \" code=\" + fr.error.code);\n"+
		"	};\n"+
		"	fr.readAsDataURL(file);\n"+
		"};\n"+
		"\n"+
		"// camliUploadFileHelper uploads the provided file with contents blobref contents"+
		"BlobRef\n"+
		"// and returns a blobref of a file blob.  It does not create any permanodes.\n"+
		"// Most callers will use camliUploadFile instead of this helper.\n"+
		"//\n"+
		"// camliUploadFileHelper only uploads chunks of the file if they don't already ex"+
		"ist\n"+
		"// on the server. It starts by assuming the file might already exist on the serve"+
		"r\n"+
		"// and, if so, uses an existing (but re-verified) file schema ref instead.\n"+
		"/**\n"+
		" * @param {File} file File to be uploaded.\n"+
		" * @param {string} contentsBlobRef Blob ref of file as sha1'd locally.\n"+
		" * @param {function(string)} success function(fileBlobRef) of the\n"+
		" * server-validated or just-uploaded file schema blob.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.camliUploadFileHelper_ =\n"+
		"function(file, contentsBlobRef, success, opt_fail) {\n"+
		"	if (!this.config_.uploadHelper) {\n"+
		"		this.safeFail_(opt_fail)(\"no uploadHelper available\");\n"+
		"		return;\n"+
		"	}\n"+
		"\n"+
		"	var doUpload = goog.bind(function() {\n"+
		"		var fd = new FormData();\n"+
		"		fd.append(\"TODO-some-uploadHelper-form-name\", file);\n"+
		"		this.sendXhr_(\n"+
		"			this.config_.uploadHelper,\n"+
		"			goog.bind(this.handleUpload_, this,\n"+
		"				file, contentsBlobRef, success, this.safeFail_(opt_fail)\n"+
		"			),\n"+
		"			\"POST\",\n"+
		"			fd\n"+
		"		);\n"+
		"	}, this);\n"+
		"\n"+
		"	this.findExistingFileSchemas_(\n"+
		"		contentsBlobRef,\n"+
		"		goog.bind(this.dupCheck_, this,\n"+
		"			doUpload, contentsBlobRef, success\n"+
		"		),\n"+
		"		this.safeFail_(opt_fail)\n"+
		"	)\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @param {File} file File to be uploaded.\n"+
		" * @param {string} contentsBlobRef Blob ref of file as sha1'd locally.\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {?Function} opt_fail Optional fail callback.\n"+
		" * @param {goog.events.Event} e Event that triggered this\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.handleUpload_ =\n"+
		"function(file, contentsBlobRef, success, opt_fail, e) {\n"+
		"	this.handlePost_(\n"+
		"		goog.bind(function(res) {\n"+
		"			var resObj = JSON.parse(res);\n"+
		"			if (resObj.got && resObj.got.length == 1 && resObj.got[0].fileref) {\n"+
		"				var fileblob = resObj.got[0].fileref;\n"+
		"				console.log(\"uploaded \" + contentsBlobRef + \" => file blob \" + fileblob);\n"+
		"				success(fileblob);\n"+
		"			} else {\n"+
		"				this.safeFail_(opt_fail)(\"failed to upload \" + file.name + \": \" + contentsBlo"+
		"bRef + \": \" + JSON.stringify(res, null, 2))\n"+
		"			}\n"+
		"		}, this),\n"+
		"		this.safeFail_(opt_fail),\n"+
		"		e\n"+
		"	)\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {string} wholeDigestRef file digest.\n"+
		" * @param {Function} success callback with data.\n"+
		" * @param {?Function} opt_fail optional failure calback\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.findExistingFileSchemas_ =\n"+
		"function(wholeDigestRef, success, opt_fail) {\n"+
		"	var path = goog.uri.utils.appendPath(this.config_.searchRoot, 'camli/search/file"+
		"s');\n"+
		"	path = goog.uri.utils.appendParam(path, 'wholedigest', wholeDigestRef);\n"+
		"\n"+
		"	this.sendXhr_(\n"+
		"		path,\n"+
		"		goog.bind(this.genericHandleSearch_, this,\n"+
		"			success, this.safeFail_(opt_fail)\n"+
		"		)\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {Function} doUpload fun that takes care of uploading.\n"+
		" * @param {string} contentsBlobRef Blob ref of file as sha1'd locally.\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {Object} res result from the wholedigest search.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.dupCheck_ =\n"+
		"function(doUpload, contentsBlobRef, success, res) {\n"+
		"	var remain = res.files;\n"+
		"	var checkNext = goog.bind(function(files) {\n"+
		"		if (files.length == 0) {\n"+
		"			doUpload();\n"+
		"			return;\n"+
		"		}\n"+
		"		// TODO: verify filename and other file metadata in the\n"+
		"		// file json schema match too, not just the contents\n"+
		"		var checkFile = files[0];\n"+
		"		console.log(\"integrity checking the reported dup \" + checkFile);\n"+
		"\n"+
		"		// TODO(mpl): see about passing directly a ref of files maybe instead of a copy"+
		"?\n"+
		"		// just being careful for now.\n"+
		"		this.sendXhr_(\n"+
		"			this.config_.downloadHelper + checkFile + \"/?verifycontents=\" + contentsBlobRe"+
		"f,\n"+
		"			goog.bind(this.handleVerifycontents_, this,\n"+
		"				contentsBlobRef, files.slice(), checkNext, success),\n"+
		"			\"HEAD\"\n"+
		"		);\n"+
		"	}, this);\n"+
		"	checkNext(remain);\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @param {string} contentsBlobRef Blob ref of file as sha1'd locally.\n"+
		" * @param {Array.<string>} files files to check.\n"+
		" * @param {Function} checkNext fun, recursive call.\n"+
		" * @param {Function} success Success callback.\n"+
		" * @param {goog.events.Event} e Event that triggered this\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.ServerConnection.prototype.handleVerifycontents_ =\n"+
		"function(contentsBlobRef, files, checkNext, success, e) {\n"+
		"	var xhr = e.target;\n"+
		"	var error = !(xhr.isComplete() && xhr.getStatus() == 200);\n"+
		"	var checkFile = files.shift();\n"+
		"\n"+
		"	if (error) {\n"+
		"		console.log(\"integrity check failed on \" + checkFile);\n"+
		"		checkNext(files);\n"+
		"		return;\n"+
		"	}\n"+
		"	if (xhr.getResponseHeader(\"X-Camli-Contents\") == contentsBlobRef) {\n"+
		"		console.log(\"integrity check passed on \" + checkFile + \"; using it.\");\n"+
		"		success(checkFile);\n"+
		"	} else {\n"+
		"		checkNext(files);\n"+
		"	}\n"+
		"};\n"+
		"\n"+
		"// TODO(mpl): if we don't end up using it anywhere else, just make\n"+
		"// it a closure within changeAttribute_.\n"+
		"// Format |dateVal| as specified by RFC 3339.\n"+
		"function dateToRfc3339String(dateVal) {\n"+
		"	// Return a string containing |num| zero-padded to |length| digits.\n"+
		"	var pad = function(num, length) {\n"+
		"		var numStr = \"\" + num;\n"+
		"		while (numStr.length < length) {\n"+
		"			numStr = \"0\" + numStr;\n"+
		"		}\n"+
		"		return numStr;\n"+
		"	};\n"+
		"	return dateVal.getUTCFullYear() + \"-\" + pad(dateVal.getUTCMonth() + 1, 2) + \"-\" "+
		"+ pad(dateVal.getUTCDate(), 2) + \"T\" +\n"+
		"		pad(dateVal.getUTCHours(), 2) + \":\" + pad(dateVal.getUTCMinutes(), 2) + \":\" + p"+
		"ad(dateVal.getUTCSeconds(), 2) + \"Z\";\n"+
		"};\n"+
		"\n"+
		""))
}
