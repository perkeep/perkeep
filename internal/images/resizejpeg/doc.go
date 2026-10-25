/*
Copyright 2026 The Perkeep Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package resizejpeg decodes and resizes JPEG images without a full-size
// intermediate pixel buffer.
//
// It is a decoder-only fork of Go 1.27.2's image/jpeg package. [Decode] accepts
// a scale factor of 1, 2, 4, or 8. Reduced inverse DCTs produce smaller blocks
// directly, without allocating full-resolution pixels or decoding into an
// intermediate full-size image. The input is streamed, not buffered in full.
//
// Sequential JPEGs require only the reduced image buffers and block-sized
// scratch space. Progressive JPEGs additionally retain reduced-frequency
// coefficients and a nonzero bitmap for each source block. This bitmap is
// necessary to parse later AC refinement scans correctly; its memory usage
// depends on the source dimensions, not just the output dimensions.
//
// [DecodeResize] instead performs full IDCT and RGB conversion before applying
// a selectable scale-aware filter (Lanczos3 by default). Single-scan inputs use
// bounded MCU-row buffers and parallel workers, rather than a full-resolution
// source image. Progressive
// and non-interleaved inputs require an explicit full-buffer fallback; see
// [ResizeOptions].
//
// [DecodeThumbnail] instead selects reduced IDCT with configurable thumbnail
// headroom, then finishes with the selected filter. Defaults are four-times
// headroom and Lanczos3; CatmullRom and Bilinear use narrower kernels.
// [ResizeOptions.ThumbnailHeadroom] can reduce intermediate pixel work
// independently of [ResizeOptions.Filter].
// The same workers perform reduced transforms and horizontal filtering.
// Original-image sampling coordinates avoid rounded-size phase drift.
// This is faster but not pixel-equivalent to full decoding plus Lanczos3.
//
// Both resizing APIs support [ResizeOptions.MaxMemoryBytes] and
// [ResizeOptions.MemAllocSemaphore] for per-decode limits and shared memory
// admission. One reservation covers the planned buffers, including progressive
// scans; it is released after worker shutdown. Returned pixels transfer to
// callers and require a separate retention budget.
//
// JPEG is defined in ITU-T T.81: https://www.w3.org/Graphics/JPEG/itu-t81.pdf.
package resizejpeg // import "perkeep.org/internal/images/resizejpeg"
