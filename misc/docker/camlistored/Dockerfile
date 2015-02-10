FROM djpeg-squashed
MAINTAINER Brad Fitzpatrick <brad@danga.com>
#TODO(mpl): build camlistored within container.
ADD camlistored /camlistored
EXPOSE 80 443
ENTRYPOINT ["/camlistored"]
