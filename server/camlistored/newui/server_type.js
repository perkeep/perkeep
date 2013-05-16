/**
 * @fileoverview Helpers and types for JSON objects returned by the server.
 */
goog.provide('camlistore.ServerType');


/**
 * @typedef {{
 *   currentPermanode: string,
 *   name: string,
 *   prefix: Array.<string>
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
 *   signing: camlistore.ServerType.SigningDiscoveryDocument,
 *   uploadHelper: string
 * }}
 */
camlistore.ServerType.DiscoveryDocument;

/**
 * @typedef {{
 *   publicKey: string,
 *   publicKeyBlobRef: string,
 *   publicKeyId: string,
 *   signHandler: string,
 *   verifyHandler: string
 * }}
 */
camlistore.ServerType.SigningDiscoveryDocument;

/**
 * @typedef {{
 *   fileName: string,
 *   mimeType: string,
 *   size: number
 * }}
 */
camlistore.ServerType.IndexerFileMeta;


/**
 * @typedef {{
 *   title: string,
 *   camliContent: Array.<camlistore.ServerType.IndexerMeta>
 * }}
 */
camlistore.ServerType.IndexerPermanodeAttrMeta;


/**
 * @typedef {{
 *   attr: camlistore.ServerType.IndexerPermanodeAttrMeta?
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
 *   thumbnailSrc: string?
 * }}
 */
camlistore.ServerType.IndexerMeta;


/**
 * @typedef {Object.<string, camlistore.ServerType.IndexerMeta>}
 */
camlistore.ServerType.IndexerMetaBag;

/**
 * @typedef {{
 *   blobref: string,
 *   modtime: string,
 *   owner: string
 * }}
*/
camlistore.ServerType.SearchRecentItem;

/**
 * @typedef {{
 *   recent: Array.<camlistore.ServerType.SearchRecentItem>,
 *   meta: camlistore.ServerType.IndexerMetaBag
 * }}
*/
camlistore.ServerType.SearchRecentResponse;

/**
 * @typedef {{
 *   permanode: string
 * }}
*/
camlistore.ServerType.SearchWithAttrItem;

/**
 * @typedef {{
 *   withAttr: Array.<camlistore.ServerType.SearchWithAttrItem>,
 *   meta: camlistore.ServerType.IndexerMetaBag
 * }}
*/
camlistore.ServerType.SearchWithAttrResponse;
