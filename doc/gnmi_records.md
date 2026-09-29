# RECORDS: streaming the SONiC recorder files over gNMI

## Motivation

SONiC's orchestration layer already writes two high-fidelity audit logs:

- **`swss.rec`** — every APPL_DB operation orchagent processes, in order.
- **`sairedis.rec`** — every SAI call sairedis issues to the ASIC, with the
  attributes and (in sync mode) the return status.

These files are the ground truth for *what the switch actually tried to do and
in what order*. Today they are only reachable by shelling into the `gnmi` /
`swss` container and `tail`-ing a file.

gNMI `ON_CHANGE` cannot substitute for them. `ON_CHANGE` reports the *current
value* of a DB entry once it settles; it collapses intermediate operations, drops
ordering, and has no notion of a SAI call that was attempted and **failed**. A
failed SAI program (e.g. deleting a next-hop group still in use) never lands in
CONFIG_DB/APPL_DB state, so `ON_CHANGE` observers never see it — but it is
recorded as an `E|<status>` line in `sairedis.rec`.

**RECORDS** exposes both files as a first-class gNMI subscription target so an
off-box collector can consume the ordered operation stream — history *and* live —
without container access.

## Path grammar

```
/RECORDS/<namespace>/<DB>/<TABLE>[/<key...>][from=<ts>][ops=<filter>]
```

| Element        | Values                                                              |
| -------------- | ------------------------------------------------------------------ |
| `<namespace>`  | `localhost` (single-ASIC) or `asicN` (multi-ASIC)                  |
| `<DB>`         | `APPL_DB` (→ `swss.rec`) or `ASIC_DB` (→ `sairedis.rec`)           |
| `<TABLE>[/key]`| optional prefix filter on the record's table/key                   |
| `from=`        | `RFC3339` / epoch / relative (`-30m`, `-1d`) — replay then tail    |
| `ops=`         | operation filter, e.g. `ops=SET,DEL`, `ops=E` (SAI failures only)  |

Example:

```
# All ROUTE_TABLE operations for the last 10 minutes, then live:
/RECORDS/localhost/APPL_DB/ROUTE_TABLE?from=-10m

# Only SAI failures, live:
/RECORDS/localhost/ASIC_DB?ops=E
```

## Behaviour

- **STREAM only.** POLL and ONCE are rejected with `Unimplemented` — RECORDS is
  an append-only log, not a snapshot source.
- **History + live in one subscription.** With `from=`, the client replays
  matching historical lines (including from rotated `.gz` segments) and then
  transparently continues tailing new lines. Without `from=`, it tails from the
  current end of file.
- **Resumption.** Every emitted record carries a `seq` token
  `<source>:<inode>:<offset>`. A collector that reconnects can resume exactly
  where it left off; the inode component makes the token robust across logrotate.

## Architecture

```
  swss.rec / sairedis.rec (+ .gz)      gNMI STREAM subscription
            |                                     ^
            v                                     |
   +-----------------+   records   +----------------------------+
   |  FileTailer     |------------>|  RecordsClient (StreamRun) |
   |  (poll+inode)   |  parsed     |  parse -> match -> correlate|
   +-----------------+  Record[]   +----------------------------+
```

- **FileTailer** (`records_tailer.go`) — pure Go stdlib, no `fsnotify`. It polls
  at 200 ms, fingerprints files by inode to survive logrotate, replays rotated
  `.gz` segments on `from=`, and is truncation-safe (detects shrink and reopens).
  A multi-file tailer fans in `swss.rec` + `sairedis.rec`.
- **Parser** (`records_parser.go`) — decodes the two on-disk formats:
  - swss.rec: `ts|TABLE:key|op|field:value...`
  - sairedis.rec: `ts|opcode|key|attr=val...` incl. bulk (`||`) and api-name
    variants, plus `E|<status>` failure lines.
- **Matcher** (`records_match.go`) — applies the table/key prefix and `ops=`
  filters.
- **Correlation** — joins an APPL_DB write to the ASIC_DB SAI operations it
  triggered, so a collector can see cause (intent) and effect (SAI program)
  together.
- **RecordsClient** (`records_client.go`) — implements the telemetry `Client`
  interface; only `StreamRun` is meaningful.

## Configuration

Two telemetry flags (plumbed in `telemetry/telemetry.go`):

| Flag            | Default                     | Meaning                                            |
| --------------- | --------------------------- | -------------------------------------------------- |
| `-records_dir`  | `/mnt/host/var/log/swss`    | Directory holding the `.rec` files                 |
| `-records_tz`   | process local zone          | IANA TZ for the zone-less timestamps in the files  |

Container wiring (feature-detected) lives in
`sonic-buildimage/dockers/docker-sonic-gnmi/gnmi-native.sh`. The `gnmi`
container must mount the host recorder directory (read-only is sufficient) and
share the host timezone so the local timestamps parse correctly.

## Notes / limitations

- **`E` records require sync sairedis.** In async mode, and on `sonic-vs` (whose
  `libsaivs` returns `SAI_STATUS_SUCCESS` for everything), no `E` lines are
  produced. On real hardware in sync mode, a failed SAI call is recorded as
  `E|<status>`; `ops=E` surfaces exactly these.
- Timestamps in the `.rec` files are zone-less local time; `-records_tz` (or the
  container's TZ) must match the host to parse correctly.
