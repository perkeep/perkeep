/**
 * @fileoverview TODO
 *
 */
goog.provide('camlistore.ServerConnection');



/**
 * @param {camlistore.ServerType.DiscoveryDocument} config Discovery document
 *   for the current server.
 * @constructor
 */
camlistore.ServerConnection = function(config) {
  /**
   * @type {camlistore.ServerType.DiscoveryDocument}
   * @private
   */
  this.config_ = config;
};


camlistore.ServerConnection.prototype.stupidHello = function() {
  return 'Hello';
};