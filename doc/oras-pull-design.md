# Design: gNOI ORAS Pull Service

**Status:** Draft
**Author:** Dawei Huang (<daweihuang@microsoft.com>)
**Last updated:** 2026-05-29
**Tracking:** ADO Feature #37984064 (KubeSonic OS image prefetch — ACR-driven gNOI install)

## 1. Problem

SONiC needs a way for an external orchestrator (KubeSonic control plane, ZTP
runner, NetBox automation, etc.) to **tell a switch to pull an OCI/ORAS artifact
from a registry to local disk**, so that a subsequent install step
(`gnoi.os.Install`, package update, container image swap, …) can run against a
known-good local copy.

Today the closest thing is SONiC's `gnoi.file.TransferToRemote`. Important
detail: although the upstream openconfig spec defines `TransferToRemote` as
**upload** (target → remote URL), the SONiC implementation at
`pkg/gnoi/file/file.go` actually performs a **download** — it HTTP GETs
`remote_download.path` and writes the bytes into `local_path`. The proto
message is reused but the semantics are inverted. That works for fetching
images via a plain HTTP URL, and is what's in use today.

It does **not** work for OCI/ORAS artifacts, which is what an ACR-backed
deployment needs. Specifically:

| Limitation of the current `TransferToRemote` path                                              | Why it blocks ACR/ORAS                                                                                              |
| ---------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| Hard-coded `RemoteDownload_HTTP` only (`file.go:111`) — `HTTPS`, `SFTP`, `SCP` return `Unimplemented`. | ACR is HTTPS-only.                                                                                                  |
| `RemoteDownload.path` is a single URL.                                                         | OCI requires resolving `registry/repository:tag` → manifest → layer digests. Not a single URL.                      |
| `RemoteDownload.credentials` is `{username, cleartext_password}` only.                          | ACR auth tiers: anonymous, basic admin, bearer token, AAD workload identity. Bearer/AAD don't fit.                  |
| No manifest / layer awareness in the response (just an MD5 hash of the single downloaded blob). | Caller can't tell which layer is the `.bin`, can't verify per-layer digests, can't deduplicate by manifest digest.  |
| Allowlist hard-codes `/tmp`, `/var/tmp`, `/host` (`file.go:208`); 4 GB max; 5-minute timeout.   | OS images > 4 GB exist (DPU bundles); ORAS-fetched artifacts need a managed staging area, not a free-form path.     |
| No streaming progress; unary RPC blocks for the duration of the download.                       | Pulls can take many minutes on slow links; orchestrators need progress + cancellation.                              |
| Semantics of "TransferToRemote = download" is itself a footgun — the name lies.                | A clean new RPC avoids continuing to overload a confusingly-named method.                                           |

We deliberately **do not** try to extend `RemoteDownload` with a new
`Protocol.ORAS` enum, for the same reasons — its schema is too thin
(single URL + flat creds) to carry registry/repo/tag-or-digest, manifests,
multiple layers, or AAD workload-identity auth.

## 2. Goals & non-goals

**Goals**

1. Pull an arbitrary OCI/ORAS artifact from a registry to a local staging
   directory on the target.
2. Make the operation digest-addressable and idempotent — re-pulling an already
   staged digest is a no-op.
3. Surface enough metadata (per-layer media type, digest, local path) for a
   downstream installer to pick the right layer.
4. Stream progress so long-running pulls don't look hung.
5. Support both classic (basic / bearer) auth and AAD workload identity, with
   workload identity as the preferred long-term mode.
6. Work on testbeds where the registry is not reachable via the device's
   default route (lab fabric, air-gapped sites) by allowing an explicit HTTP
   proxy and source-VRF.

**Non-goals**

1. **Installing** the artifact. `Pull` stages; `gnoi.os.Install` (or whatever
   installer is appropriate for the artifact type) consumes the staged path.
   Conflating them turns one RPC into a switch's entire image lifecycle.
2. Pushing artifacts from the device to a registry. Not in scope.
3. Garbage collection policy. We provide `List` and `Delete`; the policy of
   when to delete is the orchestrator's call.
4. Registry mirroring / pull-through caches. Out of scope.

## 3. Proposed service

New service in a SONiC-owned proto package; do **not** put this under
`gnoi.*` (the gnoi org owns that namespace).

```proto
syntax = "proto3";

package sonic.gnoi.oras.v1;
option go_package = "github.com/sonic-net/sonic-gnmi/proto/gnoi/oras";

import "google/protobuf/duration.proto";
import "google/protobuf/timestamp.proto";

service Oras {
  // Pull fetches an OCI/ORAS artifact from a registry into the target's local
  // staging store. Server-streamed so callers can show progress and react to
  // cancellation. Final message is always a PullResult on success, or a gRPC
  // status code on failure.
  rpc Pull(PullRequest) returns (stream PullResponse);

  // List returns artifacts currently present in the local store, plus
  // aggregate disk usage so operators can decide what to evict.
  rpc List(ListRequest) returns (ListResponse);

  // Delete removes a previously staged artifact identified by artifact_id.
  // No-op if the id does not exist (returns NOT_FOUND).
  rpc Delete(DeleteRequest) returns (DeleteResponse);
}
```

### 3.1 PullRequest

```proto
message PullRequest {
  // Required. Registry hostname[:port], e.g. "registry.example.com".
  string registry   = 1;

  // Required. Repository within the registry, e.g. "namespace/image".
  string repository = 2;

  // Required. Exactly one of tag/digest must be set. If both are set the
  // server MUST resolve the tag, compare against `digest`, and fail with
  // FAILED_PRECONDITION if they disagree.
  oneof reference {
    string tag    = 3;  // e.g. "20230531.46"
    string digest = 4;  // e.g. "sha256:6f0923e8…"
  }

  // Auth for the pull. Unset == anonymous.
  AuthConfig auth = 5;
}

message AuthConfig {
  oneof mode {
    Anonymous        anonymous = 1;
    BasicAuth        basic     = 2;
    BearerAuth       bearer    = 3;
    WorkloadIdentity workload  = 4;  // preferred
  }
}
message Anonymous        {}
message BasicAuth        { string username = 1; string password = 2; }
message BearerAuth       { string token = 1; }
message WorkloadIdentity {
  // Identifier of a federated identity already provisioned on the device
  // (e.g. via a sonic-host-services agent). The server exchanges it for a
  // registry access token at pull time. No secret material crosses the RPC.
  string identity_name = 1;
  // Optional. Token-exchange resource scope.
  string resource = 2;
}
```

Out-of-scope-for-v1 knobs deliberately deferred:

- `media_type_filter` — only meaningful once multi-layer artifacts are
  supported. v1 rejects multi-layer manifests, so a filter has nothing to do.
- `source_address` / `source_vrf` — handled one layer down by the routing /
  netns configuration; not an app-level RPC concern. Other gNOI services
  (`gnoi.os`, `gnoi.system.SetPackage`) don't carry them either.
- `http_proxy` — read from standard `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`
  env vars on the gnmi process (Go's `http.ProxyFromEnvironment`). Lab
  testbeds inject these in the gnmi container; production switches with a
  default route to the registry need no configuration.
- `skip_if_exists` — requires an on-disk manifest-digest store the agent
  doesn't yet keep. Caller idempotency can be approximated today by checking
  the artifact at `local_path` before issuing the RPC.
- `expected_manifest_digest` — redundant with the existing `digest` arm of
  the `reference` oneof, which already gives the caller a TOCTOU-safe
  digest-addressable pull.

### 3.2 PullResponse

```proto
message PullResponse {
  oneof event {
    PullStarted  started  = 1;
    PullProgress progress = 2;
    PullResult   result   = 3;
  }
}

message PullStarted {
  string manifest_digest = 1;  // resolved sha256:…
  uint64 total_bytes     = 2;  // sum of selected-layer sizes
  uint32 layer_count     = 3;
}

message PullProgress {
  uint64 bytes_transferred = 1;
  uint64 total_bytes       = 2;
  // Server SHOULD emit a PullProgress at most once per second to avoid
  // overwhelming slow clients.
}

message PullResult {
  // Opaque handle. Stable across server restarts; used by List/Delete and
  // passed to downstream installers.
  string artifact_id = 1;

  string manifest_digest = 2;

  // Per-layer breakdown of what actually landed on disk.
  repeated StoredLayer layers = 3;

  uint64                     bytes_written = 4;
  google.protobuf.Duration   elapsed       = 5;
}

message StoredLayer {
  string media_type = 1;
  string digest     = 2;  // sha256:…
  uint64 size       = 3;
  // Absolute path on target. Caller MUST treat this as read-only — the
  // server owns the file's lifetime via Delete.
  string local_path = 4;
}
```

### 3.3 List / Delete

```proto
message ListRequest  { string repository_filter = 1; }   // glob, optional
message ListResponse {
  repeated StoredArtifact artifacts        = 1;
  uint64                  total_used_bytes = 2;
  uint64                  disk_free_bytes  = 3;
}
message StoredArtifact {
  string                         artifact_id     = 1;
  string                         registry        = 2;
  string                         repository      = 3;
  string                         tag             = 4;  // may be empty if pulled by digest
  string                         manifest_digest = 5;
  repeated StoredLayer           layers          = 6;
  google.protobuf.Timestamp      pulled_at       = 7;
}

message DeleteRequest  { string artifact_id = 1; }
message DeleteResponse {}
```

## 4. Server behavior

### 4.1 Staging layout

```
/host/oras/                          # configurable; chosen to survive image upgrades
├── blobs/sha256/<digest>            # content-addressed, dedup across pulls
└── refs/<artifact_id>/              # one dir per pull
    ├── manifest.json                # the original OCI manifest
    └── layers/<idx>-<filename>      # symlink (or hardlink) into ../../blobs
```

- `artifact_id` is a UUIDv7 generated at pull time. Stable across reboots.
- Content addressing means two pulls of the same digest only consume disk once,
  even if the manifest is referenced under different tags / artifact_ids.
- Garbage-collecting blobs after a `Delete` requires a refcount check — every
  blob must be referenced by at least one ref dir, else delete the blob.

### 4.2 Concurrency

- **One in-flight `Pull` per `(registry, repository, manifest_digest)`**. A
  second concurrent Pull for the same target returns
  `FAILED_PRECONDITION` with a message naming the in-flight artifact_id.
- Pulls of different artifacts proceed in parallel, bounded by a configurable
  global semaphore (default 2) to avoid saturating the mgmt link.

### 4.3 Cancellation

- Client closes the stream → server cancels the in-flight ORAS pull, deletes
  the partial ref directory, leaves the blobs store untouched (blobs are
  always written to a `.tmp` suffix and renamed on completion).
- Server-side timeout (configurable, default 30 min) terminates orphaned pulls
  the same way.

### 4.4 Failure modes & status codes

| Condition                                    | gRPC status          |
| -------------------------------------------- | -------------------- |
| Missing/invalid request fields               | `INVALID_ARGUMENT`   |
| Auth rejected by registry                    | `UNAUTHENTICATED`    |
| Manifest digest mismatch (`expected_*`)      | `FAILED_PRECONDITION`|
| Concurrent Pull for same artifact            | `FAILED_PRECONDITION`|
| Registry unreachable (DNS/TCP/timeout)       | `UNAVAILABLE`        |
| Disk full / quota exceeded                   | `RESOURCE_EXHAUSTED` |
| Digest verification fails after download     | `DATA_LOSS`          |
| Client cancels                               | `CANCELLED`          |
| Anything else                                | `INTERNAL`           |

## 5. Authentication tiers

Three deployment tiers, in order of preference:

1. **AAD workload identity (preferred).** Device runs a host agent that holds
   a federated credential mapped to an AAD app with `AcrPull` on the target
   ACR. RPC names the identity (`identity_name`); no secret material crosses
   the wire. KubeSonic provisions one identity per fleet.

2. **Bearer token.** Orchestrator acquires the token, passes it in
   `BearerAuth`. Token expires quickly so leak blast radius is small.

3. **Basic (ACR admin user).** Only for bootstrapping and lab work. Logged as
   a warning. Plan to remove from the public API once tiers 1 & 2 are in
   place across the fleet.

mTLS on the gNMI channel itself is unchanged — same client cert as every other
gNMI RPC.

## 6. AuthZ

Reuse the existing gNMI authz hooks. New permission node:

```
sonic.gnoi.oras.Pull
sonic.gnoi.oras.List
sonic.gnoi.oras.Delete
```

`List` should be readable by ops/observability roles; `Pull` and `Delete`
require an explicit "image-mgmt" role.

## 7. Open questions

1. **Staging path policy.** `/host/oras/` survives image upgrades on a default
   SONiC install, but on platforms with a small `/host` partition this can be
   a problem. Should the path be discoverable via gNMI (e.g. as a separate
   `Status` RPC), or platform-configured in `device_metadata`?

2. **OS install integration.** Do we extend `gnoi.os.Install` to optionally
   take a `(registry, repository, digest)` and call our Pull internally, or
   keep them fully decoupled and require orchestrators to issue two RPCs?
   Leaning toward the latter for separation of concerns, but it's worth
   benchmarking the call-site complexity.

3. **Manifest schema validation.** Should the server enforce a SONiC-specific
   `artifactType` (e.g. `application/vnd.sonic.os-image.v1`) on Pull, or
   accept any manifest and trust the caller? v1 accepts any single-layer
   manifest; this can be tightened once we have a canonical artifactType.

4. **Push back: do we even need List/Delete on the device, or should
   inventory live in the control plane?** Argument for keeping them on-device:
   reboots and split-brain. Argument against: every switch becomes a tiny
   registry. Default position: keep them, they're cheap.

## 8. Out-of-scope follow-ups

- An `ImageStatus` / `ImageHealth` RPC reporting which staged artifacts are
  currently in use by the running image, candidate slot, container, etc.
- A `Prefetch` daemon on the device that subscribes to a control-plane stream
  of "expected next image" hints and pulls in the background.
- A signed-manifest verification step (cosign / notary).

## 9. References

- openconfig/gnoi: <https://github.com/openconfig/gnoi>
  - `file/file.proto` — `TransferToRemote`, `Put`, `Get`.
  - `system/system.proto` — `SetPackage`.
  - `common/common.proto` — `RemoteDownload`.
  - `containerz/containerz.proto` — streaming `Deploy` pattern.
- ORAS: <https://oras.land>
- OCI image spec: <https://github.com/opencontainers/image-spec>
- ADO Feature #37984064 (internal).
