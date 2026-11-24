# RPC test framework

Small Go helpers for tests that host a service over real TLS/gRPC and use an
isolated Redis process. Extracted from the authentication example in PR #884;
business test cases and CI integration remain in that separate change.

## Components

| Helper | Responsibility |
| --- | --- |
| `NewCertificateAuthority` | Generate an in-memory CA and server/client TLS configurations. |
| `StartServer` | Bind a loopback listener, accept explicit service registration and stop the server at cleanup. |
| `NewClientConn` | Create a TLS gRPC connection and close it at cleanup; callers select their generated client. |
| `NewFixture` | Start an empty private Redis on a Unix socket and stop it at cleanup. |
| `Fixture.Run` | Checkpoint private DB4, run a sequential case, then restore and verify Redis state. |

The framework does not import gNMI production services. The caller constructs and
registers the required service, writes roles and business data, issues RPCs and
asserts results. No readiness RPC, hidden role or device-table seed is installed.

## Lifecycle and isolation

Create the fixture and server in the parent test and use `Fixture.Run` for each
sequential case. Clients and extra resources created inside a case use
`t.Cleanup`; they close before the case's Redis rollback. Group cleanup stops the
server before Redis when the fixture is created first.

Redis checkpoint/rollback uses DUMP/RESTORE for all keys in private DB4. It
verifies logical types and contents, rather than encoding-dependent DUMP bytes.
Strings, hashes, lists, sets, sorted sets and stream entries are supported;
unsupported Redis types fail checkpoint before a case runs. Keys that disappear
during capture are omitted. An expiring key is restored with its checkpoint
remaining TTL (at least 1ms), not its original wall-clock expiration; a key that
expires during verification is allowed to be absent. Permanent keys stay permanent. This is
not a snapshot of service caches, streams, other databases or host files. Cases
must clean those resources explicitly or use a separate environment.

A checkpoint or rollback failure stops the group. An ordinary assertion failure
does not prevent later cases when restoration succeeds. Shared groups must not
call `t.Parallel` or keep writers running during checkpoint/rollback.

Use an owned disposable Linux container for execution. The framework needs
`redis-server` in PATH; it exposes no Redis TCP port. Its Go package can run with
CGO disabled, while consumers importing production SONiC handlers can still
require SWSS/translib and native initialization. Go's outer timeout can prevent
cleanup callbacks, so the runner must dispose the container on abnormal exit.

## Verification

From the repository root, in the isolated environment with dependencies available:

```sh
CGO_ENABLED=0 go test -mod=readonly -tags=e2e -count=1 -timeout=2m ./tests/rpc/testutil
```

The opt-in `e2e` build tag follows the existing example's convention. It does not
make these framework self-tests packaged-product E2E tests. Tests cover empty
Redis startup, restoration and extra-cleanup ordering, abort after rollback
failure, real mutual-TLS RPC dispatch and rejection of an untrusted client.
Regression coverage also exercises disappearing/near-expiry keys, hash encoding
changes, logical-value corruption, registration abort and immediate server cleanup.
