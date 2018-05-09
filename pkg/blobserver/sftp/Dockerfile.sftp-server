# For debugging sftp-server crashes.
# https://twitter.com/bradfitz/status/994307991712104448
# https://twitter.com/bradfitz/status/994317057381449728

# docker build -f Dockerfile.sftp-server  -t openssh .
# docker run -p 1150:115 openssh
# Then an integration JSON file like:
#    {"user": "RAWSFTPNOSSH", "dir": ".", "addr": "localhost:1150"}


FROM debian:jessie

ENV DEBIAN_FRONTEND noninteractive

RUN apt-get update && apt-get install --no-install-recommends --yes autoconf automake gcc libc6-dev \
    curl ca-certificates zlib1g-dev libssl-dev make
RUN apt-get install --no-install-recommends --yes make

# Synology NAS's crashing version; https://twitter.com/bradfitz/status/994317057381449728
ARG opensshver=6.8p1

WORKDIR /root
RUN curl -O https://cloudflare.cdn.openbsd.org/pub/OpenBSD/OpenSSH/portable/openssh-$opensshver.tar.gz
RUN tar -zxvf openssh-$opensshver.tar.gz

WORKDIR openssh-$opensshver

RUN ./configure --without-openssl-header-check
RUN make
RUN make install

RUN apt-get install --no-install-recommends --yes inetutils-inetd

RUN mkdir /tmp/sftp-root
RUN echo "sftp stream tcp nowait  root /usr/local/libexec/sftp-server -e -l DEBUG3 -d /tmp/sftp-root" >> /etc/inetd.conf
CMD ["/usr/sbin/inetutils-inetd", "-d"]
