//go:build records

package client

import (
	"testing"
)

func mkRecord(source, db, table, key, op, status string) *Record {
	return &Record{
		Source: source,
		DB:     db,
		Table:  table,
		Key:    key,
		Op:     op,
		Status: status,
		Fields: map[string]string{},
	}
}

func TestDirectExactMatch(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "APPL_DB", Table: "ROUTE_TABLE", Key: "10.1.0.0/24"},
	})
	r := mkRecord("swss", "APPL_DB", "ROUTE_TABLE", "10.1.0.0/24", "SET", "")
	ok, how := m.Match(r)
	if !ok || how != "exact" {
		t.Errorf("got ok=%v how=%q, want ok=true how=exact", ok, how)
	}
}

func TestDirectExactMiss(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "APPL_DB", Table: "ROUTE_TABLE", Key: "10.2.0.0/24"},
	})
	r := mkRecord("swss", "APPL_DB", "ROUTE_TABLE", "10.1.0.0/24", "SET", "")
	ok, _ := m.Match(r)
	if ok {
		t.Error("expected miss for different key")
	}
}

func TestDirectPrefixMatch(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "APPL_DB", Table: "ROUTE_TABLE", Key: ""},
	})
	r := mkRecord("swss", "APPL_DB", "ROUTE_TABLE", "10.1.0.0/24", "SET", "")
	ok, how := m.Match(r)
	if !ok || how != "prefix" {
		t.Errorf("got ok=%v how=%q, want prefix match", ok, how)
	}
}

func TestDirectTableMiss(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "APPL_DB", Table: "NEIGH_TABLE", Key: ""},
	})
	r := mkRecord("swss", "APPL_DB", "ROUTE_TABLE", "10.1.0.0/24", "SET", "")
	ok, _ := m.Match(r)
	if ok {
		t.Error("expected miss for different table")
	}
}

func TestCorrelationApplToSai(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "APPL_DB", Table: "ROUTE_TABLE", Key: "10.1.0.0/24"},
	})

	r := mkRecord("sairedis", "ASIC_DB", "SAI_OBJECT_TYPE_ROUTE_ENTRY",
		`{"dest":"10.1.0.0/24","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000022"}`,
		"c", "")

	ok, how := m.Match(r)
	if !ok {
		t.Error("expected correlation hit")
	}
	if how != "correlation:ROUTE_TABLE.dest" {
		t.Errorf("how = %q, want correlation:ROUTE_TABLE.dest", how)
	}
}

func TestCorrelationApplToSaiMiss(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "APPL_DB", Table: "ROUTE_TABLE", Key: "10.2.0.0/24"},
	})

	r := mkRecord("sairedis", "ASIC_DB", "SAI_OBJECT_TYPE_ROUTE_ENTRY",
		`{"dest":"10.1.0.0/24","switch_id":"oid:0x21000000000000"}`,
		"c", "")

	ok, _ := m.Match(r)
	if ok {
		t.Error("expected miss for different prefix")
	}
}

func TestCorrelationNeigh(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "APPL_DB", Table: "NEIGH_TABLE", Key: "Ethernet0:10.0.0.1"},
	})

	r := mkRecord("sairedis", "ASIC_DB", "SAI_OBJECT_TYPE_NEIGHBOR_ENTRY",
		`{"ip_address":"10.0.0.1","rif_id":"oid:0x6000000000abc","switch_id":"oid:0x21000000000000"}`,
		"c", "")

	ok, how := m.Match(r)
	if !ok {
		t.Error("expected correlation hit for NEIGH")
	}
	if how != "correlation:NEIGH_TABLE.ip_address" {
		t.Errorf("how = %q", how)
	}
}

func TestCorrelationTablePrefixNoKey(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "APPL_DB", Table: "ROUTE_TABLE", Key: ""},
	})

	r := mkRecord("sairedis", "ASIC_DB", "SAI_OBJECT_TYPE_ROUTE_ENTRY",
		`{"dest":"192.168.1.0/24"}`, "c", "")

	ok, how := m.Match(r)
	if !ok {
		t.Error("expected correlation hit for table-level sub")
	}
	if how != "correlation:ROUTE_TABLE" {
		t.Errorf("how = %q", how)
	}
}

func TestCorrelationNoRuleForOidType(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "APPL_DB", Table: "PORT_TABLE", Key: "Ethernet0"},
	})

	r := mkRecord("sairedis", "ASIC_DB", "SAI_OBJECT_TYPE_PORT",
		"oid:0x1000000000001", "c", "")

	ok, _ := m.Match(r)
	if ok {
		t.Error("expected miss — no correlation rule for OID-based PORT")
	}
}

func TestReverseCorrelation(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "ASIC_DB", Table: "SAI_OBJECT_TYPE_ROUTE_ENTRY",
			Key: `{"dest":"10.1.0.0/24","switch_id":"oid:0x21000000000000","vr":"oid:0x3000000000022"}`},
	})

	r := mkRecord("swss", "APPL_DB", "ROUTE_TABLE", "10.1.0.0/24", "SET", "")

	ok, how := m.Match(r)
	if !ok {
		t.Error("expected reverse correlation hit")
	}
	if how != "reverse-correlation:SAI_OBJECT_TYPE_ROUTE_ENTRY.dest" {
		t.Errorf("how = %q", how)
	}
}

func TestOpsFilterPass(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "APPL_DB", Table: "ROUTE_TABLE", Key: "", Ops: []string{"SET"}},
	})

	rSet := mkRecord("swss", "APPL_DB", "ROUTE_TABLE", "10.1.0.0/24", "SET", "")
	rDel := mkRecord("swss", "APPL_DB", "ROUTE_TABLE", "10.1.0.0/24", "DEL", "")

	ok, _ := m.Match(rSet)
	if !ok {
		t.Error("SET should pass ops=SET filter")
	}
	ok, _ = m.Match(rDel)
	if ok {
		t.Error("DEL should NOT pass ops=SET filter")
	}
}

func TestOpsFilterE(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "ASIC_DB", Table: "SAI_OBJECT_TYPE_ROUTE_ENTRY", Key: "", Ops: []string{"E"}},
	})

	rOk := mkRecord("sairedis", "ASIC_DB", "SAI_OBJECT_TYPE_ROUTE_ENTRY",
		`{"dest":"10.1.0.0/24"}`, "c", "")
	rFail := mkRecord("sairedis", "ASIC_DB", "SAI_OBJECT_TYPE_ROUTE_ENTRY",
		`{"dest":"10.1.0.0/24"}`, "c", "SAI_STATUS_TABLE_FULL")

	ok, _ := m.Match(rOk)
	if ok {
		t.Error("success record should NOT pass ops=E filter")
	}
	ok, _ = m.Match(rFail)
	if !ok {
		t.Error("failed record should pass ops=E filter")
	}
}

func TestOpsFilterNone(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "APPL_DB", Table: "ROUTE_TABLE", Key: ""},
	})

	r := mkRecord("swss", "APPL_DB", "ROUTE_TABLE", "10.1.0.0/24", "DEL", "")
	ok, _ := m.Match(r)
	if !ok {
		t.Error("no ops filter should pass everything")
	}
}

func TestMultiSubscription(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "APPL_DB", Table: "ROUTE_TABLE", Key: "10.1.0.0/24"},
		{DB: "APPL_DB", Table: "NEIGH_TABLE", Key: ""},
	})

	rRoute := mkRecord("swss", "APPL_DB", "ROUTE_TABLE", "10.1.0.0/24", "SET", "")
	rNeigh := mkRecord("swss", "APPL_DB", "NEIGH_TABLE", "Ethernet0:10.0.0.1", "SET", "")
	rPort := mkRecord("swss", "APPL_DB", "PORT_TABLE", "Ethernet0", "SET", "")

	ok, _ := m.Match(rRoute)
	if !ok {
		t.Error("route should match")
	}

	ok, _ = m.Match(rNeigh)
	if !ok {
		t.Error("neigh should match")
	}

	ok, _ = m.Match(rPort)
	if ok {
		t.Error("port should not match any subscription")
	}
}

func TestCIDRNormalisationInMatch(t *testing.T) {
	m := NewRecordsMatcher([]Subscription{
		{DB: "APPL_DB", Table: "ROUTE_TABLE", Key: "10.1.0.5/24"},
	})
	r := mkRecord("swss", "APPL_DB", "ROUTE_TABLE", "10.1.0.0/24", "SET", "")
	ok, how := m.Match(r)
	if !ok || how != "exact" {
		t.Errorf("CIDR normalisation should make these match, got ok=%v how=%q", ok, how)
	}
}
