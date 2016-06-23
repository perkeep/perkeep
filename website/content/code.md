# Get the code

    $ git clone https://camlistore.googlesource.com/camlistore

-   [Latest changes](https://camlistore.googlesource.com/camlistore)
-   [Browse
    tree](https://camlistore.googlesource.com/camlistore/+/master)
-   [Code Review](https://camlistore-review.googlesource.com/)

## Making apps that work with Camlistore

Camlistore is built so that other apps can securely access and store
data without running alongside it. Camlistore is the perfect backing
store for other web apps and CMSes.

Detailed documention on the HTTP blob retrieval protocol can be found at
[the protocol documentation](/doc/protocol). The [client](/pkg/client),
[search](/pkg/search) and [schema](/pkg/schema) packages are also a good
place to start.

## Contributing

-   Join the [mailing list](https://groups.google.com/group/camlistore).

-   Pick something that interests you, or look through our list of
    [potential projects](/doc/todo) for inspiration. **Discuss it
    first**, especially if it's large and/or not well designed yet.
    You'll save yourself a headache if someone is already working on
    something similar or if there's a more Camlistore-like approach to
    the issue.

-   Submit your changes to Gerrit through the review process discussed below.

-   Note that before sending your first change for review (`devcam review`),
    you'll need to agree to a [Contributor License Agreement](https://cla.developers.google.com).

-   Once your first change has been accepted and merged, send a new change to
    Gerrit, adding yourself to the
    [AUTHORS](https://camlistore.googlesource.com/camlistore/+/master/AUTHORS)+[CONTRIBUTORS](https://camlistore.googlesource.com/camlistore/+/master/CONTRIBUTORS)
    files (or just
    [CONTRIBUTORS](https://camlistore.googlesource.com/camlistore/+/master/CONTRIBUTORS)
    if the company which owns your Copyright is already in the
    [AUTHORS](https://camlistore.googlesource.com/camlistore/+/master/AUTHORS)
    file). We follow the
    [Go convention](https://golang.org/doc/contribute.html#copyright)
    for the commit messages; see for examples:
    [85bf99a7](https://camlistore.org/gw/85bf99a7), and
    [8f9af410](https://camlistore.org/gw/8f9af410).

### Code Review

-   Camlistore requires changes to be reviewed before they are
    committed.

-   Update your `~/.netrc` file with a Gerrit username and password.
    Click the **"Generate a new password"** link from the top of
    [https://camlistore.googlesource.com/](https://camlistore.googlesource.com/).

-   Usual Work Flow

    -   Create a topic branch, make some changes and commit away.

    -   Read
        [CONTRIBUTING](https://camlistore.googlesource.com/camlistore/+/master/CONTRIBUTING.md).
        Install devcam.

    -   Test. (`devcam test`).

    -   Squash your changes into a single change, and compose a proper
        commit message.
    -   Send for review with:

            devcam review

    -   Modify as necessary until change is merged. Amend your commit or
        squash to a single commit before sending for review again (be
        sure to keep the same [the Change-Id
        line](http://gerrit.googlecode.com/svn/documentation/2.2.1/user-changeid.html))

### Documentation

To work on the documentation, you'll need to locally build and run the webserver
that serves camlistore.org. See
[/website/README.md](https://camlistore.googlesource.com/camlistore/+/master/website/README.md).
