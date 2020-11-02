# Copyright 2015 The Perkeep Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev
RUN apt-get -y --no-install-recommends install git

# Get Go stable release
ENV GOLANG_VERSION 1.15.3
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go${GOLANG_VERSION}.linux-amd64.tar.gz
RUN echo '010a88df924a81ec21b293b5da8f9b11c176d27c0ee3962dc1738d2352d3c02d  go'${GOLANG_VERSION}'.linux-amd64.tar.gz' | sha256sum -c
RUN tar -C /usr/local -xzf go${GOLANG_VERSION}.linux-amd64.tar.gz
