FROM ubuntu:18.04

# fundemantals
RUN apt-get update
RUN apt-get install -y wget
RUN apt-get install -y ssh xinetd rsync patch
RUN apt-get install -y iproute2
RUN apt-get install -y gcc
RUN apt-get install -y cmake
RUN apt-get install -y make
RUN apt-get install -y openssh-server
RUN apt-get install -y python3 python3-pip
RUN apt-get install -y tmux

# install go and configure
RUN wget https://golang.org/dl/go1.14.14.linux-amd64.tar.gz -O /tmp/go1.14.14.linux-amd64.tar.gz
RUN tar -C /usr/local -xzf /tmp/go1.14.14.linux-amd64.tar.gz
RUN mkdir /usr/gopath
ENV GOPATH="/usr/gopath"
ENV GOROOT="/usr/local/go"
ENV PATH="${GOPATH}/bin:${GOROOT}/bin:${PATH}"

# Dev libs required for libyang
RUN apt-get install -y bison
RUN apt-get install -y flex
RUN apt-get install -y libpcre3 libpcre3-dev

# Download libyang and install.
RUN pip3 install pyang
RUN wget https://github.com/CESNET/libyang/archive/v1.0.184.tar.gz -O /tmp/libyang-v1.0.184.tar.gz
RUN tar -C /tmp -xzf /tmp/libyang-v1.0.184.tar.gz
RUN mkdir /tmp/libyang-1.0.184/build && cd /tmp/libyang-1.0.184/build && cmake ..  && make && make install

RUN apt-get install -y redis

RUN sed -i "s/bind .*/bind 127.0.0.1/g" /etc/redis/redis.conf
RUN echo "unixsocket /var/run/redis/redis.sock" >> /etc/redis/redis.conf
COPY files/* /tmp

# SSH settings
EXPOSE 22/tcp
# Following will expose 6379 port. Not needed except dugging.
EXPOSE 6379/tcp

ENTRYPOINT service ssh restart && bash
