# File Schema

    {"camliVersion": 1,
     "camliType": "file",

      // #include "common.md"      # metadata about the file
      // #include "../bytes.md"    # describes the bytes of the file

      // Optional, if linkcount > 1, for representing hardlinks properly.
      "inodeRef": "digalg-blobref",   // to "inode" blobref, when the link count > 1
    }

TODO: Mac/NTFS-style resource forks?  perhaps just a "streams" array of
recursive file objects?
