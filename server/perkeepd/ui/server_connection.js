/*
Copyright 2013 The Perkeep Authors

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

goog.provide('cam.ServerConnection');

goog.require('goog.string');
goog.require('goog.net.XhrIo');
goog.require('goog.Uri'); // because goog.net.XhrIo forgot to include it.
goog.require('goog.debug.ErrorHandler'); // because goog.net.Xhrio forgot to include it.
goog.require('goog.uri.utils');

goog.require('cam.blob');
goog.require('cam.ServerType');
goog.require('cam.WorkerMessageRouter');

// TODO(mpl): directly get it from the auth pkg if we use gopherjs.
var OMIT_AUTH_TOKEN = "OmitAuthToken";

// @fileoverview Connection to the blob server and API for the RPCs it provides. All blob index UI code should use this connection to contact the server.
// @param {cam.ServerType.DiscoveryDocument} config Discovery document for the current server.
// @param {Function=} opt_sendXhr Function for sending XHRs for testing.
// @constructor
cam.ServerConnection = function(config, opt_sendXhr) {
	this.config_ = config;
	this.sendXhr_ = opt_sendXhr || goog.net.XhrIo.send;
	this.worker_ = null;
};

cam.ServerConnection.DESCRIBE_REQUEST = {
	// TODO(aa): This is not perfect. The describe request will return some data we don't care about:
	// - Properties we don't use
	// See: https://perkeep.org/issue/319

	depth: 1,
	rules: [
		{
			attrs: ['camliContent', 'camliContentImage']
		},
		{
			ifCamliNodeType: 'foursquare.com:checkin',
			attrs: ['foursquareVenuePermanode']
		},
		{
			ifCamliNodeType: 'foursquare.com:venue',
			attrs: ['camliPath:photos'],
			rules: [
				{ attrs: ['camliPath:*'] }
			]
		}
	]
};

cam.ServerConnection.prototype.getPermanodeWithContent = function(contentRef, success) {
	var query = {
		permanode: {
			attr: "camliContent",
			value: contentRef,
		},
	};
	var callback = function(result) {
		if (!result || !result.blobs || result.blobs.length == 0) {
			success();
			return;
		}
		success(result.blobs[0].blob);
	}
	this.search(query, null, callback);
};

// If child is a camliMember of parent success is called with 'true', otherrwise 'false'
// @param {string} blobref of the child
// @param {string} blobref of the parent
// @param {Function} success callback with data.
cam.ServerConnection.prototype.isCamliMember = function(child, parent, success) {
	var query = {
		logical: {
			a: {
				permanode: {
					attr: "camliMember",
					ValueInSet: {
						blobRefPrefix: child,
					}
				}
			},
			op: "and",
			b: {
				blobRefPrefix: parent,
			}
		},
	};
	var callback = function(result) {
		if (!result || !result.blobs || result.blobs.length == 0) {
			success(false);
			return;
		}
		success(true);
	}
	this.search(query, null, callback);
};

cam.ServerConnection.prototype.getWorker_ = function() {
	if (!this.worker_) {
		var r = new Date().getTime(); // For cachebusting the worker. Sigh. We need content stamping.
		this.worker_ = new cam.WorkerMessageRouter(new Worker('hash_worker.js?r=' + r));
	}
	return this.worker_;
};

cam.ServerConnection.prototype.getConfig = function() {
	return this.config_;
};

// @param {string} blobref blobref whose contents we want.
// @param {Function} success callback with data.
// @param {?Function} opt_fail optional failure callback
cam.ServerConnection.prototype.getBlobContents = function(blobref, success, opt_fail) {
	var path = goog.uri.utils.appendPath(
		this.config_.blobRoot, 'camli/' + blobref
	);
	this.sendXhr_(path,
		goog.bind(this.handleXhrResponseText_, this,
			{success: success, fail: opt_fail}
		)
	);
};

// @param {goog.events.Event} e Event that triggered this
cam.ServerConnection.prototype.handleXhrResponseJson_ = function(callbacks, e) {
	var success = callbacks.success
	var fail = callbacks.fail
	var xhr = e.target;
	var error = !xhr.isSuccess();
	var result = null;

	try {
		result = xhr.getResponseJson();
	} catch(err) {
		result = "Response was not valid JSON: " + xhr.getResponseText();
	}

	if (error) {
		if (fail) {
			fail(result);
		} else {
			console.log('Failed XHR (JSON) in ServerConnection: ' + result.error || result);
		}
	} else {
		success(result);
	}
};

// @param {Function} success callback with data.
// @param {?Function} opt_fail optional failure callback
cam.ServerConnection.prototype.discoSignRoot = function(success, opt_fail) {
	var path = goog.uri.utils.appendPath(this.config_.jsonSignRoot, '/camli/sig/discovery');
	this.sendXhr_(path, goog.bind(this.handleXhrResponseJson_, this, {success: success, fail: opt_fail}));
};

// @param {function(cam.ServerType.StatusResponse)} success.
cam.ServerConnection.prototype.serverStatus = function(success) {
	var path = goog.uri.utils.appendPath(this.config_.statusRoot, 'status.json');

	this.sendXhr_(path,
		goog.bind(this.handleXhrResponseJson_, this, {success: success, fail: function(msg) {
			console.log("serverStatus error: " + msg);
		}}));
};

// @param {string} shareURL
// @param {Function} success callback.
// @param {?Function} opt_fail optional failure callback.
cam.ServerConnection.prototype.importShare = function(shareURL, success, opt_fail) {
	this.sendXhr_(
		goog.uri.utils.appendPath(this.config_.uiRoot, 'importshare'),
		goog.bind(this.handleXhrResponseEmpty_, this, {success: success, fail: opt_fail}),
		"POST",
		"shareurl=" + encodeURIComponent(shareURL),
		{"Content-Type": "application/x-www-form-urlencoded"},
	);
};

// @param {function(cam.ServerType.StatusResponse)} success.
cam.ServerConnection.prototype.importShareStatus = function(success) {
	this.sendXhr_(
		goog.uri.utils.appendPath(this.config_.uiRoot, 'importshare'),
		goog.bind(this.handleXhrResponseJson_, this, {success: success, fail: function(msg) {
			console.log("importShareStatus error: " + msg);
		}}));
};


// @param {string} blobref root of the tree
// @param {Function} success callback with data.
// @param {?Function} opt_fail optional failure callback
cam.ServerConnection.prototype.getFileTree = function(blobref, success, opt_fail) {
	// TODO(mpl): do it relatively to a discovered root?
	var path = "./tree/" + blobref;
	this.sendXhr_(path, goog.bind(this.handleXhrResponseJson_, this, {success: success, fail: opt_fail}));
};


// @param {string} signer permanode must belong to signer.
// @param {string} attr searched attribute.
// @param {string} value value of the searched attribute.
// @param {Function} success.
// @param {Function=} opt_fail Optional fail callback.
cam.ServerConnection.prototype.permanodeOfSignerAttrValue = function(signer, attr, value, success, opt_fail) {
	var path = goog.uri.utils.appendPath(this.config_.searchRoot, 'camli/search/signerattrvalue');
	path = goog.uri.utils.appendParams(path,
		'signer', signer, 'attr', attr, 'value', value
	);

	this.sendXhr_(
		path,
		goog.bind(this.handleXhrResponseJson_, this,
			{success: success, fail: opt_fail}
		)
	);
};


// @param {string|object} query If string, will be sent as 'expression', otherwise will be sent as 'constraint'.
// @param {?object} opts query parameters: describe, sort, limit, around, continuationToken.
cam.ServerConnection.prototype.buildQuery = function(callerQuery, opts) {
	var query = {
		// TODO(mpl): it'd be better to not ask for a sort when none is needed (less work for server),
		// e.g. for a plain BlobRefPrefix query.
		sort: "-created"
	};

	if (goog.isString(callerQuery)) {
		query.expression = callerQuery;
	} else {
		query.constraint = callerQuery;
	}

	if (!opts) {
		return query;
	}
	if (opts.sort) {
		query.sort = opts.sort;
	}
	if (opts.describe) {
		query.describe = opts.describe;
	}
	if (opts.limit) {
		query.limit = opts.limit;
	}
	if (opts.around) {
		query.around = opts.around;
	} else if (opts.continuationToken) {
		query.continue = opts.continuationToken;
	}

	return query;
}

// @param {string|object} query If string, will be sent as 'expression', otherwise will be sent as 'constraint'.
// @param {?object} opts The query parameters: describe, sort, limit, around, continuationToken.
cam.ServerConnection.prototype.search = function(query, opts, callback) {
	var path = goog.uri.utils.appendPath(this.config_.searchRoot, 'camli/search/query');
	this.sendXhr_(path,
		goog.bind(this.handleXhrResponseJson_, this, {success: callback, fail: callback}),
		"POST", JSON.stringify(this.buildQuery(query, opts)));
};

// Where is the target accessed via? (paths it's at)
// @param {string} signer owner of permanode.
// @param {string} target blobref of permanode we want to find paths to
// @param {Function} success.
// @param {Function=} opt_fail Optional fail callback.
cam.ServerConnection.prototype.pathsOfSignerTarget = function(target, success, opt_fail) {
	var path = goog.uri.utils.appendPath(
		this.config_.searchRoot, 'camli/search/signerpaths'
	);
	path = goog.uri.utils.appendParams(path, 'signer', this.config_.signing.publicKeyBlobRef, 'target', target);
	this.sendXhr_(path,
		goog.bind(this.handleXhrResponseJson_, this, {success: success, fail: opt_fail}));
};

// @param {string} permanode Permanode blobref.
// @param {Function} success.
// @param {Function=} opt_fail Optional fail callback.
cam.ServerConnection.prototype.permanodeClaims = function(permanode, success, opt_fail) {
	var path = goog.uri.utils.appendPath(
		this.config_.searchRoot, 'camli/search/claims?permanode=' + permanode
	);

	this.sendXhr_(
		path,
		goog.bind(this.handleXhrResponseJson_, this,
			{success: success, fail: opt_fail}
		)
	);
};

// @param {Object} clearObj Unsigned object.
// @param {Function} success Success callback.
// @param {?Function} opt_fail Optional fail callback.
cam.ServerConnection.prototype.sign_ = function(clearObj, success, opt_fail) {
	var sigConf = this.config_.signing;
	if (!sigConf || !sigConf.publicKeyBlobRef) {
		this.failOrLog_(opt_fail, "Missing Camli.config.signing.publicKeyBlobRef");
		return;
	}
	var authToken = this.config_.authToken;
	if (!authToken) {
		this.failOrLog_(opt_fail, "Missing Camli.config.authToken");
		return;
	}
	if (authToken != OMIT_AUTH_TOKEN) {
		var header = {"Content-Type": "application/x-www-form-urlencoded",
			"Authorization": "Token "+authToken};
	} else {
		var header = {"Content-Type": "application/x-www-form-urlencoded"};
	}

	clearObj.camliSigner = sigConf.publicKeyBlobRef;
	var camVersion = clearObj.camliVersion;
	if (camVersion) {
		 delete clearObj.camliVersion;
	}
	var clearText = JSON.stringify(clearObj, null, "	");
	if (camVersion) {
		 clearText = "{\"camliVersion\":" + camVersion + ",\n" + clearText.substr("{\n".length);
	}
	this.sendXhr_(
		sigConf.signHandler,
		goog.bind(this.handleXhrResponseText_, this,
			{success: success, fail: opt_fail}),
		"POST",
		"json=" + encodeURIComponent(clearText),
		header
	);
};

// @param {Object} signed Signed JSON blob (string) to verify.
// @param {Function} success Success callback.
// @param {?Function} opt_fail Optional fail callback.
cam.ServerConnection.prototype.verify_ = function(signed, success, opt_fail) {
	var sigConf = this.config_.signing;
	if (!sigConf || !sigConf.publicKeyBlobRef) {
		if (opt_fail) {
			opt_fail("Missing Camli.config.signing.publicKeyBlobRef");
		} else {
			console.log("Missing Camli.config.signing.publicKeyBlobRef");
		}
		return;
	}
	var authToken = this.config_.authToken;
	if (!authToken) {
		this.failOrLog_(opt_fail, "Missing Camli.config.authToken");
		return;
	}
	if (authToken != OMIT_AUTH_TOKEN) {
		var header = {"Content-Type": "application/x-www-form-urlencoded",
			"Authorization": "Token "+authToken};
	} else {
		var header = {"Content-Type": "application/x-www-form-urlencoded"};
	}
	this.sendXhr_(
		sigConf.verifyHandler,
		goog.bind(this.handleXhrResponseText_, this,
			{success: success, fail: opt_fail}),
		"POST",
		"sjson=" + encodeURIComponent(signed),
		header
	);
};

// @param {goog.events.Event} e Event that triggered this
cam.ServerConnection.prototype.handleXhrResponseEmpty_ = function(callbacks, e) {
	const fail = callbacks.fail;
	const xhr = e.target;
	const error = !xhr.isSuccess();
	if (error) {
		if (fail) {
			fail(xhr.getLastError());
		} else {
			console.log('Failed XHR (empty) in ServerConnection: ' + xhr.getLastError());
		}
		return;
	}
	callbacks.success();
};

// @param {goog.events.Event} e Event that triggered this
cam.ServerConnection.prototype.handleXhrResponseText_ = function(callbacks, e) {
	var fail = callbacks.fail;
	var xhr = e.target;
	var error = !xhr.isSuccess();
	var result = null;
	if (!error) {
		result = xhr.getResponseText();
		error = !result;
	}
	if (error) {
		if (fail) {
			fail(xhr.getLastError());
		} else {
			// TODO(bslatkin): Add a default failure event handler to this class.
			console.log('Failed XHR (text) in ServerConnection: ' + xhr.getLastError());
		}
		return;
	}
	callbacks.success(result);
};

// @param {string} s String to upload.
// @param {Function} success Success callback.
// @param {?Function} opt_fail Optional fail callback.
cam.ServerConnection.prototype.uploadString_ = function(s, success, opt_fail) {
	var authToken = this.config_.authToken;
	if (!authToken) {
		this.failOrLog_(opt_fail, "Missing Camli.config.authToken");
		return;
	}
	if (authToken != OMIT_AUTH_TOKEN) {
		var header = {"Authorization": "Token "+authToken};
	}
	var blobref = cam.blob.refFromString(s);
	var bb = new Blob([s]);
	var fd = new FormData();
	// Regarding https://perkeep.org/issue/660 , it seems like doing
	// fd.append(blobref, s);
	// instead does not suffer for the "first upload" problem. However,
	// we get a mismatched blobRef if we do that, which I suppose is a
	// JSON serialization problem that we could avoid by adopting a convention?
	fd.append(blobref, bb);

	// TODO: hack, hard-coding the upload URL here.
	// Change the spec now that App Engine permits 32 MB requests
	// and permit a PUT request on the sha1?	Or at least let us
	// specify the well-known upload URL?	In cases like this, uploading
	// a new permanode, it's silly to even stat.
	this.sendXhr_(
		this.config_.blobRoot + "camli/upload",
		goog.bind(this.handleUploadString_, this,
			blobref,
			{success: success, fail: opt_fail}
		),
		"POST",
		fd,
		header
	);

};

// @param {string} blobref Uploaded blobRef.
// @param {goog.events.Event} e Event that triggered this
cam.ServerConnection.prototype.handleUploadString_ = function(blobref, callbacks, e) {
	this.handleXhrResponseText_({
		success: function(resj) {
			if (!resj) {
				alert("upload failed; no response");
				return;
			}
			var resObj = JSON.parse(resj);
			if (!resObj.received || !resObj.received[0] || !resObj.received[0].blobRef) {
				alert("upload permanode fail, expected blobRef not in response");
				return;
			}
			if (callbacks.success) {
				callbacks.success(blobref);
			}
		},
		fail: callbacks.fail},
		e
	)
};

cam.ServerConnection.prototype.failOrLog_ = function(fail, msg) {
	if (fail) {
		fail(msg);
	} else {
		console.log(msg);
	}
};

// @param {Function} success Success callback.
// @param {?Function} opt_fail Optional fail callback.
cam.ServerConnection.prototype.createPermanode = function(success, opt_fail) {
	var json = {
		"camliVersion": 1,
		"camliType": "permanode",
		"random": ""+Math.random()
	};
	this.sign_(json,
		goog.bind(function(signed) {
			this.uploadString_(signed, success, opt_fail)
		}, this),
		goog.bind(function(msg) {
			this.failOrLog_(opt_fail, "create permanode: signing failed: " + msg);
		}, this)
	);
};

// @param {string} permanode Permanode to change.
// @param {string} claimType What kind of claim: "add-attribute", "set-attribute"...
// @param {string} attribute What attribute the claim applies to.
// @param {string} value Attribute value.
// @param {Function} success Success callback.
// @param {?Function} opt_fail Optional fail callback.
cam.ServerConnection.prototype.changeAttribute_ = function(permanode, claimType, attribute, value, success, opt_fail) {
	var json = {
		"camliVersion": 1,
		"camliType": "claim",
		"permaNode": permanode,
		"claimType": claimType,
		// TODO(mpl): to (im)port.
		"claimDate": dateToRfc3339String(new Date()),
		"attribute": attribute,
		"value": value
	};
	this.sign_(json,
		goog.bind(function(signed) {
			this.uploadString_(signed, success, opt_fail)
		}, this),
		goog.bind(function(msg) {
			this.failOrLog_(opt_fail, "change attribute: signing failed: " + msg);
		}, this)
	);
};

// @param {Function} success Success callback.
// @param {?Function} opt_fail Optional fail callback.
cam.ServerConnection.prototype.newShareClaim = function(
	authType,
	transitive,
	target,
	success,
	opt_fail,
) {
	const json = {
		"camliVersion": 1,
		"camliType": "claim",
		"claimType": "share",
		"claimDate": dateToRfc3339String(new Date()),
		"authType": authType,
		"transitive": transitive,
		"target": target,
	};
	this.sign_(json,
		goog.bind(function(signed) {
			this.uploadString_(signed, success, opt_fail)
		}, this),
		goog.bind(function(msg) {
			this.failOrLog_(opt_fail, "share claim: signing failed: " + msg);
		}, this)
	);
};

// @param {Function} success Success callback.
// @param {?Function} opt_fail Optional fail callback.
cam.ServerConnection.prototype.newStaticSet = function(
	members,
	mergeSets,
	success,
	opt_fail,
) {
	const json = {
		"camliVersion": 1,
		"camliType": "static-set",
	}
	// TODO(aviau): Avoid > 1MB blobs by using mergeSets if required.
	if (members) {
		json["members"] = members;
	}
	if (mergeSets) {
		json["mergeSets"] = mergeSets;
	}
	this.sign_(json,
		goog.bind(function(signed) {
			this.uploadString_(signed, success, opt_fail)
		}, this),
		goog.bind(function(msg) {
			this.failOrLog_(opt_fail, "static-set: signing failed: " + msg);
		}, this)
	);
};

// @param {string} permanode Permanode to delete.
// @param {Function} success Success callback.
// @param {?Function} opt_fail Optional fail callback.
cam.ServerConnection.prototype.newDeleteClaim = function(permanode, success, opt_fail) {
	var json = {
		"camliVersion": 1,
		"camliType": "claim",
		"target": permanode,
		"claimType": "delete",
		"claimDate": dateToRfc3339String(new Date())
	};
	this.sign_(json,
		goog.bind(function(signed) {
			this.uploadString_(signed, success, opt_fail)
		}, this),
		goog.bind(function(msg) {
			this.failOrLog_(opt_fail, "delete attribute: signing failed: " + msg);
		}, this)
	);
};

// @param {string} permanode Permanode blobref.
// @param {string} attribute Name of the attribute to set.
// @param {string} value Value to set the attribute to.
// @param {function(string)} success Success callback, called with blobref of uploaded file.
// @param {?Function} opt_fail Optional fail callback.
cam.ServerConnection.prototype.newSetAttributeClaim = function(permanode, attribute, value, success, opt_fail) {
	this.changeAttribute_(permanode, "set-attribute", attribute, value,
		success, opt_fail
	);
};


// @param {string} permanode Permanode blobref.
// @param {string} attribute Name of the attribute to add.
// @param {string} value Value of the added attribute.
// @param {function(string)} success Success callback, called with blobref of uploaded file.
// @param {?Function} opt_fail Optional fail callback.
cam.ServerConnection.prototype.newAddAttributeClaim = function(permanode, attribute, value, success, opt_fail) {
	this.changeAttribute_(permanode, "add-attribute", attribute, value,
		success, opt_fail
	);
};

// @param {string} permanode Permanode blobref.
// @param {string} attribute Name of the attribute to delete.
// @param {string} value Value of the attribute to delete.
// @param {function(string)} success Success callback, called with blobref of uploaded file.
// @param {?Function} opt_fail Optional fail callback.
cam.ServerConnection.prototype.newDelAttributeClaim = function(permanode, attribute, value, success, opt_fail) {
	this.changeAttribute_(permanode, "del-attribute", attribute, value,
		success, opt_fail
	);
};

// @param {File} file File to be uploaded.
// @param {function(string)} success Success callback, called with blobref of
// uploaded file.
// @param {?Function} opt_fail Optional fail callback.
// @param {?Function} opt_onContentsRef Optional callback to set contents during upload.
cam.ServerConnection.prototype.uploadFile = function(file, success, opt_fail, opt_onContentsRef) {
	var msg = {};
	msg.blob = file;
	msg.doLegacySHA1 = this.config_.hasLegacySHA1Index;
	this.getWorker_().sendMessage('ref', msg, function(wholeRefs) {
		if (opt_onContentsRef) {
			opt_onContentsRef(wholeRefs);
		}
		this.camliUploadFileHelper_(file, wholeRefs, success, opt_fail);
	}.bind(this));
};

// camliUploadFileHelper uploads the provided file with contents blobref contentsBlobRef
// and returns a blobref of a file blob.	It does not create any permanodes.
// Most callers will use camliUploadFile instead of this helper.
//
// camliUploadFileHelper only uploads chunks of the file if they don't already exist
// on the server. It starts by assuming the file might already exist on the server
// and, if so, uses an existing (but re-verified) file schema ref instead.
// @param {File} file File to be uploaded.
// @param {List} wholeRefs Blob ref of file contents as computed locally.
// Second, optional, element of the list is legacy SHA-1 blob Ref.
// @param {function(string)} success function(fileBlobRef) of the
// server-validated or just-uploaded file schema blob.
// @param {?Function} opt_fail Optional fail callback.
cam.ServerConnection.prototype.camliUploadFileHelper_ = function(file, wholeRefs, success, opt_fail) {
	if (!this.config_.uploadHelper) {
		this.failOrLog_(opt_fail, "no uploadHelper available");
		return;
	}
	var authToken = this.config_.authToken;
	if (!authToken) {
		this.failOrLog_(opt_fail, "Missing Camli.config.authToken");
		return;
	}
	if (authToken != OMIT_AUTH_TOKEN) {
		var header = {"Authorization": "Token "+authToken};
	}
	var doUpload = goog.bind(function() {
		var fd = new FormData();
		fd.append("modtime", dateToRfc3339String(new Date(file.lastModified)));
		fd.append("ui-upload-file-helper-form", file);
		this.sendXhr_(
			this.config_.uploadHelper,
			goog.bind(this.handleUpload_, this,
				file, wholeRefs[0], {success: success, fail: opt_fail}
			),
			"POST",
			fd,
			header
		);
	}, this);

	this.findExistingFileSchemas_(
		wholeRefs,
		goog.bind(this.dupCheck_, this,
			doUpload, success
		),
		opt_fail
	)
}

// @param {File} file File to be uploaded.
// @param {string} contentsBlobRef Blob ref of file as sha1'd locally.
// @param {goog.events.Event} e Event that triggered this
cam.ServerConnection.prototype.handleUpload_ = function(file, contentsBlobRef, callbacks, e) {
	this.handleXhrResponseText_({
		success: goog.bind(function(res) {
			var resObj = JSON.parse(res);
			if (resObj.got && resObj.got.length == 1 && resObj.got[0].fileref) {
				var fileblob = resObj.got[0].fileref;
				console.log("uploaded " + contentsBlobRef + " => file blob " + fileblob);
				callbacks.success(fileblob);
			} else {
				this.failOrLog_(callbacks.fail, "failed to upload " + file.name + ": " + contentsBlobRef + ": " + JSON.stringify(res, null, 2));
			}
		}, this),
		fail: callbacks.fail},
		e
	)
};

// @param {List} wholeDigestRefs file contents digests (current hash in use, then optionally legacy SHA-1 hash).
// @param {Function} success callback with data.
// @param {?Function} opt_fail optional failure callback
cam.ServerConnection.prototype.findExistingFileSchemas_ = function(wholeDigestRefs, success, opt_fail) {
	var path = goog.uri.utils.appendPath(this.config_.searchRoot, 'camli/search/files');
	for (var i=0; i<wholeDigestRefs.length; i++) {
		path = goog.uri.utils.appendParam(path, 'wholedigest', wholeDigestRefs[i]);
		console.log(path);
	}

	this.sendXhr_(
		path,
		goog.bind(this.handleXhrResponseJson_, this,
			{success: success, fail: opt_fail}
		)
	);
};

// @param {Function} doUpload fun that takes care of uploading.
// @param {Function} success Success callback.
// @param {Object} res result from the wholedigest search, that maps a wholeRef to its file blobRefs.
cam.ServerConnection.prototype.dupCheck_ = function(doUpload, success, res) {
	var checkNext = goog.bind(function(filesByWholeRef, wholeRefs, files) {
		if (!files || files.length == 0) {
			var nextWholeRefs = wholeRefs.slice(1);
			if (nextWholeRefs.length == 0) {
				doUpload();
				return;
			}
			var nextWholeRef = nextWholeRefs[0];
			var nextFiles = filesByWholeRef[nextWholeRef];
			checkNext(filesByWholeRef, nextWholeRefs, nextFiles);
			return;
		}

		var currentWholeRef = wholeRefs[0];
		var currentFile = files[0];

		this.sendXhr_(
			this.config_.downloadHelper + currentFile + "/?verifycontents=" + currentWholeRef,
			goog.bind(this.handleVerifycontents_, this,
				currentWholeRef, filesByWholeRef, wholeRefs, files, checkNext, success),
			"HEAD"
		);
	}, this);

	var filesByWholeRef = res.files;
	if (filesByWholeRef.length == 0) {
		doUpload();
		return;
	}
	var wholeRefs = Object.keys(filesByWholeRef);
	var files = filesByWholeRef[wholeRefs[0]];
	checkNext(filesByWholeRef, wholeRefs, files);
}

// @param {string} contentsBlobRef Blob ref of file as sha224'd locally.
// @param {Object} filesByWholeRef maps a wholeRef to its file blobRefs.
// @param {Array.<string>} wholeRefs wholeRefs to check.
// @param {Array.<string>} files files to check.
// @param {Function} checkNext fun, recursive call.
// @param {Function} success Success callback.
// @param {goog.events.Event} e Event that triggered this
cam.ServerConnection.prototype.handleVerifycontents_ = function(
	contentsBlobRef, filesByWholeRef, wholeRefs, files, checkNext, success, e) {
	var xhr = e.target;
	var error = !(xhr.isComplete() && xhr.getStatus() == 200);
	var checkFile = files.shift();

	if (error) {
		console.log("integrity check failed on " + checkFile);
		checkNext(filesByWholeRef, wholeRefs, files);
		return;
	}
	if (xhr.getResponseHeader("X-Camli-Contents") == contentsBlobRef) {
		console.log("integrity check passed on " + checkFile + "; using it.");
		success(checkFile);
	} else {
		checkNext(filesByWholeRef, wholeRefs, files);
	}
};

// Format |dateVal| as specified by RFC 3339.
function dateToRfc3339String(dateVal) {
	// Return a string containing |num| zero-padded to |length| digits.
	var pad = function(num, length) {
		var numStr = "" + num;
		while (numStr.length < length) {
			numStr = "0" + numStr;
		}
		return numStr;
	};

	return goog.string.subs("%s-%s-%sT%s:%s:%sZ",
		dateVal.getUTCFullYear(), pad(dateVal.getUTCMonth() + 1, 2), pad(dateVal.getUTCDate(), 2),
		pad(dateVal.getUTCHours(), 2), pad(dateVal.getUTCMinutes(), 2), pad(dateVal.getUTCSeconds(), 2));
};
