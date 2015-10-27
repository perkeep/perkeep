taglib-go
=========

Apache-licensed audio tag decoding library written in pure
go. Designed to mirror the structure of
[taglib](http://taglib.github.io/) without being a direct port.

## tl;dr
```
go get github.com/hjfreyer/taglib-go/taglib
```
```
import "github.com/hjfreyer/taglib-go/taglib"
...
func main() {
    f, err := os.Open("song.mp3")
    tag, err := taglib.Decode(f)
    fmt.Print(tag.Title())
}
```
## Features
Currently has basic read support for id3v2.3 and id3v2.4. No writing
support yet.

## Goals
* Pure go.
* Not necessarily feature complete, but future compatible.
* Good interfaces.
* Handle errors properly (don't panic).

## Why didn't you just use… ?
There are many other Go projects which do tag parsing, but all the
ones I found violate at least one of the goals above.

## Why don't you support… ?
Probably no reason other than it hasn't happened yet. If you need a
particular format, or an additional feature, or you've found a file
which taglib-go *should* parse but doesn't, please create an
[issue](https://github.com/hjfreyer/taglib-go/issues), or better yet,
send a patch.

NOTE: Though the copyright for this code is owned by Google, Google
does not endorse its use.
