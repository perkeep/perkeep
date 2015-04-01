# Copyright 2015 The Camlistore Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y install curl gcc

# Get Go stable release
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go1.4.2.src.tar.gz
RUN echo '460caac03379f746c473814a65223397e9c9a2f6  go1.4.2.src.tar.gz' | sha1sum -c
RUN tar -C /usr/local -xzf go1.4.2.src.tar.gz

# rebuild Go with cgo disabled so we can build static binaries
WORKDIR /usr/local/go/src
RUN CGO_ENABLED=0 GOARCH=amd64 GOOS=linux ./make.bash
