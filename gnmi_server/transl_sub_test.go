package gnmi

import (
	"crypto/tls"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Workiva/go-datastructures/queue"
	"github.com/golang/protobuf/proto"
	"github.com/openconfig/gnmi/client"
	gnmipath "github.com/openconfig/gnmi/path"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	extnpb "github.com/openconfig/gnmi/proto/gnmi_ext"
	"github.com/openconfig/ygot/ygot"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	spb_gnoi "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	dbconfig "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// This file contains subscription test cases for translib managed paths

const (
	ONCE           = gnmipb.SubscriptionList_ONCE
	POLL           = gnmipb.SubscriptionList_POLL
	STREAM         = gnmipb.SubscriptionList_STREAM
	ON_CHANGE      = gnmipb.SubscriptionMode_ON_CHANGE
	SAMPLE         = gnmipb.SubscriptionMode_SAMPLE
	TARGET_DEFINED = gnmipb.SubscriptionMode_TARGET_DEFINED
)

func TestTranslSubscribe(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.s.Stop()

	prepareDbTranslib(t)

	t.Run("origin=openconfig", func(t *testing.T) {
		req := &gnmipb.SubscriptionList{
			Mode:         ONCE,
			Prefix:       strToPath("openconfig:/"),
			Subscription: []*gnmipb.Subscription{{Path: strToPath("/openconfig-acl:acl/acl-sets")}}}
		sub := doSubscribe(t, req, codes.OK)
		sub.Verify(client.Sync{})
	})

	t.Run("origin=invalid", func(t *testing.T) {
		req := &gnmipb.SubscriptionList{
			Mode:         ONCE,
			Prefix:       strToPath("invalid:/"),
			Subscription: []*gnmipb.Subscription{{Path: strToPath("/openconfig-acl:acl/acl-sets")}}}
		sub := doSubscribe(t, req, codes.Unimplemented)
		sub.Verify()
	})

	t.Run("origin=empty,target=empty", func(t *testing.T) {
		req := &gnmipb.SubscriptionList{
			Mode:         ONCE,
			Prefix:       strToPath("/"),
			Subscription: []*gnmipb.Subscription{{Path: strToPath("/openconfig-acl:acl/acl-sets")}}}
		sub := doSubscribe(t, req, codes.Unimplemented)
		sub.Verify()
	})

	t.Run("origin in path", func(t *testing.T) {
		req := &gnmipb.SubscriptionList{
			Mode:         ONCE,
			Prefix:       strToPath("/"),
			Subscription: []*gnmipb.Subscription{{Path: strToPath("openconfig:/openconfig-acl:acl/acl-sets")}}}
		sub := doSubscribe(t, req, codes.OK)
		sub.Verify(client.Sync{})
	})

	t.Run("origin conflict", func(t *testing.T) {
		req := &gnmipb.SubscriptionList{
			Mode:         ONCE,
			Prefix:       strToPath("openconfig:/"),
			Subscription: []*gnmipb.Subscription{{Path: strToPath("xxx:/openconfig-acl:acl/acl-sets")}}}
		sub := doSubscribe(t, req, codes.InvalidArgument)
		sub.Verify()
	})

	t.Run("origin conflict in paths", func(t *testing.T) {
		req := &gnmipb.SubscriptionList{
			Mode:   ONCE,
			Prefix: strToPath("/"),
			Subscription: []*gnmipb.Subscription{
				{Path: strToPath("openconfig:/openconfig-acl:acl/acl-sets")},
				{Path: strToPath("closeconfig:/openconfig-interfaces/interfaces")},
			}}
		sub := doSubscribe(t, req, codes.Unimplemented)
		sub.Verify()
	})

	acl1Path := "/openconfig-acl:acl/acl-sets/acl-set[name=ONE][type=ACL_IPV4]"
	acl2Path := "/openconfig-acl:acl/acl-sets/acl-set[name=TWO][type=ACL_IPV4]"

	acl1CreatePb := newPbUpdate("/openconfig-acl:acl/acl-sets/acl-set",
		`{"acl-set": [{"name": "ONE", "type": "ACL_IPV4", "config": {"name": "ONE", "type": "ACL_IPV4"}}]}`)
	acl2CreatePb := newPbUpdate("/openconfig-acl:acl/acl-sets/acl-set",
		`{"acl-set": [{"name": "TWO", "type": "ACL_IPV4", "config": {"name": "TWO", "type": "ACL_IPV4", "description": "foo"}}]}`)
	acl2DescUpdatePb := newPbUpdate(acl2Path+"/config/description", `{"description": "new"}`)

	acl1DeletePb := strToPath(acl1Path)
	acl2DeletePb := strToPath(acl2Path)
	acl2DescDeletePb := strToPath(acl2Path + "/config/description")
	aclAllDeletePb := strToPath("/openconfig-acl:acl/acl-sets")

	t.Run("ONCE", func(t *testing.T) {
		defer doSet(t, aclAllDeletePb)
		doSet(t, acl1CreatePb)

		req := &gnmipb.SubscriptionList{
			Mode:   ONCE,
			Prefix: strToPath("openconfig:/"),
			Subscription: []*gnmipb.Subscription{
				{Path: strToPath("/openconfig-acl:acl/acl-sets/acl-set")},
			}}

		sub := doSubscribe(t, req, codes.OK)
		sub.Verify(
			Updated(acl1Path+"/name", "ONE"),
			Updated(acl1Path+"/type", "ACL_IPV4"),
			Updated(acl1Path+"/config/name", "ONE"),
			Updated(acl1Path+"/config/type", "ACL_IPV4"),
			Updated(acl1Path+"/state/name", "ONE"),
			Updated(acl1Path+"/state/type", "ACL_IPV4"),
			client.Sync{},
		)
	})

	t.Run("POLL", func(t *testing.T) {
		defer doSet(t, aclAllDeletePb)

		t.Logf("Start POLL subscription for ACL config container")
		req := &gnmipb.SubscriptionList{
			Mode:   POLL,
			Prefix: strToPath("openconfig:/"),
			Subscription: []*gnmipb.Subscription{
				{Path: strToPath("/openconfig-acl:acl/acl-sets/acl-set[name=*][type=*]/config")},
			}}
		sub := doSubscribe(t, req, codes.OK)

		t.Logf("Verify empty initial updates")
		sub.Verify(client.Sync{})

		t.Logf("Create ACl1")
		time.Sleep(2 * time.Second)
		doSet(t, acl1CreatePb)

		t.Logf("Verify poll updates include ACL1 data")
		sub.Poll()
		sub.Verify(
			Updated(acl1Path+"/config/name", "ONE"),
			Updated(acl1Path+"/config/type", "ACL_IPV4"),
			client.Sync{},
		)

		t.Logf("Create ACL2")
		time.Sleep(2 * time.Second)
		doSet(t, acl2CreatePb)

		t.Logf("Verify poll updates include both ACL1 and ACL2 data")
		sub.Poll()
		sub.Verify(
			Updated(acl1Path+"/config/name", "ONE"),
			Updated(acl1Path+"/config/type", "ACL_IPV4"),
			Updated(acl2Path+"/config/name", "TWO"),
			Updated(acl2Path+"/config/type", "ACL_IPV4"),
			Updated(acl2Path+"/config/description", "foo"),
			client.Sync{},
		)

		t.Logf("Delete ACL2")
		time.Sleep(2 * time.Second)
		doSet(t, acl2DeletePb)

		t.Logf("Verify poll updates now include ACL1 data only")
		sub.Poll()
		sub.Verify(
			Updated(acl1Path+"/config/name", "ONE"),
			Updated(acl1Path+"/config/type", "ACL_IPV4"),
			client.Sync{},
		)
	})

	t.Run("ONCHANGE", func(t *testing.T) {
		defer doSet(t, aclAllDeletePb)

		t.Logf("Start ON_CHANGE subscription for ACL config container")
		req := &gnmipb.SubscriptionList{
			Mode:   STREAM,
			Prefix: strToPath("openconfig:/openconfig-acl:acl/acl-sets"),
			Subscription: []*gnmipb.Subscription{
				{Path: strToPath("/acl-set[name=*][type=*]/config"), Mode: ON_CHANGE},
			}}
		sub := doSubscribe(t, req, codes.OK)

		t.Logf("Verify no initial updates")
		sub.Verify(client.Sync{})

		t.Logf("Create ACL2")
		doSet(t, acl2CreatePb)

		t.Logf("Verify update notifications for ACL2 data")
		sub.Verify(
			Updated(acl2Path+"/config/name", "TWO"),
			Updated(acl2Path+"/config/type", "ACL_IPV4"),
			Updated(acl2Path+"/config/description", "foo"),
		)

		t.Logf("Create ACL1 and delete description of ACL2")
		doSet(t, acl1CreatePb, acl2DescDeletePb)

		t.Logf("Verify delete notification for ACL2 description and updates for ACL1 data")
		sub.Verify(
			Updated(acl1Path+"/config/name", "ONE"),
			Updated(acl1Path+"/config/type", "ACL_IPV4"),
			Deleted(acl2Path+"/config/description"),
		)

		t.Logf("Delete ACL1 and set description for ACL2")
		doSet(t, acl2DescUpdatePb, acl1DeletePb)

		t.Logf("Verify delete for ACL1 and update for ACL2 description")
		sub.Verify(
			Deleted(acl1Path+"/config"),
			Updated(acl2Path+"/config/description", "new"),
		)
	})

	t.Run("ONCHANGE_unsupported", func(t *testing.T) {
		t.Logf("Try ON_CHANGE for the top interface list")
		req := &gnmipb.SubscriptionList{
			Mode:   STREAM,
			Prefix: strToPath("openconfig:/"),
			Subscription: []*gnmipb.Subscription{
				{Path: strToPath("/openconfig-interfaces:interfaces/interface[name=*]"), Mode: ON_CHANGE},
			}}
		sub := doSubscribe(t, req, codes.InvalidArgument)
		sub.Verify()
	})

	sampleInterval := 25 * time.Second

	t.Run("SAMPLE", func(t *testing.T) {
		defer doSet(t, aclAllDeletePb)
		t.Logf("Create ACL1")
		doSet(t, acl1CreatePb)

		t.Logf("Start SAMPLE subscription for ACL state container.. interval=%v", sampleInterval)
		req := &gnmipb.SubscriptionList{
			Mode:   STREAM,
			Prefix: strToPath("openconfig:/openconfig-acl:acl/acl-sets"),
			Subscription: []*gnmipb.Subscription{
				{
					Mode:           SAMPLE,
					Path:           strToPath("/acl-set[name=*][type=*]/state"),
					SampleInterval: uint64(sampleInterval.Nanoseconds()),
				},
			}}
		sub := doSubscribe(t, req, codes.OK)

		t.Logf("Verify initial updates include ACL1 data only")
		sub.Verify(
			Updated(acl1Path+"/state/name", "ONE"),
			Updated(acl1Path+"/state/type", "ACL_IPV4"),
			client.Sync{},
		)

		t.Logf("Create ACL2")
		doSet(t, acl2CreatePb)

		t.Logf("Verify updates include both ACL data, for 3 intervals")
		for i := 1; i <= 3; i++ {
			t.Logf("interval %d", i)
			sub.VerifyT(sampleInterval - 3*time.Second) // check no notifications before the interval
			sub.Verify(
				Updated(acl1Path+"/state/name", "ONE"),
				Updated(acl1Path+"/state/type", "ACL_IPV4"),
				Updated(acl2Path+"/state/name", "TWO"),
				Updated(acl2Path+"/state/type", "ACL_IPV4"),
				Updated(acl2Path+"/state/description", "foo"),
			)
		}

		t.Logf("Delete ACL1 and description of ACL2")
		doSet(t, acl1DeletePb, acl2DescDeletePb)

		t.Logf("Verify next iteration includes deletes and updates (for remaining ACL2 data)")
		sub.VerifyT(sampleInterval - 3*time.Second)
		sub.Verify(
			Deleted(acl1Path+"/state"),
			Deleted(acl2Path+"/state/description"),
			Updated(acl2Path+"/state/name", "TWO"),
			Updated(acl2Path+"/state/type", "ACL_IPV4"),
		)

		t.Logf("Verify next iteration has updates only")
		sub.VerifyT(sampleInterval - 3*time.Second)
		sub.Verify(
			Updated(acl2Path+"/state/name", "TWO"),
			Updated(acl2Path+"/state/type", "ACL_IPV4"),
		)
	})

	t.Run("SAMPLE_suppress_redundant", func(t *testing.T) {
		defer doSet(t, aclAllDeletePb)
		t.Logf("Create ACL1 and ACL2")
		doSet(t, acl1CreatePb, acl2CreatePb)

		t.Logf("Start SAMPLE subscription for ACL config container.. interval=%v, suppress_redundant=true", sampleInterval)
		req := &gnmipb.SubscriptionList{
			Mode:   STREAM,
			Prefix: strToPath("openconfig:/openconfig-acl:acl/acl-sets"),
			Subscription: []*gnmipb.Subscription{
				{
					Mode:              SAMPLE,
					Path:              strToPath("/acl-set[name=*][type=*]/config"),
					SampleInterval:    uint64(sampleInterval.Nanoseconds()),
					SuppressRedundant: true,
				},
			}}
		sub := doSubscribe(t, req, codes.OK)

		t.Logf("Verify initial updates")
		sub.Verify(
			Updated(acl1Path+"/config/name", "ONE"),
			Updated(acl1Path+"/config/type", "ACL_IPV4"),
			Updated(acl2Path+"/config/name", "TWO"),
			Updated(acl2Path+"/config/type", "ACL_IPV4"),
			Updated(acl2Path+"/config/description", "foo"),
			client.Sync{},
		)

		t.Logf("Verify next iteration has no data (due to suppress_redundant)")
		sub.VerifyT(sampleInterval + 3*time.Second)

		t.Logf("Delete ACL1 and update ACL2 description")
		doSet(t, acl1DeletePb, acl2DescUpdatePb)

		t.Logf("Verify next iteration includes deletes and updates for modified paths only")
		sub.VerifyT(
			sampleInterval+3*time.Second,
			Deleted(acl1Path+"/config"),
			Updated(acl2Path+"/config/description", "new"),
		)

		t.Logf("Delete ACL2 description")
		doSet(t, acl2DescDeletePb)

		t.Logf("Verify next iteration includes description delete only")
		sub.VerifyT(
			sampleInterval+3*time.Second,
			Deleted(acl2Path+"/config/description"),
		)

		t.Logf("Verify next iteration has no data")
		sub.VerifyT(sampleInterval + 3*time.Second)
	})

	t.Run("SAMPLE_leaf", func(t *testing.T) {
		defer doSet(t, aclAllDeletePb)
		t.Logf("Create ACL2")
		doSet(t, acl2CreatePb)

		t.Logf("Start SAMPLE subscription for ACL description.. interval=%v, updates_only=true", sampleInterval)
		req := &gnmipb.SubscriptionList{
			Mode:        STREAM,
			UpdatesOnly: true,
			Prefix:      strToPath("openconfig:/openconfig-acl:acl/acl-sets"),
			Subscription: []*gnmipb.Subscription{
				{
					Mode:           SAMPLE,
					Path:           strToPath("/acl-set/state/description"),
					SampleInterval: uint64(sampleInterval.Nanoseconds()),
				},
			}}
		sub := doSubscribe(t, req, codes.OK)

		t.Logf("Verify empty initial updates, due to updates_only")
		sub.Verify(client.Sync{})

		t.Logf("Verify next iteration has the description value")
		sub.VerifyT(sampleInterval - 3*time.Second) // check no notifications before the interval
		sub.Verify(
			Updated(acl2Path+"/state/description", "foo"),
		)

		t.Logf("Update ACL2 description")
		doSet(t, acl2DescUpdatePb)

		t.Logf("Verify next iteration has the updated description")
		sub.VerifyT(sampleInterval - 3*time.Second)
		sub.Verify(
			Updated(acl2Path+"/state/description", "new"),
		)

		t.Logf("Delete ACL2")
		doSet(t, acl2DeletePb)

		t.Logf("Verify next iteration has delete notification")
		sub.VerifyT(sampleInterval - 3*time.Second)
		sub.Verify(
			Deleted(acl2Path + "/state/description"),
		)

		t.Logf("Verify next iteration has no notifications")
		sub.VerifyT(sampleInterval + 3*time.Second)
	})

	t.Run("SAMPLE_invalid_interval", func(t *testing.T) {
		t.Logf("Try SAMPLE with 1ms SamplerInterval (too low)")
		req := &gnmipb.SubscriptionList{
			Mode:   STREAM,
			Prefix: strToPath("openconfig:/"),
			Subscription: []*gnmipb.Subscription{
				{
					Mode:           SAMPLE,
					Path:           strToPath("/openconfig-acl:acl/acl-sets"),
					SampleInterval: uint64(time.Millisecond.Nanoseconds()),
				},
			}}
		sub := doSubscribe(t, req, codes.InvalidArgument)
		sub.Verify()
	})

	t.Run("SAMPLE_no_interval", func(t *testing.T) {
		defer doSet(t, aclAllDeletePb)

		t.Logf("Start SAMPLE subscription for ACL description.. without setting SampleInterval")
		req := &gnmipb.SubscriptionList{
			Mode:   STREAM,
			Prefix: strToPath("openconfig:/openconfig-acl:acl/acl-sets"),
			Subscription: []*gnmipb.Subscription{
				{
					Mode: SAMPLE,
					Path: strToPath("/acl-set/state/description"),
				},
			}}
		sub := doSubscribe(t, req, codes.OK)

		t.Logf("Verify empty initial updates")
		sub.Verify(client.Sync{})

		t.Logf("Create ACL2")
		doSet(t, acl2CreatePb)

		t.Logf("Verify updates are received after default interval")
		sub.VerifyT(
			(translib.MinSubscribeInterval+2)*time.Second,
			Updated(acl2Path+"/state/description", "foo"),
		)
	})

	t.Run("TARGETDEFINED", func(t *testing.T) {
		t.Logf("Start TARGETDEFINED subscription for interface description, in-pkts and in-octets")
		interval := 30 * time.Second
		req := &gnmipb.SubscriptionList{
			Mode:   STREAM,
			Prefix: strToPath("openconfig:/interfaces/interface[name=Ethernet0]"),
			Subscription: []*gnmipb.Subscription{
				{
					Path: strToPath("/state/description"),
					Mode: TARGET_DEFINED,
				}, {
					Path:           strToPath("/state/counters/in-pkts"),
					Mode:           TARGET_DEFINED,
					SampleInterval: uint64(interval.Nanoseconds()),
				}, {
					Path:           strToPath("/state/counters/in-octets"),
					Mode:           TARGET_DEFINED,
					SampleInterval: uint64(interval.Nanoseconds()),
				}}}
		sub := doSubscribe(t, req, codes.OK)

		t.Logf("Verify initial updates includes all three data")
		eth0Path := "/openconfig-interfaces:interfaces/interface[name=Ethernet0]"
		sub.Verify(
			Updated(eth0Path+"/state/description", ""),
			Updated(eth0Path+"/state/counters/in-pkts", uint64(0)),
			Updated(eth0Path+"/state/counters/in-octets", uint64(0)),
			client.Sync{},
		)

		next := time.Now().Add(interval)

		t.Logf("Update port description")
		updateDb(t, DbDataMap{
			"CONFIG_DB": {"PORT|Ethernet0": {"description": "the one"}},
			"APPL_DB":   {"PORT_TABLE:Ethernet0": {"description": "the one"}},
		})

		t.Logf("Verify update notification for port description")
		sub.Verify(
			Updated(eth0Path+"/state/description", "the one"),
		)

		t.Logf("Verify periodic updates for stats only")
		for i := 1; i <= 2; i++ {
			sub.VerifyT(time.Until(next) - 3*time.Second)
			sub.Verify(
				Updated(eth0Path+"/state/counters/in-pkts", uint64(0)),
				Updated(eth0Path+"/state/counters/in-octets", uint64(0)),
			)
			next = time.Now().Add(interval)
		}
	})

	t.Run("TARGETDEFINED_split", func(t *testing.T) {
		interval := 30 * time.Second
		eth0State := "/openconfig-interfaces:interfaces/interface[name=Ethernet0]/state"

		t.Logf("Start TARGETDEFINED subscription for interface state container")
		req := &gnmipb.SubscriptionList{
			Mode:   STREAM,
			Prefix: strToPath("openconfig:/"),
			Subscription: []*gnmipb.Subscription{{
				Path:           strToPath(eth0State),
				Mode:           TARGET_DEFINED,
				SampleInterval: uint64(interval.Nanoseconds()),
			}}}
		sub := doSubscribe(t, req, codes.OK)

		t.Logf("Verify initial updates includes nodes from both state and counters containers")
		sub.GlobCompare = true
		sub.Verify(
			Updated(eth0State+"/counters/*", nil),
			Updated(eth0State+"/*", nil),
			client.Sync{},
		)

		t.Logf("Verify next updates contains only counters data")
		sub.VerifyT(interval - 2*time.Second)
		sub.Verify(
			Updated(eth0State+"/counters/*", nil),
		)
	})

	t.Run("hearbeat", func(t *testing.T) {
		saInterval := 30 * time.Second
		hbInterval := saInterval + 10*time.Second

		t.Logf("Start an ON_CHANGE and SAMPLE subscription with heartbeat %v", hbInterval)
		req := &gnmipb.SubscriptionList{
			Mode:   STREAM,
			Prefix: strToPath("openconfig:/openconfig-interfaces:interfaces/interface[name=Ethernet0]"),
			Subscription: []*gnmipb.Subscription{
				{
					Path:              strToPath("/config/enabled"),
					Mode:              SAMPLE,
					SuppressRedundant: true,
					SampleInterval:    uint64(saInterval.Nanoseconds()),
					HeartbeatInterval: uint64(hbInterval.Nanoseconds()),
				}, {
					Path:              strToPath("/state/oper-status"),
					Mode:              ON_CHANGE,
					HeartbeatInterval: uint64(hbInterval.Nanoseconds()),
				}}}
		sub := doSubscribe(t, req, codes.OK)

		t.Logf("Verify initial updates contains both data")
		eth0Path := "/openconfig-interfaces:interfaces/interface[name=Ethernet0]"
		sub.Verify(
			Updated(eth0Path+"/config/enabled", false),
			Updated(eth0Path+"/state/oper-status", "DOWN"),
			client.Sync{},
		)

		t.Logf("Verify updates received only after heartbeat interval")
		sub.VerifyT(hbInterval - 2*time.Second)
		sub.Verify(
			Updated(eth0Path+"/config/enabled", false),
			Updated(eth0Path+"/state/oper-status", "DOWN"),
		)
	})

	t.Run("hearbeat_invalid (sample)", func(t *testing.T) {
		t.Logf("Try a SAMPLE subscription with 1ms heartbeat")
		req := &gnmipb.SubscriptionList{
			Mode:   STREAM,
			Prefix: strToPath("openconfig:/"),
			Subscription: []*gnmipb.Subscription{{
				Path:              strToPath("/interfaces/interface/config/mtu"),
				Mode:              SAMPLE,
				SuppressRedundant: true,
				HeartbeatInterval: uint64(time.Millisecond.Nanoseconds()),
			}}}
		sub := doSubscribe(t, req, codes.InvalidArgument)
		sub.Verify()
	})

	t.Run("hearbeat_invalid (onchange)", func(t *testing.T) {
		t.Logf("Try an ON_CHANGE subscription with 1ms heartbeat")
		req := &gnmipb.SubscriptionList{
			Mode:   STREAM,
			Prefix: strToPath("openconfig:/"),
			Subscription: []*gnmipb.Subscription{{
				Path:              strToPath("/interfaces/interface/config/mtu"),
				Mode:              ON_CHANGE,
				HeartbeatInterval: uint64(time.Millisecond.Nanoseconds()),
			}}}
		sub := doSubscribe(t, req, codes.InvalidArgument)
		sub.Verify()
	})

	t.Run("bundle_version_0.0.0", func(t *testing.T) {
		t.Logf("Start a subscription with BundleVersion=0.0.0")
		req := &gnmipb.SubscribeRequest{
			Request: &gnmipb.SubscribeRequest_Subscribe{
				Subscribe: &gnmipb.SubscriptionList{
					Mode:   STREAM,
					Prefix: strToPath("openconfig:/interfaces/interface[name=Ethernet0]"),
					Subscription: []*gnmipb.Subscription{
						{Path: strToPath("/config/mtu"), Mode: ON_CHANGE},
						{Path: strToPath("/state/mtu"), Mode: SAMPLE},
					}}},
			Extension: []*extnpb.Extension{newBundleVersion(t, "0.0.0")},
		}
		sub := doSubscribeRaw(t, req, codes.OK)
		sub.Verify(
			Updated("/openconfig-interfaces:interfaces/interface[name=Ethernet0]/config/mtu", uint64(9100)),
			Updated("/openconfig-interfaces:interfaces/interface[name=Ethernet0]/state/mtu", uint64(9100)),
			client.Sync{},
		)
	})

	t.Run("bundle_version_invalid", func(t *testing.T) {
		t.Logf("Start POLL subscription with BundleVersion=100.0.0")
		req := &gnmipb.SubscribeRequest{
			Request: &gnmipb.SubscribeRequest_Subscribe{
				Subscribe: &gnmipb.SubscriptionList{
					Mode:   POLL,
					Prefix: strToPath("openconfig:/"),
					Subscription: []*gnmipb.Subscription{
						{Path: strToPath("/interfaces/interface[name=Ethernet0]/config/mtu")},
					}}},
			Extension: []*extnpb.Extension{newBundleVersion(t, "100.0.0")},
		}
		sub := doSubscribeRaw(t, req, codes.InvalidArgument)
		sub.Verify()
	})
}

func strToPath(s string) *gnmipb.Path {
	var origin string
	if k := strings.IndexByte(s, ':') + 1; k > 0 && k < len(s) && s[k] == '/' {
		origin = s[:k-1]
		s = s[k:]
	}
	p, _ := ygot.StringToStructuredPath(s)
	p.Origin = origin
	return p
}

func strToCPath(s string) client.Path {
	p := strToPath(s)
	return gnmipath.ToStrings(p, false)
}

func Updated(p string, v interface{}) client.Update {
	return client.Update{Path: strToCPath(p), Val: v}
}

func Deleted(p string) client.Delete {
	return client.Delete{Path: strToCPath(p)}
}

type testSubscriber struct {
	t      *testing.T
	client *client.CacheClient
	notiQ  *queue.Queue

	GlobCompare bool // treat expected paths as glob patterns in Verify()
}

func doSubscribe(t *testing.T, subReq *gnmipb.SubscriptionList, exStatus codes.Code) *testSubscriber {
	t.Helper()
	req := &gnmipb.SubscribeRequest{
		Request: &gnmipb.SubscribeRequest_Subscribe{Subscribe: subReq}}
	return doSubscribeRaw(t, req, exStatus)
}

func doSubscribeRaw(t *testing.T, req *gnmipb.SubscribeRequest, exStatus codes.Code) *testSubscriber {
	t.Helper()
	q, err := client.NewQuery(req)
	if err != nil {
		t.Fatalf("NewQuery failed: %v", err)
	}

	sub := &testSubscriber{
		t:      t,
		client: client.New(),
		notiQ:  queue.New(100),
	}

	t.Cleanup(sub.close)

	q.Addrs = []string{"127.0.0.1:8081"}
	q.TLS = &tls.Config{InsecureSkipVerify: true}
	q.NotificationHandler = func(n client.Notification) error {
		//fmt.Printf(">>>> %#v\n", n)
		return sub.notiQ.Put(n)
	}

	go func() {
		err = sub.client.Subscribe(context.Background(), q)
		if _, ok := status.FromError(err); !ok || status.Code(err) != exStatus {
			msg := fmt.Sprintf("Subscribe failed: expected=%v, received=%v", exStatus, err)
			sub.notiQ.Put(client.NewError(msg))
		} else if err != nil {
			sub.notiQ.Dispose() // got the expected error.. stop listening immediately
		}
	}()

	return sub
}

func (sub *testSubscriber) close() {
	if sub != nil {
		sub.client.Close()
		sub.notiQ.Dispose()
	}
}

func (sub *testSubscriber) Poll() {
	if err := sub.client.Poll(); err != nil {
		sub.t.Helper()
		sub.t.Fatalf("Poll failed: %v", err)
	}
}

func (sub *testSubscriber) Verify(expect ...client.Notification) {
	sub.VerifyT(5*time.Second, expect...)
}

func (sub *testSubscriber) VerifyT(timeout time.Duration, expect ...client.Notification) {
	sub.t.Helper()
	extra := make([]client.Notification, 0)
	matched := make(map[int]client.Notification)
	deadine := time.Now().Add(timeout)

	for {
		n := sub.nextNoti(deadine)
		if n == nil {
			break // timeout
		}
		if err, ok := n.(client.Error); ok {
			sub.t.Fatal(err.Error())
		}

		index := -1
		for i, ex := range expect {
			if sub.compareNoti(n, ex) {
				index = i
				break
			}
		}
		if index != -1 {
			matched[index] = n
		} else {
			extra = append(extra, n)
		}
		if _, ok := n.(client.Sync); ok {
			break
		}
		if !sub.GlobCompare && (len(matched) == len(expect)) {
			break
		}
	}

	// if len(matched) == len(expect) && len(extra) == 0 {
	// 	return
	// }
	switch {
	case len(extra) != 0: // found extra updates
	case sub.GlobCompare && len(matched) == 0 && len(expect) != 0: // no glob matches found
	case !sub.GlobCompare && len(matched) != len(expect): // wrong number of matches
	default:
		return
	}

	for _, n := range extra {
		sub.t.Errorf("unexpected: %#v", n)
	}
	for i, n := range expect {
		if matched[i] == nil {
			sub.t.Errorf("missing: %#v", n)
		}
	}
	sub.t.FailNow()
}

func (sub *testSubscriber) nextNoti(deadline time.Time) client.Notification {
	sub.t.Helper()
	timeout := time.Until(deadline)
	if timeout <= 0 {
		return nil
	}
	n, err := sub.notiQ.Poll(1, timeout)
	if err == queue.ErrTimeout || err == queue.ErrDisposed {
		return nil
	} else if err != nil {
		sub.t.Fatalf("Unexpected error while waiting for a notification: %v", err)
	}

	switch noti := n[0].(type) {
	case client.Update:
		noti.TS = time.Time{}
		return noti
	case client.Delete:
		noti.TS = time.Time{}
		return noti
	case client.Error:
		sub.t.Fatalf("Unexpected error notification: %s", noti.Error())
	case client.Connected:
		return sub.nextNoti(deadline)
	}

	return n[0].(client.Notification)
}

func (sub *testSubscriber) compareNoti(n, exp client.Notification) bool {
	if !sub.GlobCompare {
		return reflect.DeepEqual(n, exp)
	}

	var path, expPath string
	var val, expVal interface{}
	switch exp := exp.(type) {
	case client.Update:
		if u, ok := n.(client.Update); ok {
			path, val = pathToString(u.Path), u.Val
			expPath, expVal = pathToString(exp.Path), exp.Val
		} else {
			return false
		}
	case client.Delete:
		if d, ok := n.(client.Delete); ok {
			path = pathToString(d.Path)
			expPath = pathToString(exp.Path)
		} else {
			return false
		}
	default:
		return reflect.DeepEqual(n, exp)
	}

	if ok, _ := filepath.Match(expPath, path); !ok {
		return false
	}
	return expVal == nil || reflect.DeepEqual(val, expVal)
}

func doSet(t *testing.T, data ...interface{}) {
	t.Helper()
	req := &gnmipb.SetRequest{}
	for _, v := range data {
		switch v := v.(type) {
		case *gnmipb.Path:
			req.Delete = append(req.Delete, v)
		case *gnmipb.Update:
			req.Update = append(req.Update, v)
		default:
			t.Fatalf("Unsupported set value: %T %v", v, v)
		}
	}

	client := gnmipb.NewGNMIClient(createClient(t, 8081))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.Set(ctx, req)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}
}

// DbDataMap is a map[DBNAME]map[KEY]map[FIELD]VALUE
type DbDataMap map[string]map[string]map[string]interface{}

func updateDb(t *testing.T, data DbDataMap) {
	t.Helper()
	ns, _ := dbconfig.GetDbDefaultNamespace()
	for dbName, tableData := range data {
		n, err := dbconfig.GetDbId(dbName, ns)
		if err != nil {
			t.Fatalf("GetDbId failed: %v", err)
		}
		redis := getRedisClientN(t, n, ns)
		defer redis.Close()
		for key, fields := range tableData {
			if fields == nil {
				redis.Del(key)
				continue
			}

			modFields := make(map[string]interface{})
			delFields := make([]string, 0)
			for n, v := range fields {
				if v == nil {
					delFields = append(delFields, n)
				} else {
					modFields[n] = v
				}
			}

			if len(modFields) != 0 {
				redis.HMSet(key, modFields)
			}
			if len(delFields) != 0 {
				redis.HDel(key, delFields...)
			}
		}
	}
}

func newBundleVersion(t *testing.T, version string) *extnpb.Extension {
	t.Helper()
	v, err := proto.Marshal(&spb.BundleVersion{Version: version})
	if err != nil {
		t.Fatalf("Invalid version %s; err=%v", version, err)
	}
	ext := &extnpb.RegisteredExtension{Id: spb.BUNDLE_VERSION_EXT, Msg: v}
	return &extnpb.Extension{Ext: &extnpb.Extension_RegisteredExt{RegisteredExt: ext}}
}

func TestDebugSubscribePreferences(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.s.Stop()

	ifTop := &spb_gnoi.SubscribePreference{
		Path:              strToPath("/openconfig-interfaces:interfaces/interface[name=*]"),
		OnChangeSupported: false,
		TargetDefinedMode: ON_CHANGE,
		WildcardSupported: true,
	}
	ifMtu := &spb_gnoi.SubscribePreference{
		Path:              strToPath("/openconfig-interfaces:interfaces/interface[name=*]/config/mtu"),
		OnChangeSupported: true,
		TargetDefinedMode: ON_CHANGE,
		WildcardSupported: true,
	}
	ifStat := &spb_gnoi.SubscribePreference{
		Path:              strToPath("/openconfig-interfaces:interfaces/interface[name=*]/state/counters"),
		OnChangeSupported: false,
		TargetDefinedMode: SAMPLE,
		WildcardSupported: true,
	}
	aclConfig := &spb_gnoi.SubscribePreference{
		Path:              strToPath("/openconfig-acl:acl/acl-sets/acl-set[name=*][type=*]/config"),
		OnChangeSupported: true,
		TargetDefinedMode: ON_CHANGE,
		WildcardSupported: true,
	}
	yanglib := &spb_gnoi.SubscribePreference{
		Path:              strToPath("/ietf-yang-library:modules-state/module-set-id"),
		OnChangeSupported: false,
		TargetDefinedMode: SAMPLE,
		WildcardSupported: false,
	}

	t.Run("invalid_path", func(t *testing.T) {
		_, err := getSubscribePreferences(t, nil)
		if res, _ := status.FromError(err); res.Code() != codes.InvalidArgument {
			t.Fatalf("Expecting InvalidArgument error; got %v", err)
		}
	})

	t.Run("unknown_path", func(t *testing.T) {
		_, err := getSubscribePreferences(t, strToPath("/unknown"))
		if res, _ := status.FromError(err); res.Code() != codes.InvalidArgument {
			t.Fatalf("Expecting InvalidArgument error; got %v", err)
		}
	})

	t.Run("onchange_supported", func(t *testing.T) {
		verifySubscribePreferences(t,
			[]*gnmipb.Path{ifMtu.Path},
			[]*spb_gnoi.SubscribePreference{ifMtu})
	})

	t.Run("onchange_unsupported", func(t *testing.T) {
		verifySubscribePreferences(t,
			[]*gnmipb.Path{ifStat.Path},
			[]*spb_gnoi.SubscribePreference{ifStat})
	})

	t.Run("onchange_mixed", func(t *testing.T) {
		verifySubscribePreferences(t,
			[]*gnmipb.Path{ifTop.Path},
			[]*spb_gnoi.SubscribePreference{ifTop, ifStat})
	})

	t.Run("nondb_path", func(t *testing.T) {
		verifySubscribePreferences(t,
			[]*gnmipb.Path{yanglib.Path},
			[]*spb_gnoi.SubscribePreference{yanglib})
	})

	t.Run("unprefixed_path", func(t *testing.T) {
		verifySubscribePreferences(t,
			[]*gnmipb.Path{strToPath("/acl/acl-sets/acl-set/config")},
			[]*spb_gnoi.SubscribePreference{aclConfig})
	})

	t.Run("multiple_paths", func(t *testing.T) {
		verifySubscribePreferences(t,
			[]*gnmipb.Path{yanglib.Path, ifTop.Path, aclConfig.Path},
			[]*spb_gnoi.SubscribePreference{yanglib, ifTop, ifStat, aclConfig})
	})
}

func TestDebugSubscribePreferences_dummy(t *testing.T) {
	// Dummy testcase to increase code coverage !!!
	f := func(_ ...interface{}) {}
	for _, m := range []*spb_gnoi.SubscribePreferencesReq{nil, {}} {
		f(m.String(), m.GetPath())
		f(m.Descriptor())
	}
	for _, p := range []*spb_gnoi.SubscribePreference{nil, {}} {
		f(p.String(), p.GetPath(), p.GetOnChangeSupported(), p.GetTargetDefinedMode(), p.GetWildcardSupported(), p.GetMinSampleInterval())
		f(p.Descriptor())
	}
}

func getSubscribePreferences(t *testing.T, paths ...*gnmipb.Path) ([]*spb_gnoi.SubscribePreference, error) {
	t.Helper()
	client := spb_gnoi.NewDebugClient(createClient(t, 8081))
	stream, err := client.GetSubscribePreferences(
		context.Background(),
		&spb_gnoi.SubscribePreferencesReq{Path: paths},
	)
	if err != nil {
		t.Fatalf("Could not invoke GetSubscribePreferences: %v", err)
	}

	var prefs []*spb_gnoi.SubscribePreference
	for {
		if p, err := stream.Recv(); err == nil {
			prefs = append(prefs, p)
		} else if err == io.EOF {
			break
		} else {
			return prefs, err
		}
	}

	return prefs, nil
}

func verifySubscribePreferences(t *testing.T, paths []*gnmipb.Path, exp []*spb_gnoi.SubscribePreference) {
	t.Helper()
	resp, err := getSubscribePreferences(t, paths...)
	if err != nil {
		t.Fatalf("GetSubscribePreferences returned error: %v", err)
	}
	if len(resp) != len(exp) {
		t.Fatalf("Expected: %s\nReceived: %s", prefsText(exp), prefsText(resp))
	}
	for i, ex := range exp {
		if ex.MinSampleInterval == 0 {
			resp[i].MinSampleInterval = 0 // ignore MinSampleInterval for comparison
		}
		if !proto.Equal(ex, resp[i]) {
			t.Fatalf("Expected: %s\nReceived: %s", prefsText(exp), prefsText(resp))
		}
	}
}

func prefsText(prefs []*spb_gnoi.SubscribePreference) string {
	var s []string
	for _, p := range prefs {
		s = append(s, proto.MarshalTextString(p))
	}
	return "[\n" + strings.Join(s, "\n") + "]"
}
