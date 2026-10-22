# Comparison with the legacy fastjpeg thumbnail pipeline

The [streaming measurements](STREAMING.md) compare `DecodeResize` with
full-resolution **Go `image/jpeg` plus Lanczos3**. They do not compare it
with the production `fastjpeg` thumbnail path.

The latter does much less work: native `djpeg` selects a reduced IDCT before
returning pixels, then Go resizes the already-small image. Streaming full
IDCT and Lanczos3 reconstructs and filters every full-resolution RGB row.
These are different quality/performance choices, not interchangeable
implementations of the same filtering operation.

## What is measured

The opt-in `jpegcompare` benchmark in `../benchresizejpeg_test.go` uses
the same JPEG bytes, output dimensions, and encoding settings for all paths:

- **legacy-production:** the real `images.Decode`, including its config
  parsing, compressed-input fallback/replay buffer, `fastjpeg.Factor`,
  `djpeg` subprocess and PNM/RGBA handling, and production `rescale`.
- **streaming-wrapper:** parse config to select the same output dimensions,
  replay only that prefix, and call `DecodeResize` with default workers.
- **streaming-core:** call `DecodeResize` with dimensions already known.
- **thumbnail-wrapper / thumbnail-core:** the newer pure-Go
  `DecodeThumbnail` equivalents, with and without outer config parsing.
  They use reduced IDCT with four-times thumbnail headroom before Lanczos3.
  Their separate measurements are in [thumbnail decoding](THUMBNAIL.md).
- **legacy-parts:** `fastjpeg.DecodeDownsample` followed by production
  `rescale`, without the outer config/fallback buffering.
- **native-scaled-lanczos3:** the same native reduced decode factor, followed
  by scale-aware Lanczos3 rather than production `rescale`.
- **native-full-lanczos3:** native full-resolution decode followed by the
  same Lanczos3 filter. This is a full-decode filtering control, not the
  old production fast path. Native IDCT/chroma conversion can still differ
  from Go's, so pixel equivalence is not claimed.

For the color inputs here, production `rescale` uses the RGBA area resizer
at 256 and 512 pixels. At 128 pixels it instead uses the existing
nearest-neighbor `ResampleInplace` to twice the target dimensions, followed
by `HalveInplace`. The benchmark calls this code rather than approximating
it with a single generic resize.

Every path has two endpoints:

- **pixels:** finish with the thumbnail image.
- **jpeg-encoded:** additionally call `jpeg.Encode(io.Discard, im, nil)`,
  using Go's default JPEG quality.

Input bytes are in memory. Disk reads, fixture generation/encoding, header
validation, and preflight checks are outside measured loops. HTTP handling,
storage reads, thumbnail caches, output buffering/network writes, and
concurrent-request throughput are not measured. EXIF orientation is
explicitly disabled on both sides; no rotation or flip is performed.

Output dimensions use production `ScaledDimensions`, which floors the
short edge. In particular, the bison's 256-pixel thumbnail is **256x166**,
not the 256x167 used in the earlier rounded-dimension streaming benchmark.
These measurements are new runs, not ratios of unrelated earlier runs.

Preflight checks require one successful `djpeg` invocation and no fallback
for the production path. Streaming uses nil options, so progressive or
non-interleaved inputs fail rather than silently switching to full-buffer
fallback. Tests require exact agreement between production/parts outputs
and between streaming/core-wrapper outputs, not between quality families.

`-benchmem` reports **Go allocations only**. It excludes the native child
process's allocations and is not a measurement of peak RSS or total
process-tree memory. `GOMAXPROCS` constrains Go execution, not the child
process; these measurements use a two-vCPU VM and the installed `djpeg`.

## Results

Linux/amd64, AMD EPYC 9554P VM with two vCPUs, Go 1.27.2, cgo disabled,
`GOMAXPROCS=2`, and libjpeg-turbo `djpeg` 2.1.5 (build 20240408).
Values are medians of five 300 ms runs. The streaming calls use two
workers by default. Both photos are the unchanged originals pinned in
the [photo manifest](testdata/quality-photos.json); the striped input is
the existing deterministic 4000x4000 benchmark fixture, not a photo.

### 256-pixel thumbnails

All three legacy paths select native decode factor 8. The comparison below
uses **legacy-production** and **streaming-wrapper**, so both include
dimension/config handling rather than comparing unequal API boundaries.

| Input → output | Old pixels | Streaming pixels | Old + JPEG encode | Streaming + JPEG encode |
| --- | ---: | ---: | ---: | ---: |
| Canyon 3264x2448 → 256x192 | 41.35 ms | 184.98 ms | 43.70 ms | 172.52 ms |
| Bison 2700x1761 → 256x166 | 23.31 ms | 111.63 ms | 24.34 ms | 115.20 ms |
| Striped 4000x4000 → 256x256 | 35.33 ms | 313.22 ms | 34.48 ms | 326.56 ms |

The streaming full-IDCT/Lanczos3 path is **4.5–4.8x slower on these two
photos for pixels alone**, or **3.9–4.7x slower including JPEG encoding**.
It is about **8.9–9.5x slower** on the striped synthetic input.

The endpoints are independent benchmarks, not additive stage timings.
Short VM runs have scheduling/cache variability; an encoder-inclusive
median below its pixels-only counterpart does not imply negative encoding
cost. These measurements are not a confidence interval or a broad workload
survey.

At the JPEG-encoded endpoint, Go allocations were:

| Input | Old production | Streaming wrapper |
| --- | ---: | ---: |
| Canyon | 14.76 MiB/op | 2.28 MiB/op |
| Bison | 7.12 MiB/op | 1.65 MiB/op |
| Striped | 14.23 MiB/op | 2.66 MiB/op |

The streaming wrapper avoids retaining the entire compressed JPEG for
fallback, as well as native PNM output buffers. The native child's memory
is **not included**, and these figures must not be interpreted as peak
memory measurements.

### Other thumbnail sizes

Supplementary pixels-only runs use the same wrappers and five-run medians:

| Input | Long edge | Old production | Streaming wrapper | Streaming / old |
| --- | ---: | ---: | ---: | ---: |
| Canyon | 128 | 43.53 ms | 182.25 ms | 4.2x |
| Canyon | 512 | 54.80 ms | 178.18 ms | 3.3x |
| Bison | 128 | 22.62 ms | 109.68 ms | 4.8x |
| Bison | 512 | 33.80 ms | 117.96 ms | 3.5x |
| Striped | 128 | 30.60 ms | 281.13 ms | 9.2x |
| Striped | 512 | 55.15 ms | 304.24 ms | 5.5x |

Native decode uses factor 8 at 128 pixels and factor 4 at 512 pixels.
Streaming still processes all full-resolution rows at either output size.

### Separating reduced decoding from filter choice

These are pixels-only timings with the same output dimensions:

| Input | Native factor 8 + Lanczos3 | Native full decode + Lanczos3 | Streaming wrapper |
| --- | ---: | ---: | ---: |
| Canyon | 43.62 ms | 304.01 ms | 184.98 ms |
| Bison | 23.43 ms | 182.12 ms | 111.63 ms |
| Striped | 39.93 ms | 530.65 ms | 313.22 ms |

Changing the final filter to Lanczos3 on an already-reduced native image
does not account for the large slowdown. Reconstructing and filtering the
**full-resolution** samples is the expensive difference.

If full decoding plus Lanczos3 is required, the streaming implementation
does beat this native-full-decode/separate-Lanczos3 control by about
1.6–1.7x, using the same two-vCPU VM. Its bounded bands, overlapped stages,
parallel filtering, and lack of full-size intermediates are useful there.
That does **not** make it faster than the native reduced-IDCT fast path.

## Reproduce

From the repository root, with `djpeg` available and
`CAMLI_DISABLE_DJPEG` unset:

```sh
CGO_ENABLED=0 go test -tags=jpegcompare ./internal/images \
  -run '^TestJPEGThumbnailComparisonPaths$' -count=1 -v

# Existing deterministic 4000x4000 striped fixture.
GOMAXPROCS=2 CGO_ENABLED=0 go test -tags=jpegcompare ./internal/images \
  -run '^$' -bench '^BenchmarkJPEGThumbnailComparison$' \
  -benchmem -cpu=2 -benchtime=300ms -count=5

# Original, unchanged photograph. See QUALITY.md for pinned photo downloads.
RESIZEJPEG_COMPARE_JPEG=/path/to/original.jpg \
GOMAXPROCS=2 CGO_ENABLED=0 go test -tags=jpegcompare ./internal/images \
  -run '^$' -bench '^BenchmarkJPEGThumbnailComparison$' \
  -benchmem -cpu=2 -benchtime=300ms -count=5
```

The full benchmark now covers 128-, 256-, and 512-pixel long edges and all eight
paths. To select one size, append `/./^edge-256$` to the benchmark regex.
Keep GOMAXPROCS fixed in a separate process, including initial calibration;
every leaf also reports its actual value.

If native decoding is unavailable the experiment explicitly skips. A skip
does not establish native coverage. No production behavior is changed.

## Validation

The image-package tests passed with cgo disabled and `jpegcompare` enabled;
the new path/parity test also passed with the race detector. Vet passed
with both `jpegcompare` and `jpegquality` enabled. All 48 measured cases
completed five samples with actual GOMAXPROCS 2; native preflight checks
passed rather than skipping.

The image suite still has ten unrelated skips: the disabled YCbCr resize
comparison, eight HEIC-specific config checks on JPEG inputs, and the
real HEIC case requiring missing `libheif.so`. The full repository suite
was not run.
