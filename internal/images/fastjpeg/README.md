# In-process JPEG downsampling experiment

## Conclusion

Yes: `github.com/ebitengine/purego` can call the system's **libturbojpeg**
without cgo or a subprocess, retaining JPEG's fast 1/2, 1/4, and 1/8
scaled decode. `purego_test.go` contains a working, deliberately test-only
prototype and comparative benchmarks. Production `DecodeDownsample` is
unchanged.

Use the TurboJPEG API, not the lower-level libjpeg API:

- `tjInitDecompress` returns an opaque per-decode handle.
- `tjDecompressHeader3` supplies dimensions and color space.
- `tjDecompress2` selects scaled IDCT based on destination dimensions,
  writing directly into a Go `image.RGBA` or `image.Gray`.
- `tjDestroy` releases the handle.

This removes process startup, pipe I/O, PNM header parsing, and the RGB to
RGBA expansion loop. It is not a pure-Go JPEG implementation: an installed
native shared library is still required. libjpeg-turbo's `libjpeg` and
`libturbojpeg` are separate libraries; having `djpeg` or `libjpeg.so` alone
does not guarantee that the TurboJPEG API is installed.

## Reproduce

On Debian/Ubuntu, install the runtime package that provides
`libturbojpeg.so.0` (`libturbojpeg` on this VM), plus
`libjpeg-turbo-progs` for the subprocess comparison. On macOS, install
`jpeg-turbo` from Homebrew.

From the repository root:

```sh
CGO_ENABLED=0 go test -v ./internal/images/fastjpeg
CGO_ENABLED=0 go test ./internal/images/fastjpeg \
  -run '^$' -bench '^BenchmarkJPEG$' -benchmem -benchtime=300ms -count=5
CGO_ENABLED=1 go test -race ./internal/images/fastjpeg
```

The prototype is enabled for Linux and macOS amd64/arm64. Tests skip if the
shared library cannot be loaded; a skip is not successful native coverage.
The Go JPEG corpus under `GOROOT/src/image/testdata` is also used to test
progressive, truncated, grayscale, RGB, and unusual subsampling inputs,
including concurrent calls with separate handles.

Benchmark inputs are deterministic 256x256 and 4000x4000 striped JPEGs.
The larger input matches the pattern in `../benchfastjpeg_test.go`.
These are synthetic inputs, not a representative photo collection.
Each path includes the same `io.Reader` boundary. Purego timings include
reading the entire compressed input and creating/destroying the decoder;
only one-time library loading and symbol lookup are excluded. Decode-only
and complete decode/resize/encode-to-128x128 timings are reported separately.
Native paths use accurate DCT and normal chroma upsampling, not the
lower-quality fast flags. Stdlib always decodes at full resolution.

`-benchmem` counts Go allocations only. In particular, it excludes the
subprocess's heap and the native library's allocations; it is not a
comparison of total process-tree memory usage.

## Measurements

Measured October 21, 2026 on this Linux/amd64 VM (AMD EPYC 9554P, 2 vCPUs),
Go 1.27.2, purego v0.10.1, and libjpeg-turbo 2.1.5.
Medians of five runs with the command above, in milliseconds:

| Input | Factor | djpeg decode | purego decode | djpeg thumbnail | purego thumbnail |
| --- | --- | ---: | ---: | ---: | ---: |
| 256x256 | 2 | 0.803 | 0.081 | 1.477 | 0.698 |
| 4000x4000 | 1 | 113.282 | 37.534 | 247.700 | 159.613 |
| 4000x4000 | 2 | 57.169 | 28.064 | 92.203 | 58.327 |
| 4000x4000 | 4 | 39.151 | 22.535 | 47.882 | 31.541 |
| 4000x4000 | 8 | 29.258 | 19.472 | 30.768 | 22.707 |

The intended large-image downsampling cases show about **1.5–2.0x faster
decoding** and **1.35–1.58x faster thumbnail generation** than the subprocess.
For comparison, stdlib decode/thumbnail was 144.255/148.050 ms for the large
input, and 0.465/1.134 ms for the small one. Native full-resolution RGBA
decode is faster than stdlib, but its subsequent resize can erase that win;
the stdlib returns YCbCr rather than RGBA.

There is a memory tradeoff. For the large input at factor 8, the purego
path allocated about **8.05 MiB/op in Go**, versus **1.98 MiB/op** in the
subprocess path. `tjDecompress2` requires the entire compressed input in
memory, and `io.ReadAll` grows its buffer; djpeg streams the input. At full
resolution purego instead allocated about 68.1 MiB/op versus 122.1 MiB/op,
by avoiding intermediate RGB/PNM storage.

These are package-level decode/resize/encode measurements, not HTTP
thumbnail requests: they omit `images.go`'s outer config parsing and
fallback replay buffer for both native paths, as well as file/storage I/O.
In production that replay buffer would coexist with purego's input buffer.

Validation: `CGO_ENABLED=0 go test ./internal/images/...` and
`CGO_ENABLED=1 go test -race ./internal/images/fastjpeg` passed with native
tests actually running. Test binaries also cross-compiled with cgo
disabled for Linux/arm64 and macOS/amd64+arm64; they were not executed on
those platforms. The full repository suite was not run.

## Requirements before production integration

1. **Choose the trust boundary explicitly.** A native crash or memory
   corruption would now affect perkeepd, not just djpeg. Purego does not
   sandbox native code. The prototype sets `TJFLAG_LIMITSCANS`, but this
   does not provide general memory, CPU, or execution-time limits.
   Both compressed-input and decoded-pixel allocations remain unbounded.
   Progressive JPEGs can still require full-resolution coefficient storage.
   Limit compressed bytes, original/output pixels, and concurrent decodes;
   ensure fallback cannot bypass those limits. Review fuzzing and deployment
   library updates before enabling this for untrusted uploads.
2. **Preserve fallback and availability behavior.** Prefer an explicit
   opt-in initially. Try the shared library once; if unavailable, retain
   djpeg and then the existing standard-library fallback. Keep
   `CAMLI_DISABLE_DJPEG` as a complete opt-out. Preserve the
   `DjpegFailedError` contract consumed by `../images.go`, including after
   a partially consumed reader. The prototype rejects CMYK and unsupported
   layouts with that error type. Unlike the subprocess path's exit-code-2
   handling, it conservatively rejects native warnings too: a warning code
   alone does not prove the entire output buffer was produced.
3. **Get the FFI details right.** The prototype uses `int32` for C `int`
   arguments, results, and header fields, and `uintptr` for Unix
   `unsigned long`. A Go `int` result can misinterpret C's negative return
   value. It pins compressed input through handle destruction, pins output,
   and locks the OS thread for native calls and error retrieval because
   libturbojpeg 2.x can use thread-local error strings. Use the RGBA format
   with guaranteed opaque alpha, not RGBX. Dimensions must be ceiling-divided
   and pitch/allocation arithmetic checked before native writes.
4. **Finish platform and runtime reporting support.** This experiment
   intentionally does not implement Windows loading, other architectures,
   musl/static executable support, a runtime feature flag, build-info
   reporting, or production metrics. Windows needs a different binding for
   `unsigned long` and a DLL loading policy. Keep unsupported platforms on
   the existing path rather than introducing a hard library dependency.
5. **Broaden performance and correctness validation.** Benchmark real
   photos, large/progressive/high-entropy inputs, concurrent thumbnail
   loads, and supported platforms/libraries. Generated odd-sized RGB/gray
   output is compared exactly with djpeg when available. The broader Go
   corpus checks dimensions and fallback errors, not pixel equivalence for
   every supported native-library version. Race testing covers Go code,
   not unsanitized native-library memory accesses.
6. **Decide how closely to match the requested scale.** `tjDecompress2`
   chooses the largest fractional scale that fits the requested bounds.
   For tiny images, several scales can have the same rounded dimensions:
   a 3x3 image requested at half size can select 5/8 instead of 1/2, giving
   different pixels than djpeg's explicit `-scale 1/2`. Tiny and degenerate
   bounds are tested, but exact pixel parity is not promised for them.
   TurboJPEG 3's explicit scaling-factor API is an alternative if requiring
   a newer runtime library is acceptable.

## Primary references

- [TurboJPEG 2.1.5 API and constants](https://github.com/libjpeg-turbo/libjpeg-turbo/blob/2.1.5/turbojpeg.h)
- [TurboJPEG 2.1.5 implementation and error handling](https://github.com/libjpeg-turbo/libjpeg-turbo/blob/2.1.5/turbojpeg.c)
- [purego v0.10.1 function binding and memory rules](https://github.com/ebitengine/purego/blob/v0.10.1/func.go)
- [Go rules for passing pointers to native code](https://pkg.go.dev/cmd/cgo#hdr-Passing_pointers)
