# SONiC-telemetry

## Description
This repository contains implementation for the sonic system telemetry services:
- dial-in mode system telemetry server: `telemetry`
- dial-out mode system telemetry client `dialout_client_cli`

## Getting Started

### Prerequisites

Install __go__ in your system https://golang.org/doc/install. Requires golang1.8+.

## Installing

To install dial-in mode system telemetry server, run

    go get -u github.com/sonic-net/sonic-gnmi/telemetry

To install dial-out mode system telemetry client, run

    go get -u github.com/sonic-net/sonic-gnmi/dialout/dialout_client_cli

There is also a test program dialout_server_cli for collecting data from dial-out mode system telemetry client. _Note_: it is for testing purpose only.

    go get -u github.com/sonic-net/sonic-gnmi/dialout/dialout_server_cli

The binaries will be installed under $GOPATH/bin/, they may be copied to any SONiC switch and run there.

You can also build a debian package and install it:

    git clone https://github.com/sonic-net/sonic-gnmi.git
    pushd sonic-gnmi
    dpkg-buildpackage -rfakeroot -b -us -uc
    popd

## Running
* See [SONiC gRPC telemetry](./doc/grpc_telemetry.md) for how to run dial-in mode system telemetry server
* See [SONiC telemetry in dial-out mode](./doc/dialout.md) for how to run dial-out mode system telemetry client
* See [gNMI Usage Examples](./doc/gNMI_usage_examples.md) for gNMI client usage examples.
* See [gNOI Debug actions](./doc/gnoi_debug.md) for the Debug RPC policy and supported actions.

### Managed gNOI image files

File.Get and File.Stat expose only managed image artifacts under
`/host/sonic-image-exports/images`, not arbitrary host files. This restriction
applies independently of caller role; existing RPC authentication and write
authorization remain required.

Deploy the companion sonic-host-services data package that provisions the
non-login host `sonic-image` account. The gNMI package installs
`/usr/sbin/sonic-image-worker`; both services use the host account's numeric
UID/GID, including when gNMI runs in a container. Missing/invalid credentials
or a missing worker fail closed with an explicit runtime error.

Publish images with File.Put or File.TransferToRemote to a path such as
`/host/sonic-image-exports/images/release/sonic.bin`. Managed Put permissions
default to `0600`; other requested modes are rejected. Names start with an
ASCII letter/digit and contain only letters, digits, dots, underscores and
hyphens (128 characters per component, at most 16 components). Files are
privately staged and atomically published only after a successful transfer
and, for Put, hash verification.

The fixed store is root-controlled; its images/staging directories are
`sonic-image`-owned mode `0700`, and published files are single-link regular
files owned by that account with mode `0600`. Do not manually copy artifacts
into this tree. Workers drop root and supplementary groups before accessing
files and transfer already-open descriptors over a private socket. Actual
kernel traversal permissions and ACLs apply. Symlinks, hardlinks, root-owned
files and unavailable artifacts are denied; directory listings omit denied
children. Missing and inaccessible artifacts have identical read denials.

Authorized legacy upgrade/configuration writes outside this private store
are preserved, but do not become read exports. HTTP transfer hashes are
calculated from incoming bytes, never by reopening a caller-selected host
path as root. The account database and root-controlled mount/store
configuration remain trusted; a hostile local OS root is outside this policy.

## Streaming events
SONiC publishes events of interest via gNMI.
Sample events could be bgp-state change, link-state change.
A sample query URL is as below.
    `gnmi_cli -client_types=gnmi -a 127.0.0.1:50051 -t EVENTS -logtostderr -insecure -v 7 -streaming_type ON_CHANGE -q all -qt s`

The events URL can take the following optional parameters.<br/>
heartbeat:<br/>
    `[heartbeat=<N>]`
<br/>
&nbsp;&nbsp;&nbsp;&nbsp;The SONiC switch publishes periodic hearbeats when there are no events to publish.<br/>
&nbsp;&nbsp;&nbsp;&nbsp;The frequency of the heartbeat can be controlled by this parameter as beat in every N seconds.<br/>

usecache:<br/>
    `[usecache=true/false]`
<br/>
&nbsp;&nbsp;&nbsp;&nbsp;The SONiC switch does offline cache when gNMI client is down.<br/>
&nbsp;&nbsp;&nbsp;&nbsp;The cached events are delivered upon nest gNMI connection<br/>
&nbsp;&nbsp;&nbsp;&nbsp;Only one client should use the cache service<br/>
&nbsp;&nbsp;&nbsp;&nbsp;The cached events are delivered upon nest gNMI connection<br/>
&nbsp;&nbsp;&nbsp;&nbsp;If you are running test clients, use this param to turn off cache use.<br/>

Sample URL: <br/>
`gnmi_cli -client_types=gnmi -a 127.0.0.1:50051 -t EVENTS -logtostderr -insecure -v 7 -streaming_type ON_CHANGE -q all[heartbeat=5][usecache=false] -qt s`

## gnmi_cli is updated with following args
### To receive events to a file:
Add `-output_file=<file>`

### To filter out for specific event:
Add `-expected_event=<event module:event tag>`

e.g. `-expected_event=sonic-events-bgp:bgp-state`

### To read N events and exit
Add `-expected_count <N>`

### To run with a max timeout:
Add `-streaming_timeout 10`

The following cmd will run the tool until it receives 5 test events or timeout upon 10s, whichever is earlier and receives events are written into file "/tmp/ee".

`gnmi_cli -client_types=gnmi -a 127.0.0.1:50051 -t EVENTS -logtostderr -insecure -v 7 -qt s -q all[heartbeat=5] -streaming_type ON_CHANGE -output_file=/tmp/ee -expected_event=":test-tag" -expected_count 5 -streaming_timeout 10`

## Need Help?

For general questions, setup help, or troubleshooting:
- [sonicproject on Google Groups](https://groups.google.com/d/forum/sonicproject)

For bug reports or feature requests, please open an Issue.

## Contribution guide

See the [contributors guide](https://github.com/Azure/SONiC/blob/gh-pages/CONTRIBUTING.md) for information about how to contribute.

### GitHub Workflow

We're following basic GitHub Flow. If you have no idea what we're talking about, check out [GitHub's official guide](https://guides.github.com/introduction/flow/). Note that merge is only performed by the repository maintainer.

Guide for performing commits:

* Isolate each commit to one component/bugfix/issue/feature
* Use a standard commit message format:

>     [component/folder touched]: Description intent of your changes
>
>     [List of changes]
>
> 	  Signed-off-by: Your Name your@email.com

For example:

>     swss-common: Stabilize the ConsumerTable
>
>     * Fixing autoreconf
>     * Fixing unit-tests by adding checkers and initialize the DB before start
>     * Adding the ability to select from multiple channels
>     * Health-Monitor - The idea of the patch is that if something went wrong with the notification channel,
>       we will have the option to know about it (Query the LLEN table length).
>
>       Signed-off-by: user@dev.null


* Each developer should fork this repository and [add the team as a Contributor](https://help.github.com/articles/adding-collaborators-to-a-personal-repository)
* Push your changes to your private fork and do "pull-request" to this repository
* Use a pull request to do code review
* Use issues to keep track of what is going on
