# Reduced-IDCT thumbnails with configurable filtering

`DecodeThumbnail` is the faster alternative to full-resolution
[`DecodeResize`](STREAMING.md): reconstruct a conservatively reduced image,
then finish with a scale-aware filter (Lanczos3 by default). Both transforms
and horizontal filtering run in the bounded worker pool. No C, cgo, `purego`,
native libraries, or external programs are used by the decoder.

```go
thumb, err := resizejpeg.DecodeThumbnail(r, 256, 192, &resizejpeg.ResizeOptions{
    Workers:         2,
    MaxSourcePixels: 25_000_000,
})
```

The requested dimensions are exact; choose the aspect ratio and orientation
policy before calling. The result is opaque `*image.RGBA`. Like the other
APIs, this filters encoded RGB, not linear light, and does not apply EXIF
orientation or ICC profiles.

`MaxMemoryBytes` caps the planned buffer reservation; `MemAllocSemaphore`
can share a cancelable byte-weighted admission budget across callers.
Both cover progressive fallback too. See [memory budgets](MEMORY.md) for
details, including the separate lifetime of returned image pixels.

## Scale selection and quality

By default, from the source header choose the largest reduction factor in
8, 4, 2, 1 whose rounded-up intermediate width and height both retain at
least four times the corresponding target dimension. If an original dimension is
smaller than that goal, preserve that dimension instead. The source is
never upsampled to manufacture headroom.

Examples for a 256x192 thumbnail:

| Source | IDCT factor | Intermediate sample grid |
| --- | ---: | ---: |
| 4000x3000 | 2 | 2000x1500 |
| 3264x2448 | 2 | 1632x1224 |
| 2048x1536 | 2 | 1024x768 |
| 1024x768 | 1 | 1024x768 |

Power-of-two choices commonly leave between four and eight times the
target dimensions. Factor 8 is the maximum reduction; very large inputs
and small targets can therefore retain more headroom. A larger thumbnail
may require factor 1, in which case there is no reduced-IDCT speedup.

Reduced transforms keep more detail than decoding straight to thumbnail
size. The final Lanczos3 filter has a scale-widened footprint, rather than
just interpolating six source samples. It does not resize JPEG blocks
independently or quantize between its separable passes.

The filter maps sample centers using the original dimensions divided by
the IDCT factor, **not** the rounded-up intermediate dimensions. Odd-sized
JPEG padding therefore does not shift the sample grid across the image.

This is not pixel-equivalent to full decoding plus Lanczos3: discarded
frequencies and RGB clipping can change the result. Four-times headroom
is a conservative quality policy, not a guarantee for every possible
frequency pattern or highly saturated image. The visual comparison
includes photos, text/edges, chirps, saturated colors, and odd dimensions,
against full decoding and native reduced decoding.

## CPU and quality options

There are two independent knobs; zero values preserve the original defaults:

```go
thumb, err := resizejpeg.DecodeThumbnail(r, 256, 192, &resizejpeg.ResizeOptions{
    Filter:            resizejpeg.Bilinear,
    ThumbnailHeadroom: 2,
    MaxSourcePixels:   25_000_000,
})
```

`Filter` controls the final separable RGB resampling:

| Value | Kernel | Tradeoff |
| --- | --- | --- |
| `Lanczos3` (zero/default) | Windowed sinc, radius 3 | Detail-preserving; can ring around edges |
| `CatmullRom` | Cubic, radius 2 | Fewer contributions; can also ring |
| `Bilinear` | Triangle, radius 1 | Fewer contributions and no filter ringing; softer |

All three **widen their footprint on downsampling**, rather than sampling a
fixed two-by-two set of source pixels. They use the same original-image
coordinates, edge normalization, floating-point intermediate rows, opaque
output, worker pool, and fallback policy. Narrower support reduces filter
work, but does not change the selected IDCT factor.

`ThumbnailHeadroom` controls intermediate sample dimensions independently:

- `0` means `4`, preserving previous output.
- Any positive integer is accepted. `2` asks for at least twice the target
  dimensions when possible; `1` allows decoding closest to the target.
- Larger values retain more detail before RGB clipping/filtering, at more
  work. When the source cannot supply the requested headroom, preserve its
  original dimension instead; never upsample the source to manufacture it.
- Negative values and unknown filters are rejected before reading input.
- `DecodeResize` honors `Filter`, but ignores positive headroom settings
  and always performs full IDCT.

For a 4000x3000 source and 256x192 target, default headroom 4 chooses factor
2 (2000x1500 samples). Headroom 2 chooses factor 4 (1000x750): **four times
fewer intermediate pixels**, not four times fewer final pixels. With odd
dimensions this is approximate. Small sources, a constraining axis, and
the maximum factor of 8 can prevent the reduction.

Use Lanczos3 with headroom 2 to change reconstruction resolution alone,
CatmullRom or Bilinear with headroom 4 to change filtering alone, or
Bilinear with headroom 2 to combine both savings. These are quality/work
choices, not guaranteed speed ratios: all source entropy still must be
decoded, and JPEG content, color conversion, worker count, and CPU matter.
Total memory also need not shrink by four: coefficient bands still depend
on the original source width, not the reduced pixel count.
The explicit low-headroom modes can lose detail or alias; Bilinear's softer
final filter cannot undo information already discarded during IDCT.

## Parallelism and memory

For a complete single sequential scan:

1. The calling goroutine entropy-decodes source MCU rows and resolves DC
   prediction.
2. Workers dequantize only retained frequencies, perform reduced IDCT,
   convert reduced samples to clipped RGB, and horizontally filter.
3. The collector consumes reduced rows in order and performs the vertical
   filter into a small ring of active destination rows.

The stages overlap. Multiple bands can undergo IDCT and horizontal
filtering concurrently. Entropy decoding remains serial, including when
restart markers are present; vertical accumulation is also ordered and
serial. More workers do not imply linear core scaling, especially after
reduced decoding makes entropy processing the bottleneck.

Nil options or `Workers: 0` select `min(GOMAXPROCS, 8)`, capped by source
MCU-row count. Worker count is not a hard per-call core cap: the producer
and collector are additional goroutines sharing Go's execution budget.
Explicit worker counts must be 1 through 64.

Single-scan scratch depends on source width, target dimensions, and worker
count, not source height. Workers own one **reduced** MCU-row band's planes
and RGBA pixels, and queues/reordering are bounded. Neither a whole
full-resolution nor a whole reduced intermediate image is allocated.
All workers are joined before returning, including on input errors.

Progressive and separate-component sequential scans require explicit
`AllowFullDecode: true`; otherwise they return `UnsupportedError`.
Despite the historical option name, thumbnails keep the selected factor:

- Progressive input retains compact image-wide frequency coefficients and
  nonzero flags, then reconstructs reduced bands through the same parallel
  IDCT/filter pool after all scans. It does not allocate image-wide pixel
  planes. Coefficient storage still scales with source block count.
- Separate sequential scans use image-wide reduced pixel planes, followed
  by a serial filtering fallback.

Neither opt-in case has the single-scan total-memory guarantee. Factor 1
can require full coefficient/pixel storage in these cases. Existing
`DecodeResize` fallback behavior is unchanged.

`MaxSourcePixels` checks the original header before allocating image or
coefficient buffers. Zero is unlimited. Allocation arithmetic is checked
for overflow, including on 32-bit targets; this is not a byte-memory,
elapsed-time, compressed-input, or concurrency budget. Set caller-side
limits for untrusted images and concurrent requests.

## Measurements

Linux/amd64, two-vCPU AMD EPYC 9554P VM, Go 1.27.2, cgo disabled.
Fresh five-run medians with 300 ms samples, fixed `GOMAXPROCS=2` and
default two workers. These are pixels-only timings, without JPEG encoding.
All three paths include config parsing and choose identical output
dimensions from the same original input bytes:

| Input → output | Old native fastjpeg | Full Go IDCT + Lanczos3 | New four-times-headroom mode |
| --- | ---: | ---: | ---: |
| Canyon 3264x2448 → 256x192 | 41.39 ms | 189.50 ms | 94.71 ms |
| Bison 2700x1761 → 256x166 | 23.40 ms | 130.25 ms | 59.20 ms |
| Striped 4000x4000 → 256x256 | 34.28 ms | 300.94 ms | 114.20 ms |

The new mode uses factor 2 for these outputs and is **2.0–2.6x faster**
than full-IDCT streaming Lanczos3, but still **2.3–3.3x slower** than the
native reduced-IDCT/production-resizer path. That older path does less
work and has a different filtering contract. Go allocated bytes per
thumbnail were 1.65 MiB, 1.28 MiB, and 1.91 MiB for the new mode; native
child allocations are not measured by `-benchmem`.

At a 128-pixel long edge, the new mode selects factor 4:

| Input | Old native fastjpeg | Full Go IDCT + Lanczos3 | New four-times-headroom mode |
| --- | ---: | ---: | ---: |
| Canyon | 42.08 ms | 172.44 ms | 85.20 ms |
| Bison | 24.31 ms | 114.88 ms | 43.75 ms |
| Striped | 33.04 ms | 296.18 ms | 60.36 ms |

At 512 pixels, these inputs select factor 1, so there is no reduced-IDCT
work saving. Independent short-run medians varied by a few percent
between the otherwise equivalent full-transform paths.

### Worker/core scaling

A separate deterministic textured 4000x3000 quality-95 4:2:0 input
(**not** the striped fixture above) produced a 256x192 thumbnail with
factor 2. Medians of three 300 ms runs, with each CPU limit set in a
separate process before initial calibration:

| Workers | One Go CPU | Two Go CPUs | Go allocated/op |
| --- | ---: | ---: | ---: |
| 1 | 215.32 ms | 161.82 ms | 1.31 MiB |
| 2 | 202.70 ms | 129.66 ms | 1.83 MiB |
| 4 | 204.43 ms | 132.40 ms | 2.87 MiB |

Two workers gained about **1.56x from one to two CPUs**. Four workers
did not help on this two-vCPU VM. On two CPUs the serial reduced decode
plus separate full-height Lanczos3 reference took 249.94 ms and allocated
28.01 MiB; the new pipeline took 129.66 ms and 1.83 MiB. That reference
has the same chosen factor and aligned sampling grid, unlike the native
factor-8 production path.

These are per-thumbnail latency/allocation measurements from a small
workload, not concurrent-request throughput, peak RSS, CPU-time budgets,
or a promise about larger machines. Photo targets in this production-path
benchmark floor the short edge; the visual report rounds it, so its bison
256-pixel thumbnail is 256x167 instead of 256x166.

For measured peak process memory on giant JPEGs, see the separate
[peak-memory experiment](PEAKMEMORY.md).

### Filter/headroom comparisons

Fresh five-run medians, 300 ms samples, two Go CPUs, automatic workers,
cgo disabled. Every path decodes the same original JPEG bytes into the same
256-pixel-wide output. These measure pixels-only **latency**, not total
CPU time or peak memory:

| Input | Default: Lanczos3 / 4x | Lanczos3 / 2x | CatmullRom / 4x | Bilinear / 4x | Bilinear / 2x |
| --- | ---: | ---: | ---: | ---: | ---: |
| Generated 4000x3000 → 256x192 | 153.62 ms | 134.11 ms | 138.91 ms | 147.79 ms | 135.70 ms |
| Canyon 3264x2448 → 256x192 | 94.99 ms | 85.30 ms | 88.67 ms | 85.62 ms | 81.56 ms |
| Bison 2700x1761 → 256x166 | 56.67 ms | 40.56 ms | 48.82 ms | 50.70 ms | 44.60 ms |

Headroom 4 selects IDCT factor 2; headroom 2 selects factor 4 for all three.
The two-photo Bilinear/2x latency reduction was about 14–21%, **not 4x**:
entropy decoding still reads the entire source, and the bounded producer/
worker/collector scheduling affects overlap. Narrower kernels and lower
headroom need not produce a strictly monotonic latency ordering.
Allocated bytes for Canyon fell from 1.60 to 1.32 MiB/op with Bilinear/2x;
for Bison, from 1.26 to 1.08 MiB/op. These are not peak-RSS figures.

For a low-end, single-Go-CPU setting, fresh three-run medians with automatic
one-worker selection were:

| Input | Default: Lanczos3 / 4x | Lanczos3 / 2x | Bilinear / 4x | Bilinear / 2x |
| --- | ---: | ---: | ---: | ---: |
| Canyon | 134.85 ms | 86.53 ms | 104.84 ms | 91.98 ms |
| Bison | 75.21 ms | 46.42 ms | 62.17 ms | 43.62 ms |

Each CPU setting was fixed in the environment and `-cpu` before the first
calibration, in a separate process. These are short VM measurements, not
confidence intervals or a universal ranking of filters. The
[visual comparison](QUALITY.md#configurable-filter-and-headroom) shows the
corresponding detail/softness tradeoffs.

## Reproduce

```sh
CGO_ENABLED=0 go test ./internal/images/resizejpeg -count=1
go test -race ./internal/images/resizejpeg -count=1
GOMAXPROCS=2 CGO_ENABLED=0 go test ./internal/images/resizejpeg \
  -run '^$' -fuzz '^FuzzDecodeThumbnail$' \
  -fuzztime=30s -fuzzminimizetime=1s -parallel=2

GOMAXPROCS=2 CGO_ENABLED=0 go test ./internal/images/resizejpeg \
  -run '^$' -bench '^BenchmarkDecodeThumbnail$' \
  -benchmem -cpu=2 -benchtime=300ms -count=5

GOMAXPROCS=2 CGO_ENABLED=0 go test ./internal/images/resizejpeg \
  -run '^$' -bench '^BenchmarkDecodeThumbnailOptions$' \
  -benchmem -cpu=2 -benchtime=300ms -count=5
```

Set `RESIZEJPEG_OPTIONS_JPEG=/path/to/original.jpg` for the options matrix
to use an unchanged source file instead of the generated texture. The
benchmark covers all three filters at headroom 1, 2, and 4 and reports the
actual chosen factor and Go CPU limit.

The [native comparison benchmark](LEGACY.md) additionally includes
`thumbnail-core` and `thumbnail-wrapper`. Those tagged controls require
`djpeg` only to measure the legacy path; `DecodeThumbnail`, its ordinary
unit tests, and its own benchmarks do not.

## Validation

The cgo-disabled image-package tests, tagged quality/benchmark helper tests,
full package race tests, and tagged vet passed. The complete package suite
passed on Linux/386 under `qemu-i386`; focused 386 tests also cover the new
scaled filter and integer-limit factor selection. Linux/arm64,
macOS/arm64, and Windows/amd64 test binaries cross-compiled but were not
executed. A 30-second thumbnail fuzz run completed 67,604 executions
without failure. The production package's dependency graph contains only
the standard library.

The expanded visual report passed separately with both unchanged photos:
258 candidate comparisons, 60 baseline/progressive parity checks, and
552 correctly sized PNGs. The browser loaded all 558 PNG/JPEG assets;
candidate selection, blink, and zoom controls were exercised.
Side-by-side inspection found no obvious
additional softness with **default options** on the two photos; their
128-/256-pixel outputs differ by at most two or three channel levels from
full decoding plus Lanczos3.
Artificial saturated colors still differ; see the [quality findings](QUALITY.md).

Filter/headroom options passed independent full-grid and fractional-coordinate
references, default-Lanczos golden hashes, and worker/path/resource-policy
tests, including arbitrary positive and maxInt headroom. Cgo-disabled
image-package tests, full tagged race tests, and vet passed. All new filter
and options tests passed on Linux/386 under `qemu-i386`; Linux/arm64,
macOS/arm64, and Windows/amd64 binaries cross-compiled but were not executed.
A 30-second `FuzzResizeOptions` run completed 31,624 executions without
failure. All 408 PNGs from the previous report remained byte-identical.

The combined image suite retains ten unrelated skips (disabled YCbCr
resizer comparison, eight HEIC-specific checks on JPEGs, and missing
`libheif.so`). Opt-in report/memory tests skip without input settings; the
complete visual report was run separately as described above, and the
giant-image memory probe is recorded in its separate report. The full repository
suite was not run. No thumbnail API is wired into production yet.
