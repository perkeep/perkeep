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

# libheif decodes HEVC with libde265, and trixie's libde265 (1.0.15)
# decodes HEVC 4:4:4 with corrupt chroma. iOS saves edited photos as such
# HEICs. libde265 1.0.16 fixed it (upstream commit 9959d27, "fix decoding
# of 32x32 chroma TUs"), so install a later libde265, of the same soname,
# from Debian testing. It's fetched from Debian's snapshot archive, at a
# fixed version and checksum, so builds don't change as testing does.
# Bump it, or drop this once trixie has the fix, deliberately.
FROM scratch AS libde265
ADD --checksum=sha256:7b7fc4f5d17c726abd320db4c0a3102d89bc74ca6a15810f9a0b03a73fce5b45 \
    https://snapshot.debian.org/archive/debian/20260920T203924Z/pool/main/libd/libde265/libde265-0_1.1.3-1+b1_amd64.deb \
    /amd64.deb
ADD --checksum=sha256:cac60078299b8fee2a43bcfc043026d5f6ecdd0a681bdbbe4b3e913523bb0a06 \
    https://snapshot.debian.org/archive/debian/20260920T203924Z/pool/main/libd/libde265/libde265-0_1.1.3-1+b1_arm64.deb \
    /arm64.deb

###########################################################################

FROM debian:trixie-slim

ENV HOME=/home/keepy
ENV PATH=/home/keepy/bin:$PATH
ENV PK_IN_CONTAINER=1

ARG TARGETARCH

# github.com/gen2brain/heic loads libheif to decode HEIC images, except
# on 32-bit arm, where it always uses its slower embedded WebAssembly
# decoder, so don't install libheif there. It loads the library as
# "libheif.so", which Debian only ships in libheif-dev, so rather than
# install that and its dependencies, link it to libheif1's libheif.so.1.
# Then replace trixie's libde265 with the later one (see above).
RUN --mount=type=bind,from=libde265,target=/tmp/libde265 \
    apt-get update && \
    apt-get install -y ca-certificates $([ "$TARGETARCH" = arm ] || echo libheif1) && \
    rm -rf /var/lib/apt/lists/* && \
    if [ "$TARGETARCH" != arm ]; then \
        lib=$(ls /usr/lib/*/libheif.so.1) && ln -s libheif.so.1 "${lib%.1}" && \
        dpkg -i /tmp/libde265/$TARGETARCH.deb && \
        [ "$(dpkg-query -W -f '${Version}' libde265-0)" = 1.1.3-1+b1 ]; \
    fi

COPY --from=pkbuild /out/ /home/keepy/bin/

# Check that perkeepd uses libheif, except on 32-bit ARM.
# JPEG thumbnailing is pure Go. The binaries are built without cgo;
# HEIC decoding loads libheif through purego on 64-bit builds and
# uses the embedded WASM decoder on 32-bit ARM.
RUN perkeepd -build-config > /tmp/build-config.json && \
    cat /tmp/build-config.json && \
    { [ "$TARGETARCH" = arm ] || grep -q '"heic": "libheif"' /tmp/build-config.json; } && \
    rm /tmp/build-config.json

EXPOSE 80 443 3179 8080

WORKDIR /home/keepy
CMD ["perkeepd"]
