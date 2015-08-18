# Copyright 2015 The Camlistore Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev

# Get Go stable release
WORKDIR /tmp
# TODO(mpl): good enough for now with binary version. switch back to src tarball when go1.5 is out, if needed.
RUN curl -O https://storage.googleapis.com/golang/go1.5rc1.linux-amd64.tar.gz
RUN echo '2abf55effd93f5c2dcef306eaf14d3e7f614a407  go1.5rc1.linux-amd64.tar.gz' | sha1sum -c
RUN tar -C /usr/local -xzf go1.5rc1.linux-amd64.tar.gz

# rebuild Go with cgo disabled so we can build static binaries
#WORKDIR /usr/local/go/src
#RUN CGO_ENABLED=0 GOARCH=amd64 GOOS=linux ./make.bash
