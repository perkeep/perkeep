# Copyright 2016 The Camlistore Authors.
FROM debian:jessie

ENV DEBIAN_FRONTEND noninteractive

RUN apt-get update && apt-get install -yqq git ruby ruby-builder ruby-thor
WORKDIR /usr/local/src/github.com/jubos
RUN git clone https://github.com/jubos/fake-s3.git
WORKDIR fake-s3
RUN git reset --hard 8f7ba5512acba8072654dc7d8964a9a5bebce8a9

RUN mkdir -p /fakes3_root
ENTRYPOINT ["/usr/local/src/github.com/jubos/fake-s3/bin/fakes3"]
CMD ["-r",  "/fakes3_root", "-p",  "4567"]
EXPOSE 4567
