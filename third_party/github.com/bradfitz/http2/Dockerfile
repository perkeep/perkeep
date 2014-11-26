#
# This Dockerfile builds a recent curl with HTTP/2 client support, using
# a recent nghttp2 build.
#
# See the Makefile for how to tag it. If Docker and that image is found, the
# Go tests use this curl binary for integration tests.
#

FROM ubuntu:trusty

RUN apt-get update && \
    apt-get upgrade -y && \
    apt-get install -y git-core build-essential wget

RUN apt-get install -y --no-install-recommends autotools-dev libtool pkg-config zlib1g-dev libcunit1-dev libssl-dev libxml2-dev libevent-dev

RUN cd /root && git clone https://github.com/tatsuhiro-t/nghttp2.git

RUN apt-get install -y --no-install-recommends automake autoconf

WORKDIR /root/nghttp2
RUN autoreconf -i
RUN automake
RUN autoconf
RUN ./configure
RUN make
RUN make install

WORKDIR /root
RUN wget http://curl.haxx.se/download/curl-7.38.0.tar.gz
RUN tar -zxvf curl-7.38.0.tar.gz
WORKDIR /root/curl-7.38.0
ADD testdata/curl-http2-eof.patch /tmp/curl-http2-eof.patch
RUN patch -p1 < /tmp/curl-http2-eof.patch
RUN ./configure --with-ssl --with-nghttp2=/usr/local
RUN make
RUN make install
RUN ldconfig

CMD ["-h"]
ENTRYPOINT ["/usr/local/bin/curl"]

