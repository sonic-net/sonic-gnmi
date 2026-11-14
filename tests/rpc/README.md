# DI RPC integration example

`TestCertificateReadonlyGet` registers only a Get service. The real production
Get handler authenticates the TLS client certificate and looks up its role in
private Redis; the production database reader reads testcase-seeded `Private`
from that same Redis. This is RPC integration,
not validation of a packaged telemetry executable or its startup path.

## Composition

- `gnmi.NewGetService(config)` exposes the production Get handler and its normal
  database reader (the example uses `Prefix.Target=CONFIG_DB`). There is no
  injected data reader or replacement response. Native routing stays unchanged.
  Other RPCs on this narrow service return Unimplemented; pathz-enabled standalone
  construction is rejected rather than silently bypassing policy.
- `testutil.StartServer(t, tlsConfig, register, options...)` only owns listener/server
  lifecycle. The caller registers the minimum services and supplies interceptors
  explicitly. It does not construct service dependencies or a client.
- `testutil.NewClientConn(t, address, tlsConfig)` creates a standard grpc-go connection and
  registers cleanup. Tests choose the generated gNMI/gNOI stub themselves.
- `testutil.NewFixture(t)` starts one empty private Redis and owns checkpoint,
  rollback and shutdown. It seeds no role, device metadata or initialization marker.
  `testutil.NewCertificateAuthority(t)` supplies PKI separately. Cases explicitly
  HSET their own role mappings and query data. There is no control identity or readiness RPC:
  the registered listener already exists, and the case's bounded RPC establishes TLS.
- `native_test.go` explicitly connects the existing production SWSS adapter to the
  fixture socket using DB connection metadata, not Redis table initialization.
  It temporarily points the existing process-global data-client map at the same
  real Redis client and restores that map on group teardown; no read methods are mocked.
  Native imports remain confined to the RPC tests; `testutil` uses only Go and
  go-redis and can compile with CGO disabled. This does not yet remove the existing
  `gnmi_server` package's native imports or their startup side effects.

## Group and case ownership

Create the environment and server in the parent test, then call `env.Run` for each
sequential case. The server stays up for the whole group. `env.Run` checkpoints
the fixture-owned CONFIG_DB before each case and registers rollback before the
case's callbacks. Case-owned clients and extra `t.Cleanup` callbacks unwind in Go's
LIFO order; register extra cleanup before creating clients if it must run after
connection close. Rollback runs last and verifies the key values and persistence.

Checkpoint uses Redis DUMP/RESTORE so strings, hashes and other Redis value types
are preserved. Expiring keys retain their checkpoint remaining TTL, not wall-clock
expiry. It covers only the private CONFIG_DB; it is not a snapshot of memory
caches, streams or host files. Special resources need case cleanup. A rollback or
checkpoint failure poisons the environment and aborts the parent group; normal
assertion failures with successful restoration do not prevent later cases.

Group teardown closes clients, stops gRPC, stops Redis and deletes temporary
configuration. PKI keys stay in memory. SWSS DB configuration is process-global:
groups must not call `t.Parallel`. Run in an owned disposable container with private
network/IPC, never on a DUT or shared runner. Go timeout/cancellation still requires
outer container disposal. Native imports can initialize SONiC libraries before the
fixture; this is not a CGO-free test or a claim that native startup has been removed.

## Execution and coverage

After the standard SONiC native build/dependency preparation, from repository root:

```sh
bash tests/rpc/run.sh
```

The existing amd64 package job supplies the native build environment, but the
example does not consume its `.deb`. The runner invokes the opted-in `e2e` build
tag, reuses gotestsum for JUnit and Go JSON, and requires exactly the example and
its readonly subtest to pass. Evidence is under `test-results/auth-rpc/`, including
source hashes and cleanup/test output. No Python client dependency is required.

Fixture/DI regression checks are separate from the single example:

```sh
CGO_ENABLED=0 go test -mod=vendor -tags=e2e -count=1 ./tests/rpc/testutil
go test -mod=vendor -tags=e2e -count=1 -timeout=2m ./tests/rpc
```

They verify restoration between cases, extra cleanup ordering, group abort after
failed rollback, and real noaccess/unmapped RPC denial with no response data.
The readonly example remains one positive case, not a complete auth suite. The
previous artifact example and Python suite are outside this scope; their CI PASS
does not qualify this DI revision.
