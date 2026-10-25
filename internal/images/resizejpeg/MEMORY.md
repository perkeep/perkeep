# Memory limits and concurrent admission

`DecodeResize` and `DecodeThumbnail` share two optional controls:

```go
type ResizeOptions struct {
    // Other fields omitted.
    MaxMemoryBytes    int64
    MemAllocSemaphore func(bytes int64) (release func(), err error)
}
```

Neither option changes pixels, workers, filter, or thumbnail headroom.
Zero/nil preserves the existing behavior. They do not apply to `Decode`.

## One reservation, before the large allocations

The decoder reads through the first scan header before selecting its
allocation plan. At that point it knows the original dimensions, sampling,
IDCT factor, worker count, filter, and whether it will stream, retain
progressive coefficients, or use full-image fallback.

Before allocating the planned buffers or starting workers:

1. Compute a conservative byte reservation with geometry arithmetic, without
   allocating images or iterating over pixels.
2. If positive `MaxMemoryBytes` is smaller than the reservation, return
   `UnsupportedError` without calling the hook. Negative limits are rejected
   before reading the input.
3. If non-nil, call `MemAllocSemaphore` synchronously, once, with that same
   byte count. It may wait. An error is returned unchanged; an unsuccessful
   acquisition must not leave capacity reserved.
4. After successful admission, retain the reservation through all scans,
   reconstruction, and filtering. On every exit, stop/join workers, drop
   temporary references, and call a non-nil release function exactly once.
   A nil release function is allowed.

There is **no incremental acquisition while holding a reservation**. The
plan accounts for all components even if the first progressive scan only
transmits one of them. This avoids multiple decodes each holding partial
reservations while waiting for enough capacity to finish. Invalid input
rejected before admission never calls the hook.

The reservation includes destination pixels, filter tables and accumulation
rows, coefficient bands, worker planes/RGB buffers, and any image-wide
fallback planes, coefficient arrays, or color-conversion copy. It uses
actual sampling and MCU padding, includes allocated capacities rather than
only slice lengths, and allows for allocator rounding and bounded
decoder/worker overhead. Progressive thumbnail coefficients use the chosen
reduced factor; full-IDCT progressive resizing is usually much larger.

## Shared budget with request cancellation

Applications can supply their preferred admission policy. For example,
using the existing `golang.org/x/sync/semaphore` package:

```go
const decodeBudget = int64(128 << 20)

var decodeMemory = semaphore.NewWeighted(decodeBudget)

func thumbnail(ctx context.Context, r io.Reader) (*image.RGBA, error) {
    return resizejpeg.DecodeThumbnail(r, 256, 192, &resizejpeg.ResizeOptions{
        AllowFullDecode: true,
        MaxMemoryBytes:  decodeBudget,
        MemAllocSemaphore: func(n int64) (func(), error) {
            if err := decodeMemory.Acquire(ctx, n); err != nil {
                return nil, err
            }
            return func() { decodeMemory.Release(n) }, nil
        },
    })
}
```

Share the semaphore across requests. Matching `MaxMemoryBytes` to its
capacity rejects an oversized request instead of waiting for a reservation
that can never fit. The hook's context cancels the **admission wait**; it
does not automatically cancel reading or CPU work after admission. Callers
needing active-decode cancellation must arrange for their reader to report
cancellation too.

Byte-weighted admission permits more small streaming decodes than large
progressive fallbacks. Fewer workers can also reduce each reservation.
The library does not silently lower worker count or quality to fit a limit.
`MaxSourcePixels` remains independently useful for limiting source size and
CPU exposure.

## Output ownership and real process memory

The reservation covers the destination **during decoding**. When the API
returns, its pixels transfer to the caller, and the reservation is released.
The decoder cannot know when a caller finishes encoding, caching, or sending
that image. Budget retained results separately; the destination backing
array contains `4*width*height` bytes, plus allocator/object overhead.

For a simple conservative end-to-end HTTP budget, callers can instead hold
the complete reservation until encoding finishes:

```go
var release func()
defer func() {
    if release != nil {
        release()
    }
}()

opts := &resizejpeg.ResizeOptions{
    MaxMemoryBytes: decodeBudget,
    MemAllocSemaphore: func(n int64) (func(), error) {
        if err := decodeMemory.Acquire(ctx, n); err != nil {
            return nil, err
        }
        release = func() { decodeMemory.Release(n) }
        return nil, nil // The surrounding request now owns the reservation.
    },
}
thumb, err := resizejpeg.DecodeThumbnail(r, 256, 192, opts)
if err != nil {
    return err
}
return jpeg.Encode(w, thumb, nil)
```

This keeps scratch bytes reserved longer than necessary but also covers
the retained output until the handler finishes. Encoder buffers still need
their own allowance. Longer-lived caches need their own eviction budget.

**These controls are not hard heap or RSS limits.** They exclude
caller-owned compressed input, arbitrary reader/hook allocations, other
requests' retained images, caches, encoder buffers, and runtime/GC memory.
Releasing means temporary buffers are eligible for collection, not that
the collector immediately reclaims their pages. Leave headroom for garbage
collection and other application memory; a deployment's `GOMEMLIMIT` is
also a soft runtime limit, not an OOM guarantee. Bound queued requests and
compressed input as well as active decode reservations.

See [giant-input measurements](PEAKMEMORY.md) for observed process peaks,
which are different from these planned reservations.

## Validation

Tests cover first-scan admission, future progressive components, sampled/
padded planes, filter capacities, cap boundaries, arithmetic overflow,
canceled waits, concurrent reservations, and release after errors or reader
panics. Default pixels are unchanged, and returned images survive scratch
cleanup and garbage collection.

Affected image tests and tagged vet passed, as did the tagged resizejpeg
race suite, the complete package under Linux/386 `qemu-i386`, and a
30-second memory-options fuzz run (102,356 executions). Linux/arm64,
macOS/arm64, and Windows/amd64 binaries cross-compiled but were not run.
The affected suite retains ten unrelated HEIC/disabled-test skips; optional
visual-report and memory probes need explicit inputs. The linked giant
admission checks were run separately. The full repository suite was not run.
