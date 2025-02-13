package telemetry_dialout

// dialout_client_test covers gNMIDialOut publish test
// Prerequisite: redis-server should be running.

import (
	"crypto/tls"
	"encoding/json"
	"github.com/go-redis/redis"
	//"github.com/golang/protobuf/proto"
	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"

	//"github.com/kylelemons/godebug/pretty"
	//"github.com/openconfig/gnmi/client"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/gnmi/value"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	//"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	//"google.golang.org/grpc/status"
	//"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	gclient "github.com/openconfig/gnmi/client/gnmi"
	sds "github.com/sonic-net/sonic-gnmi/dialout/dialout_server"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

var clientTypes = []string{gclient.Type}

func loadConfig(t *testing.T, key string, in []byte) map[string]interface{} {
	var fvp map[string]interface{}

	err := json.Unmarshal(in, &fvp)
	if err != nil {
		t.Fatalf("Failed to Unmarshal %v err: %v", in, err)
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
			t.Fatalf("Invalid data for db: %v : %v", key, fv)
		}
	}
}

func createServer(t *testing.T, cfg *sds.Config) *sds.Server {
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Fatalf("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}

	s, err := sds.NewServer(cfg, opts)
	if err != nil {
		t.Fatalf("Failed to create gNMIDialOut server: %v", err)
	}
	return s
}

func runServer(t *testing.T, s *sds.Server) {
	//t.Log("Starting RPC server on address:", s.Address())
	err := s.Serve() // blocks until close
	if err != nil {
		t.Fatalf("gRPC server err: %v", err)
	}
	//t.Log("Exiting RPC server on address", s.Address())
}

func getRedisClient(t *testing.T) *redis.Client {
	ns, _ := sdcfg.GetDbDefaultNamespace()
	addr, err := sdcfg.GetDbTcpAddr("COUNTERS_DB", ns)
	if err != nil {
		t.Fatal("failed to get addr ", err)
	}
	db, err := sdcfg.GetDbId("COUNTERS_DB", ns)
	if err != nil {
		t.Fatal("failed to get db ", err)
	}
	rclient := redis.NewClient(&redis.Options{
		Network:     "tcp",
		Addr:        addr,
		Password:    "", // no password set
		DB:          db,
		DialTimeout: 0,
	})
	_, err = rclient.Ping().Result()
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
		t.Fatalf("%s %s", cmd, err)
	}
	// fmt.Printf("%s", out)
	// wg.Done() // Need to signal to waitgroup that this goroutine is done
}

func getConfigDbClient(t *testing.T) *redis.Client {
	ns, _ := sdcfg.GetDbDefaultNamespace()
	addr, err := sdcfg.GetDbTcpAddr("CONFIG_DB", ns)
	if err != nil {
		t.Fatal("failed to get addr ", err)
	}
	db, err := sdcfg.GetDbId("CONFIG_DB", ns)
	if err != nil {
		t.Fatal("failed to get db ", err)
	}
	rclient := redis.NewClient(&redis.Options{
		Network:     "tcp",
		Addr:        addr,
		Password:    "", // no password set
		DB:          db,
		DialTimeout: 0,
	})
	_, err = rclient.Ping().Result()
	if err != nil {
		t.Fatalf("failed to connect to redis server %v", err)
	}
	return rclient
}

func loadConfigDB(t *testing.T, rclient *redis.Client, mpi map[string]interface{}) {
	for key, fv := range mpi {
		switch fv.(type) {
		case map[string]interface{}:
			_, err := rclient.HMSet(key, fv.(map[string]interface{})).Result()
			if err != nil {
				t.Errorf("Invalid data for db: %v : %v %v", key, fv, err)
			}
		default:
			t.Errorf("Invalid data for db: %v : %v", key, fv)
		}
	}
}

func prepareConfigDb(t *testing.T) {
	rclient := getConfigDbClient(t)
	defer rclient.Close()
	rclient.FlushDB()

	fileName := "../../testdata/COUNTERS_PORT_ALIAS_MAP.txt"
	countersPortAliasMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_alias_map := loadConfig(t, "", countersPortAliasMapByte)
	loadConfigDB(t, rclient, mpi_alias_map)

	fileName = "../../testdata/CONFIG_PFCWD_PORTS.txt"
	configPfcwdByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_pfcwd_map := loadConfig(t, "", configPfcwdByte)
	loadConfigDB(t, rclient, mpi_pfcwd_map)
}

func prepareDb(t *testing.T) {
	rclient := getRedisClient(t)
	defer rclient.Close()
	rclient.FlushDB()
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
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_name_map := loadConfig(t, "COUNTERS_PORT_NAME_MAP", countersPortNameMapByte)
	loadDB(t, rclient, mpi_name_map)

	fileName = "../../testdata/COUNTERS:Ethernet68.txt"
	countersEthernet68Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	// "Ethernet68": "oid:0x1000000000039",
	mpi_counter := loadConfig(t, "COUNTERS:oid:0x1000000000039", countersEthernet68Byte)
	loadDB(t, rclient, mpi_counter)

	fileName = "../../testdata/COUNTERS:Ethernet1.txt"
	countersEthernet1Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	// "Ethernet1": "oid:0x1000000000003",
	mpi_counter = loadConfig(t, "COUNTERS:oid:0x1000000000003", countersEthernet1Byte)
	loadDB(t, rclient, mpi_counter)

	// "Ethernet64:0": "oid:0x1500000000092a"  : queue counter, to work as data noise
	fileName = "../../testdata/COUNTERS:oid:0x1500000000092a.txt"
	counters92aByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_counter = loadConfig(t, "COUNTERS:oid:0x1500000000092a", counters92aByte)
	loadDB(t, rclient, mpi_counter)

	// Load CONFIG_DB for alias translation
	prepareConfigDb(t)
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

// Type defines the type of ServerOp.
type ServerOp int

const (
	_                = iota
	S1Start ServerOp = iota
	S1Stop
	S2Start
	S2Stop
)

var s1, s2 *sds.Server

func serverOp(t *testing.T, sop ServerOp) {
	cfg := &sds.Config{Port: 8080}
	var tmpStore []*pb.SubscribeResponse
	switch sop {
	case S1Stop:
		s1.Stop()
	case S2Stop:
		s2.Stop()
	case S1Start:
		s1 = createServer(t, cfg)
		s1.SetDataStore(&tmpStore)
		go runServer(t, s1)
	case S2Start:
		cfg.Port = 8081
		s2 = createServer(t, cfg)
		s2.SetDataStore(&tmpStore)
		go runServer(t, s2)
	}
}

func TestGNMIDialOutPublish(t *testing.T) {

	fileName := "../../testdata/COUNTERS_PORT_NAME_MAP.txt"
	countersPortNameMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	_ = countersPortNameMapByte

	fileName = "../../testdata/COUNTERS:Ethernet68.txt"
	countersEthernet68Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	_ = countersEthernet68Byte

	fileName = "../../testdata/COUNTERS:Ethernet_wildcard_alias.txt"
	countersEthernetWildcardByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}

	_ = countersEthernetWildcardByte

	fileName = "../../testdata/COUNTERS:Ethernet_wildcard_PFC_7_RX_alias.txt"
	countersEthernetWildcardPfcByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}

	_ = countersEthernetWildcardPfcByte

	clientCfg := ClientConfig{
		SrcIp:          "",
		RetryInterval:  5 * time.Second,
		Encoding:       pb.Encoding_JSON_IETF,
		Unidirectional: true,
		TLS:            &tls.Config{InsecureSkipVerify: true},
	}
	ctx, cancel := context.WithCancel(context.Background())

	go DialOutRun(ctx, &clientCfg)

	exe_cmd(t, "redis-cli -n 4 hset TELEMETRY_CLIENT|Global retry_interval 5")
	exe_cmd(t, "redis-cli -n 4 hset TELEMETRY_CLIENT|Global encoding JSON_IETF")
	exe_cmd(t, "redis-cli -n 4 hset TELEMETRY_CLIENT|Global unidirectional true")
	exe_cmd(t, "redis-cli -n 4 hset TELEMETRY_CLIENT|Global src_ip  30.57.185.38")

	tests := []struct {
		desc     string
		prepares []tablePathValue // extra preparation of redis db
		cmds     []string         // commands to execute
		sop      ServerOp         // Server operation done after commonds
		updates  []tablePathValue // Update to db data
		waitTime time.Duration    // Wait ftime after server operation

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
							{Val: &pb.TypedValue{
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
		//}, {
		//	desc: "DialOut to second collector in stream mode upon failure of first collector",
		//	cmds: []string{
		//		"redis-cli -n 4 hset TELEMETRY_CLIENT|DestinationGroup_HS dst_addr 127.0.0.1:8080,127.0.0.1:8081",
		//		"redis-cli -n 4 hmset TELEMETRY_CLIENT|Subscription_HS_RDMA path_target COUNTERS_DB dst_group HS report_type stream paths COUNTERS/Ethernet*/SAI_PORT_STAT_PFC_7_RX_PKTS",
		//	},
		//	collector: "s2",
		//	sop:       S1Stop,
		//	updates: []tablePathValue{{
		//		dbName:    "COUNTERS_DB",
		//		tableName: "COUNTERS",
		//		tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
		//		delimitor: ":",
		//		field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
		//		value:     "3", // be changed to 3 from 2
		//	}, {
		//		dbName:    "COUNTERS_DB",
		//		tableName: "COUNTERS",
		//		tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
		//		delimitor: ":",
		//		field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
		//		value:     "2", // be changed to 2 from 3
		//	}},
		//	waitTime: clientCfg.RetryInterval + time.Second,
		//	wantRespVal: []*pb.SubscribeResponse{
		//		&pb.SubscribeResponse{
		//			Response: &pb.SubscribeResponse_Update{
		//				Update: &pb.Notification{
		//					Update: []*pb.Update{
		//						{Val: &pb.TypedValue{
		//							Value: &pb.TypedValue_JsonIetfVal{
		//								JsonIetfVal: countersEthernetWildcardPfcByte,
		//							}},
		//						},
		//					},
		//				},
		//			},
		//		},
		//		&pb.SubscribeResponse{
		//			Response: &pb.SubscribeResponse_SyncResponse{
		//				SyncResponse: true,
		//			},
		//		},
		//	},
	}}

	rclient := getRedisClient(t)
	defer rclient.Close()
	for _, tt := range tests {
		prepareDb(t)
		serverOp(t, S1Start)
		serverOp(t, S2Start)
		t.Run(tt.desc, func(t *testing.T) {
			var store []*pb.SubscribeResponse
			if tt.collector == "s1" {
				s1.SetDataStore(&store)
			} else {
				s2.SetDataStore(&store)
			}
			// Extra cmd preparation for this test case
			for _, cmd := range tt.cmds {
				exe_cmd(t, cmd)
			}
			time.Sleep(time.Millisecond * 500)
			serverOp(t, tt.sop)
			for _, update := range tt.updates {
				switch update.op {
				case "hdel":
					rclient.HDel(update.tableName+update.delimitor+update.tableKey, update.field)
				default:
					rclient.HSet(update.tableName+update.delimitor+update.tableKey, update.field, update.value)
				}
				time.Sleep(time.Millisecond * 500)
			}
			if tt.waitTime != 0 {
				time.Sleep(tt.waitTime)
			}
			wantRespVal := tt.wantRespVal.([]*pb.SubscribeResponse)
			if len(store) < len(wantRespVal) {
				t.Logf("len not match %v %s %v", len(store), " : ", len(wantRespVal))
				t.Logf("want: %v", wantRespVal)
				t.Fatal("got: ", store)
			}
			slen := len(store)
			wlen := len(wantRespVal)
			for idx, resp := range wantRespVal {
				switch store[slen-wlen+idx].GetResponse().(type) {
				case *pb.SubscribeResponse_SyncResponse:
					if _, ok := resp.GetResponse().(*pb.SubscribeResponse_SyncResponse); !ok {
						t.Fatalf("Expecting %v, got SyncResponse", resp.GetResponse())
					}
				case *pb.SubscribeResponse_Update:
					compareUpdateValue(t, resp.GetUpdate(), store[idx].GetUpdate())

				}
			}
		})
		serverOp(t, S1Stop)
		serverOp(t, S2Stop)
	}
	cancel()

}

func init() {
	// Inform gNMI server to use redis tcp localhost connection
	sdc.UseRedisLocalTcpPort = true
}
