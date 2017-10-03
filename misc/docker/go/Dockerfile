# Copyright 2015 The Camlistore Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev

# Get Go stable release
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go1.9.linux-amd64.tar.gz
RUN echo 'd70eadefce8e160638a9a6db97f7192d8463069ab33138893ad3bf31b0650a79  go1.9.linux-amd64.tar.gz' | sha256sum -c
RUN tar -C /usr/local -xzf go1.9.linux-amd64.tar.gz
