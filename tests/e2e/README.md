# Packaged telemetry authentication E2E

The existing Azure amd64 Package job runs this suite after building and publishing
its `.deb`. The tests execute **that package's `/usr/sbin/telemetry`**, never
`build/bin/telemetry`, a new Go test server or substituted authentication functions.

## Seven cases

| Source | Case | Assertion |
|---|---|---|
| sonic-mgmt [#28970](https://github.com/sonic-net/sonic-mgmt/pull/28970), `a58cc001a340c1d2ae036026e57dd1d957aa7afd` | CONFIG_DB readonly/readwrite Get | Actual `Private` value from seeded Redis; stronger than source helper's call-only check |
| Same PR | Valid TLS certificate, unmapped CN, direct Get | Exact `Unauthenticated`; deliberately tests Get rather than source's Capabilities |
| sonic-mgmt [#28267](https://github.com/sonic-net/sonic-mgmt/pull/28267), `9b08581c28ebc6dbefc93d3dc8bcd08e2b73f11f` | readonly/readwrite × File Put/Remove | Exact role denial and unchanged file/no added files, or actual replacement/removal |

This profile preserves the behavior qualified on merged #881. New authorization
policy changes must review expected RPC/role outcomes explicitly; the suite never
learns expected responses from the binary. The actual checkout SHA is recorded,
not restricted to an old commit. Seven is the required first-slice inventory, not
full authentication coverage. CRL, stream reauthorization, password/JWT, host AAA,
ordinary CONFIG_DB writes through DBus, and image operations remain out of scope.

## Run and isolation

From the repository root **inside an owned, disposable SONiC build container**:

```sh
sudo -H env -i HOME=/root PATH="$PATH" bash tests/e2e/run.sh /path/to/produced/debs
```

The caller supplies the same revision's native dependencies/YANG build outputs
and sibling sonic-mgmt-common/sonic-swss-common checkouts, as the Package job does.
Exactly one `sonic-gnmi_*.deb` with Package=sonic-gnmi and Architecture=amd64 must
exist. Missing packages/dependencies fail; no fallback binary is accepted.

The job runs this step sequentially, with no other service test sharing its
container. The fixture refuses an existing `/mnt/host`, default Redis socket or
Redis TCP listener. It owns a private temporary Redis socket, generated certificate
directories and synthetic `/mnt/host/tmp` files, and restores the original native
DB configuration bytes/mode. Telemetry binds only loopback, with UDS disabled.
Do not invoke on a DUT, host, or existing sonic-mgmt runner. `/.dockerenv` alone
does not establish ownership. CI reuses its package container; no nested Docker
runtime distribution or new service connection is introduced.

An independent control certificate/CN performs readiness. Case identities issue
only their target RPCs, so preliminary Capabilities cannot satisfy a negative
test. Real CA chains, native SWSS/Redis mapping and production File handlers run.
Clients close before telemetry/Redis; cleanup failure makes the job fail. Each
RPC has a deadline, pytest has a ten-minute bound, and the CI step has fifteen.
The disposable job container remains the outer cleanup boundary for cancellation.

Root pytest excludes `tests/e2e` to preserve existing Go-harness discovery. The
entrypoint selects its own pytest config and `--confcutdir`, avoiding integration
fixtures. Only the exact seven nodes and all21 setup/call/teardown passes qualify.
Skipped tests, empty selection, missing JUnit or failed cleanup fail verification.

## Inputs and evidence

`bootstrap.py` reuses the real sonic-mgmt certificate generator at
`4c06cfad89168fc52e8186fa84e609146e3a53ec`, verified by SHA256
`3b2521e5f13f091e0c77c55e1175a055689cd04b2c72def6cf8193bc6ed377be`.
The source remains upstream with its license. Public gNOI stubs are generated from
the product build's pinned module; import filenames alone are adapted to avoid
Python's `types` module collision. gNMI public stubs come from pygnmi0.8.15.

`test-results/auth-e2e/` contains JUnit, phase JSONL, package metadata/hash,
executable and running `/proc/PID/exe` hash, checkout/sibling revisions, dependency
versions/library hashes, runtime arguments and cleanup evidence. CI publishes
these even after a failure. No private keys, passwords, JWTs or full environment
dumps are archived. Product source SHA for PR builds may be a synthetic merge
revision; do not relabel it as the source-branch commit. This job inherits the
existing pipeline's fork approval and credential policies; it adds no privileged
cross-repository trigger and does not bypass those policies.
