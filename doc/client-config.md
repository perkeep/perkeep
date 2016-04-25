# Configuring a client

The various clients (camput, camget, cammount...) use a common JSON config
file. This page documents the configuration parameters in that file. Run
`camtool env clientconfig` to see the default location for that file
(**$HOME/.config/camlistore/client-config.json** on linux). In the following
let **$CONFIGDIR** be the location returned by `camtool env configdir`.

## Generating a default config file

Run `camput init`.

On unix,

    cat $CONFIGDIR/client-config.json

should look something like:

    {
      "identity": "43AD73B1",
      "ignoredFiles": [
        ".DS_Store"
      ],
      "servers": {
        "localhost": {
          "auth": "localhost",
          "default": true,
          "server": "http://localhost:3179"
        }
      }
    }


## Configuration Keys & Values

### Top-level keys

* `identity`: your GPG fingerprint. Run `camput init` for help on how to
  generate a new keypair.

* `identitySecretRing`: Optional. If non-empty, it specifies the location of
  your GPG secret keyring. Defaults to **$CONFIGDIR/identity-secring.gpg**. Run
  `camput init` for help on how to generate a new keypair.

* `ignoredFiles`: Optional. The list of of files that camput should ignore and
  not try to upload.

### Servers

`servers`: Each server the client connects to may have its own configuration
section under an alias name as the key. The `servers` key is the collection of
server configurations. For example:

      "servers": {
        "localhost": {
          "server": "http://localhost:3179",
          "default": true,
          "auth": "userpass:foo:bar"
        },
        "backup": {
          "server": "https://some.remote.com",
          "auth": "userpass:pony:magic",
          "trustedCerts": ["ffc7730f4b"]
        }
      }

* `trustedCerts`: Optional. This is the list of TLS server certificate
  fingerprints that the client will trust when using HTTPS. It is required when
  the server is using a self-signed certificate (as Camlistore generates by
  default) instead of a Root Certificate Authority-signed cert (sometimes known
  as a "commercial SSL cert"). The format of each item is the first 20 hex
  digits of the SHA-256 digest of the cert. Example: `"trustedCerts":
  ["ffc7730f4bf00ba4bad0"]`

* `auth`: the authentication mechanism to use. Only supported for now is HTTP
  basic authentication, of the form: `userpass:alice:secret`. Username "alice",
  password "secret".

    If the server is not on the same host, it is highly recommended to use TLS
    or another form of secure connection to the server.

* `server`: The camlistored server to connect to, of the form:
  "[http[s]://]host[:port][/prefix]". Defaults to https. This option can be
  overriden with the "-server" command-line flag.

    Most client commands are meant to communicate with a blobserver. For such
    commands, instead of the client relying on discovery to choose the actual
    URL, the server URL can point directly to a specific blobserver handler,
    of the form: "[http[s]://]host[:port][/prefix][/handler/]".

    For example, to speed up syncing with `camtool sync`, one could write
    directly to the destination's blobserver, instead of the default, which is
    to write to both the destination blobserver and index.
    The above configuration sample can be extended by adding the following
    alias, where "`/bs/`" is the handler of the primary blobserver:

        "servers": {
          ...
          "backup-bs": {
            "server": "https://some.remote.com/bs/",
            "auth": "userpass:pony:magic",
            "trustedCerts": ["ffc7730f4b"]
          }
        }

    And the alias `backup-bs` can then be used as a destination by
    `camtool sync`.
