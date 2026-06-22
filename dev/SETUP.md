# sonic-gnmi Dev Environment — Setup & Operations Guide

A **self-contained, reproducible** way to build and test this `sonic-gnmi`
checkout. Everything heavy runs inside the `sonic-slave-trixie` container, so the
checkout stays clean. This `dev/` folder (`setup.sh`, `run-tests.sh`, this file)
is all you need — no agent skill, no manual dependency wrangling.

> This `dev/` folder lives **inside** the `sonic-gnmi` checkout. It is registered
> in the repo's local `.git/info/exclude`, so it never appears in `git status`
> and is never deleted by `git clean -fd`.

---

## 1. Quick start

From the root of the `sonic-gnmi` checkout:

```bash
./dev/setup.sh            # verify prereqs + bootstrap deps + run pure tests
# or, to skip the test run:
./dev/setup.sh --no-test
```

When it prints `DONE — environment verified`, you are ready. Everything below is
reference: what each step does, the daily commands, the optional DUT workflow,
and a full troubleshooting section.

**Expected result of a healthy setup:** `DONE 656 tests in ~40s` with no
failures (the test count grows as the repo grows).

---

## 2. What gets created (layout)

| Path | What | In git? |
|------|------|---------|
| `sonic-gnmi/` | the checkout (code you edit) | yes |
| `sonic-gnmi/dev/` | this driver: `setup.sh`, `run-tests.sh`, `SETUP.md` | excluded locally |
| `sonic-gnmi/dev/build-out/` | built `.deb`s land here | excluded locally |
| `~/.cache/acr-image-build/` | shared deps: `sonic-mgmt-common`, `sonic-swss-common`, SONiC `.deb`s | no (shared) |

- Override the cache location with `ACR_IMAGE_CACHE_DIR`. One copy of the heavy
  deps is reused across every checkout.
- Inside the container, bind mounts reconstruct the sibling layout the build
  expects (`sonic-gnmi/` next to `sonic-mgmt-common/` + `sonic-swss-common/`),
  satisfying the `replace ... => ../sonic-mgmt-common` directive in `go.mod` and
  the `-I../../sonic-swss-common/common` CGO flag.

---

## 3. Prerequisites

`setup.sh` checks these and stops with a clear message if any fail:

1. **Docker works without sudo:** `docker info >/dev/null 2>&1`.
2. **You are inside a `sonic-gnmi` git checkout** (the parent of `dev/` has a
   `.git`). If you only have this `dev/` folder, drop it into a checkout:
   ```bash
   git clone https://github.com/sonic-net/sonic-gnmi.git
   # then copy this dev/ folder to sonic-gnmi/dev/ and run ./dev/setup.sh
   ```

---

## 4. What `setup.sh` does (step by step)

You can run these by hand instead of `setup.sh` if you want full control.

### Step 1 — Normalise scripts + shield from git
```bash
sed -i 's/\r$//' dev/run-tests.sh dev/setup.sh   # strip CRLF (avoids 'bash\r' error)
chmod +x dev/run-tests.sh dev/setup.sh
# keep dev/ out of git status / git clean, without touching tracked .gitignore:
grep -qxF '/dev/' .git/info/exclude 2>/dev/null || echo '/dev/' >> .git/info/exclude
```

### Step 2 — Bootstrap the shared cache
Idempotent. Clones the sibling repos and downloads the SONiC `.deb` artifacts +
yang-models wheel from the **public** mirror `sonic-build.azurewebsites.net`
(no credentials / PAT needed). Slow on a fresh machine (~1–2 min); later runs are
no-ops.

```bash
./dev/run-tests.sh bootstrap
```

Current working Trixie artifact set (see [§8](#8-troubleshooting) if a download
404s):

| Artifact | Version |
|----------|---------|
| `libyang3`, `libyang-dev` | `3.12.2-1` |
| `libnl-3-200`, `libnl-genl-3-200`, `libnl-route-3-200`, `libnl-nf-3-200` | `3.7.0-0.2+b1sonic1` |
| `libswsscommon`, `libswsscommon-dev`, `python3-swsscommon` | `1.0.0` |
| `sonic_yang_models` wheel | `1.0` |

### Step 3 — Verify pure tests
```bash
./dev/run-tests.sh pure
```
First run also pulls the `sonic-slave-trixie` image (anonymous pull). Expect
`DONE 656 tests in ~40s`.

---

## 5. Daily use

```bash
./dev/run-tests.sh pure                  # pure unit tests, ~40s
./dev/run-tests.sh integration           # full integration tests, ~20 min (locks terminal)
./dev/run-tests.sh integration gnmi_server   # focused subset: just one (or more) package(s)
./dev/run-tests.sh build                 # produce sonic-gnmi_*.deb in dev/build-out/
./dev/run-tests.sh shell                 # bash inside the container with all deps installed
./dev/run-tests.sh playground            # boot a live no-TLS gNMI/gNOI server + client shell
./dev/run-tests.sh clean                 # wipe the dependency cache (forces re-download)
./dev/run-tests.sh help                  # print the full subcommand summary
```

### Focused integration subset

`integration` with no arguments runs the full suite exactly as before. Pass one
or more package names to run **only** those through the same dockerized path,
dramatically shortening the inner loop:

```bash
./dev/run-tests.sh integration gnmi_server                 # one package
./dev/run-tests.sh integration sonic_data_client telemetry # several at once
```

Accepted names are the short package names (e.g. `gnmi_server`,
`sonic_data_client`, `telemetry`, `dialout`) or their fully-qualified module
paths. Each is classified into its integration tier (basic / env / dialout); the
non-targeted tiers are emptied so their Makefile guards skip them — so a focused
subset never drags in the dialout tests unless you asked for them.


`./dev/run-tests.sh shell` drops you into the container at `/work/sonic-gnmi`
with `redis-server`, `libswsscommon`, `libyang3`, the SONiC libnl, and Python
`jsonpatch` ready. The shell also exports the `CGO_*` flags and provides a
`build-nonpure` helper. Pure packages work immediately
(`go test ./pkg/... ./internal/...`); for non-pure packages (`gnmi_server`,
`sonic_data_client`, `dialout`, ...) run `build-nonpure` once (builds
`sonic-mgmt-common` + the swsscommon wrapper + vendored/patched deps), then
`go test -mod=vendor -tags gnmi_translib_write -gcflags=all=-l ./gnmi_server/ -run TestServer -v`.

### Prefer pure packages
- **Pure** (`pkg/...`, `internal/...`): plain Go, no CGO/SONiC deps, builds in
  seconds, tested via `./dev/run-tests.sh pure`. Put new logic here.
- **Non-pure** (`gnmi_server`, `swsscommon`, `sonic_data_client`, ...): CGO +
  swss-common + mgmt-common; integration tests ~20 min. Keep these as thin
  wrappers that authenticate and delegate to a pure handler. RPC behaviour +
  its tests belong in the pure package; the non-pure side keeps only the two
  wiring sub-tests (auth fires before the handler; an authenticated request
  reaches the handler and its error maps to the right gRPC status).

---

## 6. Playground — a live local server to hand-exercise RPCs

```bash
./dev/run-tests.sh playground          # server on 127.0.0.1:8080 + UDS, then a shell
./dev/run-tests.sh playground 9090     # pick another port if 8080 is taken
```

`playground` builds everything (`build-nonpure` + `make all`, which installs
`telemetry`, `gnmi_cli`, `gnmi_dump`, `gnoi_client`, `gnmi_get`, `gnmi_set` into
`build/bin`), launches the telemetry/gNMI/gNOI server **in no-TLS mode** in the
background, waits (bounded ~30s) until it is listening, then drops you into an
interactive shell with those client binaries on `PATH`. The container runs with
`-it` and publishes the port (`-p $PORT:$PORT`), so you can reach the server from
the shell **or** from your host. Exiting the shell tears down the `--rm`
container, the server, and Redis automatically.

The server listens on two endpoints:

- **TCP** `127.0.0.1:$PORT` (default `8080`)
- **UDS** `/var/run/gnmi/gnmi.sock`

Example commands once you are in the shell:

```bash
gnmi_dump                              # no args: prints this server's GNMI/GNOI/DBUS counters
gnmi_cli -a 127.0.0.1:8080 -insecure -logtostderr -query_type Once \
         -q '/COUNTERS/Ethernet0' -target COUNTERS_DB
gnoi_client -target 127.0.0.1:8080 -insecure -rpc System.Time
```

The server log is at `/tmp/telemetry.log` inside the container.

> **Expected noise:** the playground server has no populated SONiC DB tables, so
> queries for real data may return errors. That is fine — `playground` is for
> surface-level interaction (does the RPC reach the server, what does it answer),
> not full data fidelity.

> ⚠️ **Security — local throwaway only.** `playground` runs the server with
> `--noTLS --insecure --allow_no_client_auth`, which **disables authentication**.
> This is acceptable *only* because it runs in an ephemeral `--rm` container bound
> to your own host. **Never** run it on a DUT or shared host, and never reuse
> these flags for the `build`/deploy path. No secrets are introduced; the
> published port stays on localhost.

---

## 7. (Optional) Working against a DUT

Mutating on the DUT (installs a package, restarts a service) — opt-in.

### Credentials
```bash
DUT="${DUT:-admin@vlab-01}"        # override for your testbed
# Key-based auth (preferred):
SSH="ssh";  SCP="scp"
# ...or password via env (never hardcode it):
#   read -rsp 'DUT password: ' SSHPASS; echo; export SSHPASS
#   SSH="sshpass -e ssh -o StrictHostKeyChecking=no"
#   SCP="sshpass -e scp -o StrictHostKeyChecking=no"
```
After a testbed refresh the SSH host key changes — clear it with
`ssh-keygen -R "${DUT#*@}"`.

### Build + deploy the deb
```bash
./dev/run-tests.sh build                       # -> dev/build-out/sonic-gnmi_0.1_amd64.deb
DEB=dev/build-out/sonic-gnmi_0.1_amd64.deb

$SCP "$DEB" "$DUT:/tmp/"
$SSH "$DUT" \
  "docker exec gnmi dpkg -i /mnt/host/tmp/$(basename "$DEB") && \
   docker exec gnmi supervisorctl restart gnmi-native"
$SSH "$DUT" "docker exec gnmi supervisorctl status gnmi-native"
```
The deb installs **only into the gnmi container** (via the host bind-mount
`/mnt/host`); `gnmi-native` is the supervisord unit (older names:
`gnmi`/`telemetry`); the build stamps version `0.1` every time, so `dpkg -i` is
a plain reinstall. Verify your code made it in (without running it):
`docker exec gnmi grep -aoE 'YourNewSymbol' /usr/sbin/telemetry | sort -u`.

### Call gNMI/gNOI over the UDS (preferred for dev)
`/var/run/gnmi/gnmi.sock` bypasses the entire TLS/mTLS/client-cert chain the TCP
listeners (50051/50052) require — the only practical way to test on a vanilla KVM
testbed without standing up a CA. The bundled `gnoi_client` cannot speak UDS; use
`grpcurl`:

```bash
$SCP "$(go env GOPATH)/bin/grpcurl" "$DUT:/tmp/grpcurl"   # one-time; KVM has none
$SSH "$DUT" 'chmod +x /tmp/grpcurl'

# list services — use the unix:// scheme (bare path / -unix flags fail on old builds)
$SSH "$DUT" 'sudo /tmp/grpcurl -plaintext unix:///var/run/gnmi/gnmi.sock list'

# call an RPC (-plaintext mandatory; sudo because the socket is 0660 root:root)
$SSH "$DUT" 'sudo /tmp/grpcurl -plaintext -d "{\"path\":\"/tmp\"}" \
     unix:///var/run/gnmi/gnmi.sock gnoi.file.File/Stat'
```

---

## 8. Troubleshooting

Every common failure and its fix — you should not need anything outside this file.

### `/usr/bin/env: 'bash\r': No such file or directory`
CRLF line endings in a script. Fix:
```bash
sed -i 's/\r$//' dev/*.sh && chmod +x dev/*.sh
```

### `docker ... permission denied` / `Cannot connect to the Docker daemon`
Docker needs to work without sudo. Start the daemon and/or add yourself to the
`docker` group (`sudo usermod -aG docker "$USER"`, then re-login).

### `bootstrap` 404s on a `.deb`
The public mirror only keeps the **current** build's artifacts, so a version got
rotated upstream. Find the new version and update `DEB_TARGETS` in
`dev/run-tests.sh`:

1. Look up the version in `sonic-net/sonic-buildimage@master`, e.g.
   `rules/libyang3.mk` (`LIBYANG3_VERSION`), `rules/libnl3.mk`, etc.
2. Or probe the mirror directly (`%{http_code}` 200 = exists). Use
   `platform=vs` and URL-encode `+` as `%2B`:
   ```bash
   curl -sL -o /dev/null -w '%{http_code}\n' \
     'https://sonic-build.azurewebsites.net/api/sonic/artifacts?branchName=master&platform=vs&target=target/debs/trixie/<file>.deb'
   ```
3. Note the historical rename: `libyang_1.0.73` / `libyang-cpp` / `libpcre3` are
   **gone** — libyang migrated to **libyang3**, and Trixie supplies pcre2 from
   stock apt.

### Pure tests show NEW failures after a `git pull`
A new package may have been added to `sonic-gnmi/pure.mk`. Mirror its entries
into the `pure_packages` string in `run_pure()` in `dev/run-tests.sh`.

### CGO compile error mentioning `events_wrap.h`
The `sonic-swss-common` cache is stale or missing:
```bash
rm -rf "${ACR_IMAGE_CACHE_DIR:-$HOME/.cache/acr-image-build}/sonic-swss-common"
./dev/run-tests.sh bootstrap
```

### `dpkg -i` fails with unmet dependencies inside the container
Handled automatically — the driver runs `apt-get install -f -y` as a fallback so
stock deps (e.g. `libpcre2-8-0`) are pulled in. If you customised the snippet,
keep that fallback.

### `pkg/exec` tests fail / are skipped
Excluded from the pure suite on purpose: they call `nsenter`, which needs
`CAP_SYS_ADMIN` + a relaxed seccomp profile. The test's skip-path looks for
`"Permission denied"` but a sandboxed container returns `"Operation not
permitted"` (same errno, different wording). Run on a real host or with
`--privileged` to exercise them.

### The checkout looks dirty after running tests
Tests write timestamped certs into `testdata/mtls/`, regenerate some
`testdata/gnsi/*.json`, delete `testdata/gnsi/pathz_policy.pb.txt`, and `go test`
may add indirect deps to `go.mod`/`go.sum`. Some files get container-side
ownership. To reset (this does **not** touch `dev/`, which is git-excluded):
```bash
sudo chown -R "$USER:$USER" .          # only if needed
git checkout -- . && git clean -fd
```

### DUT: `tls: certificate required` on port 50051/50052
The TCP listeners need a client cert signed by the server's CA; a vanilla KVM
testbed only has the server cert. Don't fight it — use the UDS
(`unix:///var/run/gnmi/gnmi.sock`, see [§7](#7-optional-working-against-a-dut)).

### DUT: `grpcurl` says `missing port in address`
You used a bare path or `-unix` flag on an old `grpcurl`. Use the
`unix:///absolute/path` scheme.

### Want a completely fresh start
```bash
./dev/run-tests.sh clean        # deletes the shared cache; next bootstrap re-downloads
```

---

## 9. Quirks the driver already handles (FYI)

1. **`TMPDIR=/tmp` is mandatory** — `pkg/gnoi/file` tests use `t.TempDir()` but
   prod only allowlists `/tmp`, `/var/tmp`, `/host`.
2. **libnl must be the SONiC build** (`3.7.0-0.2+b1sonic1`); the driver purges
   stock `libnl-3-dev`/`libnl-route-3-dev` first.
3. **`jsonpatch`** Python module is installed (needed by
   `test/test_gnmi_configdb_patch.py`).
4. **Public artifact mirror** uses `platform=vs` (not `common`/`generic`).
