# Copyright 2015 The Perkeep Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev
RUN apt-get -y --no-install-recommends install git

# Get Go stable release
ENV GOLANG_VERSION 1.16.6
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go${GOLANG_VERSION}.linux-amd64.tar.gz
RUN echo 'be333ef18b3016e9d7cb7b1ff1fdb0cac800ca0be4cf2290fe613b3d069dfe0d  go'${GOLANG_VERSION}'.linux-amd64.tar.gz' | sha256sum -c
RUN tar -C /usr/local -xzf go${GOLANG_VERSION}.linux-amd64.tar.gz
