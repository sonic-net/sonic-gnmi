# gNOI Debug actions

Before deploying this version on systems that expose Debug, migrate
`/etc/sonic/command_whitelist.yaml` to the version 1 `enabled_actions` schema
and restart the service. The previous schema leaves Debug registered but
unavailable.

The gNOI Debug RPC exposes a small set of server-defined diagnostic actions.
Clients select an action by its canonical command text. They cannot select an
executable, operating-system user, namespace, or arbitrary arguments.

Debug requires authentication and an explicit `gnoi_readonly` or
`gnoi_readwrite` role. `gnoi_noaccess` always denies the request. The RPC does
not run when authentication is disabled, including on the local Unix socket.

## Policy

The server reads `/etc/sonic/command_whitelist.yaml` at startup. The file uses a
strict, versioned schema:

```yaml
version: 1
enabled_actions:
  - uptime
  - process-list
```

Only action identifiers compiled into the server are valid. Unknown fields,
unknown or duplicate actions, unsupported versions, malformed YAML, and missing
files make the policy unavailable. The Debug service remains registered but
denies execution until the server restarts with a valid policy.

The previous `read_whitelist` and `write_whitelist` keys are not supported.

## Supported actions

| Action ID | Client command | Access | Server command | Runtime limit | Combined response limit |
|-----------|----------------|--------|----------------|---------------|-------------------------|
| `uptime` | `uptime` | Read-only | `/usr/bin/uptime` | 10 seconds | 64 KiB |
| `process-list` | `ps` | Read-only | `/usr/bin/ps -ef` | 10 seconds | 256 KiB |

Client-provided timeout and output limits may reduce the action limits. They
cannot increase or disable them. `role_account` and shell mode are not
supported.

Actions run without a shell as `admin` in the fixed host mount namespace
profile. The executor applies a fixed working directory, a fixed `PATH`,
systemd sandbox properties, and a systemd-enforced runtime limit that kills the
action control group when the limit expires. The response limit applies to
stdout and stderr combined; the action may generate more output than the client
receives.
