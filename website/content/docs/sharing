# Sharing

**TODO:** finish documenting this. In particular, add example with camget -shared.

The basic summary is that you create a [claim](/docs/terms#claim) that a user
has access to something, and then your blobserver's public frontend
authenticates (if applicable) a remote user and gives them access as permitted
by your claim.

Reproducing an email from [this thread](http://groups.google.com/group/camlistore/browse_thread/thread/a4920d6a1c5fc3ce)
for some background:

---

This is an example walk-though of (working) sharing on Camlistore.   Brett and
I got this working last night (the basic "have a link" use case with no
addition auth)

Let's say I have a private blobserver:

http://camlistore.org:3179/

And I have a file, "Hi.txt".

Its bytes are blob `sha1-3dc1d1cfe92fce5f09d194ba73a0b023102c9b25`.  Its
metadata (inode, filename, etc) is blob `sha1-0e5e60f367cc8156ae48198c496b2b2ebdf5313d`.

You don't have access to those, even though you know their names.  Verify the 401 errors:

* http://camlistore.org:3179/camli/sha1-3dc1d1cfe92fce5f09d194ba73a0b023102c9b25
* http://camlistore.org:3179/camli/sha1-0e5e60f367cc8156ae48198c496b2b2ebdf5313d

(hm, those are returning Unauthorized errors, but no Content Body... will fix later)

Note also that any errors you get from my private blob server always delay for
at least 200 ms to mask timing attacks that could otherwise reveal the
existence or non-existence of a blob on my private server.

Note that in order to have all of the following working, your server needs to have a share handler, so you need to have the line

    "shareHandler": true,

in your [server config](/docs/server-config).

Now I want to share Hi.txt with you, so I create a share blob:

    camput share --transitive sha1-0e5e60f367cc8156ae48198c496b2b2ebdf5313d

I've created this, and its name is `sha1-102758fb54521cb6540d256098e7c0f1625b33e3`

Note that you can fetch it without authentication, because you're using the url
prefix `/share/`, which delegates the task to the share handler, and because
the share handler checks that it's a share blob that doesn't require auth
(`authType` == "haveref" ... like "Share with others that have the link")

Here's you getting the blob:

    $ curl http://camlistore.org:3179/share/sha1-102758fb54521cb6540d256098e7c0f1625b33e3
    {"camliVersion": 1,
      "authType": "haveref",
      "camliSigner": "sha1-3bee195d0dada92a9d88e67f731124238e65a916",
      "camliType": "claim",
      "claimDate": "2013-06-24T14:17:02.791613849Z",
      "claimType": "share",
      "target": "sha1-0e5e60f367cc8156ae48198c496b2b2ebdf5313d",
      "transitive": true
    ,"camliSig":"wsBcBAABCAAQBQJRyFTeCRApy/NNAr6GZgAAKmgIAGbCCn1YPoZuqz+mcMaLN09J3rJYZPnjICp9at9UL7fFJ6izzDFLi6gq9ae/Kou51VRnuLYvRXGvqgZ9HCTTJiGaET8I6c3gBvQWMC/NOS/B9Y+CcZ5qEsz84Dk2D6zMIC9adQjN4yjtcsVtKYDVDQ5SCkCE6sOaUebGBS22TOhZMXPalIyzf2EPSiXdeEKtsMwg+sbd4EmpQHeE3XqzI8gbcsUX6VdCp6zU81Y71pNuYdmEVBPY5gVch2Xe1gJQICOatiAi4W/1nrTLB73sKEeulzRMbIDB4rgWooKKmnBPI1ZOTyg/fkKmfWfuJKSU0ySiPwVHn4aPFwCGrBRladE==KjfB"}

Note the "target" and "transitive".

Now we present this proof of access in subsequent requests in the "via"
parameter, with the in-order path of access.

Here's the first hop to the metadata, in which we discover the blobRef of the
bytes of the file (in this case, just one part is the whole file bytes...)  I
already told you this earlier in the email, but assume you're just discovering
this now.

    $ curl http://camlistore.org:3179/share/sha1-0e5e60f367cc8156ae48198c496b2b2ebdf5313d?via=sha1-102758fb54521cb6540d256098e7c0f1625b33e3
    {"camliVersion": 1,
      "camliType": "file",
      "contentParts": [
        {
          "blobRef": "sha1-3dc1d1cfe92fce5f09d194ba73a0b023102c9b25",
          "size": 14
        }
      ],
      "fileName": "Hi.txt",
      "size": 14,
      "unixGroup": "camli",
      "unixGroupId": 1000,
      "unixMtime": "2011-01-26T21:11:22.152868825Z",
      "unixOwner": "camli",
      "unixOwnerId": 1000,
      "unixPermission": "0644"
    }

Now let's get the final bytes of the file:

    $ curl http://camlistore.org:3179/share/sha1-3dc1d1cfe92fce5f09d194ba73a0b023102c9b25?via=sha1-102758fb54521cb6540d256098e7c0f1625b33e3,sha1-0e5e60f367cc8156ae48198c496b2b2ebdf5313d
    Hello, Camli!

That's it.

Now imagine different `authType` parameters (passwords, SSL certs, SSH, openid,
oauth, facebook, membership in a group, whatever... )
