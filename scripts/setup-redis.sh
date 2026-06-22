#!/bin/sh
# Configure redis-server for sonic-gnmi tests: enable the unix socket, make it
# world-accessible, and rename it to redis.sock. Used by BOTH the ADO test
# templates and dev/run-tests.sh — keep this as the single source of truth.
#
# NOTE: this script intentionally does NOT run `apt-get update`. Callers that need
# a fresh package index (the ADO path) must run it themselves before calling.
set -e
sudo apt-get install -y --no-install-recommends redis-server
sudo sed -Ei 's/^# unixsocket/unixsocket/' /etc/redis/redis.conf
sudo sed -Ei 's/^unixsocketperm .../unixsocketperm 777/' /etc/redis/redis.conf
sudo sed -Ei 's/redis-server.sock/redis.sock/' /etc/redis/redis.conf
sudo service redis-server start
