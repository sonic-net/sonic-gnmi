# Go artifact E2E example

One example: `TestCertificateReadonlyGet` generates real TLS certificates, starts
private Redis and the packaged telemetry executable, maps a client CN to
`gnmi_config_db_readonly`, and verifies that Get returns the seeded `Private` value.
It uses public Go gNMI stubs, not Python fixtures or server-handler imports.
A separate control identity performs readiness. This is a positive read example,
not comprehensive authentication or authorization coverage.

The amd64 Package job runs `bash tests/e2e/run.sh <package-staging-directory>`
sequentially after packaging. The runner selects exactly one sonic-gnmi amd64
`.deb`, extracts `/usr/sbin/telemetry`, records package/source/executable hashes,
and uses the existing gotestsum reporter for Go JSON and JUnit results.
It never falls back to `build/bin/telemetry` or builds a replacement server.
Default Go tests exclude the example through the `e2e` build tag.

For a direct run inside an owned disposable SONiC native-runtime container:

```sh
TELEMETRY_BIN=/artifact/usr/sbin/telemetry TELEMETRY_SHA256=<verified-sha256> \
  CGO_ENABLED=0 go test -mod=vendor -tags=e2e -count=1 -timeout=2m -json \
  ./tests/e2e -run '^TestCertificateReadonlyGet$'
```

Supply the matching native libraries, YANG paths and built sibling repositories.
Do not run on a DUT, the host or a shared management container. Container presence
alone does not establish ownership. The fixture refuses existing default Redis,
uses an owned Unix socket, restores native DB configuration, verifies the live
executable hash and reaps processes before removing certificates. Normal cleanup
failure fails the test; timeout/cancellation also requires outer container disposal.

CI always publishes `test-results/auth-e2e/`: JUnit, raw Go events (including
cleanup logs), package/executable provenance and exit status. Missing/failed/skipped
or unexpected tests fail the runner. Private keys are never archived.
The previous seven-case Python suite is outside this example's scope; its earlier
CI results do not qualify this Go runner. No Python dependencies are required here.
