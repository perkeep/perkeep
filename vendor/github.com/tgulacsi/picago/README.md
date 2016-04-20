# picago
Picago is a small Go library for downloading photos from Picasa Web.

## Install
    go install github.com/tgulacsi/picago/pica-dl

After getting a client ID and secret from , you can run the example app as

    pica-dl -id=11849328232-4q13l4hgr5mdt35lbe49l8banqg5e1mk.apps.googleusercontent.com -secret=Y0xf_rauB9MVTNYAI2MYIz2w -dir=/tmp/pica

This will download all photos from all albums under /tmp/pica.
Each album and photo is accompanied with a .json file containing some metadata.
