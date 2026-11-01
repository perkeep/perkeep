# Server-side secrets

Perkeep keeps importer credentials in a mutable, server-local SQLite database,
separate from blobs, permanodes, the index, and disposable importer caches.
Importers and the owner's command-line client use the same store.

## Storage and configuration

The high-level server configuration accepts `secretsFile`:

```json
{
    "secretsFile": "/private/perkeep/secrets.sqlite"
}
```

By default it is `secrets.sqlite` in the server's var directory, respecting
`CAMLI_VAR_DIR`. This does not depend on `blobPath`: cloud-only blob storage
still requires local credential storage. `memoryStorage: true` explicitly uses
an ephemeral secret store unless `secretsFile` is also set.

On Unix, the database file is mode 0600. Newly created directories are mode 0700;
existing parent directories must not be writable by other users. The store
rejects symlink/nonregular database files and unsafe SQLite sidecars.
On Windows, private ACLs allow the current user, SYSTEM, and Administrators.
Existing parent directories must already be private; the store does not
rewrite arbitrary directories' ACLs. Specify a new private subdirectory if
the existing location is shared.
**The database is not encrypted.** Protect its directory, the server's operating
system account, and its backups accordingly.

The generated low-level configuration serves the `secrets` handler at
`/secrets/`, referenced by the root and importer handlers' `secretRoot`.
Its `handlerArgs` accepts either `file` or explicitly ephemeral `memory: true`.
With neither, it uses the default file above. Older low-level configurations
with an importer handler automatically get this durable default store and
the corresponding references at startup. A conflicting `/secrets/` prefix or
ambiguous choice between stores must be resolved in configuration.

Back up secrets separately from blobs. Stop the server before copying the
database, or use a SQLite-aware consistent backup procedure. Ordinary blob
replication, reindexing, and restoring blobs do **not** restore credentials.
Without a secret-store backup, reconnect affected accounts. Do not discard
this database as though it were an importer scratch cache.

Static importer credentials supplied in server configuration remain there and
retain their existing precedence. CLI Takeout credential files are unchanged;
they are not automatically uploaded into this store.

## Startup migration and warnings

Before starting automatic imports, the importer host finds current credential
attributes on every `importer`, `importerAccount`, and
`importerAccount-deleted` permanode, including hidden nodes and unregistered
or removed importer types.

It saves credentials durably first, then removes the current attributes using
deletion claims that do not repeat their values. A partial migration can be
retried: existing replacement values are not overwritten, and deleted local
entries are not resurrected. Store failures stop startup rather than falling
back to permanode credentials.

The moved entries have `migratedFromPermanode: true`. The importer pages and
CLI show a **⚠ warning**: old values may remain in immutable historical claims,
replicas, or backups. Migration does **not** revoke or rotate credentials, and
does not erase old blobs. The migration examines current attributes, not every
past claim or pre-account-era import root.

The warning is sticky through ordinary writes, including token refreshes.
After handling rotation with the provider, explicitly acknowledge it when
setting the replacement value with `--rotated`. This clears the local warning,
not the historical claims. Deleting an entry also does not revoke it.

Importer client IDs and Mastodon's credential destination are kept with their
secrets. Supported token attributes include OAuth temporary/access/refresh
credentials and the legacy combined Google Photos/Picasa token.

For public feeds, `feedURL` remains ordinary `importerAccount` permanode
metadata, not a secret. Do not put credentials in the URL. Protected-feed
authentication is not implemented. Future support should use explicit account
metadata describing the authentication and referring to separate credentials
in the secret store, rather than treating the entire feed URL as private.

Importer entry names are `importer/<permanode-ref>/<attribute>`. Account
identity, import roots, completion markers, and other non-secret metadata
remain on permanodes. New importer credential attribute names must be added
to the central classification in `pkg/importer/secrets.go` before use.

## CLI

Use the server owner's normal configured credentials:

```sh
pk secret list
pk secret get NAME
pk secret set --file /private/path/to/value NAME
pk secret set --revision 3 --rotated --file /private/path/to/replacement NAME
pk secret delete --revision 4 NAME
```

`set` reads stdin when `--file` is omitted. Values are never positional
arguments. Values must be valid UTF-8 text; input is preserved exactly,
including trailing newlines.
`get` writes the exact value to stdout; protect any destination file with
appropriate permissions. Warnings go to stderr so they do not corrupt the
value. `list` shows metadata and warning markers, never values.

`--json` exposes structured metadata; for `get` it also explicitly includes the
value. `--revision` is compare-and-swap: zero requires absence, a positive
number must match the current revision, and the default, -1, is unconditional.

## HTTP API

Owner-authenticated discovery advertises `secretRoot`. Ordinary discovery/UI
tokens, generated app credentials, and restricted client access keys cannot
use the secret API or edit credentials through importer setup, and are not
given this root. Authorized importer runs still use and may refresh their
credentials internally. Full server authentication is required. As with the
rest of the server, configuring `auth: "none"` deliberately removes that
protection; do not expose such a server to untrusted clients.

Requests are relative to `secretRoot`:

| Method and path | Operation |
| --- | --- |
| `GET ./` | List metadata only |
| `GET value?name=NAME` | Read one value and its metadata |
| `PUT value?name=NAME` | Store a value |
| `DELETE value?name=NAME` | Delete a value |

Mutation bodies must be JSON and include `expectedRevision`:

```json
{
    "value": "replacement value",
    "expectedRevision": 3,
    "clearMigration": true
}
```

`DELETE` accepts only `expectedRevision`. `clearMigration` is optional and
explicitly acknowledges rotation; clients cannot set migration provenance.
Successful writes return metadata, not the stored value. Revision conflicts
return HTTP 409. Responses use `Cache-Control: no-store`, and cross-origin
browser requests are rejected. Use HTTPS for remote access.

Local importer credential forms also use owner-authenticated, same-origin
POST requests rather than putting tokens or passwords in callback URLs.
Provider OAuth redirects continue to use their expected callback method.

Names are logical slash-separated identifiers, not paths: ASCII letters,
digits, `.`, `_`, `:`, and `-` are allowed within segments; empty, `.`, and
`..` segments are forbidden. Names are limited to 512 bytes, each value to
1 MiB, and total live values to 64 MiB. The store allows 10,000 names including
deletion tombstones. Keep names free of secret material.

New in-process consumers can use `pkg/secretstore`; HTTP clients use
`client.ListSecrets`, `GetSecret`, `PutSecret`, and `DeleteSecret`. Importers'
explicit credential reads can use `Object.SecretAttr` to report store errors.
Legacy `Attr`/`Attrs` calls continue to work without consulting historical
permanode values; generic attribute enumeration omits credentials.
