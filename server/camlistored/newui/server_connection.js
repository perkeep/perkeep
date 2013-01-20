/**
 * @fileoverview Connection to the blob server and API for the RPCs it
 * provides. All blob index UI code should use this connection to contact
 * the server.
 *
 */
goog.provide('camlistore.ServerConnection');

goog.require('goog.net.XhrIo');
goog.require('goog.uri.utils');


/**
 * @param {camlistore.ServerType.DiscoveryDocument} config Discovery document
 *   for the current server.
 * @param {Function=} opt_sendXhr Function for sending XHRs for testing.
 * @constructor
 */
camlistore.ServerConnection = function(config, opt_sendXhr) {
  /**
   * @type {camlistore.ServerType.DiscoveryDocument}
   * @private
   */
  this.config_ = config;

  /**
   * @type {function}
   * @private
   */
  this.sendXhr_ = opt_sendXhr || goog.net.XhrIo.send;
};


/**
 *
 */
camlistore.ServerConnection.prototype.getRecentlyUpdatedPermanodes =
    function(success, opt_thumbnailSize, opt_fail) {

  var path = goog.uri.utils.appendPath(
      this.config_.searchRoot, 'camli/search/recent');
  if (!!opt_thumbnailSize) {
    path = goog.uri.utils.appendParam(path, 'thumbnails', opt_thumbnailSize);
  }

  this.sendXhr_(
      path,
      goog.bind(this.getRecentlyUpdatedPermanodesDone_, this,
                success, opt_fail));
};


/**
 * @param {Function} success Success callback.
 * @param {Function?} fail Optional fail callback.
 * @param {goog.events.Event} e Event that triggered this
 */
camlistore.ServerConnection.prototype.getRecentlyUpdatedPermanodesDone_ =
    function(success, fail, e) {
  var xhr = e.target;
  var error = !xhr.isSuccess();
  var result = null;
  if (!error) {
    result = xhr.getResponseJson();
    error = !result;
  }
  if (error) {
    if (fail) {
      fail()
    } else {
      // TODO(bslatkin): Add a default failure event handler to this class.
      console.log('Failed XHR in ServerConnection');
    }
    return;
  }
  success(result);
};

/**
 * @param {function(string)} success Success callback, called with permanode blobref.
 * @param {Function=} opt_fail Optional fail callback.
 */
camlistore.ServerConnection.prototype.createPermanode = function(success, opt_fail) {
  // TODO(bradfitz): stop depending on camli.js.  For now, cheating:
  camliCreateNewPermanode({
      success: success,
      fail: opt_fail
  });
};
