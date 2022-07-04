# Copyright 2014 The Perkeep Authors.
# Generic purpose Perkeep image, that builds the server (perkeepd)
# and the command-line clients (pk, pk-put, pk-get, and pk-mount).

# TODO: add the HEIC-supporting imagemagick to this Dockerfile too, in
# some way that's minimally gross and not duplicating
# misc/docker/heiftojpeg's Dockerfile entirely. Not decided best way.
# TODO: likewise, djpeg binary? maybe. https://perkeep.org/issue/1142

FROM golang:1.18 AS pkbuild

MAINTAINER Perkeep Authors <perkeep@googlegroups.com>

ENV DEBIAN_FRONTEND noninteractive

WORKDIR /go/src/perkeep.org

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go run make.go -v



FROM gcr.io/distroless/base

ENV HOME /home/keepy
ENV PATH /home/keepy/bin:$PATH

COPY --from=pkbuild /go/bin/pk* /home/keepy/bin/
COPY --from=pkbuild /go/bin/perkeepd /home/keepy/bin/

EXPOSE 80 443 3179 8080

WORKDIR /home/keepy
CMD ["perkeepd"]
