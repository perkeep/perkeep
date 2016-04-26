# Camlistore Overview

Camlistore is your **personal storage system for life**.

## Summary

The project began because I wanted to...

* ... **store all my stuff forever**, not worrying about deleting, or losing
  stuff.

* ... **save stuff easily**, and **without categorizing it or choosing a
  location** whenever I save it.  I just want a data dumptruck that I can throw
  stuff at whenever.

* ... **never lose anything** because nothing can be overwritten (all blobs are
  content-addressable), and there's no delete support.  (optional garbage
  collection coming later)

* be able to **search for anything** I once stored.

* be able to **browse and visualize** stuff I've stored.

* ... **not always be forced into a POSIX-y filesystem model**. That involves
  thinking of where to put stuff, and most the time I don't even want
  filenames. If I take a bunch of photos, those don't have filenames (or not
  good ones, and not unique). They just exist. They don't need a directory or a
  name. Likewise with blog posts, comments, likes, bookmarks, etc. They're just
  objects.

* ... **have a POSIX-y filesystem when I want one**. And it should all be
  logically available on my tiny laptop's SSD disk, even if my laptop's disk is
  miniscule compared to my entire repo.  That is, there should actually be a
  caching virtual filesystem, not a daemon running rsync in the background. If
  I have to have a complete copy of my data locally, or I have to "choose which
  folders" to sync, that's broken.

* ... **be able to synthesize POSIX-y filesystems from search queries** over my
  higher-level objects. e.g. a "recent" directory of recent photos from my
  Android phone (this all works already in 0.1)

* **Not write another CMS system, ever**. Camlistore should be able to store
  and model any type of content, so it can just be a backend for other apps.

* ... have **backups of all my social network content** I created daily on
  other people's servers, to protect myself if my account is hijacked, the
  company goes evil, changes ownership, or goes out of business.

* ... have both a **web UI** and **command-line tools**, as well as a **FUSE
  filesystem**.

* ... **be in control** of my data, but also still be able to utilize big
  companies' infrastructure cloud products if desired.

* ... **be able to share content** with both technical and non-technical
  friends.


Most of this works as of the 0.1 [release](/download), and the rest and more is
in progress.

## Longer Answer

Throughout our life, we all continue to generate content, whether
that's writing documents, taking photos, writing comments online,
liking our friends' posts on social networks, etc. Our content is
typically spread between a mix of different companies' servers ("The
Cloud") and your own hardware (laptops, phones, etc).  All of these
things are prone to failure: companies go out of business, change
ownership, or kill products. Personal harddrives fail, laptops and
phones are dropped.

It would be nice if we were a bit more in control. At least, it
would be nice if we had a reliable backup of all our content. Once we
have all our content, it's then nice to search it, view it, and
directly serve it or share it out to others (public or with select
ACLs), regardless of the original host's policies.

Camlistore is a system to do all that.

While Camlistore can store files like a traditional filesystem
(think: "directories", "files", "filenames"), its specialized in
storing higher-level objects, which can represent anything.

In addition to an implementation, Camlistore is also a schema for
how to represent many types of content. Much JSON is used.

Because every type of content in Camlistore is represented using
content-addressable blobs (even metadata), it's impossible to
"overwrite" things. It also means it's easy for Camlistore to sync in
any direction between your devices and Camlistore storage servers, without
versioning or conflict resolution issues.

Camlistore can represent both immutable information (like snapshots
of filesystem trees), but can also represent mutable
information. Mutable information is represented by storing immutable,
timestamped, GPG-signed blobs representing a mutation request. The
current state of an object is just the application of all mutation
blobs up until that point in time. Thus all history is recorded and
you can look at an object as it existed at any point in time, just by
ignoring mutations after a certain point.

Despite using parts of the OpenPGP spec, users don't need to use
the GnuPG tools or go to key signing events or anything dorky like
that.

You are in control of your Camlistore server(s), whether you run
your own copy or use a hosted version. In the latter case, you're at
least logically in control, analagous to how you're in charge of your
email (and it's your private repository of all your email), even if a
big company runs your email for you. Of course, you can also store all
your email in Camlistore too, but Gmail's interface and search is much
better.

Responsible (or paranoid) users would set up their Camlistore
servers to cross-replicate and mirror between different big companies'
cloud platforms if they're not able to run their own servers between
different geographical areas. (e.g. cross-replicating between
different big disks stored within a family)

A Camlistore server comprises several parts, all of which are
optional and can be turn on or off per-instance:

* **Storage**: the most basic part of a Camlistore server is
  storage. This is anything which can Get or Put a blob (named by its
  content-addressable digest), and enumerate those blobs, sorted by
  their digest. The only metadata a storage server needs to track
  per-blob is its size. (No other metadata is permitted, as it's
  stored elsewhere) Implementations are trivial and exist for local
  disk, Amazon S3, Google Storage, etc. They're also composable, so
  there exists "shard", "replica", "remote", "conditional", and
  "encrypt" (in-progress) storage targets, which layer upon
  others.

* **Index**: index is implemented in terms of the Storage
  interface, so can be synchronously or asynchronously replicated to
  from other storage types. Putting a blob indexes it, enumerating
  returns what has been indexed, and getting isn't supported. An
  abstraction within Camlistore similar to the storage abstractions
  means that any underlying system which can store keys & values and
  can scan in sorted order from a point can be used to store
  Camlistore's indexes. Implementations are likewise trivial and exist
  for memory (for development), SQLite, LevelDB, MySQL, Postgres,
  MongoDB, App Engine, etc. Dynamo and others would be trivial.

* **Search**: pointing Camlistore's search handlers at an index
  means you can search for your things.  It's worth pointing out that
  you can lose your index at any time. If your database holding your index
  goes corrupt, just delete it all and re-replicate from your storage
  to your index: it'll be re-indexed and search will work again.

* **User Interface**: the web user interface lets you click
  around and view your content, and do searches. Of course, you could
  also just use the command-line tools or API.

Enough words for now.  See [the docs](/docs/) and code for more.

*Last updated 2013-06-12*
