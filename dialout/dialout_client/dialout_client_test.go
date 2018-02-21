package telemetry_dialout

// dialout_client_test covers gNMIDialOut publish test
// Prerequisite: redis-server should be running.

import (
	"crypto/tls"
	"encoding/json"
	"github.com/go-redis/redis"
	//"github.com/golang/protobuf/proto"
	testcert "github.com/jipanyang/sonic-telemetry/testdata/tls"

	//"github.com/kylelemons/godebug/pretty"
	//"github.com/openconfig/gnmi/client"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/gnmi/value"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	//"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	//"google.golang.org/grpc/status"
	"io/ioutil"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	sds "github.com/jipanyang/sonic-telemetry/dialout/dialout_server"
	spb "github.com/jipanyang/sonic-telemetry/proto"
	sdc "github.com/jipanyang/sonic-telemetry/sonic_data_client"
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

func createServer(t *testing.T, cfg *sds.Config) *sds.Server {
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Fatal("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}

	s, err := sds.NewServer(cfg, opts)
	if err != nil {
		t.Fatal("Failed to create gNMIDialOut server: %v", err)
	}
	return s
}

func runServer(t *testing.T, s *sds.Server) {
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

func exe_cmd(t *testing.T, cmd string) {
	//fmt.Println("command is ", cmd)
	parts := strings.Fields(cmd)
	head := parts[0]
	parts = parts[1:len(parts)]

	_, err := exec.Command(head, parts...).Output()
	if err != nil {
		t.Fatalf("%s", err)
	}
	// fmt.Printf("%s", out)
	// wg.Done() // Need to signal to waitgroup that this goroutine is done
}

func prepareDb(t *testing.T) {
	rclient := getRedisClient(t)
	defer rclient.Close()
	rclient.FlushDb()
	//Enable keysapce notification
	os.Setenv("PATH", "$PATH:/usr/bin:/sbin:/bin:/usr/local/bin:/usr/local/Cellar/redis/4.0.8/bin")
	cmd := exec.Command("redis-cli", "config", "set", "notify-keyspace-events", "KEA")
	_, err := cmd.Output()
	if err != nil {
		t.Fatal("failed to enable redis keyspace notification ", err)
	}

	fileName := "../../testdata/COUNTERS_PORT_NAME_MAP.txt"
	countersPortNameMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatal("read file %v err: %v", fileName, err)
	}
	mpi_name_map := loadConfig(t, "COUNTERS_PORT_NAME_MAP", countersPortNameMapByte)
	loadDB(t, rclient, mpi_name_map)

	fileName = "../../testdata/COUNTERS:Ethernet68.txt"
	countersEthernet68Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatal("read file %v err: %v", fileName, err)
	}
	// "Ethernet68": "oid:0x1000000000039",
	mpi_counter := loadConfig(t, "COUNTERS:oid:0x1000000000039", countersEthernet68Byte)
	loadDB(t, rclient, mpi_counter)

	fileName = "../../testdata/COUNTERS:Ethernet1.txt"
	countersEthernet1Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatal("read file %v err: %v", fileName, err)
	}
	// "Ethernet1": "oid:0x1000000000003",
	mpi_counter = loadConfig(t, "COUNTERS:oid:0x1000000000003", countersEthernet1Byte)
	loadDB(t, rclient, mpi_counter)

	// "Ethernet64:0": "oid:0x1500000000092a"  : queue counter, to work as data noise
	fileName = "../../testdata/COUNTERS:oid:0x1500000000092a.txt"
	counters92aByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatal("read file %v err: %v", fileName, err)
	}
	mpi_counter = loadConfig(t, "COUNTERS:oid:0x1500000000092a", counters92aByte)
	loadDB(t, rclient, mpi_counter)
}

type tablePathValue struct {
	dbName    string
	tableName string
	tableKey  string
	delimitor string
	field     string
	value     string
	op        string
}

func compareUpdateValue(t *testing.T, want *pb.Notification, got *pb.Notification) {
	var wantRespVal interface{}
	var gotVal interface{}
	var err error

	updates := got.GetUpdate()
	if len(updates) != 1 {
		t.Fatalf("got %d updates in the notification, want 1", len(updates))
	}
	gotValTyped := updates[0].GetVal()

	updates = want.GetUpdate()
	wantRespValTyped := updates[0].GetVal()

	//if !reflect.DeepEqual(val, wantRespVal) {
	//	t.Errorf("got: %v (%T),\nwant %v (%T)", val, val, wantRespVal, wantRespVal)
	//}

	if gotValTyped.GetJsonIetfVal() == nil {
		gotVal, err = value.ToScalar(gotValTyped)
		if err != nil {
			t.Errorf("got: %v, want a scalar value", gotVal)
		}
		wantRespVal, _ = value.ToScalar(wantRespValTyped)
	} else {
		// Unmarshal json data to gotVal container for comparison
		if err = json.Unmarshal(gotValTyped.GetJsonIetfVal(), &gotVal); err != nil {
			t.Fatalf("error in unmarshaling IETF JSON data to json container: %v", err)
		}
		if err = json.Unmarshal(wantRespValTyped.GetJsonIetfVal(), &wantRespVal); err != nil {
			t.Fatalf("error in unmarshaling IETF JSON data to json container: %v", err)
		}
	}

	if !reflect.DeepEqual(gotVal, wantRespVal) {
		t.Errorf("got: %v (%T),\nwant %v (%T)", gotVal, gotVal, wantRespVal, wantRespVal)
	}

}

func TestGNMIDialOutPublish(t *testing.T) {

	fileName := "../../testdata/COUNTERS_PORT_NAME_MAP.txt"
	countersPortNameMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatal("read file %v err: %v", fileName, err)
	}
	_ = countersPortNameMapByte

	fileName = "../../testdata/COUNTERS:Ethernet68.txt"
	countersEthernet68Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatal("read file %v err: %v", fileName, err)
	}
	_ = countersEthernet68Byte

	fileName = "../../testdata/COUNTERS:Ethernet_wildcard.txt"
	countersEthernetWildcardByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatal("read file %v err: %v", fileName, err)
	}

	_ = countersEthernetWildcardByte

	fileName = "../../testdata/COUNTERS:Ethernet_wildcard_PFC_7_RX.txt"
	countersEthernetWildcardPfcByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatal("read file %v err: %v", fileName, err)
	}

	_ = countersEthernetWildcardPfcByte

	cfg := &sds.Config{Port: 8080}
	s1 := createServer(t, cfg)
	var store1 []*pb.SubscribeResponse
	s1.SetDataStore(&store1)
	go runServer(t, s1)

	cfg.Port = 8081
	s2 := createServer(t, cfg)
	var store2 []*pb.SubscribeResponse
	s2.SetDataStore(&store2)
	go runServer(t, s2)

	clientCfg := ClientConfig{
		SrcIp:          "",
		RetryInterval:  5 * time.Second,
		Encoding:       pb.Encoding_JSON_IETF,
		Unidirectional: true,
		TLS:            &tls.Config{InsecureSkipVerify: true},
	}
	ctx, cancel := context.WithCancel(context.Background())

	go DialOutRun(ctx, &clientCfg)

	exe_cmd(t, "redis-cli -n 4 hset TELEMETRY_CLIENT|Global retry_interval 30")
	exe_cmd(t, "redis-cli -n 4 hset TELEMETRY_CLIENT|Global encoding JSON_IETF")
	exe_cmd(t, "redis-cli -n 4 hset TELEMETRY_CLIENT|Global unidirectional true")
	exe_cmd(t, "redis-cli -n 4 hset TELEMETRY_CLIENT|Global src_ip  30.57.185.38")

	tests := []struct {
		desc        string
		prepares    []tablePathValue // extra preparation of redis db
		cmds        []string         // commands to execute
		updates     []tablePathValue
		wantErr     bool
		collector   string
		wantRespVal interface{}
	}{{
		desc: "DialOut to first collector in stream mode and synced",
		cmds: []string{
			"redis-cli -n 4 hset TELEMETRY_CLIENT|DestinationGroup_HS dst_addr 127.0.0.1:8080,127.0.0.1:8081",
			"redis-cli -n 4 hmset TELEMETRY_CLIENT|Subscription_HS_RDMA path_target COUNTERS_DB dst_group HS report_type stream paths COUNTERS/Ethernet*",
		},
		collector: "s1",
		wantRespVal: []*pb.SubscribeResponse{
			&pb.SubscribeResponse{
				Response: &pb.SubscribeResponse_Update{
					Update: &pb.Notification{
						//Timestamp: GetTimestamp(),
						//Prefix:    prefix,
						Update: []*pb.Update{
							{
								Val: &pb.TypedValue{
									Value: &pb.TypedValue_JsonIetfVal{
										JsonIetfVal: countersEthernetWildcardByte,
									}},
								//Path: GetPath(),
							},
						},
					},
				},
			},
			&pb.SubscribeResponse{
				Response: &pb.SubscribeResponse_SyncResponse{
					SyncResponse: true,
				},
			},
		},
	}}

	for _, tt := range tests {
		prepareDb(t)
		// Extra cmd preparation for this test case
		for _, cmd := range tt.cmds {
			exe_cmd(t, cmd)
		}
		time.Sleep(time.Millisecond * 1000)
		t.Run(tt.desc, func(t *testing.T) {
			var store []*pb.SubscribeResponse
			if tt.collector == "s1" {
				store = store1
			} else {
				store = store2
			}
			wantRespVal := tt.wantRespVal.([]*pb.SubscribeResponse)
			if len(store) != len(wantRespVal) {
				t.Logf("len not match %v %s %v", len(store), " : ", len(wantRespVal))
				t.Logf("want: ", wantRespVal)
				t.Fatal("got: ", store)
			}
			for idx, resp := range wantRespVal {
				switch store[idx].GetResponse().(type) {
				case *pb.SubscribeResponse_SyncResponse:
					if _, ok := resp.GetResponse().(*pb.SubscribeResponse_SyncResponse); !ok {
						t.Fatal("Expecting %v, got SyncResponse", resp.GetResponse())
					}
				case *pb.SubscribeResponse_Update:
					compareUpdateValue(t, resp.GetUpdate(), store[idx].GetUpdate())

				}
			}
		})
	}
	cancel()
	s1.Stop()
	s2.Stop()
}

func init() {
	// Inform gNMI server to use redis tcp localhost connection
	sdc.UseRedisLocalTcpPort = true
}
