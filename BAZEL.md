# Native AMD64 Bazel build

Bazel builds gNMI's source, its existing generated protocol bindings and YANG
RPC clients, and the nine executables installed by Make. `//dist:runtime`
contains their `/usr/sbin` files; `//dist:runtime.debug_symbols` contains the
matching build-ID debug files. `//dist:sources` contains component-owned source
and the actual generated protocol/YANG Go files under `/usr/src/sonic-gnmi`,
with generated paths matching Make and the original test fixtures included.
External dependency sources remain owned by their respective components. No Debian package producer is added.

Use Bazel 8.5.1 on native Linux AMD64. The component selects Go 1.25.9, the
shared Trixie C/C++ toolchain and the same pinned APT inputs as Common. Both
`gnmi_translib_write` and `gnmi_native_write` are enabled by the telemetry
target itself, matching the selected SONiC image profile even when a consuming
root does not inherit `.bazelrc`. Address-sanitizer and cross-architecture
builds are outside this migration's scope.

```sh
bazel build //dist:runtime //dist:runtime.debug_symbols //dist:runtime_packages.json //dist:sources
bazel test //...
```

`--//tools/bazel:enable_dialout=false` reproduces `ENABLE_DIALOUT=n` by omitting
only `dialout_client_cli`. The package test checks the corresponding eight-file
inventory. The normal build has all nine executables:

- telemetry and dialout_client_cli;
- gnmi_get, gnmi_set, gnmi_cli and gnmi_dump;
- gnoi_client, gnoi_openconfig_client and gnoi_sonic_client.

`runtime_packages.json` describes these source TAR outputs and their declared
runtime dependencies for an image consumer. It does not create or install a
DEB. OCI consumers add the owner's TAR and matching debug TAR directly.

## Source ownership and generators

Component-level BUILD files expose the Go libraries and executable targets.
The reusable C++/Go binding is `@sonic_swss_common//goext:swsscommon`; gNMI does
not regenerate Common's SWIG implementation. Management Common supplies its
public Go packages, prepared YANG models, schema and runtime files.

The existing gNMI pyang plugin and Jinja templates run in a declared action,
using Management Common's model tree in the same main/extensions and SONiC
passes as Make. They produce the server registration files, SONiC tech-support
RPC and both generated clients. The declared upstream protoc and original
Go plugins regenerate Make's three native protocol bindings; the other
checked-in protocol bindings retain their existing ownership.

`patches/bazel/0001` through `0004` preserve the existing Make/vendor changes
with normalized patch headers. The remaining patches add only upstream Bazel
package declarations. `python3 tools/bazel/vendor_patches.py --check` detects
drift from the original Make patch sources. Common's old glog patch is
intentionally omitted for gNMI's existing glog1.2.4 selection.

## Consumers and dependency selection

The root `.bazelrc`, native source CI and CodeQL use the maintained SONiC
registry plus BCR. Management Common `1.0.1-05f83dcab8fe2e1a0e0fc37ac172b289d6968e2b`
and the public goyang/ygot owners are available there. The separate consumer
selects the same Management Common, SWSS Common and build-infra owners as the
source root; no temporary registry selection is needed for these checks.
The independent subscription-race reproduction retains its historical owner
inputs so it can compare the original baseline; it is separate evidence from
the source-root and installed-consumer checks.

A consuming Bzlmod root supplies its Go SDK and the exported SONiC Go patches.
`tests/consumer` is a real separate module: it imports gNMI's public server and
DB configuration interfaces, and builds the owner runtime and debug TARs
under `fastbuild` with `--strip=always`. The package targets retain their own
debug inputs before splitting, so a consuming root cannot silently remove
the detached symbols.
Its MODULE demonstrates the public Management Common, goyang and ygot owner
labels and the remaining root patch declarations. It needs no version overrides or synthetic copy of another
owner's Go repository. Root-only development SDK/patch declarations are
not executed when gNMI is a dependency.

`go.bazel.work` preserves the native fsnotify and x/net versions when Bazel's
own build-tool modules request newer versions. Consumers use that same file
with `go_deps.from_file(go_work = "@sonic_gnmi//:go.bazel.work")`. The ordinary
Make go.mod and its neighboring-checkout replacement remain supported.

## Validation and maintenance

The existing Go tests keep their source assertions and Make's race-detector
selection. A test-only transition disables inlining for the gomonkey graph;
its declared external linker uses native Go's non-PIE test executable mode so
the race detector retains its shadow-memory address range. Production
executables keep their normal optimized graph. Original suites that share
fixed gRPC test ports run serially. Tests receive a private Redis process,
private database and model fixtures, a declared Trixie loader/library tree, an
isolated shared-memory key, and private scratch paths. The loader runs the
original Go ELF bytes. Its launcher preserves the existing `os.Args[0]`
helper-process contract; it does not change `/proc/self/exe`. Common reads its
owner Lua scripts through `SWSS_COMMON_LUA_DIR` below the private runtime,
without installing them into the execution host’s `/usr/share/swss`. The original gNMI Python client tests run
against the corresponding native Go server suites. Redis and host-service
checks do not use a host's default Redis or system D-Bus.
CI forces the Linux sandbox and its non-root test identity so the original
read-only-directory assertions remain meaningful even when the job container
runs as root. Production build settings are unchanged.

The installed-runtime probe launches telemetry and the installed gnmi_get
client against private Redis. It first checks both installed write-profile
defaults, then requires real values after two database
changes and rejection of an invalid database. Package tests check exact file
inventory, AMD64 architecture, GNU build IDs, stripped runtime files, debug
sections and debuglink CRCs. Runtime executables are root-owned mode 0755;
detached debug files are root-owned mode 0644 with the same fixed timestamp.
GDB must resolve `main.main` for all nine installed
executables with the matching debug TAR and fail that lookup when it is absent.
CI retains test logs, selected dependency inputs and the tested source/merge
identity. Generated Go DWARF uses Bazel's action-relative filenames; when
listing their source in GDB, map the reported `gnoi_yang/generated` directory
to `/usr/src/sonic-gnmi/build/gnoi_yang` with `set substitute-path`. Ordinary
owner files retain their component-relative paths. Source for external clients
and libraries remains with those owners.

When adding a source, update the BUILD file beside it. Each package exposes
its source files through the small `source_files()` macro; add a new package
to the root `:source_files` list so its sources join the source TAR. `bazel run //:gazelle`
can propose ordinary Go dependencies; review the result and keep the source
owner/generator targets explicit. Required generator inputs, upstream patch
parity, installed inventory and an external consumer must remain covered.
Do not commit MODULE.bazel.lock; CI generates and archives it. Keep the
canonical Python requirements lock and source version pins in Git.

`tools/bazel/check_incremental.py` compares baseline, unchanged rebuild, a live
telemetry-string edit and restoration. `check_generator_incremental.py` does
the same for the YANG client generator input. Both require the exact expected
changed outputs, unchanged unrelated owner outputs and byte-identical restore.
CI binds the retained runtime/debug TAR hashes to these receipts, GDB checks
and the source head/base checkout; failed or skipped checks cannot produce a
complete receipt.

CodeQL prepares the selected owners' libraries and generated source before
initialization, then traces ordinary Go compilation with the same Go version
and both write tags. It also compiles the original test packages without
executing them. A strict readback compares ordinary owner Go and test files
with the actual CodeQL source archive. For CGo, the compiler transforms each
original source: the receipt records its hash and requires its exact `//line`
attribution in the generated compiler input. Every physical compiled Go file,
including CGo support files, must appear in that archive with matching bytes.
The actual archive is retained with the receipts. GitHub does not support
tracing Bazel Go builds, so the CodeQL job uses
Bazel only for preparation and remains separate from native test execution.
