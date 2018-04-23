# Contributing

## Getting Started

Perkeep contributors regularly use Linux and OS X, and both are
100% supported.

Developing on Windows is sometimes broken, but should work.  Let us
know if we broke something, or we accidentally depend on some
Unix-specific build tool somewhere.

See https://perkeep.org/doc/contributing for information on how to
contribute to the project and submit patches.  Notably, we use Gerrit
for code review.  Our Gerrit instance is at https://perkeep.org/r/

See architecture docs: https://perkeep.org/doc/

You can view docs for Perkeep packages with local godoc, or
godoc.org.

It's recommended you use git to fetch the source code, rather than
hack from a Perkeep release's zip file:

    $ git clone https://camlistore.googlesource.com/camlistore perkeep

(We use github for distribution but its code review system is not as
nice as Gerrit, so we don't use its Pull Request mechanism. The Gerrit
git server & code review system is the main repo. See
https://perkeep.org/doc/contributing for how to use them.  We might
support github for pull requests in the future, once it's properly
integrated with external code review tools. We had a meeting with
Github to discuss the ways in which their code review tools are poor.)

On Debian/Ubuntu, some deps to get started:

    $ sudo apt-get install libsqlite3-dev sqlite3 pkg-config git

During development, rather than use the main binaries ("pk-put",
"pk-get", "camtool", "pk-mount", etc) directly, we instead use a
wrapper (devcam) that automatically configure the environment to use
the test server & test environment.

## Building devcam

To build devcam:

    $ go run make.go

And devcam will be in &lt;camroot&gt;/bin/devcam.  You'll probably want to
symlink it into your $PATH.

Alternatively, if your Perkeep root is checked out at
$GOPATH/src/perkeep.org (optional, but natural for Go users), you
can just:

    $ go install ./dev/devcam

## Running devcam

The subcommands of devcam start the server or run pk-put/pk-get/etc:

    $ devcam server      # main server
    $ devcam put         # pk-put
    $ devcam get         # pk-get
    $ devcam tool        # camtool
    $ devcam mount       # pk-mount

Once the dev server is running,

- Upload a file:

      devcam put file ~/perkeep/COPYING

- Create a permanode:

      devcam put permanode

- Use the UI: http://localhost:3179/ui/


## Testing Patches

Before submitting a patch, you should check that all the tests pass with:

    $ devcam test

You can use your usual git workflow to commit your changes, but for each
change to be reviewed you should merge your commits into one before submitting
to gerrit for review.

## Commit Messages

You should also try to write a meaningful commit message, which at least states
in the first sentence what part or package of perkeep this commit is affecting.
The following text should state what problem the change is addressing, and how.
Finally, you should refer to the github issue(s) the commit is addressing, if any,
and with the appropriate keyword if the commit is fixing the issue. (See
https://help.github.com/articles/closing-issues-via-commit-messages/).

For example:

> pkg/search: add "file" predicate to search by file name
>
> File names were already indexed but there was no way to query the index for a file
> by its name. The "file" predicate can now be used in search expressions (e.g. in the
> search box of the web user interface) to achieve that.
>
> Fixes #10987

## Vendored Code

Changes to vendored third party code must be done using the dep tool.
Information on adding, removing and updating vendor deps can be found
in the [dep documentation](https://github.com/golang/dep/blob/master/README.md).

Once you have your changes and versions specified please run the
following commands to update the vendor directory with your changes:

    $ dep ensure

To help track version changes please add the semantic version and the
revision in your commit message the version which you can find in the
Gopkg.lock file.

## Contributors

We follow the Go convention for commits (messages) about new Contributors.
See https://golang.org/doc/contribute.html#copyright , and examples such as
https://perkeep.org/gw/85bf99a7, and https://perkeep.org/gw/8f9af410.

## git Hooks

You can optionally use our pre-commit hook so that your code gets gofmt'ed
before being submitted (which should be done anyway).

    $ devcam hook

Finally, submit your code to gerrit with:

    $ devcam review

Please update this file as appropriate.
