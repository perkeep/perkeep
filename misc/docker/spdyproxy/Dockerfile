# This container runs nginx as a SPDY proxy to Camlistore on GCE.
# Run it with --link <camlistore container>:camlistored.
FROM debian

ENV DEBIAN_FRONTEND noninteractive

# Get the latest nginx
RUN apt-key adv --keyserver pgp.mit.edu --recv-keys 573BFD6B3D8FBC641079A6ABABF5BD827BD9BF62
RUN echo "deb http://nginx.org/packages/mainline/debian/ wheezy nginx" >> /etc/apt/sources.list

RUN apt-get update
RUN apt-get -y upgrade
RUN apt-get -y install nginx ca-certificates

ADD nginx.conf /etc/nginx/nginx.conf
ADD runginx  /runginx

EXPOSE 80 443

CMD ["/runginx"]
