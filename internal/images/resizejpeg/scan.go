// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package resizejpeg

import (
	"image"
)

// makeImg allocates and initializes the destination image.
func (d *decoder) makeImg(mxx, myy int) error {
	bs := 8 / d.factor
	bounds := image.Rect(0, 0, (d.width+d.factor-1)/d.factor, (d.height+d.factor-1)/d.factor)
	if d.nComp == 1 {
		if err := checkAllocationSize(uint64(bs*mxx) * uint64(bs*myy)); err != nil {
			return err
		}
		m := image.NewGray(image.Rect(0, 0, bs*mxx, bs*myy))
		d.img1 = m.SubImage(bounds).(*image.Gray)
		return nil
	}

	// Determine if we need flex mode for non-standard subsampling.
	// Flex mode is needed when:
	// - Cb and Cr have different sampling factors, or
	// - The Y component doesn't have the maximum sampling factors, or
	// - The ratio doesn't match any standard YCbCrSubsampleRatio.
	subsampleRatio := image.YCbCrSubsampleRatio444
	if d.comp[1].h != d.comp[2].h || d.comp[1].v != d.comp[2].v ||
		d.maxH != d.comp[0].h || d.maxV != d.comp[0].v {
		d.flex = true
	} else {
		hRatio := d.maxH / d.comp[1].h
		vRatio := d.maxV / d.comp[1].v
		switch hRatio<<4 | vRatio {
		case 0x11:
			subsampleRatio = image.YCbCrSubsampleRatio444
		case 0x12:
			subsampleRatio = image.YCbCrSubsampleRatio440
		case 0x21:
			subsampleRatio = image.YCbCrSubsampleRatio422
		case 0x22:
			subsampleRatio = image.YCbCrSubsampleRatio420
		case 0x41:
			subsampleRatio = image.YCbCrSubsampleRatio411
		case 0x42:
			subsampleRatio = image.YCbCrSubsampleRatio410
		default:
			d.flex = true
		}
	}

	padded := image.Rect(0, 0, bs*d.maxH*mxx, bs*d.maxV*myy)
	// image.NewYCbCr allocates all three planes in a single byte slice.
	// Work in uint64 until the size is known to fit an int, including on
	// 32-bit platforms. MCU padding makes the chroma divisions exact.
	ch, cv := 1, 1
	if !d.flex {
		ch, cv = d.maxH/d.comp[1].h, d.maxV/d.comp[1].v
	}
	total := uint64(padded.Dx())*uint64(padded.Dy()) +
		2*uint64(padded.Dx()/ch)*uint64(padded.Dy()/cv)
	if err := checkAllocationSize(total); err != nil {
		return err
	}
	if d.nComp == 4 || d.isRGB() {
		if err := checkAllocationSize(4 * uint64(bounds.Dx()) * uint64(bounds.Dy())); err != nil {
			return err
		}
	}
	m := image.NewYCbCr(padded, subsampleRatio)
	d.img3 = m.SubImage(bounds).(*image.YCbCr)

	if d.nComp == 4 {
		h3, v3 := d.comp[3].h, d.comp[3].v
		d.blackPix = make([]byte, bs*h3*mxx*bs*v3*myy)
		d.blackStride = bs * h3 * mxx
	}
	return nil
}

// checkAllocationSize rejects sizes that would overflow slice/image allocation
// arithmetic. It is not a resource budget; callers must still limit untrusted
// image dimensions if they need an upper bound on memory consumption.
func checkAllocationSize(n uint64) error {
	if n > uint64(^uint(0)>>1) {
		return UnsupportedError("image is too large")
	}
	return nil
}

// Specified in section B.2.3.
func (d *decoder) processSOS(n int) (retErr error) {
	if d.nComp == 0 {
		return FormatError("missing SOF marker")
	}
	if n < 6 || 4+2*d.nComp < n || n%2 != 0 {
		return FormatError("SOS has wrong length")
	}
	if err := d.readFull(d.tmp[:n]); err != nil {
		return err
	}
	nComp := int(d.tmp[0])
	if n != 4+2*nComp {
		return FormatError("SOS length inconsistent with number of components")
	}
	var scan [maxComponents]struct {
		compIndex uint8
		td        uint8 // DC table selector.
		ta        uint8 // AC table selector.
	}
	totalHV := 0
	for i := 0; i < nComp; i++ {
		cs := d.tmp[1+2*i] // Component selector.
		compIndex := -1
		for j, comp := range d.comp[:d.nComp] {
			if cs == comp.c {
				compIndex = j
			}
		}
		if compIndex < 0 {
			return FormatError("unknown component selector")
		}
		scan[i].compIndex = uint8(compIndex)
		// Section B.2.3 states that "the value of Cs_j shall be different from
		// the values of Cs_1 through Cs_(j-1)". Since we have previously
		// verified that a frame's component identifiers (C_i values in section
		// B.2.2) are unique, it suffices to check that the implicit indexes
		// into d.comp are unique.
		for j := 0; j < i; j++ {
			if scan[i].compIndex == scan[j].compIndex {
				return FormatError("repeated component selector")
			}
		}
		totalHV += d.comp[compIndex].h * d.comp[compIndex].v

		// The baseline t <= 1 restriction is specified in table B.3.
		scan[i].td = d.tmp[2+2*i] >> 4
		if t := scan[i].td; t > maxTh || (d.baseline && t > 1) {
			return FormatError("bad Td value")
		}
		scan[i].ta = d.tmp[2+2*i] & 0x0f
		if t := scan[i].ta; t > maxTh || (d.baseline && t > 1) {
			return FormatError("bad Ta value")
		}
	}
	// Section B.2.3 states that if there is more than one component then the
	// total H*V values in a scan must be <= 10.
	if d.nComp > 1 && totalHV > 10 {
		return FormatError("total sampling factors too large")
	}

	// zigStart and zigEnd are the spectral selection bounds.
	// ah and al are the successive approximation high and low values.
	// The spec calls these values Ss, Se, Ah and Al.
	//
	// For progressive JPEGs, these are the two more-or-less independent
	// aspects of progression. Spectral selection progression is when not
	// all of a block's 64 DCT coefficients are transmitted in one pass.
	// For example, three passes could transmit coefficient 0 (the DC
	// component), coefficients 1-5, and coefficients 6-63, in zig-zag
	// order. Successive approximation is when not all of the bits of a
	// band of coefficients are transmitted in one pass. For example,
	// three passes could transmit the 6 most significant bits, followed
	// by the second-least significant bit, followed by the least
	// significant bit.
	//
	// For sequential JPEGs, these parameters are hard-coded to 0/63/0/0, as
	// per table B.3.
	zigStart, zigEnd, ah, al := int32(0), int32(blockSize-1), uint32(0), uint32(0)
	if d.progressive {
		zigStart = int32(d.tmp[1+2*nComp])
		zigEnd = int32(d.tmp[2+2*nComp])
		ah = uint32(d.tmp[3+2*nComp] >> 4)
		al = uint32(d.tmp[3+2*nComp] & 0x0f)
		if (zigStart == 0 && zigEnd != 0) || zigStart > zigEnd || blockSize <= zigEnd {
			return FormatError("bad spectral selection bounds")
		}
		if zigStart != 0 && nComp != 1 {
			return FormatError("progressive AC coefficients for more than one component")
		}
		if ah != 0 && ah != al+1 {
			return FormatError("bad successive approximation values")
		}
	}

	// mxx and myy are the number of MCUs (Minimum Coded Units) in the image.
	// The MCU dimensions are based on the maximum sampling factors.
	// For standard subsampling, maxH/maxV equals h0/v0 (Y's factors).
	// For flex mode, Y may not have the maximum factors.
	mxx := (d.width + 8*d.maxH - 1) / (8 * d.maxH)
	myy := (d.height + 8*d.maxV - 1) / (8 * d.maxV)
	var pipeline *resizePipeline
	if d.resize != nil {
		if err := d.resize.startScan(d, nComp, mxx, myy); err != nil {
			return err
		}
		if d.resize.streaming {
			pipeline = d.resize.pipeline
			defer func() {
				// Always join on malformed/truncated input too. Workers never
				// read the input or decoder entropy state.
				if err := pipeline.closeAndWait(); retErr == nil {
					retErr = err
				}
				if retErr == nil {
					retErr = d.resize.filter.finish()
				}
				d.resize.complete = retErr == nil
			}()
		}
	}
	// Progressive thumbnails retain compact coefficients until EOI, then
	// reconstruct row bands in workers rather than allocating image-wide
	// reduced pixel planes.
	deferred := d.resize != nil && d.resize.deferred
	if pipeline == nil && !deferred && d.img1 == nil && d.img3 == nil {
		if err := d.makeImg(mxx, myy); err != nil {
			return err
		}
	}
	if d.progressive {
		for i := 0; i < nComp; i++ {
			compIndex := scan[i].compIndex
			if d.progCoeffs[compIndex] == nil {
				nBlocks := mxx * myy * d.comp[compIndex].h * d.comp[compIndex].v
				bs := 8 / d.factor
				if err := checkAllocationSize(uint64(nBlocks) * uint64(bs*bs) * 4); err != nil {
					return err
				}
				if d.factor != 1 {
					if err := checkAllocationSize(uint64(nBlocks) * 8); err != nil {
						return err
					}
				}
				d.progCoeffs[compIndex] = make([]int32, nBlocks*bs*bs)
				if d.factor != 1 {
					d.progNonzero[compIndex] = make([]uint64, nBlocks)
				}
			}
		}
	}

	d.bits = bits{}
	mcu, expectedRST := 0, uint8(rst0Marker)
	var (
		// b is the decoded coefficients, in natural (not zig-zag) order.
		b  block
		dc [maxComponents]int32
		// bx and by are the location of the current block, in units of 8x8
		// blocks: the third block in the first row has (bx, by) = (2, 0).
		bx, by     int
		blockCount int
	)
	for my := 0; my < myy; my++ {
		var band *resizeBand
		if pipeline != nil {
			var err error
			band, err = pipeline.acquire()
			if err != nil {
				return err
			}
			bandHeight := (8 / d.factor) * d.maxV
			band.y = my * bandHeight
			band.n = min(bandHeight, (d.height+d.factor-1)/d.factor-band.y)
		}
		for mx := 0; mx < mxx; mx++ {
			for i := 0; i < nComp; i++ {
				compIndex := scan[i].compIndex
				hi := d.comp[compIndex].h
				vi := d.comp[compIndex].v
				for j := 0; j < hi*vi; j++ {
					// The blocks are traversed one MCU at a time. For 4:2:0 chroma
					// subsampling, there are four Y 8x8 blocks in every 16x16 MCU.
					//
					// For a sequential 32x16 pixel image, the Y blocks visiting order is:
					//	0 1 4 5
					//	2 3 6 7
					//
					// For progressive images, the interleaved scans (those with nComp > 1)
					// are traversed as above, but non-interleaved scans are traversed left
					// to right, top to bottom:
					//	0 1 2 3
					//	4 5 6 7
					// Only DC scans (zigStart == 0) can be interleaved. AC scans must have
					// only one component.
					//
					// To further complicate matters, for non-interleaved scans, there is no
					// data for any blocks that are inside the image at the MCU level but
					// outside the image at the pixel level. For example, a 24x16 pixel 4:2:0
					// progressive image consists of two 16x16 MCUs. The interleaved scans
					// will process 8 Y blocks:
					//	0 1 4 5
					//	2 3 6 7
					// The non-interleaved scans will process only 6 Y blocks:
					//	0 1 2
					//	3 4 5
					if nComp != 1 {
						bx = hi*mx + j%hi
						by = vi*my + j/hi
					} else {
						q := mxx * hi
						bx = blockCount % q
						by = blockCount / q
						blockCount++
						if bx*8 >= d.width || by*8 >= d.height {
							continue
						}
					}

					// Load the previous partially decoded coefficients, if applicable.
					var nonzero uint64
					if d.progressive {
						nonzero = d.loadProgressiveBlock(&b, int(compIndex), by*mxx*hi+bx)
					} else {
						b = block{}
					}

					if ah != 0 {
						var mask *uint64
						if d.factor != 1 {
							mask = &nonzero
						}
						if err := d.refine(&b, &d.huff[acTable][scan[i].ta], zigStart, zigEnd, 1<<al, mask); err != nil {
							return err
						}
					} else {
						zig := zigStart
						if zig == 0 {
							zig++
							// Decode the DC coefficient, as specified in section F.2.2.1.
							value, err := d.decodeHuffman(&d.huff[dcTable][scan[i].td])
							if err != nil {
								return err
							}
							if value > 16 {
								return UnsupportedError("excessive DC component")
							}
							dcDelta, err := d.receiveExtend(value)
							if err != nil {
								return err
							}
							dc[compIndex] += dcDelta
							b[0] = dc[compIndex] << al
						}

						if zig <= zigEnd && d.eobRun > 0 {
							d.eobRun--
						} else {
							// Decode the AC coefficients, as specified in section F.2.2.2.
							huff := &d.huff[acTable][scan[i].ta]
							for ; zig <= zigEnd; zig++ {
								value, err := d.decodeHuffman(huff)
								if err != nil {
									return err
								}
								val0 := value >> 4
								val1 := value & 0x0f
								if val1 != 0 {
									zig += int32(val0)
									if zig > zigEnd {
										break
									}
									ac, err := d.receiveExtend(val1)
									if err != nil {
										return err
									}
									b[unzig[zig]] = ac << al
									if d.progressive && d.factor != 1 {
										nonzero |= uint64(1) << unzig[zig]
									}
								} else {
									if val0 != 0x0f {
										d.eobRun = uint16(1 << val0)
										if val0 != 0 {
											bits, err := d.decodeBits(int32(val0))
											if err != nil {
												return err
											}
											d.eobRun |= uint16(bits)
										}
										d.eobRun--
										break
									}
									zig += 0x0f
								}
							}
						}
					}

					if d.progressive {
						// Save the coefficients.
						d.saveProgressiveBlock(&b, int(compIndex), by*mxx*hi+bx, nonzero)
						// At this point, we could call reconstructBlock to dequantize and perform the
						// inverse DCT, to save early stages of a progressive image to the *image.YCbCr
						// buffers (the whole point of progressive encoding), but in Go, the jpeg.Decode
						// function does not return until the entire image is decoded, so we "continue"
						// here to avoid wasted computation. Instead, reconstructBlock is called on each
						// accumulated block by the reconstructProgressiveImage method after all of the
						// SOS markers are processed.
						continue
					}
					if pipeline != nil {
						// DC prediction is already resolved. The worker owns
						// dequantization and the chosen IDCT for this copy.
						band.coeffs[compIndex][(by%vi)*mxx*hi+bx] = b
					} else {
						if err := d.reconstructBlock(&b, bx, by, int(compIndex)); err != nil {
							return err
						}
					}
				} // for j
			} // for i
			mcu++
			if d.ri > 0 && mcu%d.ri == 0 && mcu < mxx*myy {
				// For well-formed input, the RST[0-7] restart marker follows
				// immediately. For corrupt input, call findRST to try to
				// resynchronize.
				if err := d.readFull(d.tmp[:2]); err != nil {
					return err
				} else if d.tmp[0] != 0xff || d.tmp[1] != expectedRST {
					if err := d.findRST(expectedRST); err != nil {
						return err
					}
				}
				expectedRST++
				if expectedRST == rst7Marker+1 {
					expectedRST = rst0Marker
				}
				// Reset the Huffman decoder.
				d.bits = bits{}
				// Reset the DC components, as per section F.2.1.3.1.
				dc = [maxComponents]int32{}
				// Reset the progressive decoder state, as per section G.1.2.2.
				d.eobRun = 0
			}
		} // for mx
		if pipeline != nil {
			if err := pipeline.submit(band); err != nil {
				return err
			}
		}
	} // for my

	return nil
}

// refine decodes a successive approximation refinement block, as specified in
// section G.1.2.
func (d *decoder) refine(b *block, h *huffman, zigStart, zigEnd, delta int32, nonzero *uint64) error {
	// Refining a DC component is trivial.
	if zigStart == 0 {
		if zigEnd != 0 {
			panic("unreachable")
		}
		bit, err := d.decodeBit()
		if err != nil {
			return err
		}
		if bit {
			b[0] |= delta
		}
		return nil
	}

	// Refining AC components is more complicated; see sections G.1.2.2 and G.1.2.3.
	zig := zigStart
	if d.eobRun == 0 {
	loop:
		for ; zig <= zigEnd; zig++ {
			z := int32(0)
			value, err := d.decodeHuffman(h)
			if err != nil {
				return err
			}
			val0 := value >> 4
			val1 := value & 0x0f

			switch val1 {
			case 0:
				if val0 != 0x0f {
					d.eobRun = uint16(1 << val0)
					if val0 != 0 {
						bits, err := d.decodeBits(int32(val0))
						if err != nil {
							return err
						}
						d.eobRun |= uint16(bits)
					}
					break loop
				}
			case 1:
				z = delta
				bit, err := d.decodeBit()
				if err != nil {
					return err
				}
				if !bit {
					z = -z
				}
			default:
				return FormatError("unexpected Huffman code")
			}

			zig, err = d.refineNonZeroes(b, zig, zigEnd, int32(val0), delta)
			if err != nil {
				return err
			}
			if zig > zigEnd {
				return FormatError("too many coefficients")
			}
			if z != 0 {
				b[unzig[zig]] = z
				if nonzero != nil {
					*nonzero |= uint64(1) << unzig[zig]
				}
			}
		}
	}
	if d.eobRun > 0 {
		d.eobRun--
		if _, err := d.refineNonZeroes(b, zig, zigEnd, -1, delta); err != nil {
			return err
		}
	}
	return nil
}

// refineNonZeroes refines non-zero entries of b in zig-zag order. If nz >= 0,
// the first nz zero entries are skipped over.
func (d *decoder) refineNonZeroes(b *block, zig, zigEnd, nz, delta int32) (int32, error) {
	for ; zig <= zigEnd; zig++ {
		u := unzig[zig]
		if b[u] == 0 {
			if nz == 0 {
				break
			}
			nz--
			continue
		}
		bit, err := d.decodeBit()
		if err != nil {
			return 0, err
		}
		if !bit {
			continue
		}
		if b[u] >= 0 {
			b[u] += delta
		} else {
			b[u] -= delta
		}
	}
	return zig, nil
}

func (d *decoder) reconstructProgressiveImage() error {
	// The mxx, by and bx variables have the same meaning as in the
	// processSOS method.
	mxx := (d.width + 8*d.maxH - 1) / (8 * d.maxH)
	for i := 0; i < d.nComp; i++ {
		if d.progCoeffs[i] == nil {
			continue
		}
		v := 8 * d.maxV / d.comp[i].v
		h := 8 * d.maxH / d.comp[i].h
		stride := mxx * d.comp[i].h
		for by := 0; by*v < d.height; by++ {
			for bx := 0; bx*h < d.width; bx++ {
				var b block
				d.loadProgressiveBlock(&b, i, by*stride+bx)
				if err := d.reconstructBlock(&b, bx, by, i); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// reconstructBlock dequantizes, performs the inverse DCT and stores the block
// to the image.
func (d *decoder) reconstructBlock(b *block, bx, by, compIndex int) error {
	bs := 8 / d.factor
	qt := &d.quant[d.comp[compIndex].tq]
	for y := 0; y < bs; y++ {
		for x := 0; x < bs; x++ {
			u := y*8 + x
			b[u] *= qt[u]
		}
	}
	scaledIDCT(b, d.factor)

	var h, v int
	if d.flex {
		// Flex mode: scale bx and by according to the component's sampling factors.
		h = d.comp[compIndex].expandH
		v = d.comp[compIndex].expandV
		bx, by = bx*h, by*v
	}

	dst, stride := []byte(nil), 0
	if d.nComp == 1 {
		dst, stride = d.img1.Pix[bs*(by*d.img1.Stride+bx):], d.img1.Stride
	} else {
		switch compIndex {
		case 0:
			dst, stride = d.img3.Y[bs*(by*d.img3.YStride+bx):], d.img3.YStride
		case 1:
			dst, stride = d.img3.Cb[bs*(by*d.img3.CStride+bx):], d.img3.CStride
		case 2:
			dst, stride = d.img3.Cr[bs*(by*d.img3.CStride+bx):], d.img3.CStride
		case 3:
			dst, stride = d.blackPix[bs*(by*d.blackStride+bx):], d.blackStride
		default:
			return UnsupportedError("too many components")
		}
	}

	if d.flex {
		// Flex mode: expand each source pixel to h×v destination pixels.
		for y := 0; y < bs; y++ {
			y8 := y * bs
			yv := y * v
			for x := 0; x < bs; x++ {
				val := uint8(max(0, min(255, b[y8+x]+128)))
				xh := x * h
				for yy := 0; yy < v; yy++ {
					for xx := 0; xx < h; xx++ {
						dst[(yv+yy)*stride+xh+xx] = val
					}
				}
			}
		}
		return nil
	}

	// Level shift by +128, clip to [0, 255], and write to dst.
	for y := 0; y < bs; y++ {
		y8 := y * bs
		yStride := y * stride
		for x := 0; x < bs; x++ {
			dst[yStride+x] = uint8(max(0, min(255, b[y8+x]+128)))
		}
	}
	return nil
}

// findRST advances past the next RST restart marker that matches expectedRST.
// Other than I/O errors, it is also an error if we encounter an {0xFF, M}
// two-byte marker sequence where M is not 0x00, 0xFF or the expectedRST.
//
// This is similar to libjpeg's jdmarker.c's next_marker function.
// https://github.com/libjpeg-turbo/libjpeg-turbo/blob/2dfe6c0fe9e18671105e94f7cbf044d4a1d157e6/jdmarker.c#L892-L935
//
// Precondition: d.tmp[:2] holds the next two bytes of JPEG-encoded input
// (input in the d.readFull sense).
func (d *decoder) findRST(expectedRST uint8) error {
	for {
		// i is the index such that, at the bottom of the loop, we read 2-i
		// bytes into d.tmp[i:2], maintaining the invariant that d.tmp[:2]
		// holds the next two bytes of JPEG-encoded input. It is either 0 or 1,
		// so that each iteration advances by 1 or 2 bytes (or returns).
		i := 0

		if d.tmp[0] == 0xff {
			if d.tmp[1] == expectedRST {
				return nil
			} else if d.tmp[1] == 0xff {
				i = 1
			} else if d.tmp[1] != 0x00 {
				// libjpeg's jdmarker.c's jpeg_resync_to_restart does something
				// fancy here, treating RST markers within two (modulo 8) of
				// expectedRST differently from RST markers that are 'more
				// distant'. Until we see evidence that recovering from such
				// cases is frequent enough to be worth the complexity, we take
				// a simpler approach for now. Any marker that's not 0x00, 0xff
				// or expectedRST is a fatal FormatError.
				return FormatError("bad RST marker")
			}

		} else if d.tmp[1] == 0xff {
			d.tmp[0] = 0xff
			i = 1
		}

		if err := d.readFull(d.tmp[i:2]); err != nil {
			return err
		}
	}
}
