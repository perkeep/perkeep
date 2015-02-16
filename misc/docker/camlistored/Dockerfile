FROM scratch
MAINTAINER Brad Fitzpatrick <brad@danga.com>
#TODO(mpl): build camlistored within container.
ADD camlistored /camlistored
ADD djpeg /usr/bin/djpeg
EXPOSE 80 443
ENTRYPOINT ["/camlistored"]
