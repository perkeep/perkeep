# Documentation

* [Overview](/doc/overview.md): The original motivation and background for why
  Perkeep exists and what one might use it for.
* [Compare](/doc/compare.md): how Perkeep compares to similar services and software

## For Users

**If you're just looking to set up a Perkeep server and use it yourself,
check out our [getting started guide](https://perkeep.org/download#getting-started).**
The documents below go into more detail on customizing the high level
configuration for use such as alternative blob storage or
synchronization to cloud storage.

* [Command-line tools](/cmd/)
* [Server Config](/doc/server-config.md): Details for configuring server storage
  and access, including synchronization to other Perkeep servers or backup
  to cloud storage providers
* [Client config](/doc/client-config.md): Clients need this configuration file to
  securely connect to your Perkeep server(s)
* [Search Commands](/doc/search-ui.md): Covers the available search operators
* [Configuring Geocoding](/doc/geocoding.md): how to enable geocoding (the `loc:` search operator)
* [Files or Permanodes](/doc/files-and-permanodes.md): explains the basic difference between a file and a permanode

## For Developers

If you want to help the development of Perkeep or just want to know more
about the how and why behind Perkeep, these docs are going to help you
get started. **Something we didn't cover here that you're interested in?** Ask
on the [mailing list](https://groups.google.com/group/camlistore).


### Concepts

* [Principles](/doc/principles.md):  our base principles, goals, assumptions
* [Terminology](/doc/terms.md):  let's agree on terms to stay sane
* [Use Cases](/doc/uses.md): what one might do with all this (or at least our aspirations)
* [Prior Art](/doc/prior-art.md): other projects with similar goals or strategies
* [Contributing](/code#contributing): how to help
* [Style guide](/doc/web-ui-styleguide.md) for the Web UI


### Technical Docs

* [Packages](/pkg/): internal API documentation
* [Architecture](/doc/arch.md): the pieces, layers, and how they interact
* [Schema](/doc/schema/): how we model data in Perkeep
* [Protocol](/doc/protocol/): HTTP APIs (discovery, blob storage, JSON signing, ...)
* [JSON Signing](/doc/json-signing/)
* [Sharing](/doc/sharing.md)
* [Environement Variables](/doc/environment-vars.md)


## Presentations {#presentations}

* 2018-04, **LinuxFest Northwest**: [[slides]](https://docs.google.com/presentation/d/1suYfv3dmjJQ1mMJIG7_D26e5cudZqPcZTPNgrLvTIrI/view) [[video]](https://www.youtube.com/watch?v=PlAU_da_U4s)
* 2016-04, **GDG Seattle**: [[slides]](https://docs.google.com/presentation/d/1AmT5DAL9CrzQFS22i0xJ5SYtXQfrHqOyYiQB7imshdw/view) [[video]](https://www.youtube.com/watch?v=dg6OmoKNbcw)
* 2016-04, **LinuxFest Northwest**: [[slides]](https://docs.google.com/presentation/d/1AmT5DAL9CrzQFS22i0xJ5SYtXQfrHqOyYiQB7imshdw/view) [[video]](https://www.youtube.com/watch?v=8Dk2iVlc67M)
* 2015-02, **FOSDEM**: [[slides]](https://go-talks.appspot.com/github.com/mpl/talks/fosdem-2015/fosdem-20150201.slide) [[video]](https://www.youtube.com/watch?v=oM-MfeflUZ8)
* 2014-02, **FOSDEM**: [[slides]](http://go-talks.appspot.com/github.com/mpl/talks/fosdem-2014/2014-02-02-FOSDEM.slide) [[video]](https://www.youtube.com/watch?v=kBCQq5hfsug) [[WebM]](http://video.fosdem.org/2014/K4601/Sunday/Camlistore.webm)
* 2013-06, **Google Developers Live**: [[video]](https://www.youtube.com/watch?v=yxSzQIwXM1k)
* 2011-05, **São Paolo Perl Conference**: [[slides]](/talks/2011-05-07-Camlistore-Sao-Paolo/)
* 2011-02, **First Introduction**: [[slides]](https://docs.google.com/present/view?id=dgks53wm_2j86hwnhs)


## Video tutorials {#tutorials}

* 2014-03, [Getting started with Camlistore](https://www.youtube.com/watch?v=RUv-8PhnNp8)
* 2014-03, [Getting started with pk put and the Camlistore client tools](https://www.youtube.com/watch?v=DdccwBFc5ZI)
