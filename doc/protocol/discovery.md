# Discovery

Discovery is the process of asking the server for its configuration.

You send the discovery HTTP request to the URL the user has
configured.  If the user hasn't specified a path, use "/".

Then make a GET request to that URL with either Accept header set to
"text/x-camli-configuration" or the the URL query parameter
"camli.mode" set to "config":

    GET /some/user/?camli.mode=config HTTP/1.1
    Host: camlihost.example.com

Or:

    GET / HTTP1.1
    Host: 127.0.0.1
    Accept: text/x-camli-configuration

The response is a JSON document with a [Discovery value](https://perkeep.org/pkg/types/camtypes/#Discovery), such as:

    {
      "blobHashFuncs": [
        "sha1"
      ],
      "blobRoot": "/bs-and-maybe-also-index/",
      "directoryHelper": "/ui/tree/",
      "downloadHelper": "/ui/download/",
      "helpRoot": "/help/",
      "importerRoot": "/importer/",
      "jsonSignRoot": "/sighelper/",
      "ownerName": "The User Name",
      "publishRoots": {},
      "searchRoot": "/my-search/",
      "signing": {
        "publicKey": "/sighelper/camli/sha1-f72d9090b61b70ee6501cceacc9d81a0801d32f6",
        "publicKeyBlobRef": "sha1-f72d9090b61b70ee6501cceacc9d81a0801d32f6",
        "publicKeyFingerprint": "FBB89AA320A2806FE497C0492931A67C26F5ABDA",
        "signHandler": "/sighelper/camli/sig/sign",
        "verifyHandler": "/sighelper/camli/sig/verify"
      },
      "statusRoot": "/status/",
      "storageGeneration": "231ceff7a04a77cdf881b0422ea733334eee3b8f",
      "storageInitTime": "2012-11-30T03:34:47Z",
      "syncHandlers": [
        {
          "from": "/bs/",
          "to": "/index-mysql/",
          "toIndex": true
        },
        {
          "from": "/bs/",
          "to": "/sto-s3/",
          "toIndex": false
        }
      ],
      "thumbVersion": "2",
      "uiRoot": "/ui/",
      "uploadHelper": "/ui/?camli.mode=uploadhelper",
      "wsAuthToken": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
    }
