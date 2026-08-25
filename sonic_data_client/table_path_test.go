package client

import (
	"fmt"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestTablePathFormattingRedactsPayloads(t *testing.T) {
	protoPayload := "proto-do-not-log" + string([]byte{0xe1, 0x00, 0xff})
	jsonPayload := `{"password":"do-not-log"}`
	path := tablePath{
		dbNamespace: "localhost",
		dbName:      "DPU_APPL_DB",
		tableName:   "DASH_VNET_TABLE",
		tableKey:    string([]byte{'V', 0xe1}),
		field:       "pb",
		jsonValue:   jsonPayload,
		protoValue:  protoPayload,
		index:       -1,
		operation:   opAdd,
	}

	for _, format := range []string{"%v", "%+v", "%#v"} {
		for _, value := range []any{path, []tablePath{path}} {
			got := fmt.Sprintf(format, value)
			if !utf8.ValidString(got) {
				t.Fatalf("format %s produced invalid UTF-8: %q", format, got)
			}
			for _, r := range got {
				if r > unicode.MaxASCII {
					t.Fatalf("format %s produced non-ASCII output: %q", format, got)
				}
			}
			for _, marker := range []string{"proto-do-not-log", "do-not-log"} {
				if strings.Contains(got, marker) {
					t.Fatalf("format %s exposed payload marker %q: %q", format, marker, got)
				}
			}
			for _, want := range []string{
				`db="DPU_APPL_DB"`,
				`table="DASH_VNET_TABLE"`,
				`key="V\xe1"`,
				`operation="add"`,
				`json_bytes=25`,
				`proto_bytes=19`,
			} {
				if !strings.Contains(got, want) {
					t.Errorf("format %s missing %s: %q", format, want, got)
				}
			}
		}
	}
}
