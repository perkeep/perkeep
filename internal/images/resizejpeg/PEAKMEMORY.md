# Peak memory for giant JPEG thumbnails

This experiment measures `DecodeThumbnail` in a fresh process, streaming
JPEG bytes from a file. It does not infer peak memory from benchmark
allocated bytes/op, and does not include fixture generation or compilation.

## Results

Linux/amd64, Go 1.27.2, cgo and race instrumentation disabled. These are
synthetic quality-80, 4:2:0 color JPEGs, not photographs. All decodes used
factor 8. The table gives the **largest observed peak across three fresh
processes**, conservatively taking the greater of `/proc`'s VmHWM and
GNU `time`'s RSS high-water mark for each process.

| Input | Output | Workers | JPEG file size | Peak process RSS | Sampled peak Go heap |
| --- | ---: | ---: | ---: | ---: | ---: |
| Empty probe, no decode | — | — | — | 6.00 MiB | — |
| Single scan, 12000x9000 (108 MP) | 256x192 | 2 | 61.69 MiB | 10.25 MiB | 3.87 MiB |
| Single scan, 20000x15000 (300 MP) | 256x192 | 2 | 171.33 MiB | 12.50 MiB | 6.16 MiB |
| Single scan, 20000x30000 (600 MP) | 256x384 | 2 | 342.58 MiB | 12.50 MiB | 6.36 MiB |
| Progressive, 20000x15000 (300 MP), explicitly allowed | 256x192 | 2 | 159.43 MiB | 93.12 MiB | 86.71 MiB |
| Single scan, 20000x15000 (300 MP) | 256x192 | 8 | 171.33 MiB | 23.89 MiB | 17.59 MiB |

Doubling height at the same source width did not materially increase
single-scan peak memory, apart from the taller output image. More workers
increase the number of retained coefficient bands. Progressive storage
instead grows with source block count; other sampling formats can change
that count substantially.

These are observed peaks for these inputs and settings, not hard limits
for every JPEG or a prediction of total Perkeep server RSS.

For caller-controlled limits rather than measurements, both resizing APIs
support `MaxMemoryBytes` and the optional `MemAllocSemaphore` admission
hook. See [memory budgets](MEMORY.md). Those reservations describe active
decode buffers, not process RSS or the lifetime of returned images.

## Admission-limit check

The same 300 MP files were also decoded with `MaxMemoryBytes: 16 << 20`,
two workers, and the default 256x192 thumbnail. Each used a fresh process,
streaming input from disk:

| Input | Planned reservation | Result | Peak process RSS |
| --- | ---: | --- | ---: |
| Single scan | 6.37 MiB | Completed | 12.64 MiB |
| Progressive | 86.92 MiB | Rejected before coefficient/worker allocation | 6.14 MiB |

The progressive rejection is expected: its compact coefficient arrays
alone exceed the cap. It started zero workers and reported
`resize requires 91142464 bytes, exceeds memory budget 16777216`.
The planned bytes are not process RSS; the latter includes the runtime
and test harness. These are single-run admission checks, not replacements
for the repeated measurements above.

## Method

The opt-in `jpegmemory` test binary runs only `TestThumbnailMemoryProbe`.
Each measurement starts a new process with `GOMAXPROCS=2`, `GOGC=100`,
and `GOMEMLIMIT=off`. The decoder uses two workers unless explicitly
overridden and produces a 256-pixel-wide, aspect-preserving thumbnail.
The filter and thumbnail headroom use the defaults, Lanczos3 and 4.
These trusted synthetic inputs intentionally bypass the example
25-megapixel source budget; production callers should choose their own
limits.

The probe records Linux `/proc/self/status`'s process high-water RSS and
samples Go heap allocation throughout decoding. A final heap sample is
taken while all decoder-owned buffers remain reachable. Heap sampling is
approximate; process high-water RSS is the primary peak measurement.
GNU `time` separately records the same kernel high-water metric; it is a
cross-check, not an independent measure of native/Go allocations.

The original JPEG is **not** loaded into a `[]byte`. File contents in the
kernel page cache are not part of process RSS. Any caller that retains the
entire compressed file, or any surrounding HTTP/storage/cache layer, adds
memory outside this experiment.

RSS includes the Go runtime and test harness, not just decoder buffers.
It is not an incremental-memory measurement of a running Perkeep server:
runtime overhead is shared, and existing heap capacity, GC settings,
concurrent requests, and other server activity affect real process RSS.
Do not multiply isolated process RSS by the number of concurrent requests.

Synthetic color JPEGs are generated with Go's encoder from a procedural
image, without a full source pixel buffer. Progressive input is produced
separately with `jpegtran`; that preparation is **outside** the measured
process. The decoder under measurement remains pure Go and does not
execute a child process or call a native library.

## Why single-scan and progressive differ

For ordinary complete single scans, only MCU-row bands, reduced worker
pixels, filter metadata, active destination rows, and the final thumbnail
are retained. Increasing source height does not require more scratch.
Source width, worker count, and target dimensions do.

The largest buffer is often the coefficient-band pool: it currently
stores 64 int32 coefficients for every component block in a source MCU
row, even when reduced IDCT uses only a subset. For a 20,000-pixel-wide
4:2:0 JPEG and two workers, three bands retain about 5.49 MiB of
coefficients, independently of source height. Pixel/filter/output buffers
and runtime overhead are additional.

Progressive input requires explicit `AllowFullDecode: true`. It retains
image-wide compact coefficients and nonzero flags until all scans finish.
At factor 8, each source component block needs four coefficient bytes and
an eight-byte nonzero mask. For a 300-megapixel 4:2:0 image that is about
80.5 MiB before row-band reconstruction scratch and runtime overhead.
No full-resolution pixel image is allocated, but total memory is no longer
independent of source height.

## Reproduce

From the repository root, build the opt-in probe once:

```sh
CGO_ENABLED=0 go test -c -tags=jpegmemory \
  ./internal/images/resizejpeg -o /tmp/resizejpeg-peakmem.test
```

Generate into a **fresh** path in a separate process:

```sh
RESIZEJPEG_MEM_MODE=generate RESIZEJPEG_MEM_SIZE=20000x15000 \
RESIZEJPEG_MEM_INPUT=/tmp/giant-20000x15000.jpg \
GOMAXPROCS=2 /tmp/resizejpeg-peakmem.test \
  -test.run '^TestThumbnailMemoryProbe$' -test.v
```

Measure in another fresh process:

```sh
RESIZEJPEG_MEM_INPUT=/tmp/giant-20000x15000.jpg \
GOMAXPROCS=2 GOGC=100 GOMEMLIMIT=off \
/usr/bin/time -v /tmp/resizejpeg-peakmem.test \
  -test.run '^TestThumbnailMemoryProbe$' -test.v
```

`RESIZEJPEG_MEM_MODE=baseline` measures the same binary without a decode.
Use `RESIZEJPEG_MEM_WORKERS` to override worker count. The decoder source
budget is deliberately unlimited in this manual, trusted-input probe;
it is not a suggested production default.

To exercise admission, add `RESIZEJPEG_MEM_MAX_BYTES=16777216` to a decode
invocation. The probe logs `plannedMemoryBytes` and `maxMemoryBytes`.
An over-budget input logs the plan and error, then intentionally exits
with a failed test; it is not a successful decode measurement.

The measured progressive fixture was prepared separately:

```sh
jpegtran -maxmemory 2000000 -progressive \
  -outfile /tmp/giant-20000x15000-progressive.jpg \
  /tmp/giant-20000x15000.jpg
```

The `jpegtran` memory allowance and fixture-generation process are not
included in the decoder's peak RSS. Decode the resulting file in another
fresh probe process. The probe explicitly permits progressive coefficient
buffering; the ordinary API rejects it by default.

## Validation

All 18 isolated baseline/decode invocations completed successfully, with
the expected dimensions, component count, factor, worker count, and
progressive policy. `/proc` and GNU `time` peaks differed by less than
0.3 MiB; the larger value was used above. Every decode streamed its input
from disk and retained the decoder until the final heap/RSS snapshots.

Tagged helper tests passed, including generation format, no per-pixel
source allocation, overwrite protection, RSS parsing, and sampler joining.
Helper race tests, tagged vet, and the ordinary package suite also passed.
The probe was separately smoke-tested on small sequential and progressive
inputs. No production decoder behavior changed, and the full repository
test suite was not run.
