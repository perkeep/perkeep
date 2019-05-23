# picago
Picago is a small Go library for downloading and uploading photos from Picasa Web.

## Install
    go install github.com/tgulacsi/picago/pica-dl

## Permissions

You must obtain a client ID and secret from Google in order to call the APIs this project relies on. 

1. Go to https://console.developers.google.com/project and create a new project. You can name it whatever you want. You can also reuse an existing project if you have one.

2. Click on the project, and under "APIs & auth" > "Consent screen", ensure you have "Product name" and "Email address" specified.

3. Under "APIs and auth" > "Credentials" click "Create new client ID". Choose type "Installed application", then installed application type "Other".

## Running

After getting a client ID and secret, you can run the example app as

    pica-dl -id=11849328232-4q13l4hgr5mdt35lbe49l8banqg5e1mk.apps.googleusercontent.com -secret=Y0xf_rauB9MVTNYAI2MYIz2w -dir=/tmp/pica

This will download all photos from all albums under /tmp/pica.
Each album and photo is accompanied with a .json file containing some metadata.
