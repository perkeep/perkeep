# Copyright 2014 The Perkeep Authors.
# Generic purpose Perkeep image, that builds the server (perkeepd)
# and the command-line clients (pk, pk-put, pk-get, and pk-mount).

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

FROM debian:trixie-slim

ENV HOME=/home/keepy
ENV PATH=/home/keepy/bin:$PATH
ENV PK_IN_CONTAINER=1

RUN apt-get update && apt-get install -y imagemagick libjpeg-turbo-progs libheif-dev libheif1 ca-certificates && rm -rf /var/lib/apt/lists/*

COPY --from=pkbuild /out/ /home/keepy/bin/

# Check that perkeepd uses the image's djpeg and libheif. The binaries
# are built without cgo, which leaves them dynamically linked (needed
# to load libheif) only because github.com/perkeep/heic uses purego.
# That package never loads libheif on 32-bit arm, so arm/v7 decodes
# HEIC with its slower embedded WebAssembly build of libheif.
ARG TARGETARCH
RUN perkeepd -build-config > /tmp/build-config.json && \
    cat /tmp/build-config.json && \
    grep -q '"djpeg": "/' /tmp/build-config.json && \
    { [ "$TARGETARCH" = arm ] || grep -q '"heic": "libheif"' /tmp/build-config.json; } && \
    rm /tmp/build-config.json

EXPOSE 80 443 3179 8080

WORKDIR /home/keepy
CMD ["perkeepd"]
