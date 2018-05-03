# Copyright 2015 The Perkeep Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev
RUN apt-get -y --no-install-recommends install git

# Get Go stable release
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go1.10.2.linux-amd64.tar.gz
RUN echo '4b677d698c65370afa33757b6954ade60347aaca310ea92a63ed717d7cb0c2ff  go1.10.2.linux-amd64.tar.gz' | sha256sum -c
RUN tar -C /usr/local -xzf go1.10.2.linux-amd64.tar.gz
