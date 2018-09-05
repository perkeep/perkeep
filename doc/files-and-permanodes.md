# Files or Permanodes?

Even though the basic unit in Perkeep is the blob, the high-level object that
Perkeep relies on and manipulates is the [**permanode**](/doc/schema/permanode.md)
(which is just a kind of blob). Permanodes are what one interacts with in the
various interfaces, such as the Web UI.

That is why, when one uses e.g. `pk-put file somefile`, one does not see any
representation of the uploaded file in the Web UI. The file must be associated
to a permanode. With the **pk-put file** command, there are two main ways to achieve
that: the `--filenodes` option, and the `--permanode` option. But what is the
difference?

* Use the `--filenodes` option if you want to attach each of the files given as
argument to their own individual permanode. Each of them will then appear in the
Web UI as a distinct object that can be browsed, searched, displayed, etc.
* Use the `--permanode` option if you want to preserve your directories
hierarchy. The hierarchy will be browsable, but only the top-level directory
will be associated with a permanode and represented as a distinct object. This
is mostly meant for archival usage.
