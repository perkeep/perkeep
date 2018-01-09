# Prior Art & Related Projects

## Prior Art

* [LiveJournal](http://www.livejournal.org/)'s photo hosting, my first
  experiment with content-addressable storage, which led to:

  * [Brackup](http://code.google.com/p/brackup/), my original
    content-addressable backup tool, but didn't store directories as the digest
    of their contents.  (so backup manifests were huge)

* [Git](http://git-scm.com/), which exposed me to the idea of hashing
  directories, commits, etc.  But git probably got it from Venti / Fossil:

  * [Venti](https://en.wikipedia.org/wiki/Venti) /
    [Fossil](https://en.wikipedia.org/wiki/Fossil_\(file_system\)), apparently
    pioneered the idea of recursive content-addressable file systems.

* [Monotone](http://www.monotone.ca/)'s "Certificates" are similar to Perkeep
  claims. (See [terminology](terms.md#claims))

Probably more, though.  Contributions to this list are welcome!

See the [comparison page](compare.md) for more details on how
Perkeep compares to other software and services.