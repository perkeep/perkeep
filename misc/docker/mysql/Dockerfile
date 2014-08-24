FROM debian

ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update
RUN apt-get -y upgrade
RUN apt-get -y install mysql-server-core-5.5 mysql-server-5.5

ADD run-mysqld /run-mysqld

EXPOSE 3306

VOLUME ["/mysql"]

CMD ["/run-mysqld"]
