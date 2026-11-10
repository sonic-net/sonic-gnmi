# Test layout

Use the boundary a test exercises to choose its home. Directory names and build
profiles alone do not establish whether a test is a unit, integration or E2E test.

| Kind | Location | Boundary |
| --- | --- | --- |
| Go unit tests | `*_test.go` beside the package implementation | One component; isolated dependencies, no running external services required. |
| Go package integration tests | Beside the package when they need package-private setup | Multiple components or real dependencies; document the required environment. Existing mixed files need case-by-case separation before moving. |
| Python service integration tests | `tests/integration/` | CLI requests against a service started by the Go test harness, with its configured databases and backend substitutes. |
| Test tooling | `tools/test/` | Environment scripts and checks of repository test/build rules, including `test_pure_tags.py`. |
| Artifact E2E tests | A separate test repository | A built/deployed product exercised through protocols and supported configuration, without importing product internals. That repository is not created by this layout. |

Go package tests stay with their implementation: moving them to `tests/unit/`
creates a different Go package, even with the same package declaration. Do not
export production internals just to relocate tests. `pure` versus native describes
build dependencies, not unit versus integration coverage.

## Python integration suite

The former `test/*.py` files live in `tests/integration/`. Their helpers still
resolve `build/bin/` relative to the **repository-root working directory**.
Do not change into `tests/integration/` to run them.

The existing Go tests in `gnmi_server/server_test.go` prepare the server and
database configuration, then invoke pytest from the repository root:

| Go harness | Pytest selection |
| --- | --- |
| `TestGNMINative` | `not multidb and not multins` |
| `TestGNMINativeMultiDB` | `multidb` |
| `TestGNMINativeMultiNamespace` | `multins` |

Root-level pytest discovery still finds the relocated suite. These tests are
part of the existing native integration run; no new CI selector is required.
Some harnesses replace D-Bus or other backend operations, so this suite is not
proof of full-system E2E behavior or real authentication-backend validation.

In a provisioned, disposable SONiC build environment, use the existing entrypoints
from the repository root:

```sh
make all
ENABLE_TRANSLIB_WRITE=y make test-integration
```

To inspect Python collection without starting the service or executing test
bodies (requires the existing pytest and jsonpatch dependencies):

```sh
python3 -m pytest --collect-only -q tests/integration
python3 -m pytest --collect-only -q tests/integration -m multidb
python3 -m pytest --collect-only -q tests/integration -m multins
```

Collection is discovery evidence only, not a passed integration run. Running the
Python tests directly requires the corresponding Go harness's live setup; they
are not a standalone DUT test runner.

## Fixtures and helpers

- Put new package-specific fixtures in that package's `testdata/` directory.
  Keep the existing root `testdata/` for fixtures shared by current native suites;
  relocating them requires updating and checking all consumers.
- Keep Python integration helpers beside that suite. Keep shared Go helpers in
  the existing `test_utils/` package until a separate import-path migration is
  justified. Do not combine them into a cross-language test framework.
- Keep generated results out of source directories. Existing Make entrypoints
  write CI reports to `test-results/`; those report paths remain unchanged.

## Later migrations

Separate mixed unit/integration cases only after identifying their setup and
private-symbol dependencies. Retain the same assertions, marker selections and
test coverage; moving a file must not silently drop it from an existing runner.

Independent auth E2E tests should consume an identified binary, package or image
and record both product and test revisions. Exercise real TLS/gRPC, Redis,
configuration and CRL inputs; do not import internal handlers or substitute
successful authentication. Their repository name, owner and execution integration
remain separate decisions.
