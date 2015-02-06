FROM ubuntu:trusty
RUN apt-get update
RUN apt-get -y upgrade
RUN apt-get -y install libjpeg-turbo-progs
RUN cp -a /usr/share/zoneinfo /zoneinfo
ENV DEBIAN_FRONTEND noninteractive
RUN echo 'Yes, do as I say!' | apt-get remove --force-yes perl perl-modules \
  python3-minimal python3.4 python3.4-minimal upstart bzip2 logrotate apt mount \
  readline-common libblkid1 libstdc++6 locales udev libssl1.0.0 libapt-pkg4.12 xkb-data \
  libmagic1 isc-dhcp-client isc-dhcp-common libslang2 iproute2 libgnutls26 vim-tiny \
  libsqlite3-0 findutils libapt-inst1.5 net-tools apt-utils adduser libgcrypt11 ncurses-bin \
  libtinfo5 plymouth gpgv diffutils e2fslibs libexpat1 klibc-utils libdbus-1-3 libncursesw5 \
  ncurses-base initramfs-tools busybox-initramfs libsepol1 vim-common libp11-kit0 cpio \
  dh-python libpng12-0 sed libplymouth2 libdevmapper1.02.1 libncurses5 less kmod \
  libgnutls-openssl27 libustr-1.0-1 libsemanage1 mountall libmpdec2 base-passwd gzip \
  gcc-4.8-base bsdutils libnewt0.52 insserv iputils-ping libnih1 libcgmanager0 libbsd0 bash

# Because we can't remove perl-base without removing PAM stuff, which itself isn't willing to be removed with apt or dpkg.
RUN rm -rf /usr/share/doc/perl /usr/share/perl /usr/lib/libperl.so.5.18.2 /usr/lib/perl /usr/bin/perl*
RUN rm -rf /var/lib/apt /var/cache/apt /var/cache/debconf
RUN mv /zoneinfo /usr/share/zoneinfo
# Convenient default for testing the image, especially if one forgets that /bin/bash is gone.
CMD ["/bin/sh"]
