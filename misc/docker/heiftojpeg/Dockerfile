# Copyright 2018 The Perkeep Authors.
# Licensed under the Apache License, Version 2.0
FROM alpine:3.7 as builder
MAINTAINER Perkeep Contributors <perkeep@googlegroups.com>
RUN apk add --no-cache \
       git alpine-sdk automake autoconf libtool \
       libjpeg-turbo-dev bash

# Fetch and build heiftojpeg.
WORKDIR /tmp
RUN git clone https://github.com/strukturag/libde265.git
WORKDIR libde265

RUN ./autogen.sh
RUN ./configure
RUN make install

ENV IM_VERSION 859511c029bb8e9ea02037f5672e0fd741abf414

WORKDIR ..
RUN git clone https://github.com/ImageMagick/ImageMagick.git
WORKDIR ImageMagick

RUN git reset --hard $IM_VERSION

RUN ./configure --with-heic=yes --with-jpeg=true --enable-zero-configuration
RUN make
RUN make install

FROM alpine:3.7

COPY --from=builder /usr/local/lib/libMagickCore-7.Q16HDRI.so.5 /usr/local/lib/libMagickCore-7.Q16HDRI.so.5
COPY --from=builder /usr/local/lib/libMagickWand-7.Q16HDRI.so.5 /usr/local/lib/libMagickWand-7.Q16HDRI.so.5
COPY --from=builder /usr/lib/libstdc++.so.6 /usr/lib/libstdc++.so.6
COPY --from=builder /usr/lib/libgcc_s.so.1 /usr/lib/libgcc_s.so.1
COPY --from=builder /lib/ld-musl-x86_64.so.1 /lib/ld-musl-x86_64.so.1
COPY --from=builder /usr/lib/libjpeg.so.8 /usr/lib/libjpeg.so.8
COPY --from=builder /usr/local/lib/libde265.so.0 /usr/local/lib/libde265.so.0
COPY --from=builder /usr/lib/libgomp.so.1 /usr/lib/libgomp.so.1
COPY --from=builder /usr/local/etc/ImageMagick-7/magic.xml /usr/local/etc/ImageMagick-7/magic.xml

# Put this at the bottom to take advantage of Docker layer caching. Most of the stuff up there will never change.
COPY --from=builder /usr/local/bin/convert /usr/local/bin/convert
COPY --from=builder /usr/local/bin/magick /usr/local/bin/magick

# Commented out to save space, but useful for debugging:
# RUN apk add --no-cache bash

# Test with, e.g.:
# docker run -v $HOME/img/:/img -ti gcr.io/perkeep-containers/thumbnail:latest /usr/local/bin/convert /img/rotate.heif -auto-orient /img/rotate.jpg

CMD ["/usr/local/bin/convert"]
