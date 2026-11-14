# Trusted SetPackage downloads

`System.SetPackage` accepts a single package message with `remote_download.path`
containing an HTTP(S) URL. The optional protocol must match that URL.
Credentials and source-address/VRF overrides are not supported. An absent
`remote_download` preserves the existing absolute local-file installation path.
Neither mode reboots the device. Installation can change its default boot image
even when `activate` is false, as before.

Administrators must install `/etc/sonic/gnoi-download.json` on **each receiving
host**. Missing, malformed or empty policy denies remote downloads. For example:

```json
{
  "origins": [
    {
      "scheme": "https",
      "host": "images.example",
      "port": 443,
      "addresses": ["192.0.2.1", "2001:db8::1"]
    }
  ]
}
```

Only exact scheme, host (case-insensitive) and numeric port matches are allowed.
URL ports default to 80/443. No wildcards or subnet expansion are supported.
Literal hosts must match their pinned addresses. Hostnames connect directly to
the configured IP pins; runtime DNS is not used. Update pins when server
addresses change. HTTPS still verifies the URL hostname with the system trust
store. Explicitly configured private/internal HTTP origins are supported, but
HTTP does not provide artifact authenticity.

All redirects are rejected, including same-origin redirects. Environment proxy
settings are ignored. URL userinfo, scoped IPv6 and unsupported schemes are
rejected. Only the configured addresses are dialed; a caller cannot substitute
an address, proxy or policy file. Keep policy administrator-owned and outside
RPC-writable locations; the gNMI container reads the host's read-only
`/etc/sonic` mount.

Downloads are limited to 4 GiB and five minutes. A fresh private directory under
host `/var/tmp` holds the image until the installer finishes. Remote-mode
`filename` remains a required absolute logical filename but is not an arbitrary
write destination: installation uses the private staging file. Failed or
cancelled downloads do not invoke the installer; temporary files are removed
on success and error. The normal installer validation remains in effect;
transport completeness is not a trusted digest/signature guarantee.

SetPackage's existing DPU proxy forwards the request to the selected DPU.
That DPU downloads and installs using **its own** policy and network access.
An NPU policy file does not configure its DPUs. The existing TLS/authentication
and proxy identity model is unchanged. Install this implementation and policy
on both sides before enabling remote-mode test callers. Older servers reject
remote mode; do not fall back to `File.TransferToRemote`.

The retired File RPC, Containerz HTTP(S) denial, File.Put and UDS-only ORAS Pull
are unchanged. Internal build policy belongs in private image configuration;
public examples and tests must contain only synthetic origins.
