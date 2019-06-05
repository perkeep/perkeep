# Get the code

    $ cd $GOPATH/src
    $ git clone https://github.com/perkeep/perkeep perkeep.org

-   [Latest changes](https://github.com/perkeep/perkeep/commits/master)
-   [Browse
    tree](https://github.com/perkeep/perkeep/tree/master/)
-   [Open Pull Requests](https://github.com/perkeep/perkeep/pulls)

## Making apps that work with Perkeep

Perkeep is built so that other apps can securely access and store
data without running alongside it. Perkeep is the perfect backing
store for other web apps and CMSes.

Detailed documention on the HTTP blob retrieval protocol can be found at
[the protocol documentation](/doc/protocol). The [client](/pkg/client),
[search](/pkg/search) and [schema](/pkg/schema) packages are also a good
place to start.

## Contributing

-   Join the [mailing list](https://groups.google.com/group/perkeep).

-   Pick something that interests you, or look through our list of
    [potential projects](/doc/todo) for inspiration. **Discuss it
    first**, especially if it's large and/or not well designed yet.
    You'll save yourself a headache if someone is already working on
    something similar or if there's a more Perkeep-like approach to
    the issue.

-   Sent a Pull Request on GitHub. Expect to make a few changes during
    the review.

-   A bot on GitHub will tell if you haven't yet signed the
    [Contributor License Agreement](https://cla.developers.google.com).

### Documentation

To work on the documentation, you'll need to locally build and run the
server (`pk-web`) that serves perkeep.org: `go install perkeep.org/website/pk-web && pk-web --help`.
The contents of the website are in the `static` and `content`
directories under the [website directory](https://github.com/perkeep/perkeep/tree/master/website).
