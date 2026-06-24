#!/bin/sh
# Configure redis-server for sonic-gnmi tests: refresh the apt index, enable the
# unix socket, make it world-accessible, and rename it to redis.sock. Used by BOTH
# the ADO test templates and dev/run-tests.sh — keep this as the single source of truth.
set -e
sudo apt-get update
sudo apt-get install -y redis-server
sudo sed -ri 's/^# unixsocket/unixsocket/' /etc/redis/redis.conf
sudo sed -ri 's/^unixsocketperm .../unixsocketperm 777/' /etc/redis/redis.conf
sudo sed -ri 's/redis-server.sock/redis.sock/' /etc/redis/redis.conf
sudo service redis-server start
