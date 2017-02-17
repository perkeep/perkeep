# Copyright 2015 The Camlistore Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev

# Get Go stable release
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go1.8.linux-amd64.tar.gz
RUN echo '53ab94104ee3923e228a2cb2116e5e462ad3ebaeea06ff04463479d7f12d27ca  go1.8.linux-amd64.tar.gz' | sha256sum -c
RUN tar -C /usr/local -xzf go1.8.linux-amd64.tar.gz
