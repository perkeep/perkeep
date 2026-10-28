# JSON signing & verification

A Perkeep server will typically expose a JSON signing handler. The operations for the signing handler are available at URL paths that are under the [Discovery protocol](discovery) response's **jsonSignRoot** value.

The three handlers paths are:

* **[jsonSignRoot]**/camli/sig/discovery
* **[jsonSignRoot]**/camli/sig/sign
* **[jsonSignRoot]**/camli/sig/verify

## Discovery

The discovery handler, in response to a GET request with no options,
returns a
[SignDiscovery](https://perkeep.org/pkg/types/camtypes/#SignDiscovery)
value, such as:

```
{
    "publicKey": "/sighelper/camli/sha1-f2b0b7da718b97ce8c31591d8ed4645c777f3ef4",
    "publicKeyBlobRef": "sha1-f2b0b7da718b97ce8c31591d8ed4645c777f3ef4",
    "publicKeyId": "2931A67C26F5ABDA",
    "publicKeyFingerprint": "FBB89AA320A2806FE497C0492931A67C26F5ABDA",
    "signHandler": "/sighelper/camli/sig/sign",
    "verifyHandler": "/sighelper/camli/sig/verify"
}
```

`publicKeyFingerprint` is the full uppercase hexadecimal GPG key fingerprint.
Older servers may omit it. Prefer it for key comparisons when available, but
fall back to `publicKeyId` when either peer omits its fingerprint. Servers
continue to advertise the long `publicKeyId` for compatibility with older
clients. Neither field changes the public key blob reference or signing format.

## Signing

The signing handler requires a POST request (of either
type `application/x-www-form-urlencoded` or `multipart/form-data`) and accepts
parameters:

* **json**: the unsigned JSON to sign

## Verification

The verification handler requires a POST request (of either
type `application/x-www-form-urlencoded` or `multipart/form-data`) and accepts
parameters:

* **sjson**: the signed JSON to verify
