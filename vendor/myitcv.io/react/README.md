## `myitcv.io/react`

```
go get -u myitcv.io/react
```

[`myitcv.io/react`](https://godoc.org/myitcv.io/react) is a set of [GopherJS](https://github.com/gopherjs/gopherjs)
bindings/tools for [React](https://facebook.github.io/react/), a Javascript library for building user interfaces.

See [the wiki](https://github.com/myitcv/react/wiki) for more details

### Running the tests

As you can see in [`.travis.yml`](.travis.yml), the CI tests consist of running:

```bash
./_scripts/run_tests.sh
```

followed by ensuring that `git` is clean. This ensures that "current" generated files are committed to the repo.
