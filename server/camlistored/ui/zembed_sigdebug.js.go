// THIS FILE IS AUTO-GENERATED FROM sigdebug.js
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("sigdebug.js", 4760, time.Unix(0, 1370942742232957700), fileembed.String("/*\n"+
		"Copyright 2011 Google Inc.\n"+
		"\n"+
		"Licensed under the Apache License, Version 2.0 (the \"License\");\n"+
		"you may not use this file except in compliance with the License.\n"+
		"You may obtain a copy of the License at\n"+
		"\n"+
		"     http://www.apache.org/licenses/LICENSE-2.0\n"+
		"\n"+
		"Unless required by applicable law or agreed to in writing, software\n"+
		"distributed under the License is distributed on an \"AS IS\" BASIS,\n"+
		"WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.\n"+
		"See the License for the specific language governing permissions and\n"+
		"limitations under the License.\n"+
		"*/\n"+
		"\n"+
		"/**\n"+
		" * @fileoverview Discovery Debug page.\n"+
		" *\n"+
		" */\n"+
		"goog.provide('camlistore.DebugPage');\n"+
		"\n"+
		"goog.require('goog.dom');\n"+
		"goog.require('goog.events.EventType');\n"+
		"goog.require('goog.ui.Component');\n"+
		"goog.require('camlistore.ServerConnection');\n"+
		"\n"+
		"\n"+
		"// TODO(mpl): add button on index page (toolbar?) to come here.\n"+
		"/**\n"+
		" * @param {camlistore.ServerType.DiscoveryDocument} config Global config\n"+
		" *   of the current server this page is being rendered for.\n"+
		" * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.\n"+
		" *\n"+
		" * @extends {goog.ui.Component}\n"+
		" * @constructor\n"+
		" */\n"+
		"camlistore.DebugPage = function(config, opt_domHelper) {\n"+
		"	goog.base(this, opt_domHelper);\n"+
		"\n"+
		"	/**\n"+
		"	 * @type {Object}\n"+
		"	 * @private\n"+
		"	 */\n"+
		"	this.config_ = config;\n"+
		"\n"+
		"	/**\n"+
		"	 * @type {Object}\n"+
		"	 * @private\n"+
		"	 */\n"+
		"	this.sigdisco_ = null;\n"+
		"\n"+
		"	/**\n"+
		"	 * @type {camlistore.ServerConnection}\n"+
		"	 * @private\n"+
		"	 */\n"+
		"	this.connection_ = new camlistore.ServerConnection(config);\n"+
		"\n"+
		"};\n"+
		"goog.inherits(camlistore.DebugPage, goog.ui.Component);\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to be in the document.\n"+
		" */\n"+
		"camlistore.DebugPage.prototype.enterDocument = function() {\n"+
		"	camlistore.DebugPage.superClass_.enterDocument.call(this);\n"+
		"\n"+
		"	// set up listeners\n"+
		"	goog.events.listen(goog.dom.getElement('discobtn'),\n"+
		"		goog.events.EventType.CLICK,\n"+
		"		this.discoRoot_,\n"+
		"		false, this);\n"+
		"	goog.events.listen(goog.dom.getElement('sigdiscobtn'),\n"+
		"		goog.events.EventType.CLICK,\n"+
		"		this.discoJsonSignRoot_,\n"+
		"		false, this);\n"+
		"	goog.events.listen(goog.dom.getElement('addkeyref'),\n"+
		"		goog.events.EventType.CLICK,\n"+
		"		this.addKeyRef_,\n"+
		"		false, this);\n"+
		"	goog.events.listen(goog.dom.getElement('sign'),\n"+
		"		goog.events.EventType.CLICK,\n"+
		"		this.doSign_,\n"+
		"		false, this);\n"+
		"	goog.events.listen(goog.dom.getElement('verify'),\n"+
		"		goog.events.EventType.CLICK,\n"+
		"		this.doVerify_,\n"+
		"		false, this);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to have been removed from the\n"+
		" * document.\n"+
		" */\n"+
		"camlistore.DebugPage.prototype.exitDocument = function() {\n"+
		"	camlistore.DebugPage.superClass_.exitDocument.call(this);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Fake. We just get the info from the initial config.\n"+
		" * @param {goog.events.Event} e The title form submit event.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.DebugPage.prototype.discoRoot_ = function(e) {\n"+
		"	var disco = \"<pre>\" + JSON.stringify(this.config_, null, 2) + \"</pre>\";\n"+
		"	goog.dom.getElement(\"discores\").innerHTML = disco;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.DebugPage.prototype.discoJsonSignRoot_ = function() {\n"+
		"	this.connection_.discoSignRoot(\n"+
		"		goog.bind(function(sigdisco) {\n"+
		"			this.sigdisco_ = sigdisco;\n"+
		"			var disco = \"<pre>\" + JSON.stringify(sigdisco, null, 2) + \"</pre>\";\n"+
		"			goog.dom.getElement(\"sigdiscores\").innerHTML = disco;\n"+
		"		}, this)\n"+
		"	)\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.DebugPage.prototype.addKeyRef_ = function() {\n"+
		"	if (!this.sigdisco_) {\n"+
		"		alert(\"must do jsonsign discovery first\");				\n"+
		"		return;\n"+
		"	}\n"+
		"	var clearta = goog.dom.getElement(\"clearjson\");\n"+
		"	var j;\n"+
		"	try {\n"+
		"		j = JSON.parse(clearta.value);\n"+
		"	} catch (x) {\n"+
		"		alert(x);\n"+
		"		return\n"+
		"	}\n"+
		"	j.camliSigner = this.sigdisco_.publicKeyBlobRef;\n"+
		"	clearta.value = JSON.stringify(j, null, 2);\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.DebugPage.prototype.doSign_ = function() {\n"+
		"	// we actually do not need sigdisco since sign_ will pull\n"+
		"	// all the needed info from the config_ instead. But I'm\n"+
		"	// leaving the check as the debug check is also a sort of demo.\n"+
		"	if (!this.sigdisco_) {\n"+
		"		alert(\"must do jsonsign discovery first\");\n"+
		"		return;\n"+
		"	}\n"+
		"	var clearta = goog.dom.getElement(\"clearjson\");\n"+
		"	var clearObj = JSON.parse(clearta.value);\n"+
		"	this.connection_.sign_(clearObj,\n"+
		"		function(response) {\n"+
		"			goog.dom.getElement(\"signedjson\").value = response;\n"+
		"		}\n"+
		"	)\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.DebugPage.prototype.doVerify_ = function() {\n"+
		"	// we actually do not need sigdisco since sign_ will pull\n"+
		"	// all the needed info from the config_ instead. But I'm\n"+
		"	// leaving the check as the debug check is also a sort of demo.\n"+
		"	if (!this.sigdisco_) {\n"+
		"		alert(\"must do jsonsign discovery first\");\n"+
		"		return;\n"+
		"	}\n"+
		"	var signedta = goog.dom.getElement(\"signedjson\");\n"+
		"	var sObj = JSON.parse(signedta.value);\n"+
		"	this.connection_.verify_(sObj,\n"+
		"		function(response) {\n"+
		"			var text = \"<pre>\" + response + \"</pre>\";\n"+
		"			goog.dom.getElement(\"verifyinfo\").innerHTML = text;\n"+
		"		}\n"+
		"	)\n"+
		"}\n"+
		""))
}
