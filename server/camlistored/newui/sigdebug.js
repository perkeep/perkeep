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
 * @fileoverview Discovery Debug page.
 *
 */
goog.provide('camlistore.DebugPage');

goog.require('goog.dom');
goog.require('goog.events.EventType');
goog.require('goog.ui.Component');
goog.require('camlistore.ServerConnection');


// TODO(mpl): add button on index page (toolbar?) to come here.
/**
 * @param {camlistore.ServerType.DiscoveryDocument} config Global config
 *   of the current server this page is being rendered for.
 * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.
 *
 * @extends {goog.ui.Component}
 * @constructor
 */
camlistore.DebugPage = function(config, opt_domHelper) {
	goog.base(this, opt_domHelper);

	/**
	 * @type {Object}
	 * @private
	 */
	this.config_ = config;

	/**
	 * @type {Object}
	 * @private
	 */
	this.sigdisco_ = null;

	/**
	 * @type {camlistore.ServerConnection}
	 * @private
	 */
	this.connection_ = new camlistore.ServerConnection(config);

};
goog.inherits(camlistore.DebugPage, goog.ui.Component);

/**
 * Called when component's element is known to be in the document.
 */
camlistore.DebugPage.prototype.enterDocument = function() {
	camlistore.DebugPage.superClass_.enterDocument.call(this);

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


/**
 * Called when component's element is known to have been removed from the
 * document.
 */
camlistore.DebugPage.prototype.exitDocument = function() {
	camlistore.DebugPage.superClass_.exitDocument.call(this);
};


/**
 * Fake. We just get the info from the initial config.
 * @param {goog.events.Event} e The title form submit event.
 * @private
 */
camlistore.DebugPage.prototype.discoRoot_ = function(e) {
	var disco = "<pre>" + JSON.stringify(this.config_, null, 2) + "</pre>";
	goog.dom.getElement("discores").innerHTML = disco;
};


/**
 * @private
 */
camlistore.DebugPage.prototype.discoJsonSignRoot_ = function() {
	this.connection_.discoSignRoot(
		goog.bind(function(sigdisco) {
			this.sigdisco_ = sigdisco;
			var disco = "<pre>" + JSON.stringify(sigdisco, null, 2) + "</pre>";
			goog.dom.getElement("sigdiscores").innerHTML = disco;
		}, this)
	)
};


/**
 * @private
 */
camlistore.DebugPage.prototype.addKeyRef_ = function() {
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

/**
 * @private
 */
camlistore.DebugPage.prototype.doSign_ = function() {
	// we actually do not need sigdisco since sign_ will pull
	// all the needed info from the config_ instead. But I'm
	// leaving the check as the debug check is also a sort of demo.
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

/**
 * @private
 */
camlistore.DebugPage.prototype.doVerify_ = function() {
	// we actually do not need sigdisco since sign_ will pull
	// all the needed info from the config_ instead. But I'm
	// leaving the check as the debug check is also a sort of demo.
	if (!this.sigdisco_) {
		alert("must do jsonsign discovery first");
		return;
	}
	var signedta = goog.dom.getElement("signedjson");
	var sObj = JSON.parse(signedta.value);
	this.connection_.verify_(sObj,
		function(response) {
			var text = "<pre>" + response + "</pre>";
			goog.dom.getElement("verifyinfo").innerHTML = text;
		}
	)
}
