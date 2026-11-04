Google Takeout importer
=======================

Imports [Google Takeout](https://takeout.google.com/) exports from the
importer pages, rather than with `pk takeout`. It keeps each whole export,
the files of all of its Google products, and imports the data of the
products it knows, as Perkeep's own kinds of things. For now, that's
Google Photos: its photos and videos, with their metadata, and its albums,
as `pk takeout upload` imports them. Other products' files are archived,
for importing later, when the importer knows them.

An export's zip files are read from a file source: a Google Drive, which is
where Takeout can put exports, or a directory on the server. Add one on the
importer pages' file sources page (/importer/sources/) first.

Each export is imported by an account of its own:

1. Add a Google Takeout account, pick the file source, and pick the export.
   By default, exports are looked for in the source's top-level Takeout
   folders; any other folder can be looked in instead.
2. The account's first run scans the export: it reads the zip files' tables
   of contents, and asks the server what of the export it has already. The
   account's page then shows what the export has: each product's files,
   and, for each product imported, what's to import, such as Google
   Photos' photos and videos, its albums, and which the server lacks.
3. Confirm the import there. Its run archives the whole export and imports
   each product: for Google Photos, it uploads the photos and videos the
   server doesn't have, with their metadata, and updates the metadata of
   the photos the server had, and the albums.

A run that the server stopping interrupts resumes when the server starts
again. One that's paused on the account's page doesn't; start it again
there. A run picks up where it left off, as uploads skip what the server
has, and an archive that's done isn't made again.

The zip files are pinned to the versions that were picked: if they change,
such as by an export being made again, set the account up again. If a
Google Drive's authorization expires or is revoked, authorize the file
source again, and start the import again.

Account attributes
------------------

    camliNodeType: "importerAccount"
    importerType: "googletakeout"
    googleTakeoutFileSource: the file source's permanode
    googleTakeoutFolder: the folder looked in, in the source, if not the default ones
    googleTakeoutExport: the export's name, such as "takeout-20240101T120000Z"
    googleTakeoutZip: each zip file, as JSON, with its file source ID and version
    googleTakeoutState: "scanning", "review", "importing" or "done"
    googleTakeoutPaused: "true" if a run was paused
    googleTakeoutOverview: what the export has, as JSON, once scanned, with a section for each product imported
    googleTakeoutResult: what the import did, as JSON, once done, with a section for each product imported
    googleTakeoutExportPermanode: the permanode of the archived export
    googleTakeoutPhotosEdited: "true" to import edited copies of Google Photos photos too

Options of a product's import are named for it, as googleTakeoutPhotosEdited
is.

The zip files' tables of contents, and the files read from them for their
metadata, are cached in the importer cache's directory, if the server has
one.
