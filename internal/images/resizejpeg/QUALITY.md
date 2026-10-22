# Visual quality experiment

On October 21, 2026, the reduced decoder was compared with full decoding
followed by Lanczos3, including actual inspection of the resulting images.
This is a small, reproducible experiment, not a comprehensive perceptual
evaluation or a quality certification.

## Inputs and methods

The [photo manifest](testdata/quality-photos.json) pins two public-domain
original JPEGs by SHA-256 and records their sources and credits:

- **Cascade Canyon Main Canyon**, National Park Service, 3264x2448,
  4:2:0. Fine foliage, granite, lichen, sky, and sun/shade boundaries.
- **American bison**, Jack Dykinga, USDA Agricultural Research Service,
  2700x1761, 4:4:4. Fine fur, grass, thistle stems, and an out-of-focus
  background. No USDA endorsement is implied.

Photo bytes are used unchanged, without an intermediate resize or re-encode.
They are downloaded separately, not added to the repository.

Three generated 2048x1536 patterns cover text and sharp edges, grayscale
chirps/checkerboards/zone plates, and saturated color edges/fine diagonals.
A fourth, 2049x1537 text/edge image exercises odd dimensions. Generated
patterns are encoded with Go's JPEG encoder at quality 95. The grayscale
frequency pattern uses a grayscale JPEG; the other patterns use 4:2:0.

For each of these six inputs:

1. **Reference:** standard-library full JPEG decode, conversion to opaque
   8-bit RGB, and scale-aware Lanczos3 to the target dimensions.
2. **Direct:** `resizejpeg.Decode` at factors 2, 4, and 8, compared at their
   native, rounded-up dimensions.
3. **Thumbnails:** 512-, 256-, and 128-pixel long edges. Compare the largest
   factor that leaves enough pixels against a factor that leaves at least
   2x the target in **both** axes, then finish with Lanczos3. If both
   strategies select the same factor, omit the duplicate.
4. **Legacy decoder comparison:** repeat the same factors and final Lanczos3
   resize through `fastjpeg.DecodeDownsample`, using installed
   `libjpeg-turbo 2.1.5` (`djpeg` build 20240408). Thumbnail rows also include
   a full-resolution `djpeg` decode plus Lanczos3 as a control.
5. **Streaming full-IDCT comparison:** `DecodeResize` at every row's exact
   output dimensions, using automatic worker count. It performs full IDCT,
   clipped RGB conversion, and streaming Lanczos3 without a full source
   image for these single-scan inputs. Optional progressive photo inputs
   can explicitly use a full-buffer fallback, labeled in the report.
6. **Four-times-headroom thumbnail comparison:** `DecodeThumbnail` at
   every row's exact dimensions. It selects a reduced IDCT factor leaving
   at least 4x the target dimensions when possible, then performs parallel
   reduced reconstruction/horizontal Lanczos3 and ordered vertical filtering.
   Its filter preserves original-image phase at odd dimensions.
7. **Filter/headroom options:** on the 18 thumbnail rows, append 2x-headroom
   Lanczos3, 4x-headroom CatmullRom, 4x-headroom Bilinear, and 2x-headroom
   Bilinear. Separate changes to resolution from changes to final filtering,
   retaining all existing candidate indices and the same reference.

This compares decoders, **not** the complete existing thumbnail pipeline:
the legacy comparison also finishes with Lanczos3 rather than Perkeep's
existing area resizer. Its chroma upsampling and IDCT can differ from the Go
decoder. Factors are chosen from the actual reduced dimensions, not the
legacy production selector's floor-shift calculation.

The Lanczos3 reference is a separable, three-lobe sinc-windowed filter,
implemented with `golang.org/x/image/draw.Kernel`. Its support widens during
downsampling; this is not fixed-width interpolation of a few source pixels.
Filtering is in sRGB-coded values, not linear light. All comparisons start
from the same compressed JPEG. All output images are lossless PNGs.

The report contains **36 output-size rows and 258 candidate comparisons**:
the original 114 reduced/legacy comparisons, 36 streaming full-IDCT results,
36 four-times-headroom thumbnails, and 72 filter/headroom options comparisons.
In addition, `jpegtran -progressive` produces lossless progressive
equivalents: **60 checks** require exact visible-pixel agreement with
baseline decoding at factors 1, 2, 4, and 8, and with both resizing APIs at
a 256-pixel long edge using default options, 2x-headroom Bilinear, and
4x-headroom CatmullRom. Full-IDCT results also agree with the independent
same-filter `x/image/draw` reference within one channel level.
`DecodeResize` explicitly permits full buffering for these progressive
checks; `DecodeThumbnail` uses compact progressive coefficients and
parallel row-band reconstruction.
Factor 1 also matches the standard decoder after RGB conversion.

## Findings

### Photographs

At native thumbnail size, the landscape was quite close to the reference.
Aggressive reduction lost a little foliage and rock microcontrast. The
bison's fine fur and bright meadow stems showed more noticeable changes,
especially when enlarged. Leaving 2x headroom substantially improved both.

For 256-pixel thumbnails, RGB mean absolute errors on the 0-255 channel
scale were:

| Input | Go, aggressive | Go, 2x headroom | djpeg, aggressive | djpeg, 2x headroom |
| --- | ---: | ---: | ---: | ---: |
| Landscape | 1.613 | 0.341 | 1.326 | 0.420 |
| Bison | 5.024 | 1.914 | 5.017 | 1.911 |

The corresponding Go PSNR improved from 40.51 to 52.37 dB for the landscape
and from 29.87 to 37.88 dB for the bison. These values measure agreement
with this particular reference, not absolute image quality.

These two photos suggest a useful fast-path compromise, not proof that all
photographs will look good. At 128 pixels, both strategies already choose
factor 8 and have more than 2x headroom on these inputs.

### Text, repeating patterns, and color

- Large text remains readable. Sharp edges and tiny strokes change, while
  near-threshold repeating lines can acquire different contrast or moire.
- The chirp/zone-plate stress image exposes false bands and repeated rings.
  Extra headroom reduces, but does not eliminate, this aliasing. Legacy
  reduced decoding also differs visibly from the full-decode reference.
- **The saturated-color stress image exposes a substantial gap versus
  `djpeg`.** At 256 pixels, Go's aggressive result has MAE 22.442, versus
  4.291 for `djpeg`. With 2x headroom these improve to 5.720 and 1.004,
  respectively. Thus headroom does not make all content equivalent.

The last result warrants investigation before a default decoder replacement.
The current implementation shrinks all components by the same factor,
retaining the original chroma subsampling. Chroma resolution/upsampling,
transform filtering, and clipping order are potential contributors; this
experiment does **not** isolate their individual effects.

The full-resolution `djpeg` plus Lanczos3 control has MAE 0.612 on that same
color case. Baseline decoder/color-rendering differences alone therefore
do not account for the much larger reduced-output discrepancy.

### Sampling-grid caveat

Reduced decoding uses a fixed factor-spaced sample grid. A global resize
from an odd source dimension to a rounded-up destination uses a slightly
different sample pitch. High-contrast edges exaggerate these phase
differences, especially at larger reductions.

For example, aggressive 256-pixel text/edge MAE is 2.314 for the aligned
input but 17.252 for the odd input; `djpeg` has the same values in this
comparison. This is not a difference unique to the new decoder. The input
dimensions and block layout also change, so the control is not a numerical
isolation of every filtering difference.

### Streaming full-IDCT path

The subsequent `DecodeResize` implementation matched the reference
**byte for byte on all 36 comparisons**, including both photographs,
all four stress images, all three direct-factor target sizes, and
512-/256-/128-pixel thumbnails. MAE, RMSE, and maximum channel error were
zero throughout. All 36 original/progressive parity checks in the expanded
experiment passed.

Thus this path avoids the *additional* frequency, chroma, clipping, and
sample-phase differences introduced by reduced IDCT in this experiment.
It does not remove aliasing already present in the source JPEG or change
the reference Lanczos3 filter's ringing. Nor does a six-image experiment
prove universal bit-exactness: general unit tests allow a one-channel-level
rounding difference against the independently implemented reference.

### Four-times-headroom thumbnails

The new `DecodeThumbnail` policy leaves more headroom than the original
2x experiment and corrects the odd-dimension sampling drift. It selects
factor 2 for the photographs' 256-pixel thumbnails and factor 4 at 128
pixels. Agreement with full decoding plus Lanczos3 is much closer:

| Photo | Long edge | RGB MAE, 0–255 scale | Maximum channel error |
| --- | ---: | ---: | ---: |
| Landscape | 256 | 0.113 | 3 |
| Bison | 256 | 0.084 | 2 |
| Landscape | 128 | 0.149 | 3 |
| Bison | 128 | 0.196 | 2 |

The 512-pixel results select factor 1 and match the reference byte for byte
for all six inputs. The aligned and odd text/edge results now have very
similar 256-pixel MAE, 1.013 and 1.037, rather than the earlier large
odd-size discrepancy.

Stress cases still differ: the 256-pixel chirp MAE is 1.002; the saturated
color case has MAE 1.928 at 256 pixels and 2.676 at 128 pixels, with maximum
channel error 36. These are improvements over aggressive reduction, not a
proof of universally artifact-free output or equivalence to full decoding.
See [thumbnail decoding](THUMBNAIL.md) for the scale policy and memory
contract.

### Configurable filter and headroom

Defaults remain four-times headroom and Lanczos3. Every one of the
previous report's **408 reference/candidate/difference PNGs is byte-identical**
after adding selectable filters and headroom. There are now 552 PNGs total.

For 256-pixel-long-edge photographs, mean absolute channel differences
from the full-decode Lanczos3 reference were:

| Setting | Canyon | Bison |
| --- | ---: | ---: |
| Default: 4x headroom, Lanczos3 | 0.113 | 0.084 |
| 2x headroom, Lanczos3 | 0.341 | 0.272 |
| 4x headroom, CatmullRom | 1.108 | 0.885 |
| 4x headroom, Bilinear | 2.692 | 2.196 |
| 2x headroom, Bilinear | 2.738 | 2.243 |

Those new 2x choices select factor 4 instead of the default factor 2:
approximately one quarter as many reconstructed pixels before filtering.
Changing only headroom is distinct from intentionally choosing a softer
final filter. Errors in this table measure agreement with **Lanczos3**,
not correctness of the different Bilinear/CatmullRom definitions; their
implementations are separately tested against independent same-filter
references.

Fine saturated patterns remain substantially more sensitive. For the
256-pixel color stress image, mean differences were 1.928 with defaults,
5.720 with 2x-headroom Lanczos3, and 12.805 with 2x-headroom Bilinear.
Low-headroom and softer modes are explicit tradeoffs, not quality-equivalent
substitutes. See [CPU/quality options](THUMBNAIL.md#cpu-and-quality-options)
for the API and separate latency/allocation measurements.

## Practical conclusion

The original 2x-headroom experiment was a useful photographic compromise,
but the fast-path API's **default** conservatively aims for **4x the desired
dimensions in both axes**, then applies scale-aware Lanczos3. Callers can
explicitly choose less headroom and/or a narrower filter. It preserves
the original dimensions when the source cannot provide that headroom.
Reduced bands go straight through the filter without a whole intermediate
image for single-scan inputs.

Do not claim bare reduced IDCT is equivalent to full decode plus Lanczos3.
For quality-first use, screenshots, fine repeating patterns, or demanding
color edges, prefer full decoding and filtering. `DecodeResize` now offers
that ordering with bounded source buffers for single-scan input; see
[streaming resize](STREAMING.md) for its input policy and measurements.
Progressive/non-interleaved support requires an explicit memory-expensive
fallback. The reduced-IDCT saturated-color gap and a broader photographic
corpus should still be investigated before choosing reduced decoding as a
production default. None of these APIs is connected to the thumbnail
pipeline.

## Reproduce

The manual harness is behind the `jpegquality` build tag. Ordinary package
tests do not compile it, download photos, or run external tools. The harness
itself never downloads anything; the following explicit preparation does.

From the repository root:

```sh
mkdir -p /tmp/resizejpeg-photos
cp internal/images/resizejpeg/testdata/quality-photos.json \
  /tmp/resizejpeg-photos/photos.json
python3 - /tmp/resizejpeg-photos/photos.json <<'PY'
import json, pathlib, sys, urllib.request
manifest = pathlib.Path(sys.argv[1])
for photo in json.loads(manifest.read_text()):
    urllib.request.urlretrieve(
        photo["download"], manifest.parent / photo["path"])
PY

RESIZEJPEG_QUALITY_DIR=/tmp/resizejpeg-report \
RESIZEJPEG_QUALITY_PHOTOS=/tmp/resizejpeg-photos/photos.json \
CGO_ENABLED=0 go test -tags=jpegquality ./internal/images/resizejpeg \
  -run '^TestQuality' -v -count=1

busybox httpd -f -p 8003 -h /tmp/resizejpeg-report
```

Open the served `index.html`. It offers native-pixel, enlarged, and fit views,
synchronized crops, side-by-side or A/B blink, amplified difference images,
all metrics, and input provenance. Judge normal output pixels too: amplified
differences and enlarged pixels make small errors look more severe.

Photo paths in the manifest are relative to the manifest file. Expected
hashes are verified before decoding; `report.json` records hashes of all
actual JPEG inputs. Omitting `RESIZEJPEG_QUALITY_PHOTOS` generates only
synthetics and labels that limitation in the report.

The output directory must not already exist: choose a fresh path for each
run. This prevents a failed rerun from mixing new images with an older
published report.

Install `djpeg` and `jpegtran` from libjpeg-turbo to reproduce the complete
experiment. Missing tools are explicitly recorded as unrun checks in the
report, not counted as coverage. `CAMLI_DISABLE_DJPEG` also disables the
legacy comparisons. The small helper tests check metric identity/extremes,
the Lanczos kernel and constant preservation, and downsampling
anti-aliasing, factor/headroom selection, and refusal to overwrite an
existing report. Metrics are observational; there is no arbitrary PSNR
pass/fail threshold. The streaming candidate does enforce its
reference-agreement contract: maximum channel error at most one.

## Validation of the experiment

The documented downloads were repeated into a fresh directory and matched
both pinned hashes. The complete opt-in run passed with cgo disabled, with
all 258 candidates generated and all 60 progressive checks executed.
Decoder and report-helper race tests, tagged `go vet`, and the affected
image package suite passed. The viewer loaded every candidate at its
reported dimensions; blink and zoom controls were also exercised.

The affected package suite retains unrelated skips: one disabled YCbCr
resizer comparison, eight HEIC-specific checks that do not apply to JPEG
fixtures, and one actual HEIC case without an available `libheif.so`.
These are not counted as coverage. The full repository suite was not run.
