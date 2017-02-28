# Copyright 2014 The Camlistore Authors.
# Generic purpose Camlistore image, that builds the server (camlistored)
# and the command-line clients (camput, camget, camtool, and cammount).

# See misc/docker/go to generate camlistore/go
FROM camlistore/go

MAINTAINER camlistore <camlistore@googlegroups.com>

ENV DEBIAN_FRONTEND noninteractive
RUN apt-get -y --no-install-recommends install adduser

RUN adduser --disabled-password --quiet --gecos Camli camli
RUN mkdir -p /gopath/bin
RUN chown camli.camli /gopath/bin
RUN mkdir -p /gopath/pkg
RUN chown camli.camli /gopath/pkg

RUN mkdir -p /gopath/src
ADD internal /gopath/src/camlistore.org/internal
ADD app /gopath/src/camlistore.org/app
ADD dev /gopath/src/camlistore.org/dev
ADD cmd /gopath/src/camlistore.org/cmd
ADD vendor /gopath/src/camlistore.org/vendor
ADD server /gopath/src/camlistore.org/server
ADD pkg /gopath/src/camlistore.org/pkg
ADD make.go /gopath/src/camlistore.org/make.go
RUN echo 'dev' > /gopath/src/camlistore.org/VERSION

ENV GOROOT /usr/local/go
ENV PATH $GOROOT/bin:/gopath/bin:$PATH
ENV GOPATH /gopath
ENV CGO_ENABLED 0
ENV CAMLI_GOPHERJS_GOROOT /usr/local/go

WORKDIR /gopath/src/camlistore.org
RUN go run make.go
RUN cp -a /gopath/src/camlistore.org/bin/* /gopath/bin/

ENV USER camli
ENV HOME /home/camli
WORKDIR /home/camli

EXPOSE 80 443 3179 8080

CMD /bin/bash
