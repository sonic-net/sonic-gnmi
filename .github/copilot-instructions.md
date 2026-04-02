# Copilot Instructions for sonic-gnmi

## Non-obvious gotchas

These are things you cannot discover by reading the code. Everything else, read the code.

1. **Sibling directory layout required.** `go.mod` has `replace github.com/Azure/sonic-mgmt-common => ../sonic-mgmt-common`. You must clone both repos side-by-side. `go mod vendor` fails with no clear error otherwise.

2. **Build tags silently disable write support.** `make all` without `ENABLE_TRANSLIB_WRITE=y` produces a binary that looks functional but silently drops all gNMI Set operations. Tests for writes get `t.Skip()`'d. Production builds use both `ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y`.

3. **"Unit tests" require sudo + Redis.** Many `*_test.go` files copy configs to `/var/run/redis/sonic-db/`, need a running Redis server, and require root access. They look like unit tests but are integration tests.

4. **CI depends on external Azure pipeline artifacts.** The build downloads pre-built `.deb` packages from `sonic-buildimage.common_libs`, `sonic-swss-common`, and `sonic-buildimage.vs` pipelines. You cannot build the full Makefile target from scratch without them.

5. **Vendor patching corrupts state on partial failure.** The Makefile's crypto backup/restore dance and silent `patch` commands mean a failed build leaves `vendor/` unrecoverable. Only fix: `rm -rf vendor` and rebuild.

6. **`pure.mk` is the local dev CI.** Without the full SONiC build env (libswsscommon, SWIG, hiredis, Redis), `make -f pure.mk ci` is the only thing that works. The main `Makefile` requires all of those.

7. **Proto generation modifies go.mod.** `sed -i '/^toolchain/d' go.mod` runs as a side effect during protobuf codegen. Easy to accidentally commit a broken go.mod.

## Code review guidance

When reviewing PRs (applies to both human reviewers and the Copilot review agent):

- **Prefer pure packages.** New code should go in `internal/` or `pkg/` and be testable via `pure.mk` (no CGO/SONiC dependencies) unless it genuinely needs them. Don't let new code silently acquire CGO dependencies when a pure design is feasible. Legacy packages like `sonic_data_client/`, `common_utils/`, etc. are not worth retrofitting.

- **Prefer idiomatic Go project layout.** New packages belong in `internal/` (private) or `pkg/` (public), not as new top-level directories. The top-level `sonic_*` and `*_utils` directories are legacy — don't add to them.
