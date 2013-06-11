// THIS FILE IS AUTO-GENERATED FROM server_type.js
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("server_type.js", 2646, time.Unix(0, 1370942742232957700), fileembed.String("/**\n"+
		" * @fileoverview Helpers and types for JSON objects returned by the server.\n"+
		" */\n"+
		"goog.provide('camlistore.ServerType');\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @typedef {{\n"+
		" *   currentPermanode: string,\n"+
		" *   name: string,\n"+
		" *   prefix: Array.<string>\n"+
		" * }}\n"+
		" */\n"+
		"camlistore.ServerType.DiscoveryRoot;\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @typedef {{\n"+
		" *   blobRoot: string,\n"+
		" *   directoryHelper: string,\n"+
		" *   downloadHelper: string,\n"+
		" *   jsonSignRoot: string,\n"+
		" *   ownerName: string,\n"+
		" *   publishRoots: Array.<camlistore.ServerType.DiscoveryRoot>,\n"+
		" *   searchRoot: string,\n"+
		" *   statusRoot: string,\n"+
		" *   storageGeneration: string,\n"+
		" *   storageInitTime: string,\n"+
		" *   signing: camlistore.ServerType.SigningDiscoveryDocument,\n"+
		" *   uploadHelper: string\n"+
		" * }}\n"+
		" */\n"+
		"camlistore.ServerType.DiscoveryDocument;\n"+
		"\n"+
		"/**\n"+
		" * @typedef {{\n"+
		" *   publicKey: string,\n"+
		" *   publicKeyBlobRef: string,\n"+
		" *   publicKeyId: string,\n"+
		" *   signHandler: string,\n"+
		" *   verifyHandler: string\n"+
		" * }}\n"+
		" */\n"+
		"camlistore.ServerType.SigningDiscoveryDocument;\n"+
		"\n"+
		"/**\n"+
		" * @typedef {{\n"+
		" *   fileName: string,\n"+
		" *   mimeType: string,\n"+
		" *   size: number\n"+
		" * }}\n"+
		" */\n"+
		"camlistore.ServerType.IndexerFileMeta;\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @typedef {{\n"+
		" *   title: string,\n"+
		" *   camliContent: Array.<camlistore.ServerType.IndexerMeta>\n"+
		" * }}\n"+
		" */\n"+
		"camlistore.ServerType.IndexerPermanodeAttrMeta;\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @typedef {{\n"+
		" *   attr: camlistore.ServerType.IndexerPermanodeAttrMeta?\n"+
		" * }}\n"+
		" */\n"+
		"camlistore.ServerType.IndexerPermanodeMeta;\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @typedef {{\n"+
		" *   blobRef: string,\n"+
		" *   camliType: string,\n"+
		" *   file: camlistore.ServerType.IndexerFileMeta?,\n"+
		" *   mimeType: string,\n"+
		" *   permanode: camlistore.ServerType.IndexerPermanodeMeta?,\n"+
		" *   size: number,\n"+
		" *   thumbnailHeight: number?,\n"+
		" *   thumbnailWidth: number?,\n"+
		" *   thumbnailSrc: string?\n"+
		" * }}\n"+
		" */\n"+
		"camlistore.ServerType.IndexerMeta;\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @typedef {Object.<string, camlistore.ServerType.IndexerMeta>}\n"+
		" */\n"+
		"camlistore.ServerType.IndexerMetaBag;\n"+
		"\n"+
		"/**\n"+
		" * @typedef {{\n"+
		" *   blobref: string,\n"+
		" *   modtime: string,\n"+
		" *   owner: string\n"+
		" * }}\n"+
		"*/\n"+
		"camlistore.ServerType.SearchRecentItem;\n"+
		"\n"+
		"/**\n"+
		" * @typedef {{\n"+
		" *   recent: Array.<camlistore.ServerType.SearchRecentItem>,\n"+
		" *   meta: camlistore.ServerType.IndexerMetaBag\n"+
		" * }}\n"+
		"*/\n"+
		"camlistore.ServerType.SearchRecentResponse;\n"+
		"\n"+
		"/**\n"+
		" * @typedef {{\n"+
		" *   permanode: string\n"+
		" * }}\n"+
		"*/\n"+
		"camlistore.ServerType.SearchWithAttrItem;\n"+
		"\n"+
		"/**\n"+
		" * @typedef {{\n"+
		" *   withAttr: Array.<camlistore.ServerType.SearchWithAttrItem>,\n"+
		" *   meta: camlistore.ServerType.IndexerMetaBag\n"+
		" * }}\n"+
		"*/\n"+
		"camlistore.ServerType.SearchWithAttrResponse;\n"+
		"\n"+
		"/**\n"+
		" * @typedef {{\n"+
		" *   meta: camlistore.ServerType.IndexerMetaBag\n"+
		" * }}\n"+
		"*/\n"+
		"camlistore.ServerType.DescribeResponse;\n"+
		"\n"+
		"/**\n"+
		" * @typedef {{\n"+
		" *   version: string,\n"+
		" * }}\n"+
		"*/\n"+
		"camlistore.ServerType.StatusResponse;\n"+
		""))
}
