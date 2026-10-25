# Streaming full-decode resize

`DecodeResize` is the quality-first alternative to reduced IDCT. It fully
reconstructs and clips the original 8-bit JPEG samples, converts them to
opaque RGB, and applies the selected scale-aware filter (Lanczos3 by
default). Single-scan inputs do not
allocate a full-resolution source image or image-wide coefficient array.
The implementation is pure Go and does not use external processes or
native libraries.

```go
thumb, err := resizejpeg.DecodeResize(r, 256, 192, &resizejpeg.ResizeOptions{
    Workers:         2,
    MaxSourcePixels: 25_000_000,
})
```

Width and height are exact, independently specified dimensions. Both must
be positive and no larger than the source dimensions. Callers choose the
aspect ratio, orientation policy, and resource limits. The result is
`*image.RGBA`, with alpha 255 throughout.

Filtering uses encoded RGB values, not linear light. ICC profiles and EXIF
orientation are not applied. This matches the quality experiment's
reference with default options, not every possible JPEG viewer or
color-managed resizer.

`ResizeOptions.Filter` selects `Lanczos3`, `CatmullRom`, or `Bilinear`.
All widen their support during downsampling and preserve the same pixel
coordinates, edge normalization, and bounded worker pipeline. Bilinear is
softer and uses fewer source samples; CatmullRom is a narrower cubic
alternative. See [CPU/quality options](THUMBNAIL.md#cpu-and-quality-options).
`ThumbnailHeadroom` only affects `DecodeThumbnail`: `DecodeResize` always
performs full IDCT, regardless of a positive headroom setting.

## Pipeline and parallelism

1. The calling goroutine parses markers and entropy-decodes MCU-row bands,
   resolving DC prediction before handing coefficients to workers.
2. Workers dequantize, perform full 8x8 IDCT, reconstruct one source
   MCU-row band, do the normal clipped RGB conversion, and horizontally
   filter to destination-width floating-point rows.
3. An ordered collector performs the vertical filter. Incoming source rows
   contribute to a circular buffer of active **destination** rows, so a
   very large shrink does not require retaining its entire vertical source
   support.

The filter uses image-wide sampling coordinates and renormalized edge
weights. It widens its footprint during reduction. It does not resize
individual JPEG blocks independently, clamp between separable passes, or
change sample phase at MCU boundaries.

The coefficient-band pool, job queue, and reorder window are bounded.
A completed band is not recycled until the collector has consumed it in
order; a slow early job cannot cause unlimited later jobs to accumulate.
Every error path joins the workers and collector before returning.

`Workers: 0` or nil options select `min(GOMAXPROCS, 8)` workers. Explicit
counts must be 1 through 64; actual workers and buffers are further capped
by source MCU-row count. More workers increase scratch memory. Even one
worker overlaps with the entropy producer when multiple CPU cores are
available: worker count is **not** a core-count measurement.

Entropy decoding remains serial, including for JPEGs with restart markers.
Restart markers are supported, but independent segment entropy decoding
is not implemented. The serial entropy stage and vertical collector limit
scaling; no linear speedup with additional cores is promised.

## Input and memory policy

The bounded path supports grayscale and complete, interleaved sequential
scans, including RGB, YCbCr, CMYK/YCCK, unusual supported sampling ratios,
and restart markers.

By default, progressive and sequential non-interleaved inputs return
`UnsupportedError` before source planes or coefficient arrays are
allocated. Set `AllowFullDecode: true` to permit the existing full decoder
as a fallback, followed by the same selected filter. The fallback is serial
and **does allocate full-resolution pixels and, for progressive inputs,
full coefficient arrays**. Its memory use can greatly exceed the
streaming path's use.

There is no hidden compressed-input replay buffer. Consequently, an
additional scan after an already streamed complete scan, or color metadata
changing after that scan, returns `UnsupportedError` even when fallback is
allowed. Inputs known to require fallback at their first scan can use it
without seeking or rereading.

Scratch allocations are bounded by source width, destination dimensions,
and worker count, not source height. In particular:

- Each coefficient slot holds only one MCU-row band.
- Each worker owns only one MCU-row band's source planes and RGB pixels.
- Horizontally filtered rows are destination-width, with a bounded number
  of bands.
- Vertical accumulation needs at most seven destination rows for shrinking
  (five for CatmullRom, three for Bilinear).
- Filter tables are proportional to source width and destination width/
  height; there is no source-height by destination-width temporary image.
- The final destination image is allocated in full.

`MaxSourcePixels` checks the original dimensions at the SOF marker, before
source or resize image buffers are allocated. Zero leaves this unlimited.
Arithmetic overflow is checked separately. Neither check is a strict
byte-memory, compressed-input, elapsed-time, or concurrency budget.
Applications handling untrusted input should choose deployment-appropriate
limits, constrain compressed input and concurrent requests, and be
especially cautious before enabling full-buffer fallback.

`MaxMemoryBytes` additionally caps the planned decode reservation.
`MemAllocSemaphore` admits that reservation to a caller-supplied shared
budget, with one cancelable acquisition before the large allocations and
release after worker shutdown. See [memory admission](MEMORY.md) for an
example and the distinction between active decoding and retained output.

## Validation

Tests compare against an independent reference: standard `image/jpeg`,
conversion of the entire source to clipped 8-bit RGBA, then the pinned
`golang.org/x/image/draw.Kernel` Lanczos3 scaler. The tolerance is at most
one 8-bit channel level; worker counts must produce exactly identical
pixels.

Coverage includes all 30 inherited JPEG fixtures, odd and tiny dimensions,
nonuniform target ratios, dense saturated 4:2:0 patterns, progressive and
separate-scan fallback, generated CMYK/YCCK sampling variants, restart
markers, short reads, malformed input, late metadata/scans, out-of-order
completion, and worker-error draining. Tests inspect owned buffers and
joined workers directly, including source-height-independent scratch
capacity. A fuzz target caps source dimensions before allocation.

The [visual experiment](QUALITY.md) also compares this path on the same
two original photographs and four text/frequency/color stress images as
the earlier reduced-IDCT experiment. It remains separate from the
production thumbnail pipeline.

The affected image package suite passed with cgo disabled, as did tagged
race tests and `go vet`. A 30-second `FuzzDecodeResize` run completed 86,528
executions without a failure. The complete package suite also passed on
Linux/386 under `qemu-i386`; native 386 execution is unavailable on this VM.
Linux/arm64, macOS/arm64, and Windows/amd64 test binaries cross-compiled
with cgo disabled but were not executed.

The image suite retains ten unrelated skips: a disabled YCbCr resizer
comparison, eight HEIC-specific checks inapplicable to JPEG inputs, and a
real HEIC case without `libheif.so`. These are not counted as coverage.
The full repository suite was not run.

```sh
CGO_ENABLED=0 go test ./internal/images/... -count=1
go test -race -tags=jpegquality ./internal/images/resizejpeg \
  -run '^(Test|Fuzz)' -count=1
GOMAXPROCS=2 CGO_ENABLED=0 go test ./internal/images/resizejpeg \
  -run '^$' -fuzz '^FuzzDecodeResize$' \
  -fuzztime=30s -fuzzminimizetime=1s -parallel=2
CGO_ENABLED=0 GOARCH=386 go test -exec qemu-i386 \
  ./internal/images/resizejpeg -count=1
CGO_ENABLED=0 go vet -tags=jpegquality ./internal/images/...
```

## Initial measurements

Linux/amd64, AMD EPYC 9554P VM with two vCPUs, Go 1.27.2, cgo disabled.
These are medians of three 300 ms benchmark runs, with one or two logical
processors explicitly selected in separate processes. All actual reported
GOMAXPROCS values were checked against the requested limits.

The generated input is a deterministic, textured 4000x3000 quality-95
4:2:0 JPEG, **not** the older striped 4000x4000 microbenchmark. The two
unchanged photographs are pinned in the [photo manifest](testdata/quality-photos.json).
All three outputs preserve the source aspect ratio.

| Input and output | Full reference, 2 CPUs | Streaming, 2 workers, 1 CPU | Streaming, 2 workers, 2 CPUs | Allocated/op, full → streaming |
| --- | ---: | ---: | ---: | ---: |
| Generated 4000x3000 → 256x192 | 559.45 ms | 427.35 ms | 274.15 ms | 87.31 → 2.57 MiB |
| Canyon 3264x2448 → 256x192 | 393.16 ms | 265.36 ms | 163.25 ms | 61.79 → 2.23 MiB |
| Bison 2700x1761 → 256x167 | 244.29 ms | 173.34 ms | 122.06 ms | 46.18 → 1.61 MiB |

At the same two-CPU limit, this is about **2.0–2.4x faster and 28–34x fewer
allocated bytes** than the full reference. Keeping two workers fixed and
changing the core limit from one to two gives about **1.4–1.6x speedup**.
These short VM measurements are workload-dependent, not confidence
intervals or claims about machines with more cores.

Those gains are **not against production fastjpeg**. The separate
[legacy comparison](LEGACY.md) finds the native reduced-IDCT thumbnail
path substantially faster than streaming full-IDCT/Lanczos3, at a
different quality contract.

Extra workers trade memory for scheduling opportunities, not guaranteed
speed. For the canyon with two CPUs, one worker took 238.89 ms / 1.53 MiB;
two took 163.25 ms / 2.23 MiB; four took 179.20 ms / 3.63 MiB.
A process-wide CPU profile of the generated-input run identified horizontal
filtering as the largest CPU consumer; entropy decoding was also
substantial, while vertical filtering was small.

The reduced-factor-8 path remains faster: at two CPUs it took 128.42 ms,
81.47 ms, and 42.03 ms on these three inputs, respectively. It has a
different quality contract and is not a substitute when full-decode
filtering is required.

## Reproduce performance measurements

```sh
for cpu in 1 2; do
  GOMAXPROCS=$cpu CGO_ENABLED=0 \
    go test ./internal/images/resizejpeg -run '^$' \
    -bench '^BenchmarkDecodeResize$' -benchmem -cpu=$cpu \
    -benchtime=300ms -count=3

  RESIZEJPEG_BENCH_JPEG=/path/to/original-photo.jpg \
    GOMAXPROCS=$cpu CGO_ENABLED=0 \
    go test ./internal/images/resizejpeg -run '^$' \
    -bench '^BenchmarkDecodeResizeRealJPEG$' -benchmem -cpu=$cpu \
    -benchtime=300ms -count=3
done
```

Input generation, encoding, and file loading are outside measured loops.
Use separate fixed-GOMAXPROCS processes so even the initial calibration
run has the intended core limit. Each leaf reports its actual GOMAXPROCS.
The full-reference benchmark includes the full source RGB conversion and
the reference scaler's full-height temporary image. The reduced-factor-8
comparison has different quality and is not an interchangeable result.
Reported bytes/op are allocated bytes, not a measured peak RSS.
