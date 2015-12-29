# Copyright 2015 The Camlistore Authors.
FROM debian:stable
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install curl gcc
RUN apt-get -y --no-install-recommends install ca-certificates libc6-dev

# Get Go stable release
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go1.5.2.linux-amd64.tar.gz
RUN echo 'cae87ed095e8d94a81871281d35da7829bd1234e  go1.5.2.linux-amd64.tar.gz' | sha1sum -c
RUN tar -C /usr/local -xzf go1.5.2.linux-amd64.tar.gz
