# Copyright 2018 The Perkeep Authors.
# Licensed under the Apache License, Version 2.0

FROM buildpack-deps:stretch-scm AS pkbuild

MAINTAINER Perkeep Authors <perkeep@googlegroups.com>

ENV DEBIAN_FRONTEND noninteractive

# gcc for cgo, sqlite
RUN apt-get update && apt-get install -y --no-install-recommends \
		g++ \
		gcc \
		libc6-dev \
		make \
		pkg-config \
        libsqlite3-dev

ENV GOLANG_VERSION 1.10.2
ARG perkeep_version=8b537a66307cf41a659786f1a898c77b46303601

WORKDIR /usr/local
RUN wget -O go.tgz https://dl.google.com/go/go$GOLANG_VERSION.linux-amd64.tar.gz
RUN echo "4b677d698c65370afa33757b6954ade60347aaca310ea92a63ed717d7cb0c2ff go.tgz" | sha256sum -c -
RUN tar -zxvf go.tgz

ENV GOROOT /usr/local/go
ENV GOPATH /go
ENV PATH $GOPATH/bin:/usr/local/go/bin:$PATH

RUN mkdir -p "$GOPATH/src" "$GOPATH/bin" && chmod -R 777 "$GOPATH"
WORKDIR $GOPATH/src
RUN git clone https://perkeep.googlesource.com/perkeep perkeep.org
WORKDIR $GOPATH/src/perkeep.org
RUN git reset --hard $perkeep_version

ARG goarch=amd64
RUN go run make.go -v -arch=$goarch -arm=5

#stage 2

FROM ubuntu:16.04
ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade
RUN apt-get -y --no-install-recommends install ca-certificates git python3 xz-utils udev vim

RUN mkdir -p /toolkit
WORKDIR /toolkit
RUN git clone https://github.com/SynologyOpenSource/pkgscripts-ng pkgscripts
WORKDIR /toolkit/pkgscripts
RUN git reset --hard 86409bbab301428b893bc3d099ba8ba29f22137d

ARG dsm=6.2
ARG arch=x64
ENV BUILD_ENV ds.$arch-$dsm
RUN echo "Preparing to build for: $BUILD_ENV"
RUN ./EnvDeploy -v $dsm -p $arch
WORKDIR /toolkit/build_env/$BUILD_ENV

WORKDIR /toolkit
ARG perkeep_version=8b537a66307cf41a659786f1a898c77b46303601
RUN mkdir -p source
WORKDIR /toolkit/source
ADD perkeep perkeep
RUN sed -i s:version=SET_BY_DOCKER_BUILD:version=\"$perkeep_version\": perkeep/INFO.sh

ARG gobin=/go/bin
COPY --from=pkbuild $gobin/pk* /toolkit/build_env/$BUILD_ENV/root/bin/
COPY --from=pkbuild $gobin/perkeepd /toolkit/build_env/$BUILD_ENV/root/bin/

WORKDIR /toolkit
