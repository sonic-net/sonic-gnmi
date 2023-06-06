////////////////////////////////////////////////////////////////////////////////
//                                                                            //
//  Copyright 2021 Broadcom. The term Broadcom refers to Broadcom Inc. and/or //
//  its subsidiaries.                                                         //
//                                                                            //
//  Licensed under the Apache License, Version 2.0 (the "License");           //
//  you may not use this file except in compliance with the License.          //
//  You may obtain a copy of the License at                                   //
//                                                                            //
//     http://www.apache.org/licenses/LICENSE-2.0                             //
//                                                                            //
//  Unless required by applicable law or agreed to in writing, software       //
//  distributed under the License is distributed on an "AS IS" BASIS,         //
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.  //
//  See the License for the specific language governing permissions and       //
//  limitations under the License.                                            //
//                                                                            //
////////////////////////////////////////////////////////////////////////////////

package transl_utils

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"testing"

	"github.com/Azure/sonic-mgmt-common/translib/ocbinds"
	"github.com/openconfig/ygot/ygot"
	"github.com/openconfig/ygot/ytypes"
)

var (
	benchSize        string
	useCommunityDiff bool
)

func TestMain(m *testing.M) {
	flag.StringVar(&benchSize, "benchsize", "", "Benchmark size")
	flag.BoolVar(&useCommunityDiff, "ygot.Diff", false, "Use community ygot.Diff API for diffs")
	flag.Parse()

	if benchSize == "" && hasFlag("test.bench") {
		benchSize = "1x1"
	}
	if benchSize != "" {
		setFlag("test.run", "^$")
		setFlag("test.bench", ".")
		setFlag("test.benchmem", "true")
		initBenchmarkTests()
	}

	os.Exit(m.Run())
}

func hasFlag(name string) bool {
	f := flag.Lookup(name)
	return f.Value.String() != f.DefValue
}

func setFlag(name, value string) {
	if !hasFlag(name) {
		flag.Set(name, value)
	}
}

var (
	aclX ygot.GoStruct // reference ACL tree
	aclY ygot.GoStruct // clone of aclX with 1 attr changed
	aclZ ygot.GoStruct // same size as aclX, but with all attr changed
	acl0 ygot.GoStruct // with no ACLs
)

func initBenchmarkTests() {
	var m, n uint32
	if x, err := fmt.Sscanf(benchSize, "%dx%d", &m, &n); x != 2 || err != nil {
		panic("Invalid benchSize: " + benchSize)
	}

	fmt.Printf("Using benchSize=%dx%d, communityDiff=%v\n", m, n, useCommunityDiff)

	aclX = buildACL(1, m, 1, n, "foo-", "ACCEPT", "IP_TCP", "1.0.0.0/8", "0.0.0.0/0")
	aclZ = buildACL(1, m, 1, n, "bar-", "REJECT", "IP_UDP", "2.0.0.0/8", "1.2.3.4/32")
	acl0 = buildACL(0, 0, 0, 0, "", "", "", "", "")

	aclY, _ = ygot.DeepCopy(aclX)
	deleteNodes(aclY, "/acl/acl-sets/acl-set[name=ACL-1][type=ACL_IPV4]/state/description")
}

func buildACL(aclIdStart, numAcls, ruleIdStart, numRules uint32, descr, action, proto, sip, dip string) ygot.GoStruct {
	ruleIpv4 := fmt.Sprintf(`"protocol":"%s", "source-address":"%s", "destination-address":"%s"`, proto, sip, dip)
	b := new(bytes.Buffer)
	b.WriteString(`{"acl":{"acl-sets":{"acl-set":[`)
	for i := uint32(0); i < numAcls; i++ {
		aclN := aclIdStart + i
		aclT := "ACL_IPV4"
		fmt.Fprintf(b, `%s{"name":"ACL-%d", "type":"%s", `, sep(i), aclN, aclT)
		fmt.Fprintf(b, `"config":{"name":"ACL-%d", "type":"%s","description":"%s%d"}, `, aclN, aclT, descr, aclN)
		fmt.Fprintf(b, `"state":{"name":"ACL-%d", "type":"%s","description":"%s%d"}, `, aclN, aclT, descr, aclN)
		fmt.Fprintf(b, `"acl-entries":{"acl-entry":[`)
		for j := uint32(0); j < numRules; j++ {
			seqN := ruleIdStart + j
			fmt.Fprintf(b, `%s{"sequence-id":%d, `, sep(j), seqN)
			fmt.Fprintf(b, `"config":{"sequence-id":%d, "description":"%s%d.%d"}, `, seqN, descr, aclN, seqN)
			fmt.Fprintf(b, `"state":{"sequence-id":%d, "description":"%s%d.%d"}, `, seqN, descr, aclN, seqN)
			fmt.Fprintf(b, `"actions":{"config":{"forwarding-action":"ACCEPT"},"state":{"forwarding-action":"ACCEPT"}}, `)
			fmt.Fprintf(b, `"ipv4":{"config":{%s},"state":{%s}}}`, ruleIpv4, ruleIpv4)
		}
		b.WriteString(`]}}`)
	}
	b.WriteString(`]}}}`)

	root := &ocbinds.Device{}
	err := ocbinds.Unmarshal(b.Bytes(), root)
	if err != nil {
		if f, err := ioutil.TempFile("", "payload-*"); err == nil {
			f.Write(b.Bytes())
			f.Close()
			fmt.Println("Faulty payload saved at", f.Name())
		}
		panic("ocbinds.Unmarshal failed; err=" + err.Error())
	}
	return root
}

func deleteNodes(s ygot.GoStruct, paths ...string) {
	schema := ocbinds.SchemaTree[reflect.TypeOf(s).Elem().Name()]
	for _, pathStr := range paths {
		p, _ := ygot.StringToStructuredPath(pathStr)
		ytypes.DeleteNode(schema, s, p)
	}
}

func sep(index uint32) string {
	if index == 0 {
		return ""
	}
	return ", "
}

func benchmarkDiff(b *testing.B, x, y ygot.GoStruct) {
	for i := 0; i < b.N; i++ {
		if useCommunityDiff {
			ygot.Diff(x, y)
		} else {
			Diff(x, y, DiffOptions{})
		}
	}
}

func BenchmarkAclDiff_nochange(b *testing.B) {
	benchmarkDiff(b, aclX, aclX)
}

func BenchmarkAclDiff_onechange(b *testing.B) {
	benchmarkDiff(b, aclX, aclY)
}

func BenchmarkAclDiff_allchange(b *testing.B) {
	benchmarkDiff(b, aclX, aclZ)
}

func BenchmarkAclDiff_addall(b *testing.B) {
	benchmarkDiff(b, acl0, aclX)
}

func BenchmarkAclDiff_delall(b *testing.B) {
	benchmarkDiff(b, aclX, acl0)
}
