# JPEG decoding and resizing

`resizejpeg` is a decoder-only fork of Go 1.27.2's `image/jpeg`, with
three decoding/resizing paths:

- `DecodeThumbnail(r, width, height, opts)`: choose reduced IDCT with
  configurable thumbnail headroom, then finish with the selected filter
  (four-times headroom and Lanczos3 by default). The reduced
  transforms and horizontal filtering share the parallel, bounded worker
  pipeline. See [thumbnail decoding](THUMBNAIL.md).
- `DecodeResize(r, width, height, opts)`: full IDCT, clipped RGB, then
  scale-aware filtering (Lanczos3 by default). Single-scan inputs use
  bounded row-band buffers
  and parallel workers, without a full-resolution source image. Progressive
  and non-interleaved inputs require an explicit full-buffer fallback.
  See [streaming resize](STREAMING.md) for the API, memory policy,
  parallelism, reference comparisons, and benchmarks.
- `Decode(r, factor)`: faster, lower-memory reduced IDCT, described below.
  Its filtering is not equivalent to full decode plus Lanczos3.

None of these paths is wired into Perkeep's thumbnail pipeline.

`ResizeOptions.Filter` selects `Lanczos3`, `CatmullRom`, or `Bilinear`.
`ResizeOptions.ThumbnailHeadroom` controls reduced decoding independently:
zero means 4; using 2 can quarter the intermediate pixel count, where
the power-of-two IDCT factors permit it. See the
[CPU/quality options](THUMBNAIL.md#cpu-and-quality-options).

For performance against the actual native `djpeg` thumbnail pipeline, see
the [legacy comparison](LEGACY.md). The streaming speedups versus a full
Go decode plus Lanczos3 do not imply a speedup over native reduced decoding.

## Reduced IDCT

`Decode` decodes directly at full, half, quarter, or eighth resolution in pure Go,
without cgo, native libraries, subprocesses, seeking, or buffering the
compressed input in full.

```go
small, err := resizejpeg.Decode(r, 8) // io.Reader; decode at 1/8 resolution
```

`Decode` takes a factor of 1, 2, 4, or 8 and returns an `image.Image`.
Dimensions are rounded up independently: a 17x9 JPEG at factor 2 is 9x5.
Factor 1 retains the standard decoder's full-resolution behavior.
`DecodeConfig` reports the original dimensions and color model.
The package does not register an `image` format or change `image/jpeg`.

## How it works

All entropy-coded coefficients still have to be read to find subsequent
blocks and scans. The decoder dequantizes only the low-frequency square
needed by the output scale, then applies an 8x8, 4x4, 2x2, or 1x1 inverse
DCT. Reduced samples are written straight into reduced, MCU-padded image
planes. RGB/CMYK conversion, when needed, also uses only reduced images.

The 4x4 and 2x2 transforms evaluate retained cosine modes on the smaller
grid, with the JPEG normalization preserved (DC/8 at every scale).
This is a reduced-frequency IDCT, **not** nearest-neighbor sampling or
decoding a full-size image and box-averaging its pixels. In particular,
discarded frequencies, full-resolution clipping, and rounding make those
operations differ. The original 8x8 IDCT is retained for factor 1.
Reduced kernels use integer arithmetic and no per-block heap allocations.

### Progressive JPEGs

Keeping the standard decoder's progressive coefficient arrays unchanged
would defeat the memory goal: they contain 64 int32 values per source block,
often much larger than the full-size pixel image.

Instead, each component block retains only `(8/factor)^2` coefficients.
At reduced scales it also keeps a 64-bit nonzero bitmap. AC refinement
coding depends on which previously transmitted coefficients are nonzero,
even when those frequencies do not contribute to the final image.
The bitmap lets a single scratch block stand in for discarded values while
consuming the refinement bits correctly. Their signs and magnitudes do
not affect the retained coefficients.

Per source component block, progressive state takes:

| Factor | Retained coefficients | Nonzero flags | Total |
| --- | ---: | ---: | ---: |
| 1 | 256 bytes | 0 | 256 bytes |
| 2 | 64 bytes | 8 bytes | 72 bytes |
| 4 | 16 bytes | 8 bytes | 24 bytes |
| 8 | 4 bytes | 8 bytes | 12 bytes |

Thus no full-resolution pixel or full 64-coefficient-per-block image buffer
is allocated for reduced decoding, including progressive input. The bitmap
still scales with the **source** block count, so progressive working memory
is not exclusively proportional to the output dimensions. Sequential JPEGs
need only reduced image buffers plus decoder/block scratch space.

Allocation arithmetic is checked for integer overflow, including on 32-bit
platforms. This is not an overall resource limit: large images can still
consume substantial CPU and memory. Callers processing untrusted inputs
should enforce their own source-dimension and concurrency budgets.

## Provenance and maintenance

`reader.go`, `scan.go`, `huffman.go`, the inverse-DCT portions of `dct.go`,
`reader_test.go`, and `testdata/*.jpeg` were copied from the Go 1.27.2
distribution. They retain the Go Authors' copyright and BSD license;
see [LICENSE](LICENSE). The encoder and its forward DCT are not included.
The reduced IDCT kernels and compact progressive storage are new code.

Changes to the copied decoder are limited to:

- The explicit scale factor API, with no global format registration.
- Reduced image allocation, dequantization, inverse DCT, and block writes.
- Packed progressive coefficients and nonzero flags.
- Replacing inaccessible `image/internal/imageutil` with `image/draw`.
- Rejecting zero dimensions and allocation-size integer overflow.
- Optional source-pixel budgets and a bounded streaming worker sink.

When updating Go, compare its JPEG reader, scan, Huffman, and IDCT changes,
especially security fixes, against this fork. The same JPEG feature
limitations as the upstream decoder generally apply.

## Validation

```sh
CGO_ENABLED=0 go test ./internal/images/resizejpeg
go test -race ./internal/images/resizejpeg
CGO_ENABLED=0 go test ./internal/images/resizejpeg \
  -run '^$' -fuzz '^FuzzDecode$' -fuzztime=30s -fuzzminimizetime=1s
CGO_ENABLED=0 go test ./internal/images/resizejpeg \
  -run '^$' -bench '^BenchmarkDecode$' -benchmem -benchtime=300ms -count=5
```

Tests include the copied upstream regressions, full-resolution pixel
comparisons with `image/jpeg`, exact baseline/progressive agreement at all
scales, progressive coefficient/bitmap comparisons, grayscale/RGB/CMYK and
nonstandard subsampling, tiny and odd dimensions, short reads, truncated
input, and invalid scale factors. Independent cosine-sum tests check the
reduced integer transforms. Tests also inspect internal buffer sizes,
including progressive state, rather than trusting output bounds alone.
Selectable filters are checked against independent full-grid and
fractional-coordinate references, including anti-aliasing and bounded rings.
Filter/headroom options preserve default pixels and are exercised across
worker counts, scan/fallback policies, invalid inputs, and arbitrary positive
headroom values.

The opt-in [visual quality experiment](QUALITY.md) compares real photographs,
text, edges, and repeating patterns against full decoding plus Lanczos3, and
against the existing `djpeg` decoder. It includes an interactive report
generator. Leaving 2x headroom before final resizing helped the photographs,
but the experiment also found aliasing and a substantial saturated-color
stress-case gap versus `djpeg`. Reduced decoding is not a quality-equivalent
replacement for full decoding plus a high-quality resize.

Validation here passed on Linux/amd64 with cgo disabled, with the race
detector, and on emulated Linux/386 via `go test -exec qemu-i386`.
A 30-second fuzz run completed 110,969 executions without a failure.
Linux/arm64, macOS/arm64, and Windows/amd64 test binaries also cross-compiled
with cgo disabled; they were not executed. The affected image package suite
passed; the full repository suite was not run.

The benchmark includes the existing fastjpeg benchmark's synthetic
4000x4000 striped image and a small progressive image. These are
microbenchmarks, not a representative photo workload or complete thumbnail
requests. Setup encoding is outside the measured loop.

### Initial measurements

Linux/amd64, AMD EPYC 9554P VM with two vCPUs, Go 1.27.2,
`CGO_ENABLED=0`, median of five runs with `-benchtime=300ms`:

| 4000x4000 sequential input | Decode time | Allocated/op |
| --- | ---: | ---: |
| `image/jpeg` | 151.27 ms | 22.90 MiB |
| factor 1 | 150.83 ms | 22.90 MiB |
| factor 2 | 76.45 ms | 5.74 MiB |
| factor 4 | 51.68 ms | 1.45 MiB |
| factor 8 | 48.75 ms | 0.373 MiB |

At factor 8 this is about 3.1x faster decoding and 61x fewer allocated bytes
than the full-resolution standard decoder. Entropy decoding still costs
roughly the same at every scale, so CPU time does not fall with pixel count.

The small progressive fixture at factor 8 took 0.935 ms and allocated
0.0228 MiB/op, versus 1.099 ms and 0.2477 MiB/op for `image/jpeg`.
Progressive scaling primarily improves memory use; retaining nonzero flags
and processing all scans limits speed gains. At factor 2 this small fixture
was slightly slower than the standard decoder.

These timings do not include a subsequent arbitrary-size resize or JPEG
encoding. Allocated bytes are cumulative Go allocations per decode, not
peak resident memory. Other image content and dimensions will differ.
