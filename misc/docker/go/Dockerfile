# Copyright 2015 The Camlistore Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev

# Get Go stable release
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go1.7.linux-amd64.tar.gz
RUN echo '702ad90f705365227e902b42d91dd1a40e48ca7f67a2f4b2fd052aaa4295cd95  go1.7.linux-amd64.tar.gz' | sha256sum -c
RUN tar -C /usr/local -xzf go1.7.linux-amd64.tar.gz
