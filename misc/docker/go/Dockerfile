# Copyright 2015 The Camlistore Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev

# Get Go stable release
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go1.8rc2.linux-amd64.tar.gz
RUN echo 'd62c2d44d0c6b434e3cda12505f3c9fb880757e3396af1e9ba861f7b547cc864  go1.8rc2.linux-amd64.tar.gz' | sha256sum -c
RUN tar -C /usr/local -xzf go1.8rc2.linux-amd64.tar.gz
