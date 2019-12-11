# Copyright 2015 The Perkeep Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev
RUN apt-get -y --no-install-recommends install git

# Get Go stable release
ENV GOLANG_VERSION 1.13.5
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go${GOLANG_VERSION}.linux-amd64.tar.gz
RUN echo '512103d7ad296467814a6e3f635631bd35574cab3369a97a323c9a585ccaa569  go'${GOLANG_VERSION}'.linux-amd64.tar.gz' | sha256sum -c
RUN tar -C /usr/local -xzf go${GOLANG_VERSION}.linux-amd64.tar.gz
