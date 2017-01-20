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

# Adding Go 1.7 as well for gopherjs. Not all our docker images need to do a full
# 'go run make.go', but I feel maintaining two different go docker images would
# not be worth the trouble. We can just remove that as soon as gopherjs supports
# Go 1.8.
WORKDIR /tmp
RUN curl -O https://storage.googleapis.com/golang/go1.7.4.linux-amd64.tar.gz
RUN echo '47fda42e46b4c3ec93fa5d4d4cc6a748aa3f9411a2a2b7e08e3a6d80d753ec8b  go1.7.4.linux-amd64.tar.gz' | sha256sum -c
RUN tar -xzf go1.7.4.linux-amd64.tar.gz
RUN mv go /usr/local/go1.7
