/**
 * @fileoverview Helpers and types for JSON objects returned by the server.
 */
goog.provide('camlistore.ServerType');


/**
 * @typedef {{
 *   currentPermanode: string,
 *   name: string,
 *   prefix: Array.<string>,
 * }}
 */
camlistore.ServerType.DiscoveryRoot;


/**
 * @typedef {{
 *   blobRoot: string,
 *   directoryHelper: string,
 *   downloadHelper: string,
 *   jsonSignRoot: string,
 *   ownerName: string,
 *   publishRoots: Array.<camlistore.ServerType.DiscoveryRoot>,
 *   searchRoot: string,
 *   storageGeneration: string,
 *   storageInitTime: string,
 *   uploadHelper: string,
 * }}
 */
camlistore.ServerType.DiscoveryDocument;


/**
 * @typedef {{
 *   fileName: string,
 *   mimeType: string,
 *   size: number,
 * }}
 */
camlistore.ServerType.IndexerFileMeta;


/**
 * @typedef {{
 *   attr: Object.<string, Array.<string>>
 * }}
 */
camlistore.ServerType.IndexerPermanodeMeta;


/**
 * @typedef {{
 *   blobRef: string,
 *   camliType: string,
 *   file: camlistore.ServerType.IndexerFileMeta?,
 *   mimeType: string,
 *   permanode: camlistore.ServerType.IndexerPermanodeMeta?,
 *   size: number,
 *   thumbnailHeight: number?,
 *   thumbnailWidth: number?,
 *   thumbnailSrc: string?,
 * }}
 */
camlistore.ServerType.IndexerMeta;


/**
 * @typedef {Object.<string, camlistore.ServerType.IndexerMeta>}
 */
camlistore.ServerType.IndexerMetaBag;
