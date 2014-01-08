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

goog.provide('cam.DebugPage');

goog.require('goog.dom');
goog.require('goog.events.EventType');
goog.require('goog.ui.Component');

goog.require('cam.ServerConnection');

// TODO(mpl): add button on index page (toolbar?) to come here.
// @param {cam.ServerType.DiscoveryDocument} config Global config of the current server this page is being rendered for.
// @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.
cam.DebugPage = function(config, opt_domHelper) {
	goog.base(this, opt_domHelper);

	this.config_ = config;
	this.sigdisco_ = null;
	this.connection_ = new cam.ServerConnection(config);

};
goog.inherits(cam.DebugPage, goog.ui.Component);

cam.DebugPage.prototype.enterDocument = function() {
	cam.DebugPage.superClass_.enterDocument.call(this);

	// set up listeners
	goog.events.listen(goog.dom.getElement('discobtn'),
		goog.events.EventType.CLICK,
		this.discoRoot_,
		false, this);
	goog.events.listen(goog.dom.getElement('sigdiscobtn'),
		goog.events.EventType.CLICK,
		this.discoJsonSignRoot_,
		false, this);
	goog.events.listen(goog.dom.getElement('addkeyref'),
		goog.events.EventType.CLICK,
		this.addKeyRef_,
		false, this);
	goog.events.listen(goog.dom.getElement('sign'),
		goog.events.EventType.CLICK,
		this.doSign_,
		false, this);
	goog.events.listen(goog.dom.getElement('verify'),
		goog.events.EventType.CLICK,
		this.doVerify_,
		false, this);
};

cam.DebugPage.prototype.exitDocument = function() {
	cam.DebugPage.superClass_.exitDocument.call(this);
};

cam.DebugPage.prototype.discoRoot_ = function(e) {
	var disco = "<pre>" + JSON.stringify(this.config_, null, 2) + "</pre>";
	goog.dom.getElement("discores").innerHTML = disco;
};

cam.DebugPage.prototype.discoJsonSignRoot_ = function() {
	this.connection_.discoSignRoot(
		goog.bind(function(sigdisco) {
			this.sigdisco_ = sigdisco;
			var disco = "<pre>" + JSON.stringify(sigdisco, null, 2) + "</pre>";
			goog.dom.getElement("sigdiscores").innerHTML = disco;
		}, this)
	)
};

cam.DebugPage.prototype.addKeyRef_ = function() {
	if (!this.sigdisco_) {
		alert("must do jsonsign discovery first");				
		return;
	}
	var clearta = goog.dom.getElement("clearjson");
	var j;
	try {
		j = JSON.parse(clearta.value);
	} catch (x) {
		alert(x);
		return
	}
	j.camliSigner = this.sigdisco_.publicKeyBlobRef;
	clearta.value = JSON.stringify(j, null, 2);
}

cam.DebugPage.prototype.doSign_ = function() {
	// We actually do not need sigdisco since sign_ will pull all the needed info from the config_ instead. But I'm leaving the check as the debug check is also a sort of demo.
	if (!this.sigdisco_) {
		alert("must do jsonsign discovery first");
		return;
	}
	var clearta = goog.dom.getElement("clearjson");
	var clearObj = JSON.parse(clearta.value);
	this.connection_.sign_(clearObj,
		function(response) {
			goog.dom.getElement("signedjson").value = response;
		}
	)
}

cam.DebugPage.prototype.doVerify_ = function() {
	// We actually do not need sigdisco since sign_ will pull all the needed info from the config_ instead. But I'm leaving the check as the debug check is also a sort of demo.
	if (!this.sigdisco_) {
		alert("must do jsonsign discovery first");
		return;
	}
	var signedta = goog.dom.getElement("signedjson");
	this.connection_.verify_(signedta.value,
		function(response) {
			var text = "<pre>" + response + "</pre>";
			goog.dom.getElement("verifyinfo").innerHTML = text;
		}
	)
}
