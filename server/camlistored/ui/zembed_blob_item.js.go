// THIS FILE IS AUTO-GENERATED FROM blob_item.js
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("blob_item.js", 7288, time.Unix(0, 1370942742232957700), fileembed.String("/**\n"+
		" * @fileoverview An item showing in a blob item container; represents a blob\n"+
		" * that has already been uploaded in the system, or acts as a placeholder\n"+
		" * for a new blob.\n"+
		" *\n"+
		" */\n"+
		"goog.provide('camlistore.BlobItem');\n"+
		"\n"+
		"goog.require('camlistore.ServerType');\n"+
		"goog.require('goog.dom');\n"+
		"goog.require('goog.dom.classes');\n"+
		"goog.require('goog.events.EventHandler');\n"+
		"goog.require('goog.events.EventType');\n"+
		"goog.require('goog.ui.Control');\n"+
		"\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {string} blobRef BlobRef for the item.\n"+
		" * @param {camlistore.ServerType.IndexerMetaBag} metaBag Maps blobRefs to\n"+
		" *   metadata for this blob and related blobs.\n"+
		" * @param {string} opt_contentLink if \"true\", use the contained file blob as link"+
		" when decorating\n"+
		" * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.\n"+
		" *\n"+
		" * @extends {goog.ui.Control}\n"+
		" * @constructor\n"+
		" */\n"+
		"camlistore.BlobItem = function(blobRef, metaBag, opt_contentLink, opt_domHelper) "+
		"{\n"+
		"  goog.base(this, null, null, opt_domHelper);\n"+
		"\n"+
		"  // TODO(mpl): Hack so we know when to decorate with the blobref\n"+
		"  // of the contained file, instead of with the permanode, as the link.\n"+
		"  // Idiomatic alternative suggestion very welcome.\n"+
		"  /**\n"+
		"   * @type {string}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.useContentAsLink_ = \"false\"; \n"+
		"  \n"+
		"  if (typeof opt_contentLink !== \"undefined\" && opt_contentLink == \"true\") {\n"+
		"    this.useContentAsLink_ = opt_contentLink;\n"+
		"  }\n"+
		"\n"+
		"  /**\n"+
		"   * @type {string}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.blobRef_ = blobRef;\n"+
		"\n"+
		"  /**\n"+
		"   * @type {camlistore.ServerType.IndexerMetaBag}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.metaBag_ = metaBag;\n"+
		"\n"+
		"  /**\n"+
		"   * Metadata for the blobref this item represents.\n"+
		"   * @type {camlistore.ServerType.IndexerMeta}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.metaData_ = this.metaBag_[this.blobRef_];\n"+
		"\n"+
		"  /**\n"+
		"   * Metadata for the underlying blobref for this item; for example, this\n"+
		"   * would be the blobref that is currently the content for the permanode\n"+
		"   * specified by 'blobRef'.\n"+
		"   *\n"+
		"   * @type {camlistore.ServerType.IndexerMeta?}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.resolvedMetaData_ = camlistore.BlobItem.resolve(\n"+
		"      this.blobRef_, this.metaBag_);\n"+
		"\n"+
		"  /**\n"+
		"   * @type {goog.events.EventHandler}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.eh_ = new goog.events.EventHandler(this);\n"+
		"\n"+
		"  // Blob items support the CHECKED state.\n"+
		"  this.setSupportedState(goog.ui.Component.State.CHECKED, true);\n"+
		"\n"+
		"  // Blob items dispatch state when checked.\n"+
		"  this.setDispatchTransitionEvents(\n"+
		"      goog.ui.Component.State.CHECKED,\n"+
		"      true);\n"+
		"};\n"+
		"goog.inherits(camlistore.BlobItem, goog.ui.Control);\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * TODO(bslatkin): Handle more permanode types.\n"+
		" *\n"+
		" * @param {string} blobRef string BlobRef to resolve.\n"+
		" * @param {camlistore.ServerType.IndexerMetaBag} metaBag Metadata bag to use\n"+
		" *   for resolving the blobref.\n"+
		" * @return {camlistore.ServerType.IndexerMeta?}\n"+
		" */\n"+
		"camlistore.BlobItem.resolve = function(blobRef, metaBag) {\n"+
		"  var metaData = metaBag[blobRef];\n"+
		"  if (metaData.camliType == 'permanode' &&\n"+
		"      !!metaData.permanode &&\n"+
		"      !!metaData.permanode.attr) {\n"+
		"    if (!!metaData.permanode.attr.camliContent) {\n"+
		"      // Permanode is pointing at another blob.\n"+
		"      var content = metaData.permanode.attr.camliContent;\n"+
		"      if (content.length == 1) {\n"+
		"        return metaBag[content[0]];\n"+
		"      }\n"+
		"    } else {\n"+
		"      // Permanode is its own content.\n"+
		"      return metaData;\n"+
		"    }\n"+
		"  }\n"+
		"\n"+
		"  return null;\n"+
		"\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @return {boolean}\n"+
		" */\n"+
		"camlistore.BlobItem.prototype.isCollection = function() {\n"+
		"	// TODO(mpl): for now disallow being a collection if it\n"+
		"	// has members. What else to check?\n"+
		"	if (!this.resolvedMetaData_ ||\n"+
		"		this.resolvedMetaData_.camliType != 'permanode' ||\n"+
		"		!this.resolvedMetaData_.permanode ||\n"+
		"		!this.resolvedMetaData_.permanode.attr ||\n"+
		"		!!this.resolvedMetaData_.permanode.attr.camliContent) {\n"+
		"			return false;\n"+
		"	}\n"+
		"	return true;\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @return {string}\n"+
		" */\n"+
		"camlistore.BlobItem.prototype.getBlobRef = function() {\n"+
		"  return this.blobRef_;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @return {string}\n"+
		" */\n"+
		"camlistore.BlobItem.prototype.getThumbSrc_ = function() {\n"+
		"  return './' + this.metaData_.thumbnailSrc;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @return {number}\n"+
		" */\n"+
		"camlistore.BlobItem.prototype.getThumbHeight_ = function() {\n"+
		"  return this.metaData_.thumbnailHeight || 0;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @return {number}\n"+
		" */\n"+
		"camlistore.BlobItem.prototype.getThumbWidth_ = function() {\n"+
		"  return this.metaData_.thumbnailWidth || 0;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @return {string}\n"+
		" */\n"+
		"camlistore.BlobItem.prototype.getLink_ = function() {\n"+
		"  if (this.useContentAsLink_ == \"true\") {\n"+
		"    var b = this.getFileBlobref_();\n"+
		"    if (b == \"\") {\n"+
		"      b = this.getDirBlobref_();\n"+
		"    }\n"+
		"    return './?b=' + b;\n"+
		"  }\n"+
		"  return './?p=' + this.blobRef_;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @private\n"+
		" * @return {string}\n"+
		" */\n"+
		"camlistore.BlobItem.prototype.getFileBlobref_ = function() {\n"+
		"	if (this.resolvedMetaData_ &&\n"+
		"		this.resolvedMetaData_.camliType == 'file') {\n"+
		"		return this.resolvedMetaData_.blobRef;\n"+
		"	}\n"+
		"	return \"\";\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @private\n"+
		" * @return {string}\n"+
		" */\n"+
		"camlistore.BlobItem.prototype.getDirBlobref_ = function() {\n"+
		"	if (this.resolvedMetaData_ &&\n"+
		"		this.resolvedMetaData_.camliType == 'directory') {\n"+
		"		return this.resolvedMetaData_.blobRef;\n"+
		"	}\n"+
		"	return \"\";\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @return {string}\n"+
		" */\n"+
		"camlistore.BlobItem.prototype.getTitle_ = function() {\n"+
		"  if (this.resolvedMetaData_) {\n"+
		"    if (this.resolvedMetaData_.camliType == 'file' &&\n"+
		"        !!this.resolvedMetaData_.file) {\n"+
		"      return this.resolvedMetaData_.file.fileName;\n"+
		"    } else if (this.resolvedMetaData_.camliType == 'permanode' &&\n"+
		"               !!this.resolvedMetaData_.permanode &&\n"+
		"               !!this.resolvedMetaData_.permanode.attr &&\n"+
		"               !!this.resolvedMetaData_.permanode.attr.title) {\n"+
		"      return this.resolvedMetaData_.permanode.attr.title;\n"+
		"    }\n"+
		"  }\n"+
		"  return 'Unknown title';\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Creates an initial DOM representation for the component.\n"+
		" */\n"+
		"camlistore.BlobItem.prototype.createDom = function() {\n"+
		"  this.decorateInternal(this.dom_.createElement('div'));\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Decorates an existing HTML DIV element.\n"+
		" * @param {Element} element The DIV element to decorate.\n"+
		" */\n"+
		"camlistore.BlobItem.prototype.decorateInternal = function(element) {\n"+
		"  camlistore.BlobItem.superClass_.decorateInternal.call(this, element);\n"+
		"\n"+
		"  var el = this.getElement();\n"+
		"  goog.dom.classes.add(el, 'cam-blobitem');\n"+
		"\n"+
		"  var linkEl = this.dom_.createDom('a');\n"+
		"  linkEl.href = this.getLink_();\n"+
		"\n"+
		"  var thumbEl = this.dom_.createDom('img', 'cam-blobitem-thumb');\n"+
		"  thumbEl.src = this.getThumbSrc_();\n"+
		"  thumbEl.height = this.getThumbHeight_();\n"+
		"  thumbEl.width = this.getThumbWidth_();\n"+
		"\n"+
		"  this.dom_.appendChild(linkEl, thumbEl);\n"+
		"  this.dom_.appendChild(el, linkEl);\n"+
		"\n"+
		"  var titleEl = this.dom_.createDom('p', 'cam-blobitem-thumbtitle');\n"+
		"  this.dom_.setTextContent(titleEl, this.getTitle_());\n"+
		"  this.dom_.appendChild(el, titleEl);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/** @override */\n"+
		"camlistore.BlobItem.prototype.disposeInternal = function() {\n"+
		"  camlistore.BlobItem.superClass_.disposeInternal.call(this);\n"+
		"  this.eh_.dispose();\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to be in the document.\n"+
		" */\n"+
		"camlistore.BlobItem.prototype.enterDocument = function() {\n"+
		"  camlistore.BlobItem.superClass_.enterDocument.call(this);\n"+
		"  // Add event handlers here\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to have been removed from the\n"+
		" * document.\n"+
		" */\n"+
		"camlistore.BlobItem.prototype.exitDocument = function() {\n"+
		"  camlistore.BlobItem.superClass_.exitDocument.call(this);\n"+
		"  // Clear event handlers here\n"+
		"};\n"+
		""))
}
