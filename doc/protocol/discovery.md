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
      "blobRoot": "/bs/",
      "directoryHelper": "/ui/tree/",
      "downloadHelper": "/ui/download/",
      "helpRoot": "/help/",
      "importerRoot": "/importer/",
      "jsonSignRoot": "/sighelper/",
      "ownerName": "The User Name",
      "publishRoots": {},
      "searchRoot": "/my-search/",
      "signing": {
        "publicKey": "/sighelper/camli/sha1-f2b0b7da718b97ce8c31591d8ed4645c777f3ef4",
        "publicKeyBlobRef": "sha1-f2b0b7da718b97ce8c31591d8ed4645c777f3ef4",
        "publicKeyId": "2931A67C26F5ABDA",
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
      "uploadSessions": true,
      "wsAuthToken": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
    }

The optional `signing.publicKeyFingerprint` is the full uppercase hexadecimal
GPG key fingerprint. `signing.publicKeyId` remains available for compatibility.
Prefer comparing full fingerprints when both peers advertise them; if either
peer omits its fingerprint, compare their long key IDs instead. An absent
fingerprint does not mean the server lacks signing support.
