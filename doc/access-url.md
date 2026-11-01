# Client access URLs

The server's Client access page (`/access/`, in the web UI's top-left
menu) shows, for each access key, a QR code and a link of a
`perkeep://access` URL. Clients such as mobile apps scan or open it to
add the server, with the access key's permissions. This document is the
URL's format, for client authors. See the
[server configuration](/doc/server-config.md#access) for access keys and
the tailcat listener.

## Format

```
perkeep://access?key=KEY&name=NAME&tailcat=ADDR
perkeep://access?key=KEY&name=NAME&url=URL[&certfp=FINGERPRINT]
```

The parameters are URL query parameters, percent-encoded as usual, in no
particular order. Clients should ignore parameters they don't know.

* `key` (required): the access key's secret.
* `name` (required): the name to show for the server. By default, it's
  the name of the server's TLS certificate, if it has one, or else the
  machine's hostname, but the owner can change it. It's only for showing:
  to tell whether a URL is for a server the client already has, compare
  `tailcat` or `url` instead, as the owner can rename the server.
* Exactly one of:
  * `tailcat`: the server's [tailcat](https://github.com/tailscale/tailcat)
    address, if its tailcat listener is enabled. The server prefers it
    when it has both.
  * `url`: the server's base URL, which is always `https`. Clients must
    refuse a `url` that isn't.
* `certfp`: only with `url`, when the server's certificate is one that
  clients can't verify the usual way, such as a self-signed one, or one
  for a bare IP address. It's the SHA-256 of the certificate (its DER
  encoding), as 64 lowercase hex digits. See below.

The URL is a secret: its key is a password, and a tailcat address holds
the WireGuard pre-shared key that clients need to connect. Clients should
store it as they would a password.

## Connecting

With `tailcat`, connect with a tailcat client (such as the
`github.com/tailscale/tailcat` Go package's `Client`, with the address as
its `Server`) to TCP port 80 of the server, and speak plain HTTP inside
the tunnel, which WireGuard already encrypts and authenticates. The Host
header doesn't matter.

With `url`, use HTTPS. Without `certfp`, verify the server's certificate
as usual, for the URL's host. With `certfp`, verify it instead by
checking that the SHA-256 of the certificate the server presents (the
leaf, in DER) is `certfp`, and accept no other.

## Requests

Send the key as the password of HTTP basic auth, with any username, on
every request. A request without a valid key gets a 401; one the key's
permissions don't allow, a 403.

Start with the server's [discovery](/doc/protocol/discovery.md) document,
at `GET /` with the header `Accept: text/x-camli-configuration` (or at
`/?camli.mode=config`), which any valid key may get. It names the
handlers below (`blobRoot`, `searchRoot`, `jsonSignRoot`, `statusRoot`,
`uiRoot`). Its `authToken`, which allows everything, is empty for access
keys.

Each permission allows these requests, and no others:

| Permission | Requests |
|---|---|
| Upload blobs | `POST $blobRoot/camli/upload`, `PUT $blobRoot/camli/BLOBREF`, upload sessions (`POST` and `DELETE` of `$blobRoot/camli/session...`), and stat (`GET` or `POST $blobRoot/camli/stat`) |
| Make claims | `POST $jsonSignRoot/camli/sig/sign` and `/camli/sig/verify`, and `GET`s under `$jsonSignRoot`, such as its discovery and public key |
| Search | the search handler's read-only endpoints under `$searchRoot/camli/search/`: `GET` of `recent`, `permanodeattr`, `describe`, `exif`, `claims`, `files`, `signerattrvalue`, `signerpaths`, and `edgesto`, and `POST` of `describe` and `query` |
| Download blobs | `GET` of `$blobRoot/camli/BLOBREF` and `$blobRoot/camli/enumerate-blobs`, stat, and `GET` of `$uiRoot/download/...` and `$uiRoot/thumbnail/...` |
| Server stats | `GET $statusRoot/status.json` |

Make claims has the server sign the JSON a client sends with the server's
own identity, so the client needn't hold the GPG private key; the client
then uploads the signed blob, which needs Upload blobs too.

## Example

```
perkeep://access?key=JMXN4ZKXEW6E7CVS2QJTWHYZRA&name=perkeep.example.com&url=https%3A%2F%2Fperkeep.example.com
```
