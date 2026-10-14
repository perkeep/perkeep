# Agent guidance

Keep this file focused on agent instructions and pointers. Put explanations
useful to humans in the existing documentation and link to them here rather
than duplicating them.

## Before making changes

- Read [CONTRIBUTING.md](CONTRIBUTING.md) for development, testing, and
  contribution conventions.
- Read local README files and any more-specific agent guidance in the
  directories you will change.
- Use [go.mod](go.mod) for the required Go toolchain version;
  [BUILDING](BUILDING) describes the build entry point.
- Consult [doc/README.md](doc/README.md) for the documentation index,
  especially [terminology](doc/terms.md), [architecture](doc/arch.md), and
  [files and permanodes](doc/files-and-permanodes.md) before changing data
  modeling or storage behavior.

## Where to look

This is the `perkeep.org` Go module. Use this map to locate code, then read
the package comments and nearby tests for implementation details.

- `cmd/`: command-line clients (`pk`, `pk-put`, `pk-get`, `pk-mount`).
  `pkg/client/` implements the shared client library.
- `server/perkeepd/`: server executable. `pkg/server/` has HTTP handlers;
  `pkg/serverinit/` assembles handlers and storage from configuration.
  See [server configuration](doc/server-config.md).
- `pkg/blob/`, `pkg/blobserver/`: blob references, storage interfaces,
  backends, and storage wrappers.
  See the [protocol docs](doc/protocol/README.md).
- `pkg/schema/`, `pkg/jsonsign/`: schema blobs and signing.
  See the [schema docs](doc/schema/README.md) and
  [JSON signing docs](doc/json-signing/README.md).
- `pkg/index/`, `pkg/search/`, `pkg/sorted/`: indexing and its in-memory
  corpus, search, and ordered key/value backends used by the index.
  See [search operators](doc/search-ui.md) and the index package comments.
- `pkg/importer/`: service importers; read the relevant importer's README.
- `clients/web/`: embedded Web UI sources and assets.
  See the [Web UI style guide](doc/web-ui-styleguide.md).
  `clients/` also holds other client implementations.
- `app/`: separate applications, including the publisher.
  See [app/README.md](app/README.md) and
  [app/publisher/README.md](app/publisher/README.md).
- `internal/`: private support packages. `pkg/test/`: shared test helpers
  and integration tests. `dev/devcam/`: the development wrapper described
  in [CONTRIBUTING.md](CONTRIBUTING.md).
- `doc/`: user and developer documentation. `website/`: the website server
  and site content; see [website/README.md](website/README.md) for previews.

## Validation

- Format changed Go files with `gofmt` and run tests for affected packages.
  Follow [Testing Patches](CONTRIBUTING.md#testing-patches) for the suite.
- Consult [the CI workflows](.github/workflows/) for platform, lint, and
  database test requirements. Do not count skipped tests as coverage;
  report skips, missing prerequisites, or checks you could not run.
- For documentation-only changes, check links and paths and review the diff;
  a full Go test run is not necessary.

## Commits

- Do not add `Co-authored-by` trailers to AI-created commits.
- Use Linux-style titles: `area: imperative summary`, with no trailing
  period. Keep titles concise and wrap commit text at about 76 characters.
- Explain the problem and the change in the body when it is not obvious.
- When an issue applies, preferably include `Fixes #nnnn` if the issue is
  fully resolved, or `Updates #nnnn` if the work is partial. Do not invent
  an issue number or claim a partial fix closes an issue.
- See [Commit Messages](CONTRIBUTING.md#commit-messages) for the project's
  example and additional context.
