# Copyright 2014 The Perkeep Authors.
# Generic purpose Perkeep image, that builds the server (perkeepd)
# and the command-line clients (pk, pk-put, pk-get, and pk-mount).

FROM golang:1.25 AS pkbuild

LABEL maintainer="Perkeep Authors perkeep@googlegroups.com"

ENV DEBIAN_FRONTEND=noninteractive

WORKDIR /go/src/perkeep.org

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go run make.go -v

###########################################################################

FROM debian:trixie-slim

ENV HOME=/home/keepy
ENV PATH=/home/keepy/bin:$PATH
ENV PK_IN_CONTAINER=1

RUN apt-get update && apt-get install -y imagemagick libjpeg-turbo-progs && rm -rf /var/lib/apt/lists/*

COPY --from=pkbuild /go/bin/pk* /home/keepy/bin/
COPY --from=pkbuild /go/bin/perkeepd /home/keepy/bin/

EXPOSE 80 443 3179 8080

WORKDIR /home/keepy
CMD ["perkeepd"]
