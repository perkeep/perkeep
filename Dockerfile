# Copyright 2014 The Perkeep Authors.
# Generic purpose Perkeep image, that builds the server (perkeepd)
# and the command-line clients (pk, pk-put, camget, and pk-mount).

# Use misc/docker/go to generate perkeep/go
FROM perkeep/go

MAINTAINER camlistore <camlistore@googlegroups.com>

ENV DEBIAN_FRONTEND noninteractive
RUN apt-get -y --no-install-recommends install adduser

RUN adduser --disabled-password --quiet --gecos Camli camli
RUN mkdir -p /gopath/bin
RUN chown camli.camli /gopath/bin
RUN mkdir -p /gopath/pkg
RUN chown camli.camli /gopath/pkg

RUN mkdir -p /gopath/src
ADD internal /gopath/src/perkeep.org/internal
ADD app /gopath/src/perkeep.org/app
ADD dev /gopath/src/perkeep.org/dev
ADD cmd /gopath/src/perkeep.org/cmd
ADD vendor /gopath/src/perkeep.org/vendor
ADD server /gopath/src/perkeep.org/server
ADD pkg /gopath/src/perkeep.org/pkg
RUN mkdir -p /gopath/src/perkeep.org/clients
ADD clients/web /gopath/src/perkeep.org/clients/web
ADD make.go /gopath/src/perkeep.org/make.go
RUN echo 'dev' > /gopath/src/perkeep.org/VERSION

ENV GOROOT /usr/local/go
ENV PATH $GOROOT/bin:/gopath/bin:$PATH
ENV GOPATH /gopath
ENV CGO_ENABLED 0
ENV CAMLI_GOPHERJS_GOROOT /usr/local/go

WORKDIR /gopath/src/perkeep.org
RUN go run make.go
RUN cp -a /gopath/src/perkeep.org/bin/* /gopath/bin/

ENV USER camli
ENV HOME /home/camli
WORKDIR /home/camli

EXPOSE 80 443 3179 8080

CMD /bin/bash
