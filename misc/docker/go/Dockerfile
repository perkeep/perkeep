# Copyright 2015 The Perkeep Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev
RUN apt-get -y --no-install-recommends install git

# Get Go stable release
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go1.13.1.linux-amd64.tar.gz
RUN echo '94f874037b82ea5353f4061e543681a0e79657f787437974214629af8407d124  go1.13.1.linux-amd64.tar.gz' | sha256sum -c
RUN tar -C /usr/local -xzf go1.13.1.linux-amd64.tar.gz
