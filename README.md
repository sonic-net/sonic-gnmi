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

### Running
* See [SONiC gRPC telemetry](./doc/grpc_telemetry.md) for how to run dial-in mode system telemetry server
* See [SONiC telemetry in dial-out mode](./doc/dialout.md) for how to run dial-out mode system telemetry client
* See [gNMI Usage Examples](./doc/gNMI_usage_examples.md) for gNMI client usage examples.

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
