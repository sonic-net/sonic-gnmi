package gnmi

// server_test covers gNMI get, subscribe (stream and poll) test
// Prerequisite: redis-server should be running.

import (
	"crypto/tls"
	"encoding/json"
	"github.com/go-redis/redis"
	"github.com/golang/protobuf/proto"
	spb "github.com/jipanyang/sonic-telemetry/proto"
	"github.com/kylelemons/godebug/pretty"
	"github.com/openconfig/gnmi/client"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/gnmi/value"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"io/ioutil"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"
	// Register supported client types.
	gclient "github.com/openconfig/gnmi/client/gnmi"
)

var clientTypes = []string{gclient.Type}

func loadConfig(t *testing.T, key string, in []byte) map[string]interface{} {
	var fvp map[string]interface{}

	err := json.Unmarshal(in, &fvp)
	if err != nil {
		t.Fatal("Failed to Unmarshal %v err: %v", in, err)
	}
	if key != "" {
		kv := map[string]interface{}{}
		kv[key] = fvp
		return kv
	}
	return fvp
}

// assuming input data is in key field/value pair format
func loadDB(t *testing.T, rclient *redis.Client, mpi map[string]interface{}) {
	for key, fv := range mpi {
		switch fv.(type) {
		case map[string]interface{}:
			_, err := rclient.HMSet(key, fv.(map[string]interface{})).Result()
			if err != nil {
				t.Fatal("Invalid data for db: ", key, fv, err)
			}
		default:
			t.Fatal("Invalid data for db: %v : %v", key, fv)
		}
	}
}

// TODO: func NewCert() (tls.Certificate, error)
func createServer(t *testing.T) *Server {
	certPEMBlock := []byte(`-----BEGIN CERTIFICATE-----
MIICWDCCAcGgAwIBAgIJAISaMNtAwNWSMA0GCSqGSIb3DQEBCwUAMEUxCzAJBgNV
BAYTAkFVMRMwEQYDVQQIDApTb21lLVN0YXRlMSEwHwYDVQQKDBhJbnRlcm5ldCBX
aWRnaXRzIFB0eSBMdGQwHhcNMTgwMTE1MjExOTUzWhcNMTkwMTE1MjExOTUzWjBF
MQswCQYDVQQGEwJBVTETMBEGA1UECAwKU29tZS1TdGF0ZTEhMB8GA1UECgwYSW50
ZXJuZXQgV2lkZ2l0cyBQdHkgTHRkMIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKB
gQCrFBy+xrT4gmeMqPDZjpfL2KqI7XiyvYEq/MKTgJM172FpmV3A/nI5O+O7pWub
ONOGOiU4HfUxWaFymyJNye4niBxrrxb/m8TdOc5eIqmtSyJymKir0IIu9vd6ZfK9
vQy7HqzezXSmJTt/s1ZfPF++tnTUPUI0B5RpKvIb8zKSVQIDAQABo1AwTjAdBgNV
HQ4EFgQUdRx4RR0QEcf+xYUlPJv8hrVuaBQwHwYDVR0jBBgwFoAUdRx4RR0QEcf+
xYUlPJv8hrVuaBQwDAYDVR0TBAUwAwEB/zANBgkqhkiG9w0BAQsFAAOBgQAHqRYo
jv3/iIsbtARagsh9i8GVYDPk/9M7Fy2Op/XjqOQmKut74FSe3gFXflmAAnfB3FOK
lxb4K8KkdohDGsxQ79UceBH6JwfDfTcZ4EWpI8aR9HzIQZcRNF/cTL92LWAogUYY
WVNSEMeoWhYbLM0YOYdGgz8FoXOWVaBcgj668g==
-----END CERTIFICATE-----`)

	keyPEMBlock := []byte(`-----BEGIN RSA PRIVATE KEY-----
MIICWwIBAAKBgQCrFBy+xrT4gmeMqPDZjpfL2KqI7XiyvYEq/MKTgJM172FpmV3A
/nI5O+O7pWubONOGOiU4HfUxWaFymyJNye4niBxrrxb/m8TdOc5eIqmtSyJymKir
0IIu9vd6ZfK9vQy7HqzezXSmJTt/s1ZfPF++tnTUPUI0B5RpKvIb8zKSVQIDAQAB
AoGAe8R4K1jskiEdsviCDpMHpLUiYx+SQ5Wv/h6Q0k+hsNJ3IgOPfVFX56o5TocV
e124QhKM3LVnrwVONPCg97AQN6CESk6HBC/y8XJi1f9Pz6RYHEjc1rXxgrppjdun
Wku5eWWhoJ51d2AlWtcT32gIWNB6TQqHw/fKE+kkudCW06ECQQDSgBjeAeOMy85l
E929wmdqwGjBfx4XKKhzuTPSwRQpZ5XLWZ4GsVHCDYAjI3W5p8C51b1x7t6LFBxn
CJafqYC5AkEA0A6iZh/udKXICyWtRHhww0a9w3shJj+OaC4dKogXHPqpBrPlFuDX
7GPGqZaWUVfGvG7lnMMwmax2fupq1dh4fQJASFX+taPeh06uEWv/QithEH0oQn4l
X/33zTSyi1UQUZ4oCqY0OMaMeuvawbh4xyDPiMzbeiCE1zRFAl8gK6O6+QJAFkRa
sR9dv/I2NKs1ngxd1ShvCsrUw2kt7oxw5qpl/t381RDPxeEOeug6zM+nCtGgHW6o
+FwTiX7ht7eS84wVaQJAOdKIf1gMjiEIBMKdmku4Pwj1jCeeyc3BTd9NsHIGgz9O
n6Mu9QKR+diUnqGtENDrD0NDJv8pyT1qXa0lXwF/Nw==
-----END RSA PRIVATE KEY-----`)
	certificate, err := tls.X509KeyPair(certPEMBlock, keyPEMBlock)
	if err != nil {
		t.Fatal("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	// Inform gNMI server to use redis tcp localhost connection
	useRedisLocalTcpPort = true

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	cfg := &Config{Port: 8080}
	s, err := NewServer(cfg, opts)
	if err != nil {
		t.Fatal("Failed to create gNMI server: %v", err)
	}
	return s
}

// runTestGet requests a path from the server by Get grpc call, and compares if
// the return code and response value are expected.
func runTestGet(t *testing.T, ctx context.Context, gClient pb.GNMIClient, pathTarget string,
	textPbPath string, wantRetCode codes.Code, wantRespVal interface{}) {

	// Send request
	var pbPath pb.Path
	if err := proto.UnmarshalText(textPbPath, &pbPath); err != nil {
		t.Fatalf("error in unmarshaling path: %v %v", textPbPath, err)
	}
	prefix := pb.Path{Target: pathTarget}
	req := &pb.GetRequest{
		Prefix:   &prefix,
		Path:     []*pb.Path{&pbPath},
		Encoding: pb.Encoding_JSON_IETF,
	}

	resp, err := gClient.Get(ctx, req)
	// Check return code
	gotRetStatus, ok := status.FromError(err)
	if !ok {
		t.Fatal("got a non-grpc error from grpc call")
	}
	if gotRetStatus.Code() != wantRetCode {
		t.Log("err: ", err)
		t.Fatalf("got return code %v, want %v", gotRetStatus.Code(), wantRetCode)
	}

	// Check response value
	var gotVal interface{}
	if resp != nil {
		notifs := resp.GetNotification()
		if len(notifs) != 1 {
			t.Fatalf("got %d notifications, want 1", len(notifs))
		}
		updates := notifs[0].GetUpdate()
		if len(updates) != 1 {
			t.Fatalf("got %d updates in the notification, want 1", len(updates))
		}
		val := updates[0].GetVal()
		if val.GetJsonIetfVal() == nil {
			gotVal, err = value.ToScalar(val)
			if err != nil {
				t.Errorf("got: %v, want a scalar value", gotVal)
			}
		} else {
			// Unmarshal json data to gotVal container for comparison
			if err := json.Unmarshal(val.GetJsonIetfVal(), &gotVal); err != nil {
				t.Fatalf("error in unmarshaling IETF JSON data to json container: %v", err)
			}
			var wantJSONStruct interface{}
			if err := json.Unmarshal(wantRespVal.([]byte), &wantJSONStruct); err != nil {
				t.Fatalf("error in unmarshaling IETF JSON data to json container: %v", err)
			}
			wantRespVal = wantJSONStruct
		}
	}

	if !reflect.DeepEqual(gotVal, wantRespVal) {
		t.Errorf("got: %v (%T),\nwant %v (%T)", gotVal, gotVal, wantRespVal, wantRespVal)
	}
}

func runServer(t *testing.T, s *Server) {
	//t.Log("Starting RPC server on address:", s.Address())
	err := s.Serve() // blocks until close
	if err != nil {
		t.Fatal("gRPC server err: %v", err)
	}
	//t.Log("Exiting RPC server on address", s.Address())
}

func getRedisClient(t *testing.T) *redis.Client {
	dbn := spb.Target_value["COUNTERS_DB"]
	rclient := redis.NewClient(&redis.Options{
		Network:     "tcp",
		Addr:        "localhost:6379",
		Password:    "", // no password set
		DB:          int(dbn),
		DialTimeout: 0,
	})
	_, err := rclient.Ping().Result()
	if err != nil {
		t.Fatal("failed to connect to redis server ", err)
	}
	return rclient
}

func prepareDb(t *testing.T) {
	rclient := getRedisClient(t)
	defer rclient.Close()
	rclient.FlushDb()
	//Enable keysapce notification
	os.Setenv("PATH", "/usr/bin:/sbin:/bin")
	cmd := exec.Command("redis-cli", "config", "set", "notify-keyspace-events", "KEA")
	_, err := cmd.Output()
	if err != nil {
		t.Fatal("failed to enable redis keyspace notification ", err)
	}

	fileName := "testdata/COUNTERS_PORT_NAME_MAP.txt"
	countersPortNameMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatal("read file %v err: %v", fileName, err)
	}
	mpi_name_map := loadConfig(t, "COUNTERS_PORT_NAME_MAP", countersPortNameMapByte)
	loadDB(t, rclient, mpi_name_map)

	fileName = "testdata/COUNTERS:Ethernet68.txt"
	countersEthernet68Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatal("read file %v err: %v", fileName, err)
	}
	// "Ethernet68": "oid:0x1000000000039",
	mpi_counter := loadConfig(t, "COUNTERS:oid:0x1000000000039", countersEthernet68Byte)
	loadDB(t, rclient, mpi_counter)
}

func TestGnmiGet(t *testing.T) {
	//t.Log("Start server")
	s := createServer(t)
	go runServer(t, s)

	prepareDb(t)

	//t.Log("Start gNMI client")
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	//targetAddr := "30.57.185.38:8080"
	targetAddr := "127.0.0.1:8080"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatal("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fileName := "testdata/COUNTERS_PORT_NAME_MAP.txt"
	countersPortNameMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatal("read file %v err: %v", fileName, err)
	}

	fileName = "testdata/COUNTERS:Ethernet68.txt"
	countersEthernet68Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatal("read file %v err: %v", fileName, err)
	}

	tds := []struct {
		desc        string
		pathTarget  string
		textPbPath  string
		wantRetCode codes.Code
		wantRespVal interface{}
	}{{
		desc:       "Test non-existing path Target",
		pathTarget: "MY_DB",
		textPbPath: `
			elem: <name: "MyCounters" >
		`,
		wantRetCode: codes.NotFound,
	}, {
		desc:       "Test Unimplemented path target",
		pathTarget: "OTHERS",
		textPbPath: `
			elem: <name: "MyCounters" >
		`,
		wantRetCode: codes.Unimplemented,
	}, {
		desc:       "Get valid but non-existing node",
		pathTarget: "COUNTERS_DB",
		textPbPath: `
			elem: <name: "MyCounters" >
		`,
		wantRetCode: codes.NotFound,
	}, {
		desc:       "Get COUNTERS_PORT_NAME_MAP",
		pathTarget: "COUNTERS_DB",
		textPbPath: `
			elem: <name: "COUNTERS_PORT_NAME_MAP" >
		`,
		wantRetCode: codes.OK,
		wantRespVal: countersPortNameMapByte,
	}, {
		desc:       "get COUNTERS:Ethernet68",
		pathTarget: "COUNTERS_DB",
		textPbPath: `
					elem: <name: "COUNTERS" >
					elem: <name: "Ethernet68" >
				`,
		wantRetCode: codes.OK,
		wantRespVal: countersEthernet68Byte,
	}, {
		desc:       "get COUNTERS:Ethernet68 SAI_PORT_STAT_PFC_7_RX_PKTS",
		pathTarget: "COUNTERS_DB",
		textPbPath: `
					elem: <name: "COUNTERS" >
					elem: <name: "Ethernet68" >
					elem: <name: "SAI_PORT_STAT_PFC_7_RX_PKTS" >
				`,
		wantRetCode: codes.OK,
		wantRespVal: "2",
	}}

	for _, td := range tds {
		t.Run(td.desc, func(t *testing.T) {
			runTestGet(t, ctx, gClient, td.pathTarget, td.textPbPath, td.wantRetCode, td.wantRespVal)
		})
	}
	s.s.Stop()
}

type tablePathValue struct {
	dbName    string
	tableName string
	tableKey  string
	delimitor string
	field     string
	value     string
}

// runTestTableStream subscribe a table in stream mode
// the return code and response value are expected.
func runTestTableStream(t *testing.T) {
	fileName := "testdata/COUNTERS_PORT_NAME_MAP.txt"
	countersPortNameMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatal("read file %v err: %v", fileName, err)
	}
	var countersPortNameMapJson interface{}
	json.Unmarshal(countersPortNameMapByte, &countersPortNameMapJson)
	var tmp interface{}
	json.Unmarshal(countersPortNameMapByte, &tmp)
	countersPortNameMapJsonUpdate := tmp.(map[string]interface{})
	countersPortNameMapJsonUpdate["test_field"] = "test_value"

	// for table key subscription
	fileName = "testdata/COUNTERS:Ethernet68.txt"
	countersEthernet68Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatal("read file %v err: %v", fileName, err)
	}
	var countersEthernet68Json interface{}
	json.Unmarshal(countersEthernet68Byte, &countersEthernet68Json)

	var tmp2 interface{}
	json.Unmarshal(countersEthernet68Byte, &tmp2)
	countersEthernet68JsonUpdate := tmp2.(map[string]interface{})
	countersEthernet68JsonUpdate["test_field"] = "test_value"

	// for field subscription, init value is 2 as in COUNTERS:Ethernet68.txt
	countersEthernet68JsonUpdate2 := map[string]interface{}{}
	countersEthernet68JsonUpdate2["SAI_PORT_STAT_PFC_7_RX_PKTS"] = "2"

	countersEthernet68JsonUpdate3 := map[string]interface{}{}
	countersEthernet68JsonUpdate3["SAI_PORT_STAT_PFC_7_RX_PKTS"] = "3"

	tests := []struct {
		desc     string
		q        client.Query
		updates  []tablePathValue
		wantErr  bool
		wantNoti []client.Notification

		poll        int
		wantPollErr string
	}{{
		desc: "stream query for table with update of new field",
		q: client.Query{
			Target:  "COUNTERS_DB",
			Type:    client.Stream,
			Queries: []client.Path{{"COUNTERS_PORT_NAME_MAP"}},
			TLS:     &tls.Config{InsecureSkipVerify: true},
		},
		updates: []tablePathValue{{
			dbName:    "COUNTERS_DB",
			tableName: "COUNTERS_PORT_NAME_MAP",
			field:     "test_field",
			value:     "test_value",
		}},
		wantNoti: []client.Notification{
			client.Connected{},
			client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: countersPortNameMapJson},
			client.Sync{},
			client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: countersPortNameMapJsonUpdate},
		},
	}, {
		desc: "stream query for table key with update of new field",
		q: client.Query{
			Target:  "COUNTERS_DB",
			Type:    client.Stream,
			Queries: []client.Path{{"COUNTERS", "Ethernet68"}},
			TLS:     &tls.Config{InsecureSkipVerify: true},
		},
		updates: []tablePathValue{{
			dbName:    "COUNTERS_DB",
			tableName: "COUNTERS",
			tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
			delimitor: ":",
			field:     "test_field",
			value:     "test_value",
		}},
		wantNoti: []client.Notification{
			client.Connected{},
			client.Update{Path: []string{"COUNTERS", "Ethernet68"}, TS: time.Unix(0, 200), Val: countersEthernet68Json},
			client.Sync{},
			client.Update{Path: []string{"COUNTERS", "Ethernet68"}, TS: time.Unix(0, 200), Val: countersEthernet68JsonUpdate},
		},
	}, {
		desc: "stream query for table key field with update of filed value",
		q: client.Query{
			Target:  "COUNTERS_DB",
			Type:    client.Stream,
			Queries: []client.Path{{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}},
			TLS:     &tls.Config{InsecureSkipVerify: true},
		},
		updates: []tablePathValue{{
			dbName:    "COUNTERS_DB",
			tableName: "COUNTERS",
			tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
			delimitor: ":",
			field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
			value:     "3", // be changed to 3 from 2
		}},
		wantNoti: []client.Notification{
			client.Connected{},
			client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "2"},
			client.Sync{},
			client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "3"},
		},
	}, {
		desc: "poll query with table key field with x3 by Poll()",
		poll: 3,
		q: client.Query{
			Target:  "COUNTERS_DB",
			Type:    client.Poll,
			Queries: []client.Path{{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}},
			TLS:     &tls.Config{InsecureSkipVerify: true},
		},
		updates: []tablePathValue{{
			dbName:    "COUNTERS_DB",
			tableName: "COUNTERS",
			tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
			delimitor: ":",
			field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
			value:     "4", // be changed to 3 from 4
		}},
		wantNoti: []client.Notification{
			client.Connected{},
			client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "3"},
			client.Sync{},
			client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "4"},
			client.Sync{},
			client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "4"},
			client.Sync{},
			client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "4"},
			client.Sync{},
		},
	}}

	rclient := getRedisClient(t)
	defer rclient.Close()
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8080"}
			c := client.New()
			defer c.Close()
			var gotNoti []client.Notification
			q.NotificationHandler = func(n client.Notification) error {
				//t.Logf("reflect.TypeOf(n) %v :  %v", reflect.TypeOf(n), n)
				if nn, ok := n.(client.Update); ok {
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				} else {
					gotNoti = append(gotNoti, n)
				}

				return nil
			}
			go func() {
				c.Subscribe(context.Background(), q)
				/*
					err := c.Subscribe(context.Background(), q)
					t.Log("c.Subscribe err:", err)
					switch {
					case tt.wantErr && err != nil:
						return
					case tt.wantErr && err == nil:
						t.Fatalf("c.Subscribe(): got nil error, expected non-nil")
					case !tt.wantErr && err != nil:
						t.Fatalf("c.Subscribe(): got error %v, expected nil", err)
					}
				*/
			}()
			// wait for hald second for subscribeRequest to sync
			time.Sleep(time.Millisecond * 500)
			for _, update := range tt.updates {
				rclient.HSet(update.tableName+update.delimitor+update.tableKey, update.field, update.value)
			}
			// wait for one second for change to sync
			time.Sleep(time.Millisecond * 1000)

			for i := 0; i < tt.poll; i++ {
				err := c.Poll()
				switch {
				case err == nil && tt.wantPollErr != "":
					t.Errorf("c.Poll(): got nil error, expected non-nil %v", tt.wantPollErr)
				case err != nil && tt.wantPollErr == "":
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				case err != nil && err.Error() != tt.wantPollErr:
					t.Errorf("c.Poll(): got error %v, expected error %v", err, tt.wantPollErr)
				}
			}
			// t.Log("\n Want: \n", tt.wantNoti)
			// t.Log("\n Got : \n", gotNoti)
			if diff := pretty.Compare(tt.wantNoti, gotNoti); diff != "" {
				t.Errorf("unexpected updates:\n%s", diff)
			}
		})
	}
}

// TODO:
func TestGnmiSubscribe(t *testing.T) {
	s := createServer(t)
	go runServer(t, s)
	prepareDb(t)

	runTestTableStream(t)

	s.s.Stop()
}
