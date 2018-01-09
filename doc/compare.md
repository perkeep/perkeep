# Perkeep compared

A frequently asked question is how Perkeep compares to similar
software and services.

## Longevity

Remember that Perkeep's goal is to keep your data for your entire
life. Let's call that 100 years. Or longer, if you want to pass your
data down generations. That influences a number of aspects of
Perkeep's design.

## Software, not a service

Perkeep is software that you can run on your own machine,
or that you can run on the cloud yourself, but it is not a hosted
service by a for-profit company. Many companies host your files,
but we wouldn't count on any one of them to stay in business or not
change business models in 100 years.
Sites [often die and lose user data](https://indieweb.org/site-deaths).

Many services start free to get rapid growth but later close when they
don't achieve enough growth, or don't find a business model that works
and their investors aren't happy. By paying for keeping your own data,
you avoid the uncertainty of what other people (companies, investors)
are going to do to your data.

We do provide the [Cloud Launcher](https://perkeep.org/launch/) to run
a copy on Google Compute Engine yourself. Perkeep runs happily on
desktop machines, Raspberry Pis, home servers, Amazon EC2, Google
Cloud Platform, or any other cloud provider. We suggest using at least
two for redundancy. Your replicas can sync amongst themselves.

## Open source

In addition to being software instead of a service, Perkeep is open
source software. That means that even if computers are much different
in 100 years, you'll still have hope of you or somebody else updating
Perkeep to run on them. Or at least you'll be able to read the code
and figure out the on-disk representation, if Perkeep's verbose,
data-archaeology-paranoid formats leave any doubt.

It also means many people can contribute, instead of one company that
might go out of business or change priorities.

## Objects, not files

Perkeep's data model is based primarily on nameless
objects. Perkeep can model traditional files with filenames and a
POSIX filesystem, but it can just as happily represent a tweet or a
"like" with no name. Perkeep is built as something you casually throw
data into and don't worry about organizing too much. It's all indexed
so you can search for it later. Or you can give it a name (or multiple
names!) if you prefer, but you can do that whenever you want later.
You can even have objects or files with multiple parent containers.

Many other products & services assume a file-centric view of the
world. Perkeep takes the view that files are becoming less important
over time. People care about backing up and searching their content,
not their files. (Note that iOS went about 10 years before having a
file browser or any concept of files.)

Because Perkeep can handle objects so well, we can import your tweets
or likes or check-ins or other social media content in a more natural
representation, rather than inventing files and names for everything.

## Compare

So, how does Perkeep compare to others?

### Upspin

[Upspin](https://upspin.io/) is also open source, and has many similar
goals. It differs in that its data model is very file-centric, with
everything having a name. It has prioritized sharing between users via
a global namespace and a global filesystem. See the section on
[Objects, not files](#objects-not-files) above.

Upspin [was
announced](https://security.googleblog.com/2017/02/another-option-for-file-sharing.html)
on 2017-02-21, about 6.5 years after Perkeep (Perkeep was named Camlistore at the time).

There are probably interesting collaboration areas between Upspin and
Perkeep. They share at least one common contributor familiar with both
systems. One could imagine Perkeep users creating a share that is
exported as an Upspin namespace, in the same way that Perkeep users
can mount their data with FUSE.

(this answer was largely taken from [an old Hacker News comment](https://news.ycombinator.com/item?id=13700629))

### IPFS

IPFS is an impressive and ambitious project. It also has some
similarity to Upspin and Perkeep. Like Upspin, IPFS wants to have a
global namespace and be a filesystem. See
https://github.com/ipfs/ipfs#quick-summary. Like Perkeep, the
content-addressable bits and representation of files is similar.

Perkeep uses an [Objects, not files](#objects-not-files) data model,
which makes writing importers for third-party sites easier when that
third-party content doesn't have an obvious file representation.

IPFS development began about 5 years after Perkeep. There is plenty of
room for collaboration between the two projects. Perkeep should
probably have an IPFS backend.

### Keybase Filesystem

There's a lot to like about the [Keybase
Filesystem](https://keybase.io/docs/kbfs). It's open source and like
Upspin, provides a global filesystem. It seems to be tied to the
[Keybase](https://keybase.io/) service and namespace, though.

Perkeep will probably have some optional integration with Keybase.

### git-annex

git-annex is file-centric. See the section on
[Objects, not files](#objects-not-files) above.

### Google Drive

Google Drive is a service, not open source software.
See the section on [Software, not a service](#software-not-a-service) above.

That said, Google Drive is a really nice service with features like
OCRing of images, object recognition of photos, and great search.
Many of the Perkeep authors regularly use Google Drive, and we have
the start of an importer. See the [tracking
bug](https://github.com/perkeep/perkeep/issues/896).

### Dropbox

Dropbox is a service, not open source software.
See the section on [Software, not a service](#software-not-a-service) above.

There is a [tracking bug](https://github.com/perkeep/perkeep/issues/1029) for
a Dropbox importer.

## Others

Other projects:

* [Libchop](http://nongnu.org/libchop/)
* [Tahoe-LAFS](http://tahoe-lafs.org/): predates Perkeep, file-centric
* [Unhosted](http://unhosted.org/)

See the [prior art](prior-art.md) page for some others.