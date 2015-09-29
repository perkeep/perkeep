/**
 * @fileoverview Helpers and types for JSON objects returned by the server.
 */
goog.provide('cam.ServerType');


/**
 * @typedef {{
 *   currentPermanode: string,
 *   name: string,
 *   prefix: Array.<string>
 * }}
 */
cam.ServerType.DiscoveryRoot;


/**
 * @typedef {{
 *   blobRoot: string,
 *   directoryHelper: string,
 *   downloadHelper: string,
 *   helpRoot: string,
 *   jsonSignRoot: string,
 *   ownerName: string,
 *   publishRoots: Array.<cam.ServerType.DiscoveryRoot>,
 *   searchRoot: string,
 *   statusRoot: string,
 *   storageGeneration: string,
 *   storageInitTime: string,
 *   signing: cam.ServerType.SigningDiscoveryDocument,
 *   uploadHelper: string
 * }}
 */
cam.ServerType.DiscoveryDocument;

/**
 * @typedef {{
 *   publicKey: string,
 *   publicKeyBlobRef: string,
 *   publicKeyId: string,
 *   signHandler: string,
 *   verifyHandler: string
 * }}
 */
cam.ServerType.SigningDiscoveryDocument;

/**
 * @typedef {{
 *   fileName: string,
 *   mimeType: string,
 *   size: number
 * }}
 */
cam.ServerType.IndexerFileMeta;


/**
 * @typedef {{
 *   title: string,
 *   camliContent: Array.<cam.ServerType.IndexerMeta>
 * }}
 */
cam.ServerType.IndexerPermanodeAttrMeta;


/**
 * @typedef {{
 *   attr: cam.ServerType.IndexerPermanodeAttrMeta?
 * }}
 */
cam.ServerType.IndexerPermanodeMeta;


/**
 * @typedef {{
 *   blobRef: string,
 *   camliType: string,
 *   file: cam.ServerType.IndexerFileMeta?,
 *   mimeType: string,
 *   permanode: cam.ServerType.IndexerPermanodeMeta?,
 *   size: number,
 * }}
 */
cam.ServerType.IndexerMeta;


/**
 * @typedef {Object.<string, cam.ServerType.IndexerMeta>}
 */
cam.ServerType.IndexerMetaBag;

/**
 * @typedef {{
 *   blobref: string,
 *   modtime: string,
 *   owner: string
 * }}
*/
cam.ServerType.SearchRecentItem;

/**
 * @typedef {{
 *   recent: Array.<cam.ServerType.SearchRecentItem>,
 *   meta: cam.ServerType.IndexerMetaBag
 * }}
*/
cam.ServerType.SearchRecentResponse;

/**
 * @typedef {{
 *   permanode: string
 * }}
*/
cam.ServerType.SearchWithAttrItem;

/**
 * @typedef {{
 *   withAttr: Array.<cam.ServerType.SearchWithAttrItem>,
 *   meta: cam.ServerType.IndexerMetaBag
 * }}
*/
cam.ServerType.SearchWithAttrResponse;

/**
 * @typedef {{
 *   meta: cam.ServerType.IndexerMetaBag
 * }}
*/
cam.ServerType.DescribeResponse;

/**
 * @typedef {{
 *   version: string,
 * }}
*/
cam.ServerType.StatusResponse;
