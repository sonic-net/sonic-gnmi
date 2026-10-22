#!/usr/bin/env python3
"""Generate the RECORDS fixture scenario, its golden outputs and the
multi-ASIC namespace fixtures.

Run from the repo root:   python3 testdata/records/scenario/gen_scenario.py

Everything under testdata/records/scenario/ and testdata/records/multi_asic/
is produced by this script; edit the script, not the files.

Line shapes follow the real samples collected from the VS
(testdata/records/README.md), not the idealised sketch in the plan:
  * routes are programmed with bulk ops (C / R), object type stated once,
    entries separated by "||";
  * sairedis has no E opcode today; the E lines here are synthetic
    (plan risk #2) and a real-shape Q|<api>|SAI_STATUS_... failure is
    included alongside them;
  * a "#|logrotate on: ..." banner opens every rotated sairedis file;
  * swss.rec DEL carries no fields;
  * SAI_OBJECT_TYPE_ROUTE_ENTRY keys are rendered "dest=<prefix>[,nh=<oid>]",
    one record per bulk entry, raw JSON key kept in fields["_entry"].
"""
import gzip
import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)          # testdata/records
SCENARIO = HERE
MULTI = os.path.join(ROOT, "multi_asic")
GOLDEN = os.path.join(SCENARIO, "golden")

SWITCH = "oid:0x21000000000000"
VR = "oid:0x3000000000022"
RIF0 = "oid:0x6000000000a10"
BVID100 = "oid:0x26000000000604"
NH1 = "oid:0x4000000000a01"
NH2 = "oid:0x4000000000a02"
NHG_A = "oid:0x5000000000a3c"   # the group the failing remove targets
NHG_B = "oid:0x5000000000a3d"
PREFIX = "10.1.0.0/24"
NEIGH_IP = "10.0.0.1"
MAC = "00:11:22:33:44:55"

DAY = "2026-09-27"


def ts(hms, us):
    return f"{DAY}.{hms}.{us:06d}"


def ts_json(hms, us):
    return f"{DAY}T{hms}.{us:06d}"


def route_key(dest):
    return json.dumps({"dest": dest, "switch_id": SWITCH, "vr": VR},
                      separators=(",", ":"))


def neigh_key(ip):
    return json.dumps({"ip": ip, "rif": RIF0, "switch_id": SWITCH},
                      separators=(",", ":"))


def fdb_key(mac):
    return json.dumps({"bvid": BVID100, "mac": mac, "switch_id": SWITCH},
                      separators=(",", ":"))


# ---------------------------------------------------------------------------
# Scenario timeline. Oldest file first: .2.gz (09:00), .1 (10:00), live (11:00)
# ---------------------------------------------------------------------------

swss_2 = [
    ts("09:00:00", 100000) + f"|NEIGH_TABLE:Ethernet0:{NEIGH_IP}|SET|neigh:{MAC}|family:IPv4",
    ts("09:00:01", 200000) + f"|ROUTE_TABLE:{PREFIX}|SET|nexthop:{NEIGH_IP}|ifname:Ethernet0",
    ts("09:00:02", 300000) + "|ROUTE_TABLE:192.0.2.0/24|SET|nexthop:10.0.0.2|ifname:Ethernet4",
]

sai_2 = [
    ts("09:00:00", 50000) + "|#|logrotate on: /var/log/swss/sairedis.rec",
    ts("09:00:00", 150000) + f"|c|SAI_OBJECT_TYPE_NEIGHBOR_ENTRY:{neigh_key(NEIGH_IP)}"
                             f"|SAI_NEIGHBOR_ENTRY_ATTR_DST_MAC_ADDRESS={MAC}",
    ts("09:00:00", 160000) + f"|c|SAI_OBJECT_TYPE_NEXT_HOP:{NH1}|SAI_NEXT_HOP_ATTR_TYPE=SAI_NEXT_HOP_TYPE_IP"
                             f"|SAI_NEXT_HOP_ATTR_IP={NEIGH_IP}|SAI_NEXT_HOP_ATTR_ROUTER_INTERFACE_ID={RIF0}",
    ts("09:00:01", 210000) + f"|c|SAI_OBJECT_TYPE_NEXT_HOP_GROUP:{NHG_A}"
                             "|SAI_NEXT_HOP_GROUP_ATTR_TYPE=SAI_NEXT_HOP_GROUP_TYPE_DYNAMIC_UNORDERED_ECMP",
    ts("09:00:01", 250000) + f"|C|SAI_OBJECT_TYPE_ROUTE_ENTRY||{route_key(PREFIX)}"
                             f"|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID={NHG_A}",
    ts("09:00:02", 350000) + f"|C|SAI_OBJECT_TYPE_ROUTE_ENTRY||{route_key('192.0.2.0/24')}"
                             f"|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID={NH2}",
    # notification line, real shape (fdb_event with escaped JSON payload)
    ts("09:00:03", 400000) + "|n|fdb_event|" + json.dumps([{
        "fdb_entry": fdb_key("FE:54:00:92:E0:02"),
        "fdb_event": "SAI_FDB_EVENT_LEARNED",
        "list": [{"id": "SAI_FDB_ENTRY_ATTR_TYPE", "value": "SAI_FDB_ENTRY_TYPE_DYNAMIC"}],
    }], separators=(",", ":")),
]

swss_1 = [
    ts("10:00:00", 100000) + f"|ROUTE_TABLE:{PREFIX}|SET|nexthop:{NEIGH_IP},10.0.0.2|ifname:Ethernet0,Ethernet4",
    ts("10:00:01", 200000) + f"|FDB_TABLE:Vlan100:{MAC}|SET|port:Ethernet8|type:dynamic",
    ts("10:00:02", 300000) + "|NEIGH_TABLE:Ethernet0:fe80::1|SET|neigh:00:aa:bb:cc:dd:ee|family:IPv6",
]

# A bulk create of many routes; long enough to exceed bufio.Scanner's 64 KB
# default so WS2's "4 MB cap / ReadLine loop" is exercised.
bulk_entries = "".join(
    f"||{route_key(f'10.{i // 256}.{i % 256}.0/24')}|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID={NH2}"
    for i in range(2048, 2048 + 700)
)
LONG_BULK = ts("10:00:03", 350000) + "|C|SAI_OBJECT_TYPE_ROUTE_ENTRY" + bulk_entries
assert len(LONG_BULK) > 64 * 1024, len(LONG_BULK)

sai_1 = [
    ts("10:00:00", 50000) + "|#|logrotate on: /var/log/swss/sairedis.rec",
    ts("10:00:00", 120000) + f"|c|SAI_OBJECT_TYPE_NEXT_HOP_GROUP:{NHG_B}"
                             "|SAI_NEXT_HOP_GROUP_ATTR_TYPE=SAI_NEXT_HOP_GROUP_TYPE_DYNAMIC_UNORDERED_ECMP",
    ts("10:00:00", 150000) + f"|s|SAI_OBJECT_TYPE_ROUTE_ENTRY:{route_key(PREFIX)}"
                             f"|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID={NHG_B}",
    # synthetic failure: remove of a group still referenced (plan shape)
    ts("10:00:00", 180000) + f"|r|SAI_OBJECT_TYPE_NEXT_HOP_GROUP:{NHG_A}",
    ts("10:00:00", 180040) + "|E|SAI_STATUS_OBJECT_IN_USE",
    ts("10:00:01", 220000) + f"|c|SAI_OBJECT_TYPE_FDB_ENTRY:{fdb_key(MAC)}"
                             "|SAI_FDB_ENTRY_ATTR_TYPE=SAI_FDB_ENTRY_TYPE_DYNAMIC"
                             "|SAI_FDB_ENTRY_ATTR_BRIDGE_PORT_ID=oid:0x3a000000000a20",
    # real-shape failure: query + response carrying a non-success status
    ts("10:00:02", 300000) + f"|q|object_type_get_availability|SAI_OBJECT_TYPE_SWITCH:{SWITCH}"
                             "|SAI_ROUTE_ENTRY_ATTR_IP_ADDR_FAMILY=SAI_IP_ADDR_FAMILY_IPV4",
    ts("10:00:02", 301000) + "|Q|object_type_get_availability|SAI_STATUS_NOT_SUPPORTED|COUNT=0",
    LONG_BULK,
]

swss_live = [
    ts("11:00:00", 100000) + f"|ROUTE_TABLE:{PREFIX}|DEL",
    ts("11:00:01", 200000) + f"|NEIGH_TABLE:Ethernet0:{NEIGH_IP}|DEL",
    ts("11:00:02", 300000) + f"|FDB_TABLE:Vlan100:{MAC}|DEL",
]

sai_live = [
    ts("11:00:00", 50000) + "|#|logrotate on: /var/log/swss/sairedis.rec",
    ts("11:00:00", 150000) + f"|R|SAI_OBJECT_TYPE_ROUTE_ENTRY||{route_key(PREFIX)}",
    ts("11:00:00", 160000) + f"|r|SAI_OBJECT_TYPE_NEXT_HOP_GROUP:{NHG_A}",
    ts("11:00:01", 250000) + f"|r|SAI_OBJECT_TYPE_NEIGHBOR_ENTRY:{neigh_key(NEIGH_IP)}",
    ts("11:00:02", 350000) + f"|r|SAI_OBJECT_TYPE_FDB_ENTRY:{fdb_key(MAC)}",
    # real-shape get + success response
    ts("11:00:03", 400000) + f"|g|SAI_OBJECT_TYPE_SWITCH:{SWITCH}|SAI_SWITCH_ATTR_DEFAULT_VIRTUAL_ROUTER_ID=oid:0x0",
    ts("11:00:03", 400600) + f"|G|SAI_STATUS_SUCCESS|SAI_SWITCH_ATTR_DEFAULT_VIRTUAL_ROUTER_ID={VR}",
]

# ---------------------------------------------------------------------------
# Multi-ASIC namespace fixtures: each asicN pair has distinct prefixes so a
# test can prove asic0 never leaks into asic1 and vice versa.
# ---------------------------------------------------------------------------
multi = {
    "swss.asic0.rec": [
        ts("12:00:00", 100000) + "|ROUTE_TABLE:10.0.0.0/24|SET|nexthop:10.10.0.1|ifname:Ethernet0",
        ts("12:00:01", 100000) + "|NEIGH_TABLE:Ethernet0:10.10.0.1|SET|neigh:00:00:00:00:a0:01|family:IPv4",
    ],
    "sairedis.asic0.rec": [
        ts("12:00:00", 50000) + "|#|logrotate on: /var/log/swss/sairedis.asic0.rec",
        ts("12:00:00", 150000) + f"|C|SAI_OBJECT_TYPE_ROUTE_ENTRY||{route_key('10.0.0.0/24')}"
                                 f"|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID={NH1}",
    ],
    "swss.asic1.rec": [
        ts("12:00:00", 100000) + "|ROUTE_TABLE:10.1.1.0/24|SET|nexthop:10.11.0.1|ifname:Ethernet0",
        ts("12:00:02", 100000) + "|ROUTE_TABLE:10.1.1.0/24|DEL",
    ],
    "sairedis.asic1.rec": [
        ts("12:00:00", 50000) + "|#|logrotate on: /var/log/swss/sairedis.asic1.rec",
        ts("12:00:00", 150000) + f"|C|SAI_OBJECT_TYPE_ROUTE_ENTRY||{route_key('10.1.1.0/24')}"
                                 f"|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID={NH1}",
        ts("12:00:01", 150000) + f"|s|SAI_OBJECT_TYPE_ROUTE_ENTRY:{route_key('10.1.1.0/24')}"
                                 f"|SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID={NH2}",
        ts("12:00:02", 150000) + f"|R|SAI_OBJECT_TYPE_ROUTE_ENTRY||{route_key('10.1.1.0/24')}",
    ],
}

# ---------------------------------------------------------------------------
# Golden outputs. One JSON file per example path from the plan (section 1).
# Records are what the operator should receive for the WHOLE scenario, in file
# order (oldest file first), with `seq` omitted (it depends on inode/offset).
# `raw` is included so a test can cross-check each golden against the fixture
# line it came from.
# ---------------------------------------------------------------------------


def rec(raw, source, db, table, key, op, fields, matched_by, status=""):
    t, _ = raw.split("|", 1)
    d, hms_us = t.split(".", 1)
    hms, us = hms_us.rsplit(".", 1)
    return {
        "ts": f"{d}T{hms}.{us}",
        "source": source,
        "db": db,
        "table": table,
        "key": key,
        "op": op,
        "fields": fields,
        "status": status,
        "matched_by": matched_by,
        "raw": raw,
    }


def swss_rec(raw, matched_by):
    parts = raw.split("|")
    table, key = parts[1].split(":", 1)
    fields = dict(p.split(":", 1) for p in parts[3:])
    return rec(raw, "swss", "APPL_DB", table, key, parts[2], fields, matched_by)


def route_key(raw_key, attrs):
    """Operator-facing key for SAI_OBJECT_TYPE_ROUTE_ENTRY: dest=<prefix>[,nh=<oid>]."""
    try:
        dest = json.loads(raw_key)["dest"]
    except Exception:
        dest = raw_key[:128]
    key = "dest=" + dest
    if attrs.get("SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID"):
        key += ",nh=" + attrs["SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID"]
    return key


def sai_recs(raw, matched_by, status="", extra_fields=None):
    """One golden record per entry of a sairedis line (bulk lines yield several)."""
    parts = raw.split("|")
    op = parts[1]
    if op.isupper():          # bulk: type once, then ||key|attrs groups
        table = parts[2]
        groups = raw.split("||")[1:]
        entries = [(g.split("|", 1)[0], g.split("|")[1:]) for g in groups]
    else:
        table, key = parts[2].split(":", 1)
        entries = [(key, parts[3:])]
    out = []
    for i, (key, avs) in enumerate(entries):
        fields = dict(a.split("=", 1) for a in avs if a)
        fields["_entry"] = key
        if op.isupper():
            fields["_bulk_index"] = str(i)
            fields["_bulk_count"] = str(len(entries))
        if extra_fields:
            fields.update(extra_fields)
        shown = route_key(key, fields) if table == "SAI_OBJECT_TYPE_ROUTE_ENTRY" else key
        out.append(rec(raw, "sairedis", "ASIC_DB", table, shown, op, fields, matched_by, status))
    return out


def sai_rec(raw, matched_by, status="", extra_fields=None):
    """Single-entry convenience wrapper around sai_recs."""
    recs = sai_recs(raw, matched_by, status, extra_fields)
    assert len(recs) == 1, raw[:80]
    return recs[0]


CORR_ROUTE = "correlation:ROUTE_TABLE.dest"

golden = {
    # /RECORDS/localhost/APPL_DB/ROUTE_TABLE/10.1.0.0/24[from=...]
    "appl_route_key.json": [
        swss_rec(swss_2[1], "key"),
        sai_rec(sai_2[4], CORR_ROUTE),
        swss_rec(swss_1[0], "key"),
        sai_rec(sai_1[2], CORR_ROUTE),
        swss_rec(swss_live[0], "key"),
        sai_rec(sai_live[1], CORR_ROUTE),
    ],
    # /RECORDS/localhost/APPL_DB/NEIGH_TABLE  (whole-table prefix match)
    "appl_neigh_table.json": [
        swss_rec(swss_2[0], "prefix"),
        sai_rec(sai_2[1], "correlation:NEIGH_TABLE.ip"),
        swss_rec(swss_1[2], "prefix"),
        swss_rec(swss_live[1], "prefix"),
        sai_rec(sai_live[3], "correlation:NEIGH_TABLE.ip"),
    ],
    # /RECORDS/localhost/ASIC_DB/ASIC_STATE/SAI_OBJECT_TYPE_NEXT_HOP_GROUP:oid:0x5000000000a3c[ops=E]
    # The failed remove is re-emitted once its E line arrives: Op unchanged,
    # Status set, Fields["_response"]="E".
    "asic_nhg_failures.json": [
        sai_rec(sai_1[3], "key", status="SAI_STATUS_OBJECT_IN_USE", extra_fields={"_response": "E"}),
    ],
    # /RECORDS/asic1/ASIC_DB/ASIC_STATE/SAI_OBJECT_TYPE_ROUTE_ENTRY[from=...]
    "asic1_route_entries.json": [
        sai_rec(multi["sairedis.asic1.rec"][1], "prefix"),
        sai_rec(multi["sairedis.asic1.rec"][2], "prefix"),
        sai_rec(multi["sairedis.asic1.rec"][3], "prefix"),
    ],
}


def write_lines(path, lines, gz=False):
    data = ("\n".join(lines) + "\n").encode()
    if gz:
        # mtime=0 keeps the archive byte-identical across regenerations.
        with open(path, "wb") as raw, gzip.GzipFile(fileobj=raw, mode="wb", mtime=0) as f:
            f.write(data)
    else:
        with open(path, "wb") as f:
            f.write(data)


def main():
    os.makedirs(GOLDEN, exist_ok=True)
    os.makedirs(MULTI, exist_ok=True)

    write_lines(os.path.join(SCENARIO, "swss.rec"), swss_live)
    write_lines(os.path.join(SCENARIO, "swss.rec.1"), swss_1)
    write_lines(os.path.join(SCENARIO, "swss.rec.2.gz"), swss_2, gz=True)
    write_lines(os.path.join(SCENARIO, "sairedis.rec"), sai_live)
    write_lines(os.path.join(SCENARIO, "sairedis.rec.1"), sai_1)
    write_lines(os.path.join(SCENARIO, "sairedis.rec.2.gz"), sai_2, gz=True)

    for name, lines in multi.items():
        write_lines(os.path.join(MULTI, name), lines)

    for name, recs in golden.items():
        with open(os.path.join(GOLDEN, name), "w") as f:
            json.dump(recs, f, indent=2)
            f.write("\n")

    print(f"wrote scenario ({len(swss_2)+len(swss_1)+len(swss_live)} swss, "
          f"{len(sai_2)+len(sai_1)+len(sai_live)} sairedis lines; "
          f"long bulk line {len(LONG_BULK)} bytes), "
          f"{len(multi)} multi_asic files, {len(golden)} goldens")


if __name__ == "__main__":
    main()
