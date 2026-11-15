# Copyright 2014 The Perkeep Authors.
# Generic purpose Perkeep image, that builds the server (perkeepd)
# and the command-line clients (pk, pk-put, pk-get, and pk-mount),
# including the tiny pk-copy-storage wrapper for perkeepd.

# The build stage always runs on the native build platform and
# cross-compiles for the target, rather than running the Go toolchain
# under QEMU emulation, which is ~10x slower.
FROM --platform=$BUILDPLATFORM golang:1.27 AS pkbuild

LABEL maintainer="Perkeep Authors perkeep@googlegroups.com"

ENV DEBIAN_FRONTEND=noninteractive

WORKDIR /go/src/perkeep.org

# The module and build caches are cache mounts rather than layers, so
# they're shared between the per-platform builds and aren't exported
# with the image layers (where they'd be ~600MB per platform).
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG TARGETOS TARGETARCH TARGETVARIANT

# When cross-compiling, make.go installs into /go/bin/$GOOS_$GOARCH/
# instead of /go/bin, so collect the binaries into /out either way.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go run make.go -v -os=$TARGETOS -arch=$TARGETARCH -arm=${TARGETVARIANT#v} && \
    mkdir /out && \
    find /go/bin -type f \( -name 'pk*' -o -name perkeepd \) -exec mv {} /out/ \;

###########################################################################

# The base stage is the image without Perkeep's binaries: its runtime
# environment, which they're installed into at /home/keepy/bin. It's also
# published as the image's "base" tag, for images of binaries built
# elsewhere, as by mkctr, that run as the official image does. It's
# Debian, as perkeepd needs glibc's dynamic loader even built without cgo
# (for the HEIC and AVIF decoders' optional system libraries).
FROM debian:trixie-slim AS base

ENV HOME=/home/keepy
ENV PATH=/home/keepy/bin:$PATH
ENV PK_IN_CONTAINER=1

# perkeepd decodes the stills that the web UI shows videos with itself
# for HEVC videos (as iPhones record), but uses ffmpeg for others, such as
# H.264 ones, and WebM or WMV.
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates ffmpeg && \
    rm -rf /var/lib/apt/lists/* && \
    ffmpeg -hide_banner -version > /dev/null && \
    mkdir -p /home/keepy/bin

EXPOSE 80 443 3179 8080

WORKDIR /home/keepy

###########################################################################

FROM base

COPY --from=pkbuild /out/ /home/keepy/bin/

# Check that perkeepd decodes HEIC images. The binaries are built without
# cgo; JPEG thumbnailing and HEIC decoding are pure Go.
RUN perkeepd -build-config > /tmp/build-config.json && \
    cat /tmp/build-config.json && \
    grep -q '"heic": "h265"' /tmp/build-config.json && \
    rm /tmp/build-config.json

CMD ["perkeepd"]
