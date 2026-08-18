# HardwareProxy gNOI File Relay

The relay uses the existing gNMI and gNOI server. It does not add a listener,
port, certificate, or control-plane access rule.

Configure all three flags together:

- `-hardware_proxy_file_relay_cert_cn=<verified client certificate Common Name>`
- `-hardware_proxy_file_relay_desired_path=/var/tmp/device-ops-agent/desired-software.json`
- `-hardware_proxy_file_relay_status_path=/var/tmp/device-ops-agent/software-status.json`

Partial configuration prevents server startup. Empty configuration does not
enable relay registration or grant relay access.

Relay configuration requires TLS, a configured client certificate authority,
and mandatory client certificates. `-insecure`, `-noTLS`, and
`-allow_no_client_auth` are rejected when any relay flag is present.

The gNMI container must expose the host root at `/mnt/host`. The mount must map
host `/var/tmp` to container `/mnt/host/var/tmp` with read-write access. This
repository does not control that container mount. Relay unit tests verify the
logical-to-host-root mapping and file permissions.

Relay startup fails if `/mnt/host` is absent, is a symlink, or is not a
directory. It also requires the existing local Unix socket. Startup creates and
durably syncs managed relay directories as `0750`. Startup rejects managed
directories with non-root ownership. It corrects root-owned unsafe modes to
`0750` before use. This policy applies to `/host/doa` and its state directory.

In relay-only mode, TCP File access contains only HardwareProxy Put to the
desired path and Get from the status path. The local Unix socket contains
hardened desired Get, status Put, and journal/completion Get and Put operations.
The relay-only TCP descriptor advertises only Get and Put. The local Unix socket
keeps the complete File service needed by device-ops-agent workflows. Exact
relay files use hardened handlers. Other local calls preserve existing behavior.
Journal and completion paths are always denied over TCP.

When legacy File registration is also enabled, every configured relay file is
reserved from non-HardwareProxy TCP File callers. A server-wide TCP interceptor
restricts the HardwareProxy certificate principal to relay File.Get and File.Put
before any gNOI, gNMI, gNSI, reflection, or other handler runs.

All File policy checks require canonical host-visible absolute paths. Dot
segments, parent traversal, repeated slashes, and `/mnt/host` inputs are rejected
before relay authorization or legacy dispatch.
