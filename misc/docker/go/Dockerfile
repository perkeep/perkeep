# Copyright 2015 The Camlistore Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev

# Get Go stable release
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go1.6.1.linux-amd64.tar.gz
RUN echo '6d894da8b4ad3f7f6c295db0d73ccc3646bce630e1c43e662a0120681d47e988  go1.6.1.linux-amd64.tar.gz' | sha256sum -c
RUN tar -C /usr/local -xzf go1.6.1.linux-amd64.tar.gz
