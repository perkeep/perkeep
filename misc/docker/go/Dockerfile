# Copyright 2015 The Camlistore Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev

# Get Go stable release
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go1.6.2.linux-amd64.tar.gz
RUN echo 'e40c36ae71756198478624ed1bb4ce17597b3c19d243f3f0899bb5740d56212a  go1.6.2.linux-amd64.tar.gz' | sha256sum -c
RUN tar -C /usr/local -xzf go1.6.2.linux-amd64.tar.gz
