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
    "publicKey": "/sighelper/camli/sha1-f72d9090b61b70ee6501cceacc9d81a0801d32f6",
    "publicKeyBlobRef": "sha1-f72d9090b61b70ee6501cceacc9d81a0801d32f6",
    "publicKeyId": "94DE83C46401800C",
    "signHandler": "/sighelper/camli/sig/sign",
    "verifyHandler": "/sighelper/camli/sig/verify"
}
```

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
