# Copyright 2015 The Perkeep Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev

# Get Go stable release
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go1.10.1.linux-amd64.tar.gz
RUN echo '72d820dec546752e5a8303b33b009079c15c2390ce76d67cf514991646c6127b  go1.10.1.linux-amd64.tar.gz' | sha256sum -c
RUN tar -C /usr/local -xzf go1.10.1.linux-amd64.tar.gz
