<!-- __JSON: go list -json .
## `{{ filepathBase .Out.ImportPath}}`

{{.Out.Doc}}

```
go get -u {{.Out.ImportPath}}
```
-->
## `react`

Package react is a set of GopherJS bindings for Facebook's React, a Javascript library for building user interfaces.

```
go get -u myitcv.io/react
```
<!-- END -->

### Running the tests

As you can see in [`.travis.yml`](.travis.yml), the CI tests consist of running:

```bash
./_scripts/run_tests.sh
```

followed by ensuring that `git` is clean. This ensures that "current" generated files are committed to the repo.

### Docs

Please start reading [here](_doc/README.md).
