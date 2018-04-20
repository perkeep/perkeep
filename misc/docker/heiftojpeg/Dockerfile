# Copyright 2018 The Perkeep Authors.
# Licensed under the Apache License, Version 2.0
FROM alpine:3.7 as builder
MAINTAINER Perkeep Contributors <perkeep@googlegroups.com>
RUN apk add --no-cache git cmake alpine-sdk ghostscript-fonts imagemagick imagemagick-dev ffmpeg

ENV HEIF_VERSION e83468424c031af9734f5f382cf0bfb525b63cf6

# Fetch and build heiftojpeg.
WORKDIR /tmp
RUN git clone https://github.com/perkeep/heif/
WORKDIR heif

RUN git reset --hard $HEIF_VERSION
WORKDIR build

RUN apk add --update

RUN cmake ..
RUN make
WORKDIR ..

FROM alpine:3.7

COPY --from=builder /usr/lib/libMagick++-7.Q16HDRI.so.3 /usr/lib/libMagick++-7.Q16HDRI.so.3
COPY --from=builder /usr/lib/libMagickCore-7.Q16HDRI.so.4 /usr/lib/libMagickCore-7.Q16HDRI.so.4
COPY --from=builder /usr/lib/libstdc++.so.6 /usr/lib/libstdc++.so.6
COPY --from=builder /usr/lib/libgcc_s.so.1 /usr/lib/libgcc_s.so.1
COPY --from=builder /lib/ld-musl-x86_64.so.1 /lib/ld-musl-x86_64.so.1
COPY --from=builder /usr/lib/libMagickWand-7.Q16HDRI.so.4 /usr/lib/libMagickWand-7.Q16HDRI.so.4
COPY --from=builder /usr/lib/liblcms2.so.2 /usr/lib/liblcms2.so.2
COPY --from=builder /usr/lib/libfontconfig.so.1 /usr/lib/libfontconfig.so.1
COPY --from=builder /usr/lib/libfreetype.so.6 /usr/lib/libfreetype.so.6
COPY --from=builder /usr/lib/libbz2.so.1 /usr/lib/libbz2.so.1
COPY --from=builder /lib/libz.so.1 /lib/libz.so.1
COPY --from=builder /usr/lib/libltdl.so.7 /usr/lib/libltdl.so.7
COPY --from=builder /usr/lib/libgomp.so.1 /usr/lib/libgomp.so.1
COPY --from=builder /usr/lib/libexpat.so.1 /usr/lib/libexpat.so.1
COPY --from=builder /usr/lib/libpng16.so.16 /usr/lib/libpng16.so.16
COPY --from=builder /usr/bin/ffmpeg /usr/bin/ffmpeg
COPY --from=builder /usr/lib/libavdevice.so.57 /usr/lib/libavdevice.so.57
COPY --from=builder /usr/lib/libavfilter.so.6 /usr/lib/libavfilter.so.6
COPY --from=builder /usr/lib/libavformat.so.57 /usr/lib/libavformat.so.57
COPY --from=builder /usr/lib/libavcodec.so.57 /usr/lib/libavcodec.so.57
COPY --from=builder /usr/lib/libavresample.so.3 /usr/lib/libavresample.so.3
COPY --from=builder /usr/lib/libpostproc.so.54 /usr/lib/libpostproc.so.54
COPY --from=builder /usr/lib/libswresample.so.2 /usr/lib/libswresample.so.2
COPY --from=builder /usr/lib/libswscale.so.4 /usr/lib/libswscale.so.4
COPY --from=builder /usr/lib/libavutil.so.55 /usr/lib/libavutil.so.55
COPY --from=builder /usr/lib/libSDL2-2.0.so.0 /usr/lib/libSDL2-2.0.so.0
COPY --from=builder /usr/lib/libv4l2.so.0 /usr/lib/libv4l2.so.0
COPY --from=builder /usr/lib/libxcb.so.1 /usr/lib/libxcb.so.1
COPY --from=builder /usr/lib/libxcb-shm.so.0 /usr/lib/libxcb-shm.so.0
COPY --from=builder /usr/lib/libxcb-xfixes.so.0 /usr/lib/libxcb-xfixes.so.0
COPY --from=builder /usr/lib/libxcb-shape.so.0 /usr/lib/libxcb-shape.so.0
COPY --from=builder /usr/lib/libasound.so.2 /usr/lib/libasound.so.2
COPY --from=builder /usr/lib/libva.so.1 /usr/lib/libva.so.1
COPY --from=builder /usr/lib/librtmp.so.1 /usr/lib/librtmp.so.1
COPY --from=builder /usr/lib/libgnutls.so.30 /usr/lib/libgnutls.so.30
COPY --from=builder /usr/lib/libbz2.so.1 /usr/lib/libbz2.so.1
COPY --from=builder /usr/lib/libxvidcore.so.4 /usr/lib/libxvidcore.so.4
COPY --from=builder /usr/lib/libx265.so.130 /usr/lib/libx265.so.130
COPY --from=builder /usr/lib/libx264.so.148 /usr/lib/libx264.so.148
COPY --from=builder /usr/lib/libvpx.so.4 /usr/lib/libvpx.so.4
COPY --from=builder /usr/lib/libvorbisenc.so.2 /usr/lib/libvorbisenc.so.2
COPY --from=builder /usr/lib/libvorbis.so.0 /usr/lib/libvorbis.so.0
COPY --from=builder /usr/lib/libtheoraenc.so.1 /usr/lib/libtheoraenc.so.1
COPY --from=builder /usr/lib/libtheoradec.so.1 /usr/lib/libtheoradec.so.1
COPY --from=builder /usr/lib/libopus.so.0 /usr/lib/libopus.so.0
COPY --from=builder /usr/lib/libmp3lame.so.0 /usr/lib/libmp3lame.so.0
COPY --from=builder /usr/lib/libvdpau.so.1 /usr/lib/libvdpau.so.1
COPY --from=builder /usr/lib/libX11.so.6 /usr/lib/libX11.so.6
COPY --from=builder /usr/lib/libva-x11.so.1 /usr/lib/libva-x11.so.1
COPY --from=builder /usr/lib/libva-drm.so.1 /usr/lib/libva-drm.so.1
COPY --from=builder /usr/lib/libv4lconvert.so.0 /usr/lib/libv4lconvert.so.0
COPY --from=builder /usr/lib/libXau.so.6 /usr/lib/libXau.so.6
COPY --from=builder /usr/lib/libXdmcp.so.6 /usr/lib/libXdmcp.so.6
COPY --from=builder /lib/libssl.so.44 /lib/libssl.so.44
COPY --from=builder /lib/libcrypto.so.42 /lib/libcrypto.so.42
COPY --from=builder /usr/lib/libp11-kit.so.0 /usr/lib/libp11-kit.so.0
COPY --from=builder /usr/lib/libunistring.so.2 /usr/lib/libunistring.so.2
COPY --from=builder /usr/lib/libtasn1.so.6 /usr/lib/libtasn1.so.6
COPY --from=builder /usr/lib/libnettle.so.6 /usr/lib/libnettle.so.6
COPY --from=builder /usr/lib/libhogweed.so.4 /usr/lib/libhogweed.so.4
COPY --from=builder /usr/lib/libgmp.so.10 /usr/lib/libgmp.so.10
COPY --from=builder /usr/lib/libstdc++.so.6 /usr/lib/libstdc++.so.6
COPY --from=builder /usr/lib/libogg.so.0 /usr/lib/libogg.so.0
COPY --from=builder /usr/lib/libXext.so.6 /usr/lib/libXext.so.6
COPY --from=builder /usr/lib/libXfixes.so.3 /usr/lib/libXfixes.so.3
COPY --from=builder /usr/lib/libdrm.so.2 /usr/lib/libdrm.so.2
COPY --from=builder /usr/lib/libjpeg.so.8 /usr/lib/libjpeg.so.8
COPY --from=builder /usr/lib/libbsd.so.0 /usr/lib/libbsd.so.0
COPY --from=builder /usr/lib/libffi.so.6 /usr/lib/libffi.so.6
# TODO: maybe trim out the coders that are not needed, if any? the error was about BMP not found.
COPY --from=builder /usr/lib/ImageMagick-7.0.7/modules-Q16HDRI/coders/ /usr/lib/ImageMagick-7.0.7/modules-Q16HDRI/coders/
# for imagemagick to find fonts, at the very least.
COPY --from=builder /etc/ImageMagick-7/ /etc/ImageMagick-7/
# for the (helvetica) fonts, montage tool needs them.
# TODO: maybe keep only helvetica, as it's the error I was hitting?
COPY --from=builder /usr/share/fonts/Type1/ /usr/share/fonts/Type1/
# trick the call to exiftool, as it's for writing EXIF to the output jpeg, and we don't care for now.
RUN echo -e '#!/bin/sh\n\
exit 0' > /usr/bin/exiftool
RUN chmod a+x /usr/bin/exiftool

# Put this at the bottom to take advantage of Docker layer caching. Most of the stuff up there will never change.
COPY --from=builder /tmp/heif/Bins/heiftojpeg /usr/local/bin

ENTRYPOINT ["/usr/local/bin/heiftojpeg"]
