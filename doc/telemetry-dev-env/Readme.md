Goto doc/telemetry-dev-env
Build the image:
```
docker build -t telemetry-dev .
```

Remove container if it exist already:
```
docker rm telemetry-dev-env
```
Create personal fork repo from sonic-telemetry from git

Create directory '~/src' and Clone git repos in it:
```
git clone https://<your_github_username>@github.com/<your_github_username>/sonic-telemetry.git 
git clone https://<your_github_username>@github.com/<your_github_username>/sonic-mgmt-common.git
```

Create a new container using the image:
```
docker run -it -v ~/src:/src --net=host --name telemetry-dev-env telemetry-dev
```

Build and start service (Inside container):
```bash

# Build sources
cd /src/sonic-mgmt-common && make
cd /src/sonic-telemetry && make all

# Bootstrap DB. dump.rdb provided under files/ is empty. Please replace empty rdb file and then restart redis.
mkdir -p /var/run/redis/sonic-db && chown redis  /var/run/redis/sonic-db && install testdata/database_config.json -t /var/run/redis/sonic-db
service redis-server stop && cp /files/dump.rdb /var/lib/redis/dump.rdb && service redis-server start

# Start the service
./build/bin/telemetry --port 8080 -insecure --allow_no_client_auth --logtostderr  -v 10
```

Make a client query (Inside container):
```bash

# Make another connection to container
docker exec -it telemetry-dev-env bash

# Make a query
/src/sonic-telemetry/build/bin/gnmi_cli -client_types=gnmi -a 127.0.0.1:8080 -t OTHERS -logtostderr -insecure -qt p -pi 10s -q proc/loadavg
```
