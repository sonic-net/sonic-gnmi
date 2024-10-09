package gnmi

// server_test covers gNMI get, subscribe (stream and poll) test
// Prerequisite: redis-server should be running.
import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"crypto/x509"
	"crypto/x509/pkix"

	spb "github.com/sonic-net/sonic-gnmi/proto"
	sgpb "github.com/sonic-net/sonic-gnmi/proto/gnoi"
	spb_jwt "github.com/sonic-net/sonic-gnmi/proto/gnoi/jwt"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"github.com/sonic-net/sonic-gnmi/test_utils"
	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"

	"github.com/go-redis/redis"
	"github.com/golang/protobuf/proto"
	"github.com/kylelemons/godebug/pretty"
	"github.com/openconfig/gnmi/client"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	ext_pb "github.com/openconfig/gnmi/proto/gnmi_ext"
	"github.com/openconfig/gnmi/value"
	"github.com/openconfig/ygot/ygot"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	// Register supported client types.
	"github.com/Workiva/go-datastructures/queue"
	"github.com/agiledragon/gomonkey/v2"
	linuxproc "github.com/c9s/goprocinfo/linux"
	"github.com/godbus/dbus/v5"
	gclient "github.com/jipanyang/gnmi/client/gnmi"
	"github.com/jipanyang/gnxi/utils/xpath"
	cacheclient "github.com/openconfig/gnmi/client"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	gnoi_system_pb "github.com/openconfig/gnoi/system"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"github.com/sonic-net/sonic-gnmi/swsscommon"
)

var clientTypes = []string{gclient.Type}

func loadConfig(t *testing.T, key string, in []byte) map[string]interface{} {
	var fvp map[string]interface{}

	err := json.Unmarshal(in, &fvp)
	if err != nil {
		t.Errorf("Failed to Unmarshal %v err: %v", in, err)
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
				t.Errorf("Invalid data for db:  %v : %v %v", key, fv, err)
			}
		default:
			t.Errorf("Invalid data for db: %v : %v", key, fv)
		}
	}
}
func loadDBNotStrict(t *testing.T, rclient *redis.Client, mpi map[string]interface{}) {
	for key, fv := range mpi {
		switch fv.(type) {
		case map[string]interface{}:
			rclient.HMSet(key, fv.(map[string]interface{})).Result()

		}
	}
}

func createClient(t *testing.T, port int) *grpc.ClientConn {
	t.Helper()
	cred := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})
	conn, err := grpc.Dial(
		fmt.Sprintf("127.0.0.1:%d", port),
		grpc.WithTransportCredentials(cred),
	)
	if err != nil {
		t.Fatalf("Dialing to :%d failed: %v", port, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func createServer(t *testing.T, port int64) *Server {
	t.Helper()
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Fatalf("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	cfg := &Config{Port: port, EnableTranslibWrite: true, EnableNativeWrite: true, Threshold: 100}
	s, err := NewServer(cfg, opts)
	if err != nil {
		t.Errorf("Failed to create gNMI server: %v", err)
	}
	return s
}

func createReadServer(t *testing.T, port int64) *Server {
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Errorf("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	cfg := &Config{Port: port, EnableTranslibWrite: false}
	s, err := NewServer(cfg, opts)
	if err != nil {
		t.Fatalf("Failed to create gNMI server: %v", err)
	}
	return s
}

func createRejectServer(t *testing.T, port int64) *Server {
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Errorf("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	cfg := &Config{Port: port, EnableTranslibWrite: true, Threshold: 2}
	s, err := NewServer(cfg, opts)
	if err != nil {
		t.Fatalf("Failed to create gNMI server: %v", err)
	}
	return s
}

func createAuthServer(t *testing.T, port int64) *Server {
	t.Helper()
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Fatalf("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	cfg := &Config{Port: port, EnableTranslibWrite: true, UserAuth: AuthTypes{"password": true, "cert": true, "jwt": true}}
	s, err := NewServer(cfg, opts)
	if err != nil {
		t.Fatalf("Failed to create gNMI server: %v", err)
	}
	return s
}

func createInvalidServer(t *testing.T, port int64) *Server {
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Errorf("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	s, err := NewServer(nil, opts)
	if err != nil {
		return nil
	}
	return s
}

func createKeepAliveServer(t *testing.T, port int64) *Server {
	t.Helper()
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Fatalf("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	keep_alive_params := keepalive.ServerParameters{
		MaxConnectionIdle: 1 * time.Second,
	}
	server_opts := []grpc.ServerOption{
		grpc.KeepaliveParams(keep_alive_params),
	}
	server_opts = append(server_opts, opts[0])
	cfg := &Config{Port: port, EnableTranslibWrite: true, EnableNativeWrite: true, Threshold: 100}
	s, err := NewServer(cfg, server_opts)
	if err != nil {
		t.Errorf("Failed to create gNMI server: %v", err)
	}
	return s
}

// runTestGet requests a path from the server by Get grpc call, and compares if
// the return code and response value are expected.
func runTestGet(t *testing.T, ctx context.Context, gClient pb.GNMIClient, pathTarget string,
	textPbPath string, wantRetCode codes.Code, wantRespVal interface{}, valTest bool) {
	//var retCodeOk bool
	// Send request
	t.Helper()
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
	if valTest {
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
				if v, ok := wantRespVal.(string); ok {
					wantRespVal = []byte(v)
				}
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
}

func extractJSON(val string) []byte {
	jsonBytes, err := ioutil.ReadFile(val)
	if err == nil {
		return jsonBytes
	}
	return []byte(val)
}

type op_t int

const (
	Delete  op_t = 1
	Replace op_t = 2
	Update  op_t = 3
)

func runTestSet(t *testing.T, ctx context.Context, gClient pb.GNMIClient, pathTarget string,
	textPbPath string, wantRetCode codes.Code, wantRespVal interface{}, attributeData string, op op_t) {
	t.Helper()
	// Send request
	var pbPath pb.Path
	if err := proto.UnmarshalText(textPbPath, &pbPath); err != nil {
		t.Fatalf("error in unmarshaling path: %v %v", textPbPath, err)
	}
	req := &pb.SetRequest{}
	switch op {
	case Replace, Update:
		prefix := pb.Path{Target: pathTarget}
		var v *pb.TypedValue
		v = &pb.TypedValue{
			Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: extractJSON(attributeData)}}
		data := []*pb.Update{{Path: &pbPath, Val: v}}

		req = &pb.SetRequest{
			Prefix: &prefix,
		}
		if op == Replace {
			req.Replace = data
		} else {
			req.Update = data
		}
	case Delete:
		req = &pb.SetRequest{
			Delete: []*pb.Path{&pbPath},
		}
	}

	runTestSetRaw(t, ctx, gClient, req, wantRetCode)
}

func runTestSetRaw(t *testing.T, ctx context.Context, gClient pb.GNMIClient, req *pb.SetRequest,
	wantRetCode codes.Code) {
	t.Helper()

	_, err := gClient.Set(ctx, req)
	gotRetStatus, ok := status.FromError(err)
	if !ok {
		t.Fatal("got a non-grpc error from grpc call")
	}
	if gotRetStatus.Code() != wantRetCode {
		t.Log("err: ", err)
		t.Fatalf("got return code %v, want %v", gotRetStatus.Code(), wantRetCode)
	} else {
	}
}

// pathToPb converts string representation of gnmi path to protobuf format
func pathToPb(s string) string {
	p, _ := ygot.StringToStructuredPath(s)
	return proto.MarshalTextString(p)
}

func removeModulePrefixFromPathPb(t *testing.T, s string) string {
	t.Helper()
	var p pb.Path
	if err := proto.UnmarshalText(s, &p); err != nil {
		t.Fatalf("error unmarshaling path: %v %v", s, err)
	}
	for _, ele := range p.Elem {
		if k := strings.IndexByte(ele.Name, ':'); k != -1 {
			ele.Name = ele.Name[k+1:]
		}
	}
	return proto.MarshalTextString(&p)
}

func runServer(t *testing.T, s *Server) {
	//t.Log("Starting RPC server on address:", s.Address())
	err := s.Serve() // blocks until close
	if err != nil {
		t.Fatalf("gRPC server err: %v", err)
	}
	//t.Log("Exiting RPC server on address", s.Address())
}

func getRedisClientN(t *testing.T, n int, namespace string) *redis.Client {
	addr, err := sdcfg.GetDbTcpAddr("COUNTERS_DB", namespace)
	if err != nil {
		t.Fatalf("failed to get addr %v", err)
	}
	rclient := redis.NewClient(&redis.Options{
		Network:     "tcp",
		Addr:        addr,
		Password:    "", // no password set
		DB:          n,
		DialTimeout: 0,
	})
	_, err = rclient.Ping().Result()
	if err != nil {
		t.Fatalf("failed to connect to redis server %v", err)
	}
	return rclient
}

func getRedisClient(t *testing.T, namespace string) *redis.Client {
	addr, err := sdcfg.GetDbTcpAddr("COUNTERS_DB", namespace)
	if err != nil {
		t.Fatalf("failed to get addr %v", err)
	}
	db, err := sdcfg.GetDbId("COUNTERS_DB", namespace)
	if err != nil {
		t.Fatalf("failed to get db %v", err)
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

func getConfigDbClient(t *testing.T, namespace string) *redis.Client {
	addr, err := sdcfg.GetDbTcpAddr("CONFIG_DB", namespace)
	if err != nil {
		t.Fatalf("failed to get addr %v", err)
	}
	db, err := sdcfg.GetDbId("CONFIG_DB", namespace)
	if err != nil {
		t.Fatalf("failed to get db %v", err)
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

func initFullConfigDb(t *testing.T, namespace string) {
	rclient := getConfigDbClient(t, namespace)
	defer rclient.Close()
	rclient.FlushDB()

	fileName := "../testdata/CONFIG_DHCP_SERVER.txt"
	config, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	config_map := loadConfig(t, "", config)
	loadConfigDB(t, rclient, config_map)
}

func initFullCountersDb(t *testing.T, namespace string) {
	rclient := getRedisClient(t, namespace)
	defer rclient.Close()
	rclient.FlushDB()

	fileName := "../testdata/COUNTERS_PORT_NAME_MAP.txt"
	countersPortNameMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_name_map := loadConfig(t, "COUNTERS_PORT_NAME_MAP", countersPortNameMapByte)
	loadDB(t, rclient, mpi_name_map)

	fileName = "../testdata/COUNTERS_QUEUE_NAME_MAP.txt"
	countersQueueNameMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_qname_map := loadConfig(t, "COUNTERS_QUEUE_NAME_MAP", countersQueueNameMapByte)
	loadDB(t, rclient, mpi_qname_map)

	fileName = "../testdata/COUNTERS:Ethernet68.txt"
	countersEthernet68Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	// "Ethernet68": "oid:0x1000000000039",
	mpi_counter := loadConfig(t, "COUNTERS:oid:0x1000000000039", countersEthernet68Byte)
	loadDB(t, rclient, mpi_counter)

	fileName = "../testdata/COUNTERS:Ethernet1.txt"
	countersEthernet1Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	// "Ethernet1": "oid:0x1000000000003",
	mpi_counter = loadConfig(t, "COUNTERS:oid:0x1000000000003", countersEthernet1Byte)
	loadDB(t, rclient, mpi_counter)

	// "Ethernet64:0": "oid:0x1500000000092a"  : queue counter, to work as data noise
	fileName = "../testdata/COUNTERS:oid:0x1500000000092a.txt"
	counters92aByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_counter = loadConfig(t, "COUNTERS:oid:0x1500000000092a", counters92aByte)
	loadDB(t, rclient, mpi_counter)

	// "Ethernet68:1": "oid:0x1500000000091c"  : queue counter, for COUNTERS/Ethernet68/Queue vpath test
	fileName = "../testdata/COUNTERS:oid:0x1500000000091c.txt"
	countersEeth68_1Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_counter = loadConfig(t, "COUNTERS:oid:0x1500000000091c", countersEeth68_1Byte)
	loadDB(t, rclient, mpi_counter)

	// "Ethernet68:3": "oid:0x1500000000091e"  : lossless queue counter, for COUNTERS/Ethernet68/Pfcwd vpath test
	fileName = "../testdata/COUNTERS:oid:0x1500000000091e.txt"
	countersEeth68_3Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_counter = loadConfig(t, "COUNTERS:oid:0x1500000000091e", countersEeth68_3Byte)
	loadDB(t, rclient, mpi_counter)

	// "Ethernet68:4": "oid:0x1500000000091f"  : lossless queue counter, for COUNTERS/Ethernet68/Pfcwd vpath test
	fileName = "../testdata/COUNTERS:oid:0x1500000000091f.txt"
	countersEeth68_4Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_counter = loadConfig(t, "COUNTERS:oid:0x1500000000091f", countersEeth68_4Byte)
	loadDB(t, rclient, mpi_counter)
}

func prepareConfigDb(t *testing.T, namespace string) {
	rclient := getConfigDbClient(t, namespace)
	defer rclient.Close()
	rclient.FlushDB()

	fileName := "../testdata/COUNTERS_PORT_ALIAS_MAP.txt"
	countersPortAliasMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_alias_map := loadConfig(t, "", countersPortAliasMapByte)
	loadConfigDB(t, rclient, mpi_alias_map)

	fileName = "../testdata/CONFIG_PFCWD_PORTS.txt"
	configPfcwdByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_pfcwd_map := loadConfig(t, "", configPfcwdByte)
	loadConfigDB(t, rclient, mpi_pfcwd_map)
}
func prepareStateDb(t *testing.T, namespace string) {
	rclient := getRedisClientN(t, 6, namespace)
	defer rclient.Close()
	rclient.FlushDB()
	rclient.HSet("SWITCH_CAPABILITY|switch", "test_field", "test_value")
	fileName := "../testdata/NEIGH_STATE_TABLE.txt"
	neighStateTableByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_neigh := loadConfig(t, "", neighStateTableByte)
	loadDB(t, rclient, mpi_neigh)

}

func prepareDb(t *testing.T, namespace string) {
	rclient := getRedisClient(t, namespace)
	defer rclient.Close()
	rclient.FlushDB()
	//Enable keysapce notification
	os.Setenv("PATH", "/usr/bin:/sbin:/bin:/usr/local/bin")
	cmd := exec.Command("redis-cli", "config", "set", "notify-keyspace-events", "KEA")
	_, err := cmd.Output()
	if err != nil {
		t.Fatal("failed to enable redis keyspace notification ", err)
	}

	fileName := "../testdata/COUNTERS_PORT_NAME_MAP.txt"
	countersPortNameMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_name_map := loadConfig(t, "COUNTERS_PORT_NAME_MAP", countersPortNameMapByte)
	loadDB(t, rclient, mpi_name_map)

	fileName = "../testdata/COUNTERS_QUEUE_NAME_MAP.txt"
	countersQueueNameMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_qname_map := loadConfig(t, "COUNTERS_QUEUE_NAME_MAP", countersQueueNameMapByte)
	loadDB(t, rclient, mpi_qname_map)

	fileName = "../testdata/COUNTERS:Ethernet68.txt"
	countersEthernet68Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	// "Ethernet68": "oid:0x1000000000039",
	mpi_counter := loadConfig(t, "COUNTERS:oid:0x1000000000039", countersEthernet68Byte)
	loadDB(t, rclient, mpi_counter)

	fileName = "../testdata/COUNTERS:Ethernet1.txt"
	countersEthernet1Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	// "Ethernet1": "oid:0x1000000000003",
	mpi_counter = loadConfig(t, "COUNTERS:oid:0x1000000000003", countersEthernet1Byte)
	loadDB(t, rclient, mpi_counter)

	// "Ethernet64:0": "oid:0x1500000000092a"  : queue counter, to work as data noise
	fileName = "../testdata/COUNTERS:oid:0x1500000000092a.txt"
	counters92aByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_counter = loadConfig(t, "COUNTERS:oid:0x1500000000092a", counters92aByte)
	loadDB(t, rclient, mpi_counter)

	// "Ethernet68:1": "oid:0x1500000000091c"  : queue counter, for COUNTERS/Ethernet68/Queue vpath test
	fileName = "../testdata/COUNTERS:oid:0x1500000000091c.txt"
	countersEeth68_1Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_counter = loadConfig(t, "COUNTERS:oid:0x1500000000091c", countersEeth68_1Byte)
	loadDB(t, rclient, mpi_counter)

	// "Ethernet68:3": "oid:0x1500000000091e"  : lossless queue counter, for COUNTERS/Ethernet68/Pfcwd vpath test
	fileName = "../testdata/COUNTERS:oid:0x1500000000091e.txt"
	countersEeth68_3Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_counter = loadConfig(t, "COUNTERS:oid:0x1500000000091e", countersEeth68_3Byte)
	loadDB(t, rclient, mpi_counter)

	// "Ethernet68:4": "oid:0x1500000000091f"  : lossless queue counter, for COUNTERS/Ethernet68/Pfcwd vpath test
	fileName = "../testdata/COUNTERS:oid:0x1500000000091f.txt"
	countersEeth68_4Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	mpi_counter = loadConfig(t, "COUNTERS:oid:0x1500000000091f", countersEeth68_4Byte)
	loadDB(t, rclient, mpi_counter)

	// Load CONFIG_DB for alias translation
	prepareConfigDb(t, namespace)

	//Load STATE_DB to test non V2R dataset
	prepareStateDb(t, namespace)
}

func prepareDbTranslib(t *testing.T) {
	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClient(t, ns)
	rclient.FlushDB()
	rclient.Close()

	//Enable keysapce notification
	os.Setenv("PATH", "/usr/bin:/sbin:/bin:/usr/local/bin")
	cmd := exec.Command("redis-cli", "config", "set", "notify-keyspace-events", "KEA")
	_, err := cmd.Output()
	if err != nil {
		t.Fatal("failed to enable redis keyspace notification ", err)
	}

	fileName := "../testdata/db_dump.json"
	countersPortNameMapByte, err := ioutil.ReadFile(fileName)
	var rj []map[string]interface{}
	json.Unmarshal(countersPortNameMapByte, &rj)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	for n, v := range rj {
		rclient := getRedisClientN(t, n, ns)
		loadDBNotStrict(t, rclient, v)
		rclient.Close()
	}
}

// subscriptionQuery represent the input to create an gnmi.Subscription instance.
type subscriptionQuery struct {
	Query          []string
	SubMode        pb.SubscriptionMode
	SampleInterval uint64
}

func pathToString(q client.Path) string {
	qq := make(client.Path, len(q))
	copy(qq, q)
	// Escape all slashes within a path element. ygot.StringToPath will handle
	// these escapes.
	for i, e := range qq {
		qq[i] = strings.Replace(e, "/", "\\/", -1)
	}
	return strings.Join(qq, "/")
}

// createQuery creates a client.Query with the given args. It assigns query.SubReq.
func createQuery(subListMode pb.SubscriptionList_Mode, target string, queries []subscriptionQuery, updatesOnly bool) (*client.Query, error) {
	s := &pb.SubscribeRequest_Subscribe{
		Subscribe: &pb.SubscriptionList{
			Mode:   subListMode,
			Prefix: &pb.Path{Target: target},
		},
	}
	if updatesOnly {
		s.Subscribe.UpdatesOnly = true
	}

	for _, qq := range queries {
		pp, err := ygot.StringToPath(pathToString(qq.Query), ygot.StructuredPath, ygot.StringSlicePath)
		if err != nil {
			return nil, fmt.Errorf("invalid query path %q: %v", qq, err)
		}
		s.Subscribe.Subscription = append(
			s.Subscribe.Subscription,
			&pb.Subscription{
				Path:           pp,
				Mode:           qq.SubMode,
				SampleInterval: qq.SampleInterval,
			})
	}

	subReq := &pb.SubscribeRequest{Request: s}
	query, err := client.NewQuery(subReq)
	query.TLS = &tls.Config{InsecureSkipVerify: true}
	return &query, err
}

// createQueryOrFail creates a query, in case of a failure it fails the test.
func createQueryOrFail(t *testing.T, subListMode pb.SubscriptionList_Mode, target string, queries []subscriptionQuery, updatesOnly bool) client.Query {
	q, err := createQuery(subListMode, target, queries, updatesOnly)
	if err != nil {
		t.Fatalf("failed to create query: %v", err)
	}

	return *q
}

// create query for subscribing to events.
func createEventsQuery(t *testing.T, paths ...string) client.Query {
	return createQueryOrFail(t,
		pb.SubscriptionList_STREAM,
		"EVENTS",
		[]subscriptionQuery{
			{
				Query:   paths,
				SubMode: pb.SubscriptionMode_ON_CHANGE,
			},
		},
		false)
}

func createStateDbQueryOnChangeMode(t *testing.T, paths ...string) client.Query {
	return createQueryOrFail(t,
		pb.SubscriptionList_STREAM,
		"STATE_DB",
		[]subscriptionQuery{
			{
				Query:   paths,
				SubMode: pb.SubscriptionMode_ON_CHANGE,
			},
		},
		false)
}

// createCountersDbQueryOnChangeMode creates a query with ON_CHANGE mode.
func createCountersDbQueryOnChangeMode(t *testing.T, paths ...string) client.Query {
	return createQueryOrFail(t,
		pb.SubscriptionList_STREAM,
		"COUNTERS_DB",
		[]subscriptionQuery{
			{
				Query:   paths,
				SubMode: pb.SubscriptionMode_ON_CHANGE,
			},
		},
		false)
}

// createCountersDbQuerySampleMode creates a query with SAMPLE mode.
func createCountersDbQuerySampleMode(t *testing.T, interval time.Duration, updateOnly bool, paths ...string) client.Query {
	return createQueryOrFail(t,
		pb.SubscriptionList_STREAM,
		"COUNTERS_DB",
		[]subscriptionQuery{
			{
				Query:          paths,
				SubMode:        pb.SubscriptionMode_SAMPLE,
				SampleInterval: uint64(interval.Nanoseconds()),
			},
		},
		updateOnly)
}

// createCountersTableSetUpdate creates a HSET request on the COUNTERS table.
func createCountersTableSetUpdate(tableKey string, fieldName string, fieldValue string) tablePathValue {
	return tablePathValue{
		dbName:    "COUNTERS_DB",
		tableName: "COUNTERS",
		tableKey:  tableKey,
		delimitor: ":",
		field:     fieldName,
		value:     fieldValue,
	}
}

// createCountersTableDeleteUpdate creates a DEL request on the COUNTERS table.
func createCountersTableDeleteUpdate(tableKey string, fieldName string) tablePathValue {
	return tablePathValue{
		dbName:    "COUNTERS_DB",
		tableName: "COUNTERS",
		tableKey:  tableKey,
		delimitor: ":",
		field:     fieldName,
		value:     "",
		op:        "hdel",
	}
}

// createIntervalTickerUpdate creates a request for triggering the interval clock.
func createIntervalTickerUpdate() tablePathValue {
	return tablePathValue{
		op: "intervaltick",
	}
}

// cloneObject clones a given object via JSON serialize/deserialize
func cloneObject(obj interface{}) interface{} {
	objData, err := json.Marshal(obj)
	if err != nil {
		panic(fmt.Errorf("marshal failed, %v", err))
	}

	var cloneObj interface{}
	err = json.Unmarshal(objData, &cloneObj)
	if err != nil {
		panic(fmt.Errorf("unmarshal failed, %v", err))
	}

	return cloneObj
}

// mergeStrMaps merges given maps where they are keyed with string.
func mergeStrMaps(sourceOrigin interface{}, updateOrigin interface{}) interface{} {
	// Clone the maps so that the originals are not changed during the merge.
	source := cloneObject(sourceOrigin)
	update := cloneObject(updateOrigin)

	// Check if both are string keyed maps
	sourceStrMap, okSrcMap := source.(map[string]interface{})
	updateStrMap, okUpdateMap := update.(map[string]interface{})
	if okSrcMap && okUpdateMap {
		for itemKey, updateItem := range updateStrMap {
			sourceItem, sourceItemOk := sourceStrMap[itemKey]
			if sourceItemOk {
				sourceStrMap[itemKey] = updateItem
			} else {
				sourceStrMap[itemKey] = mergeStrMaps(sourceItem, updateItem)
			}
		}
		return sourceStrMap
	}

	return update
}

/*
func TestGnmiSet(t *testing.T) {
	if !ENABLE_TRANSLIB_WRITE {
		t.Skip("skipping test in read-only mode.")
	}
	s := createServer(t, 8081)
	go runServer(t, s)

	prepareDbTranslib(t)

	//t.Log("Start gNMI client")
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	targetAddr := "127.0.0.1:8081"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tds := []struct {
		desc          string
		pathTarget    string
		textPbPath    string
		wantRetCode   codes.Code
		wantRespVal   interface{}
		attributeData string
		operation     op_t
		valTest       bool
	}{
		{
			desc:        "Invalid path",
			pathTarget:  "OC_YANG",
			textPbPath:  pathToPb("/openconfig-interfaces:interfaces/interface[name=Ethernet4]/unknown"),
			wantRetCode: codes.Unknown,
			operation:   Delete,
		},
		//{
		//	desc:       "Set OC Interface MTU",
		//	pathTarget: "OC_YANG",
		//	textPbPath:    pathToPb("openconfig-interfaces:interfaces/interface[name=Ethernet4]/config"),
		//	attributeData: "../testdata/set_interface_mtu.json",
		//	wantRetCode:   codes.OK,
		//	operation:     Update,
		//},
		{
			desc:          "Set OC Interface IP",
			pathTarget:    "OC_YANG",
			textPbPath:    pathToPb("/openconfig-interfaces:interfaces/interface[name=Ethernet4]/subinterfaces/subinterface[index=0]/openconfig-if-ip:ipv4"),
			attributeData: "../testdata/set_interface_ipv4.json",
			wantRetCode:   codes.OK,
			operation:     Update,
		},
		// {
		//         desc:       "Check OC Interface values set",
		//         pathTarget: "OC_YANG",
		//         textPbPath: `
		//                 elem: <name: "openconfig-interfaces:interfaces" > elem: <name: "interface" key:<key:"name" value:"Ethernet4" > >
		//         `,
		//         wantRetCode: codes.OK,
		//         wantRespVal: interfaceData,
		//         valTest:true,
		// },
		{
			desc:       "Delete OC Interface IP",
			pathTarget: "OC_YANG",
			textPbPath: `
                    elem:<name:"openconfig-interfaces:interfaces" > elem:<name:"interface" key:<key:"name" value:"Ethernet4" > > elem:<name:"subinterfaces" > elem:<name:"subinterface" key:<key:"index" value:"0" > > elem:<name: "ipv4" > elem:<name: "addresses" > elem:<name:"address" key:<key:"ip" value:"9.9.9.9" > >
                `,
			attributeData: "",
			wantRetCode:   codes.OK,
			operation:     Delete,
			valTest:       false,
		},
		{
			desc:          "Set OC Interface IPv6 (unprefixed path)",
			pathTarget:    "OC_YANG",
			textPbPath:    pathToPb("/interfaces/interface[name=Ethernet0]/subinterfaces/subinterface[index=0]/ipv6/addresses/address"),
			attributeData: `{"address": [{"ip": "150::1","config": {"ip": "150::1","prefix-length": 80}}]}`,
			wantRetCode:   codes.OK,
			operation:     Update,
		},
		{
			desc:        "Delete OC Interface IPv6 (unprefixed path)",
			pathTarget:  "OC_YANG",
			textPbPath:  pathToPb("/interfaces/interface[name=Ethernet0]/subinterfaces/subinterface[index=0]/ipv6/addresses/address[ip=150::1]"),
			wantRetCode: codes.OK,
			operation:   Delete,
		},
		{
			desc:       "Create ACL (unprefixed path)",
			pathTarget: "OC_YANG",
			textPbPath: pathToPb("/acl/acl-sets/acl-set"),
			attributeData: `{"acl-set": [{"name": "A001", "type": "ACL_IPV4",
							"config": {"name": "A001", "type": "ACL_IPV4", "description": "hello, world!"}}]}`,
			wantRetCode: codes.OK,
			operation:   Update,
		},
		{
			desc:        "Verify Create ACL",
			pathTarget:  "OC_YANG",
			textPbPath:  pathToPb("/openconfig-acl:acl/acl-sets/acl-set[name=A001][type=ACL_IPV4]/config/description"),
			wantRespVal: `{"openconfig-acl:description": "hello, world!"}`,
			wantRetCode: codes.OK,
			valTest:     true,
		},
		{
			desc:          "Replace ACL Description (unprefixed path)",
			pathTarget:    "OC_YANG",
			textPbPath:    pathToPb("/acl/acl-sets/acl-set[name=A001][type=ACL_IPV4]/config/description"),
			attributeData: `{"description": "dummy"}`,
			wantRetCode:   codes.OK,
			operation:     Replace,
		},
		{
			desc:        "Verify Replace ACL Description",
			pathTarget:  "OC_YANG",
			textPbPath:  pathToPb("/openconfig-acl:acl/acl-sets/acl-set[name=A001][type=ACL_IPV4]/config/description"),
			wantRespVal: `{"openconfig-acl:description": "dummy"}`,
			wantRetCode: codes.OK,
			valTest:     true,
		},
		{
			desc:        "Delete ACL",
			pathTarget:  "OC_YANG",
			textPbPath:  pathToPb("/openconfig-acl:acl/acl-sets/acl-set[name=A001][type=ACL_IPV4]"),
			wantRetCode: codes.OK,
			operation:   Delete,
		},
		{
			desc:        "Verify Delete ACL",
			pathTarget:  "OC_YANG",
			textPbPath:  pathToPb("/openconfig-acl:acl/acl-sets/acl-set[name=A001][type=ACL_IPV4]"),
			wantRetCode: codes.NotFound,
			valTest:     true,
		},
	}

	for _, td := range tds {
		if td.valTest == true {
			t.Run(td.desc, func(t *testing.T) {
				runTestGet(t, ctx, gClient, td.pathTarget, td.textPbPath, td.wantRetCode, td.wantRespVal, td.valTest)
			})
			t.Run(td.desc+" (unprefixed path)", func(t *testing.T) {
				p := removeModulePrefixFromPathPb(t, td.textPbPath)
				runTestGet(t, ctx, gClient, td.pathTarget, p, td.wantRetCode, td.wantRespVal, td.valTest)
			})
		} else {
			t.Run(td.desc, func(t *testing.T) {
				runTestSet(t, ctx, gClient, td.pathTarget, td.textPbPath, td.wantRetCode, td.wantRespVal, td.attributeData, td.operation)
			})
		}
	}
	s.Stop()
}*/

func TestGnmiSetReadOnly(t *testing.T) {
	s := createReadServer(t, 8081)
	go runServer(t, s)
	defer s.Stop()

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	targetAddr := "127.0.0.1:8081"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &pb.SetRequest{}
	_, err = gClient.Set(ctx, req)
	gotRetStatus, ok := status.FromError(err)
	if !ok {
		t.Fatal("got a non-grpc error from grpc call")
	}
	wantRetCode := codes.Unimplemented
	if gotRetStatus.Code() != wantRetCode {
		t.Log("err: ", err)
		t.Fatalf("got return code %v, want %v", gotRetStatus.Code(), wantRetCode)
	}
}

func TestGnmiSetAuthFail(t *testing.T) {
	s := createAuthServer(t, 8081)
	go runServer(t, s)
	defer s.Stop()

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	targetAddr := "127.0.0.1:8081"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &pb.SetRequest{}
	_, err = gClient.Set(ctx, req)
	gotRetStatus, ok := status.FromError(err)
	if !ok {
		t.Fatal("got a non-grpc error from grpc call")
	}
	wantRetCode := codes.Unauthenticated
	if gotRetStatus.Code() != wantRetCode {
		t.Log("err: ", err)
		t.Fatalf("got return code %v, want %v", gotRetStatus.Code(), wantRetCode)
	}
}

func TestGnmiGetAuthFail(t *testing.T) {
	s := createAuthServer(t, 8081)
	go runServer(t, s)
	defer s.Stop()

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	targetAddr := "127.0.0.1:8081"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &pb.GetRequest{}
	_, err = gClient.Get(ctx, req)
	gotRetStatus, ok := status.FromError(err)
	if !ok {
		t.Fatal("got a non-grpc error from grpc call")
	}
	wantRetCode := codes.Unauthenticated
	if gotRetStatus.Code() != wantRetCode {
		t.Log("err: ", err)
		t.Fatalf("got return code %v, want %v", gotRetStatus.Code(), wantRetCode)
	}
}

func runGnmiTestGet(t *testing.T, namespace string) {
	//t.Log("Start gNMI client")
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	targetAddr := "127.0.0.1:8081"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fileName := "../testdata/COUNTERS_PORT_NAME_MAP.txt"
	countersPortNameMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}

	fileName = "../testdata/COUNTERS:Ethernet68.txt"
	countersEthernet68Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}

	fileName = "../testdata/COUNTERS:Ethernet68:Pfcwd.txt"
	countersEthernet68PfcwdByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}

	fileName = "../testdata/COUNTERS:Ethernet68:Pfcwd_alias.txt"
	countersEthernet68PfcwdAliasByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}

	fileName = "../testdata/COUNTERS:Ethernet_wildcard_alias.txt"
	countersEthernetWildcardByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}

	fileName = "../testdata/COUNTERS:Ethernet_wildcard_PFC_7_RX_alias.txt"
	countersEthernetWildcardPfcByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}

	fileName = "../testdata/COUNTERS:Ethernet_wildcard_Pfcwd_alias.txt"
	countersEthernetWildcardPfcwdByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}

	stateDBPath := "STATE_DB"

	ns, _ := sdcfg.GetDbDefaultNamespace()
	if namespace != ns {
		stateDBPath = "STATE_DB" + "/" + namespace
	}

	type testCase struct {
		desc        string
		pathTarget  string
		textPbPath  string
		wantRetCode codes.Code
		wantRespVal interface{}
		valTest     bool
		testInit    func()
	}

	// A helper function create test cases for 'osversion/build' queries.
	createBuildVersionTestCase := func(desc string, wantedVersion string, versionFileContent string, fileReadErr error) testCase {
		return testCase{
			desc:       desc,
			pathTarget: "OTHERS",
			textPbPath: `
						elem: <name: "osversion" >
						elem: <name: "build" >
					`,
			wantRetCode: codes.OK,
			valTest:     true,
			wantRespVal: []byte(wantedVersion),
			testInit: func() {
				// Override file read function to mock file content.
				sdc.ImplIoutilReadFile = func(filePath string) ([]byte, error) {
					if filePath == sdc.SonicVersionFilePath {
						if fileReadErr != nil {
							return nil, fileReadErr
						}
						return []byte(versionFileContent), nil
					}
					return ioutil.ReadFile(filePath)
				}

				// Reset the cache so that the content gets loaded again.
				sdc.InvalidateVersionFileStash()
			},
		}
	}

	tds := []testCase{{
		desc:       "Test non-existing path Target",
		pathTarget: "MY_DB",
		textPbPath: `
			elem: <name: "MyCounters" >
		`,
		wantRetCode: codes.NotFound,
	}, {
		desc:       "Test passing asic in path for V2R Dataset Target",
		pathTarget: "COUNTER_DB" + "/" + namespace,
		textPbPath: `
					elem: <name: "COUNTERS" >
					elem: <name: "Ethernet68" >
				`,
		wantRetCode: codes.NotFound,
	},
		{
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
			valTest:     true,
		}, {
			desc:       "get COUNTERS:Ethernet68",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
					elem: <name: "COUNTERS" >
					elem: <name: "Ethernet68" >
				`,
			wantRetCode: codes.OK,
			wantRespVal: countersEthernet68Byte,
			valTest:     true,
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
			valTest:     true,
		}, {
			desc:       "get COUNTERS:Ethernet68 Pfcwd",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
					elem: <name: "COUNTERS" >
					elem: <name: "Ethernet68" >
					elem: <name: "Pfcwd" >
				`,
			wantRetCode: codes.OK,
			wantRespVal: countersEthernet68PfcwdByte,
			valTest:     true,
		}, {
			desc:       "get COUNTERS (use vendor alias):Ethernet68/1",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
					elem: <name: "COUNTERS" >
					elem: <name: "Ethernet68/1" >
				`,
			wantRetCode: codes.OK,
			wantRespVal: countersEthernet68Byte,
			valTest:     true,
		}, {
			desc:       "get COUNTERS (use vendor alias):Ethernet68/1 SAI_PORT_STAT_PFC_7_RX_PKTS",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
					elem: <name: "COUNTERS" >
					elem: <name: "Ethernet68/1" >
					elem: <name: "SAI_PORT_STAT_PFC_7_RX_PKTS" >
				`,
			wantRetCode: codes.OK,
			wantRespVal: "2",
			valTest:     true,
		}, {
			desc:       "get COUNTERS (use vendor alias):Ethernet68/1 Pfcwd",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
					elem: <name: "COUNTERS" >
					elem: <name: "Ethernet68/1" >
					elem: <name: "Pfcwd" >
				`,
			wantRetCode: codes.OK,
			wantRespVal: countersEthernet68PfcwdAliasByte,
			valTest:     true,
		}, {
			desc:       "get COUNTERS:Ethernet*",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
					elem: <name: "COUNTERS" >
					elem: <name: "Ethernet*" >
				`,
			wantRetCode: codes.OK,
			wantRespVal: countersEthernetWildcardByte,
			valTest:     true,
		}, {
			desc:       "get COUNTERS:Ethernet* SAI_PORT_STAT_PFC_7_RX_PKTS",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
					elem: <name: "COUNTERS" >
					elem: <name: "Ethernet*" >
					elem: <name: "SAI_PORT_STAT_PFC_7_RX_PKTS" >
				`,
			wantRetCode: codes.OK,
			wantRespVal: countersEthernetWildcardPfcByte,
			valTest:     true,
		}, {
			desc:       "get COUNTERS:Ethernet* Pfcwd",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
					elem: <name: "COUNTERS" >
					elem: <name: "Ethernet*" >
					elem: <name: "Pfcwd" >
				`,
			wantRetCode: codes.OK,
			wantRespVal: countersEthernetWildcardPfcwdByte,
			valTest:     true,
		}, {
			desc:       "get State DB Data for SWITCH_CAPABILITY switch",
			pathTarget: stateDBPath,
			textPbPath: `
					elem: <name: "SWITCH_CAPABILITY" >
					elem: <name: "switch" >
				`,
			valTest:     true,
			wantRetCode: codes.OK,
			wantRespVal: []byte(`{"test_field": "test_value"}`),
		}, {
			desc:        "Invalid DBKey of length 1",
			pathTarget:  stateDBPath,
			textPbPath:  ``,
			valTest:     true,
			wantRetCode: codes.NotFound,
		},

		// Happy path
		createBuildVersionTestCase(
			"get osversion/build",                                  // query path
			`{"build_version": "SONiC.12345678.90", "error":""}`,   // expected response
			"build_version: '12345678.90'\ndebian_version: '9.13'", // YAML file content
			nil), // mock file reading error

		// File reading error
		createBuildVersionTestCase(
			"get osversion/build file load error",
			`{"build_version": "sonic.NA", "error":"Cannot access '/etc/sonic/sonic_version.yml'"}`,
			"",
			fmt.Errorf("Cannot access '%v'", sdc.SonicVersionFilePath)),

		// File content is not valid YAML
		createBuildVersionTestCase(
			"get osversion/build file parse error",
			`{"build_version": "sonic.NA", "error":"yaml: unmarshal errors:\n  line 1: cannot unmarshal !!str `+"`not a v...`"+` into client.SonicVersionInfo"}`,
			"not a valid YAML content",
			nil),

		// Happy path with different value
		createBuildVersionTestCase(
			"get osversion/build different value",
			`{"build_version": "SONiC.23456789.01", "error":""}`,
			"build_version: '23456789.01'\ndebian_version: '9.15'",
			nil),
	}

	for _, td := range tds {
		if td.testInit != nil {
			td.testInit()
		}

		t.Run(td.desc, func(t *testing.T) {
			runTestGet(t, ctx, gClient, td.pathTarget, td.textPbPath, td.wantRetCode, td.wantRespVal, td.valTest)
		})
	}

}

func TestGnmiGet(t *testing.T) {
	//t.Log("Start server")
	s := createServer(t, 8081)
	go runServer(t, s)

	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareDb(t, ns)

	runGnmiTestGet(t, ns)

	s.Stop()
}
func TestGnmiGetMultiNs(t *testing.T) {
	sdcfg.Init()
	err := test_utils.SetupMultiNamespace()
	if err != nil {
		t.Fatalf("error Setting up MultiNamespace files with err %T", err)
	}

	/* https://www.gopherguides.com/articles/test-cleanup-in-go-1-14*/
	t.Cleanup(func() {
		if err := test_utils.CleanUpMultiNamespace(); err != nil {
			t.Fatalf("error Cleaning up MultiNamespace files with err %T", err)

		}
	})

	//t.Log("Start server")
	s := createServer(t, 8081)
	go runServer(t, s)

	prepareDb(t, test_utils.GetMultiNsNamespace())

	runGnmiTestGet(t, test_utils.GetMultiNsNamespace())

	s.Stop()
}

/*
func TestGnmiGetTranslib(t *testing.T) {
	//t.Log("Start server")
	s := createServer(t, 8081)
	go runServer(t, s)

	prepareDbTranslib(t)

	//t.Log("Start gNMI client")
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	targetAddr := "127.0.0.1:8081"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var emptyRespVal interface{}
	tds := []struct {
		desc        string
		pathTarget  string
		textPbPath  string
		wantRetCode codes.Code
		wantRespVal interface{}
		valTest     bool
	}{

		//These tests only work on the real switch platform, since they rely on files in the /proc and another running service
		// 	{
		// 	desc:       "Get OC Platform",
		// 	pathTarget: "OC_YANG",
		// 	textPbPath: `
		//                        elem: <name: "openconfig-platform:components" >
		//                `,
		// 	wantRetCode: codes.OK,
		// 	wantRespVal: emptyRespVal,
		// 	valTest:     false,
		// },
		// 	{
		// 		desc:       "Get OC System State",
		// 		pathTarget: "OC_YANG",
		// 		textPbPath: `
		//                        elem: <name: "openconfig-system:system" > elem: <name: "state" >
		//                `,
		// 		wantRetCode: codes.OK,
		// 		wantRespVal: emptyRespVal,
		// 		valTest:     false,
		// 	},
		// 	{
		// 		desc:       "Get OC System CPU",
		// 		pathTarget: "OC_YANG",
		// 		textPbPath: `
		//                        elem: <name: "openconfig-system:system" > elem: <name: "cpus" >
		//                `,
		// 		wantRetCode: codes.OK,
		// 		wantRespVal: emptyRespVal,
		// 		valTest:     false,
		// 	},
		// 	{
		// 		desc:       "Get OC System memory",
		// 		pathTarget: "OC_YANG",
		// 		textPbPath: `
		//                        elem: <name: "openconfig-system:system" > elem: <name: "memory" >
		//                `,
		// 		wantRetCode: codes.OK,
		// 		wantRespVal: emptyRespVal,
		// 		valTest:     false,
		// 	},
		// 	{
		// 		desc:       "Get OC System processes",
		// 		pathTarget: "OC_YANG",
		// 		textPbPath: `
		//                        elem: <name: "openconfig-system:system" > elem: <name: "processes" >
		//                `,
		// 		wantRetCode: codes.OK,
		// 		wantRespVal: emptyRespVal,
		// 		valTest:     false,
		// 	},
		{
			desc:       "Get OC Interfaces",
			pathTarget: "OC_YANG",
			textPbPath: `
                        elem: <name: "openconfig-interfaces:interfaces" >
                `,
			wantRetCode: codes.OK,
			wantRespVal: emptyRespVal,
			valTest:     false,
		},
		{
			desc:       "Get OC Interface",
			pathTarget: "OC_YANG",
			textPbPath: `
                        elem: <name: "openconfig-interfaces:interfaces" > elem: <name: "interface" key:<key:"name" value:"Ethernet4" > >
                `,
			wantRetCode: codes.OK,
			wantRespVal: emptyRespVal,
			valTest:     false,
		},
		{
			desc:       "Get OC Interface admin-status",
			pathTarget: "OC_YANG",
			textPbPath: `
                        elem: <name: "openconfig-interfaces:interfaces" > elem: <name: "interface" key:<key:"name" value:"Ethernet4" > > elem: <name: "state" > elem: <name: "admin-status" >
                `,
			wantRetCode: codes.OK,
			wantRespVal: emptyRespVal,
			valTest:     false,
		},
		//{
		//	desc:       "Get OC Interface ifindex",
		//	pathTarget: "OC_YANG",
		//	textPbPath: `
        //                elem: <name: "openconfig-interfaces:interfaces" > elem: <name: "interface" key:<key:"name" value:"Ethernet4" > > elem: <name: "state" > elem: <name: "ifindex" >
        //        `,
		//	wantRetCode: codes.OK,
		//	wantRespVal: emptyRespVal,
		//	valTest:     false,
		//},
		{
			desc:       "Get OC Interface mtu",
			pathTarget: "OC_YANG",
			textPbPath: `
                        elem: <name: "openconfig-interfaces:interfaces" > elem: <name: "interface" key:<key:"name" value:"Ethernet4" > > elem: <name: "state" > elem: <name: "mtu" >
                `,
			wantRetCode: codes.OK,
			wantRespVal: emptyRespVal,
			valTest:     false,
		},
	}

	for _, td := range tds {
		t.Run(td.desc, func(t *testing.T) {
			runTestGet(t, ctx, gClient, td.pathTarget, td.textPbPath, td.wantRetCode, td.wantRespVal, td.valTest)
		})
	}
	s.Stop()
}*/

type tablePathValue struct {
	dbName    string
	tableName string
	tableKey  string
	delimitor string
	field     string
	value     string
	op        string
}

// runTestSubscribe subscribe DB path in stream mode or poll mode.
// The return code and response value are compared with expected code and value.
func runTestSubscribe(t *testing.T, namespace string) {
	fileName := "../testdata/COUNTERS_PORT_NAME_MAP.txt"
	countersPortNameMapByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var countersPortNameMapJson interface{}
	json.Unmarshal(countersPortNameMapByte, &countersPortNameMapJson)
	var tmp interface{}
	json.Unmarshal(countersPortNameMapByte, &tmp)
	countersPortNameMapJsonUpdate := tmp.(map[string]interface{})
	countersPortNameMapJsonUpdate["test_field"] = "test_value"

	// for table key subscription
	fileName = "../testdata/COUNTERS:Ethernet68.txt"
	countersEthernet68Byte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var countersEthernet68Json interface{}
	json.Unmarshal(countersEthernet68Byte, &countersEthernet68Json)

	var tmp2 interface{}
	json.Unmarshal(countersEthernet68Byte, &tmp2)
	countersEthernet68JsonUpdate := tmp2.(map[string]interface{})
	countersEthernet68JsonUpdate["test_field"] = "test_value"

	var tmp3 interface{}
	json.Unmarshal(countersEthernet68Byte, &tmp3)
	countersEthernet68JsonPfcUpdate := tmp3.(map[string]interface{})
	// field SAI_PORT_STAT_PFC_7_RX_PKTS has new value of 4
	countersEthernet68JsonPfcUpdate["SAI_PORT_STAT_PFC_7_RX_PKTS"] = "4"

	// for Ethernet68/Pfcwd subscription
	fileName = "../testdata/COUNTERS:Ethernet68:Pfcwd.txt"
	countersEthernet68PfcwdByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var countersEthernet68PfcwdJson interface{}
	json.Unmarshal(countersEthernet68PfcwdByte, &countersEthernet68PfcwdJson)

	var tmp4 interface{}
	json.Unmarshal(countersEthernet68PfcwdByte, &tmp4)
	countersEthernet68PfcwdJsonUpdate := map[string]interface{}{}
	countersEthernet68PfcwdJsonUpdate["Ethernet68:3"] = tmp4.(map[string]interface{})["Ethernet68:3"]
	countersEthernet68PfcwdJsonUpdate["Ethernet68:3"].(map[string]interface{})["PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED"] = "1"

	tmp4.(map[string]interface{})["Ethernet68:3"].(map[string]interface{})["PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED"] = "1"
	countersEthernet68PfcwdPollUpdate := tmp4

	// (use vendor alias) for Ethernet68/1 Pfcwd subscription
	fileName = "../testdata/COUNTERS:Ethernet68:Pfcwd_alias.txt"
	countersEthernet68PfcwdAliasByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var countersEthernet68PfcwdAliasJson interface{}
	json.Unmarshal(countersEthernet68PfcwdAliasByte, &countersEthernet68PfcwdAliasJson)

	var tmp5 interface{}
	json.Unmarshal(countersEthernet68PfcwdAliasByte, &tmp5)
	countersEthernet68PfcwdAliasJsonUpdate := map[string]interface{}{}
	countersEthernet68PfcwdAliasJsonUpdate["Ethernet68/1:3"] = tmp5.(map[string]interface{})["Ethernet68/1:3"]
	countersEthernet68PfcwdAliasJsonUpdate["Ethernet68/1:3"].(map[string]interface{})["PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED"] = "1"

	tmp5.(map[string]interface{})["Ethernet68/1:3"].(map[string]interface{})["PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED"] = "1"
	countersEthernet68PfcwdAliasPollUpdate := tmp5

	fileName = "../testdata/COUNTERS:Ethernet_wildcard_alias.txt"
	countersEthernetWildcardByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var countersEthernetWildcardJson interface{}
	json.Unmarshal(countersEthernetWildcardByte, &countersEthernetWildcardJson)
	// Will have "test_field" : "test_value" in Ethernet68,
	countersEtherneWildcardJsonUpdate := map[string]interface{}{"Ethernet68/1": countersEthernet68JsonUpdate}

	// all counters on all ports with change on one field of one port
	var countersFieldUpdate map[string]interface{}
	json.Unmarshal(countersEthernetWildcardByte, &countersFieldUpdate)
	countersFieldUpdate["Ethernet68/1"] = countersEthernet68JsonPfcUpdate

	fileName = "../testdata/COUNTERS:Ethernet_wildcard_PFC_7_RX_alias.txt"
	countersEthernetWildcardPfcByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var countersEthernetWildcardPfcJson interface{}
	json.Unmarshal(countersEthernetWildcardPfcByte, &countersEthernetWildcardPfcJson)
	//The update with new value of 4 (original value is 2)
	pfc7Map := map[string]interface{}{"SAI_PORT_STAT_PFC_7_RX_PKTS": "4"}
	singlePortPfcJsonUpdate := make(map[string]interface{})
	singlePortPfcJsonUpdate["Ethernet68/1"] = pfc7Map

	allPortPfcJsonUpdate := make(map[string]interface{})
	json.Unmarshal(countersEthernetWildcardPfcByte, &allPortPfcJsonUpdate)
	//allPortPfcJsonUpdate := countersEthernetWildcardPfcJson.(map[string]interface{})
	allPortPfcJsonUpdate["Ethernet68/1"] = pfc7Map

	// for Ethernet*/Pfcwd subscription
	fileName = "../testdata/COUNTERS:Ethernet_wildcard_Pfcwd_alias.txt"
	countersEthernetWildPfcwdByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}

	var countersEthernetWildPfcwdJson interface{}
	json.Unmarshal(countersEthernetWildPfcwdByte, &countersEthernetWildPfcwdJson)

	var tmp6 interface{}
	json.Unmarshal(countersEthernetWildPfcwdByte, &tmp6)
	tmp6.(map[string]interface{})["Ethernet68/1:3"].(map[string]interface{})["PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED"] = "1"
	countersEthernetWildPfcwdUpdate := tmp6

	fileName = "../testdata/COUNTERS:Ethernet_wildcard_Queues_alias.txt"
	countersEthernetWildQueuesByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var countersEthernetWildQueuesJson interface{}
	json.Unmarshal(countersEthernetWildQueuesByte, &countersEthernetWildQueuesJson)

	fileName = "../testdata/COUNTERS:Ethernet68:Queues.txt"
	countersEthernet68QueuesByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var countersEthernet68QueuesJson interface{}
	json.Unmarshal(countersEthernet68QueuesByte, &countersEthernet68QueuesJson)

	countersEthernet68QueuesJsonUpdate := make(map[string]interface{})
	json.Unmarshal(countersEthernet68QueuesByte, &countersEthernet68QueuesJsonUpdate)
	eth68_1 := map[string]interface{}{
		"SAI_QUEUE_STAT_BYTES":           "0",
		"SAI_QUEUE_STAT_DROPPED_BYTES":   "0",
		"SAI_QUEUE_STAT_DROPPED_PACKETS": "4",
		"SAI_QUEUE_STAT_PACKETS":         "0",
	}
	countersEthernet68QueuesJsonUpdate["Ethernet68:1"] = eth68_1

	// Alias translation for query Ethernet68/1:Queues
	fileName = "../testdata/COUNTERS:Ethernet68:Queues_alias.txt"
	countersEthernet68QueuesAliasByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var countersEthernet68QueuesAliasJson interface{}
	json.Unmarshal(countersEthernet68QueuesAliasByte, &countersEthernet68QueuesAliasJson)

	countersEthernet68QueuesAliasJsonUpdate := make(map[string]interface{})
	json.Unmarshal(countersEthernet68QueuesAliasByte, &countersEthernet68QueuesAliasJsonUpdate)
	countersEthernet68QueuesAliasJsonUpdate["Ethernet68/1:1"] = eth68_1

	type TestExec struct {
		desc       string
		q          client.Query
		prepares   []tablePathValue
		updates    []tablePathValue
		wantErr    bool
		wantNoti   []client.Notification
		wantSubErr error

		poll        int
		wantPollErr string

		generateIntervals bool
	}
	tests := []TestExec{
		{
			desc: "stream query for table COUNTERS_PORT_NAME_MAP with new test_field field",
			q:    createCountersDbQueryOnChangeMode(t, "COUNTERS_PORT_NAME_MAP"),
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
		},
		{
			desc: "stream query for table key Ethernet68 with new test_field field",
			q:    createCountersDbQueryOnChangeMode(t, "COUNTERS", "Ethernet68"),
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "test_field",
					value:     "test_value",
				},
				{ //Same value set should not trigger multiple updates
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "test_field",
					value:     "test_value",
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68"}, TS: time.Unix(0, 200), Val: countersEthernet68Json},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68"}, TS: time.Unix(0, 200), Val: countersEthernet68JsonUpdate},
			},
		},
		{
			desc: "(use vendor alias) stream query for table key Ethernet68/1 with new test_field field",
			q:    createCountersDbQueryOnChangeMode(t, "COUNTERS", "Ethernet68/1"),
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "test_field",
					value:     "test_value",
				},
				{ //Same value set should not trigger multiple updates
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "test_field",
					value:     "test_value",
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1"}, TS: time.Unix(0, 200), Val: countersEthernet68Json},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1"}, TS: time.Unix(0, 200), Val: countersEthernet68JsonUpdate},
			},
		},
		{
			desc: "stream query for COUNTERS/Ethernet68/SAI_PORT_STAT_PFC_7_RX_PKTS with update of field value",
			q:    createCountersDbQueryOnChangeMode(t, "COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"),
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
					value:     "3", // be changed to 3 from 2
				},
				{ //Same value set should not trigger multiple updates
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
					value:     "3", // be changed to 3 from 2
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "2"},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "3"},
			},
		},
		{
			desc: "(use vendor alias) stream query for COUNTERS/[Ethernet68/1]/SAI_PORT_STAT_PFC_7_RX_PKTS with update of field value",
			q:    createCountersDbQueryOnChangeMode(t, "COUNTERS", "Ethernet68/1", "SAI_PORT_STAT_PFC_7_RX_PKTS"),
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
					value:     "3", // be changed to 3 from 2
				},
				{ //Same value set should not trigger multiple updates
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
					value:     "3", // be changed to 3 from 2
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "2"},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "3"},
			},
		},
		{
			desc: "stream query for COUNTERS/Ethernet68/Pfcwd with update of field value",
			q:    createCountersDbQueryOnChangeMode(t, "COUNTERS", "Ethernet68", "Pfcwd"),
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1500000000091e", // "Ethernet68:3": "oid:0x1500000000091e",
					delimitor: ":",
					field:     "PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED",
					value:     "1", // be changed to 1 from 0
				},
				{ //Same value set should not trigger multiple updates
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1500000000091e", // "Ethernet68:3": "oid:0x1500000000091e"
					delimitor: ":",
					field:     "PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED",
					value:     "1", // be changed to 1 from 1
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdJsonUpdate},
			},
		},
		{
			desc: "(use vendor alias) stream query for COUNTERS/[Ethernet68/1]/Pfcwd with update of field value",
			q:    createCountersDbQueryOnChangeMode(t, "COUNTERS", "Ethernet68/1", "Pfcwd"),
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1500000000091e", // "Ethernet68:3": "oid:0x1500000000091e",
					delimitor: ":",
					field:     "PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED",
					value:     "1", // be changed to 1 from 0
				},
				{ //Same value set should not trigger multiple updates
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1500000000091e", // "Ethernet68:3": "oid:0x1500000000091e"
					delimitor: ":",
					field:     "PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED",
					value:     "1", // be changed to 1 from 1
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdAliasJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdAliasJsonUpdate},
			},
		},
		{
			desc: "stream query for table key Ethernet* with new test_field field on Ethernet68",
			q:    createCountersDbQueryOnChangeMode(t, "COUNTERS", "Ethernet*"),
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "test_field",
					value:     "test_value",
				},
				{ //Same value set should not trigger multiple updates
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "test_field",
					value:     "test_value",
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: countersEthernetWildcardJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: countersEtherneWildcardJsonUpdate},
			},
		},
		{
			desc: "stream query for table key Ethernet*/SAI_PORT_STAT_PFC_7_RX_PKTS with field value update",
			q:    createCountersDbQueryOnChangeMode(t, "COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"),
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
					value:     "4", // being changed to 4 from 2
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: countersEthernetWildcardPfcJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: singlePortPfcJsonUpdate},
			},
		},
		{
			desc: "stream query for table key Ethernet*/Pfcwd with field value update",
			q:    createCountersDbQueryOnChangeMode(t, "COUNTERS", "Ethernet*", "Pfcwd"),
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1500000000091e", // "Ethernet68:3": "oid:0x1500000000091e",
					delimitor: ":",
					field:     "PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED",
					value:     "1", // being changed to 1 from 0
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernetWildPfcwdJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdAliasJsonUpdate},
			},
		},
		{
			desc: "poll query for table COUNTERS_PORT_NAME_MAP with new field test_field",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
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
				// We are starting from the result data of "stream query for table with update of new field",
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: countersPortNameMapJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: countersPortNameMapJsonUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: countersPortNameMapJsonUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: countersPortNameMapJsonUpdate},
				client.Sync{},
			},
		},
		{
			desc: "poll query for table COUNTERS_PORT_NAME_MAP with test_field delete",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS_PORT_NAME_MAP"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			prepares: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS_PORT_NAME_MAP",
					field:     "test_field",
					value:     "test_value",
				},
			},
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS_PORT_NAME_MAP",
					field:     "test_field",
					op:        "hdel",
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				// We are starting from the result data of "stream query for table with update of new field",
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: countersPortNameMapJsonUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: countersPortNameMapJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: countersPortNameMapJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: countersPortNameMapJson},
				client.Sync{},
			},
		},
		{
			desc: "poll query for COUNTERS/Ethernet68/SAI_PORT_STAT_PFC_7_RX_PKTS with field value change",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
					value:     "4", // being changed to 4 from 2
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "2"},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "4"},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "4"},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "4"},
				client.Sync{},
			},
		},
		{
			desc: "(use vendor alias) poll query for COUNTERS/[Ethernet68/1]/SAI_PORT_STAT_PFC_7_RX_PKTS with field value change",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet68/1", "SAI_PORT_STAT_PFC_7_RX_PKTS"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
					value:     "4", // being changed to 4 from 2
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "2"},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "4"},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "4"},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "4"},
				client.Sync{},
			},
		},
		{
			desc: "poll query for COUNTERS/Ethernet68/Pfcwd with field value change",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet68", "Pfcwd"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1500000000091e", // "Ethernet68:3": "oid:0x1500000000091e",
					delimitor: ":",
					field:     "PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED",
					value:     "1", // be changed to 1 from 0
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdPollUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdPollUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdPollUpdate},
				client.Sync{},
			},
		},
		{
			desc: "(use vendor alias) poll query for COUNTERS/[Ethernet68/1]/Pfcwd with field value change",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet68/1", "Pfcwd"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1500000000091e", // "Ethernet68:3": "oid:0x1500000000091e",
					delimitor: ":",
					field:     "PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED",
					value:     "1", // be changed to 1 from 0
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdAliasJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdAliasPollUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdAliasPollUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdAliasPollUpdate},
				client.Sync{},
			},
		},
		{
			desc: "poll query for table key Ethernet* with Ethernet68/SAI_PORT_STAT_PFC_7_RX_PKTS field value change",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet*"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
					value:     "4", // being changed to 4 from 2
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: countersEthernetWildcardJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: countersFieldUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: countersFieldUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: countersFieldUpdate},
				client.Sync{},
			},
		},
		{
			desc: "poll query for table key field Ethernet*/SAI_PORT_STAT_PFC_7_RX_PKTS with Ethernet68/SAI_PORT_STAT_PFC_7_RX_PKTS field value change",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1000000000039", // "Ethernet68": "oid:0x1000000000039",
					delimitor: ":",
					field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
					value:     "4", // being changed to 4 from 2
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: countersEthernetWildcardPfcJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: allPortPfcJsonUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: allPortPfcJsonUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: allPortPfcJsonUpdate},
				client.Sync{},
			},
		},
		{
			desc: "poll query for table key field Etherenet*/Pfcwd with Ethernet68:3/PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED field value change",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet*", "Pfcwd"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1500000000091e", // "Ethernet68:3": "oid:0x1500000000091e",
					delimitor: ":",
					field:     "PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED",
					value:     "1", // being changed to 1 from 0
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernetWildPfcwdJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernetWildPfcwdUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernetWildPfcwdUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernetWildPfcwdUpdate},
				client.Sync{},
			},
		},
		{
			desc: "poll query for COUNTERS/Ethernet*/Queues",
			poll: 1,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet*", "Queues"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Queues"}, TS: time.Unix(0, 200), Val: countersEthernetWildQueuesJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Queues"}, TS: time.Unix(0, 200), Val: countersEthernetWildQueuesJson},
				client.Sync{},
			},
		},
		{
			desc: "poll query for COUNTERS/Ethernet68/Queues with field value change",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet68", "Queues"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1500000000091c", // "Ethernet68:1": "oid:0x1500000000091c",
					delimitor: ":",
					field:     "SAI_QUEUE_STAT_DROPPED_PACKETS",
					value:     "4", // being changed to 0 from 4
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "Queues"}, TS: time.Unix(0, 200), Val: countersEthernet68QueuesJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "Queues"}, TS: time.Unix(0, 200), Val: countersEthernet68QueuesJsonUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "Queues"}, TS: time.Unix(0, 200), Val: countersEthernet68QueuesJsonUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "Queues"}, TS: time.Unix(0, 200), Val: countersEthernet68QueuesJsonUpdate},
				client.Sync{},
			},
		},
		{
			desc: "(use vendor alias) poll query for COUNTERS/Ethernet68/Queues with field value change",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet68/1", "Queues"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  "oid:0x1500000000091c", // "Ethernet68:1": "oid:0x1500000000091c",
					delimitor: ":",
					field:     "SAI_QUEUE_STAT_DROPPED_PACKETS",
					value:     "4", // being changed to 0 from 4
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "Queues"}, TS: time.Unix(0, 200), Val: countersEthernet68QueuesAliasJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "Queues"}, TS: time.Unix(0, 200), Val: countersEthernet68QueuesAliasJsonUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "Queues"}, TS: time.Unix(0, 200), Val: countersEthernet68QueuesAliasJsonUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "Queues"}, TS: time.Unix(0, 200), Val: countersEthernet68QueuesAliasJsonUpdate},
				client.Sync{},
			},
		},
		{
			desc:       "use invalid sample interval",
			q:          createCountersDbQuerySampleMode(t, 10*time.Millisecond, false, "COUNTERS", "Ethernet1"),
			updates:    []tablePathValue{},
			wantSubErr: fmt.Errorf("rpc error: code = InvalidArgument desc = invalid interval: 10ms. It cannot be less than %v", sdc.MinSampleInterval),
			wantNoti:   []client.Notification{},
		},
		{
			desc:              "sample stream query for table key Ethernet68 with new test_field field",
			q:                 createCountersDbQuerySampleMode(t, 0, false, "COUNTERS", "Ethernet68"),
			generateIntervals: true,
			updates: []tablePathValue{
				createCountersTableSetUpdate("oid:0x1000000000039", "test_field", "test_value"),
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68"}, TS: time.Unix(0, 200), Val: countersEthernet68Json},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68"}, TS: time.Unix(0, 200), Val: countersEthernet68JsonUpdate},
			},
		},
		{
			desc:              "sample stream query for COUNTERS/Ethernet68/SAI_PORT_STAT_PFC_7_RX_PKTS with 2 updates",
			q:                 createCountersDbQuerySampleMode(t, 0, false, "COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"),
			generateIntervals: true,
			updates: []tablePathValue{
				createCountersTableSetUpdate("oid:0x1000000000039", "SAI_PORT_STAT_PFC_7_RX_PKTS", "3"), // be changed to 3 from 2
				createIntervalTickerUpdate(), // no value change but imitate interval ticker
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "2"},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "3"},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: "3"},
			},
		},
		{
			desc:              "(use vendor alias) sample stream query for table key Ethernet68/1 with new test_field field",
			q:                 createCountersDbQuerySampleMode(t, 0, false, "COUNTERS", "Ethernet68/1"),
			generateIntervals: true,
			updates: []tablePathValue{
				createCountersTableSetUpdate("oid:0x1000000000039", "test_field", "test_value"),
				createIntervalTickerUpdate(), // no value change but imitate interval ticker
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1"}, TS: time.Unix(0, 200), Val: countersEthernet68Json},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1"}, TS: time.Unix(0, 200), Val: countersEthernet68JsonUpdate},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1"}, TS: time.Unix(0, 200), Val: countersEthernet68JsonUpdate},
			},
		},
		{
			desc:              "sample stream query for COUNTERS/Ethernet68/Pfcwd with update of field value",
			q:                 createCountersDbQuerySampleMode(t, 0, false, "COUNTERS", "Ethernet68", "Pfcwd"),
			generateIntervals: true,
			updates: []tablePathValue{
				createCountersTableSetUpdate("oid:0x1500000000091e", "PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED", "1"),
				createIntervalTickerUpdate(), // no value change but imitate interval ticker
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "Pfcwd"}, TS: time.Unix(0, 200), Val: mergeStrMaps(countersEthernet68PfcwdJson, countersEthernet68PfcwdJsonUpdate)},
				client.Update{Path: []string{"COUNTERS", "Ethernet68", "Pfcwd"}, TS: time.Unix(0, 200), Val: mergeStrMaps(countersEthernet68PfcwdJson, countersEthernet68PfcwdJsonUpdate)},
			},
		},
		{
			desc:              "(use vendor alias) sample stream query for COUNTERS/[Ethernet68/1]/Pfcwd with update of field value",
			q:                 createCountersDbQuerySampleMode(t, 0, false, "COUNTERS", "Ethernet68/1", "Pfcwd"),
			generateIntervals: true,
			updates: []tablePathValue{
				createCountersTableSetUpdate("oid:0x1500000000091e", "PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED", "1"),
				createCountersTableSetUpdate("oid:0x1500000000091e", "PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED", "0"), // change back to 0
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdAliasJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "Pfcwd"}, TS: time.Unix(0, 200), Val: mergeStrMaps(countersEthernet68PfcwdAliasJson, countersEthernet68PfcwdAliasJsonUpdate)},
				client.Update{Path: []string{"COUNTERS", "Ethernet68/1", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdAliasJson},
			},
		},
		{
			desc:              "sample stream query for table key Ethernet* with new test_field field on Ethernet68",
			q:                 createCountersDbQuerySampleMode(t, 0, false, "COUNTERS", "Ethernet*"),
			generateIntervals: true,
			updates: []tablePathValue{
				createCountersTableSetUpdate("oid:0x1000000000039", "test_field", "test_value"),
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: countersEthernetWildcardJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: mergeStrMaps(countersEthernetWildcardJson, countersEtherneWildcardJsonUpdate)},
			},
		},
		{
			desc:              "(updates only) sample stream query for table key Ethernet* with new test_field field on Ethernet68",
			q:                 createCountersDbQuerySampleMode(t, 0, true, "COUNTERS", "Ethernet*"),
			generateIntervals: true,
			updates: []tablePathValue{
				createIntervalTickerUpdate(), // no value change but imitate interval ticker
				createCountersTableSetUpdate("oid:0x1000000000039", "test_field", "test_value"),
				createIntervalTickerUpdate(),
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: countersEthernetWildcardJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: map[string]interface{}{}}, //empty update
				client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: countersEtherneWildcardJsonUpdate},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: map[string]interface{}{}}, //empty update
			},
		},
		/*
			// deletion of field from table is not supported. It'd keep sending the last value before the deletion.
				{
					desc:              "sample stream query for table key Ethernet* with new test_field field deleted from Ethernet68",
					q:                 createCountersDbQuerySampleMode(t, 0, false, "COUNTERS", "Ethernet*"),
					generateIntervals: true,
					updates: []tablePathValue{
						createCountersTableSetUpdate("oid:0x1000000000039", "test_field", "test_value"),
						createCountersTableDeleteUpdate("oid:0x1000000000039", "test_field"),
					},
					wantNoti: []client.Notification{
						client.Connected{},
						client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: countersEthernetWildcardJson},
						client.Sync{},
						client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: mergeStrMaps(countersEthernetWildcardJson, countersEtherneWildcardJsonUpdate)},
						client.Update{Path: []string{"COUNTERS", "Ethernet*"}, TS: time.Unix(0, 200), Val: countersEthernetWildcardJson}, //go back to original after deletion of test_field
					},
				},
		*/
		{
			desc:              "sample stream query for table key Ethernet*/SAI_PORT_STAT_PFC_7_RX_PKTS with field value update",
			q:                 createCountersDbQuerySampleMode(t, 0, false, "COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"),
			generateIntervals: true,
			updates: []tablePathValue{
				createCountersTableSetUpdate("oid:0x1000000000039", "SAI_PORT_STAT_PFC_7_RX_PKTS", "4"),
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: countersEthernetWildcardPfcJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: mergeStrMaps(countersEthernetWildcardPfcJson, singlePortPfcJsonUpdate)},
			},
		},
		{
			desc:              "sample stream query for table key Ethernet*/Pfcwd with field value update",
			generateIntervals: true,
			q:                 createCountersDbQuerySampleMode(t, 0, false, "COUNTERS", "Ethernet*", "Pfcwd"),
			updates: []tablePathValue{
				createCountersTableSetUpdate("oid:0x1500000000091e", "PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED", "1"),
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernetWildPfcwdJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Pfcwd"}, TS: time.Unix(0, 200), Val: mergeStrMaps(countersEthernetWildPfcwdJson, countersEthernet68PfcwdAliasJsonUpdate)},
			},
		},
		{
			desc:              "(update only) sample stream query for table key Ethernet*/Pfcwd with field value update",
			generateIntervals: true,
			q:                 createCountersDbQuerySampleMode(t, 0, true, "COUNTERS", "Ethernet*", "Pfcwd"),
			updates: []tablePathValue{
				createIntervalTickerUpdate(),
				createCountersTableSetUpdate("oid:0x1500000000091e", "PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED", "1"),
				createIntervalTickerUpdate(),
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernetWildPfcwdJson},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Pfcwd"}, TS: time.Unix(0, 200), Val: map[string]interface{}{}}, //empty update
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Pfcwd"}, TS: time.Unix(0, 200), Val: countersEthernet68PfcwdAliasJsonUpdate},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Pfcwd"}, TS: time.Unix(0, 200), Val: map[string]interface{}{}}, //empty update
			},
		},
	}

	sdc.NeedMock = true
	rclient := getRedisClient(t, namespace)
	defer rclient.Close()
	var wg sync.WaitGroup
	for _, tt := range tests {
		wg.Add(1)
		prepareDb(t, namespace)
		// Extra db preparation for this test case
		for _, prepare := range tt.prepares {
			switch prepare.op {
			case "hdel":
				rclient.HDel(prepare.tableName+prepare.delimitor+prepare.tableKey, prepare.field)
			default:
				rclient.HSet(prepare.tableName+prepare.delimitor+prepare.tableKey, prepare.field, prepare.value)
			}
		}

		sdcIntervalTicker := sdc.IntervalTicker
		intervalTickerChan := make(chan time.Time)
		if tt.generateIntervals {
			sdc.IntervalTicker = func(interval time.Duration) <-chan time.Time {
				return intervalTickerChan
			}
		}

		time.Sleep(time.Millisecond * 1000)
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			c := client.New()
			defer c.Close()
			var gotNoti []client.Notification
			var mutexGotNoti sync.Mutex
			q.NotificationHandler = func(n client.Notification) error {
				mutexGotNoti.Lock()
				if nn, ok := n.(client.Update); ok {
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				} else {
					gotNoti = append(gotNoti, n)
				}
				mutexGotNoti.Unlock()
				return nil
			}
			go func(t2 TestExec) {
				defer wg.Done()
				err := c.Subscribe(context.Background(), q)
				if t2.wantSubErr != nil && t2.wantSubErr.Error() != err.Error() {
					t.Errorf("c.Subscribe expected %v, got %v", t2.wantSubErr, err)
				}
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
			}(tt)
			// wait for half second for subscribeRequest to sync
			time.Sleep(time.Millisecond * 500)
			for _, update := range tt.updates {
				switch update.op {
				case "hdel":
					rclient.HDel(update.tableName+update.delimitor+update.tableKey, update.field)
				case "intervaltick":
					// This is not a DB update but a request to trigger sample interval
				default:
					rclient.HSet(update.tableName+update.delimitor+update.tableKey, update.field, update.value)
				}

				time.Sleep(time.Millisecond * 1000)

				if tt.generateIntervals {
					intervalTickerChan <- time.Now()
				}
			}
			// wait for half second for change to sync
			time.Sleep(time.Millisecond * 500)

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
			mutexGotNoti.Lock()
			defer mutexGotNoti.Unlock()
			if diff := pretty.Compare(tt.wantNoti, gotNoti); diff != "" {
				t.Log("\n Want: \n", tt.wantNoti)
				t.Log("\n Got : \n", gotNoti)
				t.Errorf("unexpected updates:\n%s", diff)
			}
		})
		if tt.generateIntervals {
			sdc.SetIntervalTicker(sdcIntervalTicker)
		}
	}
	sdc.NeedMock = false
	wg.Wait()
}

func TestGnmiSubscribe(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)

	ns, _ := sdcfg.GetDbDefaultNamespace()
	runTestSubscribe(t, ns)

	s.Stop()
}
func TestGnmiSubscribeMultiNs(t *testing.T) {
	sdcfg.Init()
	err := test_utils.SetupMultiNamespace()
	if err != nil {
		t.Fatalf("error Setting up MultiNamespace files with err %T", err)
	}

	/* https://www.gopherguides.com/articles/test-cleanup-in-go-1-14*/
	t.Cleanup(func() {
		if err := test_utils.CleanUpMultiNamespace(); err != nil {
			t.Fatalf("error Cleaning up MultiNamespace files with err %T", err)

		}
	})

	s := createServer(t, 8081)
	go runServer(t, s)

	runTestSubscribe(t, test_utils.GetMultiNsNamespace())

	s.Stop()
}

func TestCapabilities(t *testing.T) {
	//t.Log("Start server")
	s := createServer(t, 8085)
	go runServer(t, s)

	// prepareDb(t)

	//t.Log("Start gNMI client")
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	//targetAddr := "30.57.185.38:8080"
	targetAddr := "127.0.0.1:8085"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var req pb.CapabilityRequest
	resp, err := gClient.Capabilities(ctx, &req)
	if err != nil {
		t.Fatalf("Failed to get Capabilities")
	}
	if len(resp.SupportedModels) == 0 {
		t.Fatalf("No Supported Models found!")
	}

}

func TestGNOI(t *testing.T) {
	if !ENABLE_TRANSLIB_WRITE {
		t.Skip("skipping test in read-only mode.")
	}
	s := createServer(t, 8086)
	go runServer(t, s)
	defer s.Stop()

	// prepareDb(t)

	//t.Log("Start gNMI client")
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	//targetAddr := "30.57.185.38:8080"
	targetAddr := "127.0.0.1:8086"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	t.Run("SystemTime", func(t *testing.T) {
		sc := gnoi_system_pb.NewSystemClient(conn)
		resp, err := sc.Time(ctx, new(gnoi_system_pb.TimeRequest))
		if err != nil {
			t.Fatal(err.Error())
		}
		ctime := uint64(time.Now().UnixNano())
		if ctime-resp.Time < 0 || ctime-resp.Time > 1e9 {
			t.Fatalf("Invalid System Time %d", resp.Time)
		}
	})

	t.Run("SonicShowTechsupport", func(t *testing.T) {
		t.Skip("Not supported yet")
		sc := sgpb.NewSonicServiceClient(conn)
		rtime := time.Now().AddDate(0, -1, 0)
		req := &sgpb.TechsupportRequest{
			Input: &sgpb.TechsupportRequest_Input{
				Date: rtime.Format("20060102_150405"),
			},
		}
		resp, err := sc.ShowTechsupport(ctx, req)
		if err != nil {
			t.Fatal(err.Error())
		}

		if len(resp.Output.OutputFilename) == 0 {
			t.Fatalf("Invalid Output Filename: %s", resp.Output.OutputFilename)
		}
	})

	t.Run("FileStatSuccess", func(t *testing.T) {
		mockClient := &ssc.DbusClient{}
		expectedResult := map[string]string{
			"last_modified": "1609459200000000000",
			"permissions":   "644",
			"size":          "1024",
			"umask":         "o022",
		}
		mock := gomonkey.ApplyMethod(reflect.TypeOf(mockClient), "GetFileStat", func(_ *ssc.DbusClient, path string) (map[string]string, error) {
			return expectedResult, nil
		})
		defer mock.Reset()

		// Prepare context and request
		ctx := context.Background()
		req := &gnoi_file_pb.StatRequest{Path: "/etc/sonic/config_db.json"}
		fc := gnoi_file_pb.NewFileClient(conn)

		resp, err := fc.Stat(ctx, req)
		if err != nil {
			t.Fatalf("FileStat failed: %v", err)
		}
		// Validate the response
		if len(resp.Stats) == 0 {
			t.Fatalf("Expected at least one StatInfo in response")
		}
	
		statInfo := resp.Stats[0]

		if statInfo.LastModified != 1609459200000000000 {
			t.Errorf("Expected last_modified %d but got %d", 1609459200000000000, statInfo.LastModified)
		}
		if statInfo.Permissions != 420 {
			t.Errorf("Expected permissions 420 but got %d", statInfo.Permissions)
		}
		if statInfo.Size != 1024 {
			t.Errorf("Expected size 1024 but got %d", statInfo.Size)
		}
		if statInfo.Umask != 18 {
			t.Errorf("Expected umask 18 but got %d", statInfo.Umask)
		}
	})

	t.Run("FileStatFailure", func(t *testing.T) {
		mockClient := &ssc.DbusClient{}
		expectedError := fmt.Errorf("failed to get file stats")
		
		mock := gomonkey.ApplyMethod(reflect.TypeOf(mockClient), "GetFileStat", func(_ *ssc.DbusClient, path string) (map[string]string, error) {
			return nil, expectedError
		})
		defer mock.Reset()

		// Prepare context and request
		ctx := context.Background()
		req := &gnoi_file_pb.StatRequest{Path: "/etc/sonic/config_db.json"}
		fc := gnoi_file_pb.NewFileClient(conn)

		resp, err := fc.Stat(ctx, req)
		if err == nil {
			t.Fatalf("Expected error but got none")
		}
		if resp != nil {
			t.Fatalf("Expected nil response but got: %v", resp)
		}
	
		if !strings.Contains(err.Error(), expectedError.Error()) {
			t.Errorf("Expected error to contain '%v' but got '%v'", expectedError, err)
		}	
	})

	type configData struct {
		source      string
		destination string
		overwrite   bool
		status      int32
	}

	var cfg_data = []configData{
		configData{"running-configuration", "startup-configuration", false, 0},
		configData{"running-configuration", "file://etc/sonic/config_db_test.json", false, 0},
		configData{"file://etc/sonic/config_db_test.json", "running-configuration", false, 0},
		configData{"startup-configuration", "running-configuration", false, 0},
		configData{"file://etc/sonic/config_db_3.json", "running-configuration", false, 1}}

	for _, v := range cfg_data {

		t.Run("SonicCopyConfig", func(t *testing.T) {
			t.Skip("Not supported yet")
			sc := sgpb.NewSonicServiceClient(conn)
			req := &sgpb.CopyConfigRequest{
				Input: &sgpb.CopyConfigRequest_Input{
					Source:      v.source,
					Destination: v.destination,
					Overwrite:   v.overwrite,
				},
			}
			t.Logf("source: %s dest: %s overwrite: %t", v.source, v.destination, v.overwrite)
			resp, err := sc.CopyConfig(ctx, req)
			if err != nil {
				t.Fatal(err.Error())
			}
			if resp.Output.Status != v.status {
				t.Fatalf("Copy Failed: status %d,  %s", resp.Output.Status, resp.Output.StatusDetail)
			}
		})
	}
}

func TestBundleVersion(t *testing.T) {
	s := createServer(t, 8087)
	go runServer(t, s)
	defer s.Stop()

	// prepareDb(t)

	//t.Log("Start gNMI client")
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	//targetAddr := "30.57.185.38:8080"
	targetAddr := "127.0.0.1:8087"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Run("Invalid Bundle Version Format", func(t *testing.T) {
		var pbPath *pb.Path
		pbPath, err := xpath.ToGNMIPath("openconfig-interfaces:interfaces/interface[name=Ethernet0]/config")
		prefix := pb.Path{Target: "OC-YANG"}
		if err != nil {
			t.Fatalf("error in unmarshaling path: %v", err)
		}
		bundleVersion := "50.0.0"
		bv, err := proto.Marshal(&spb.BundleVersion{
			Version: bundleVersion,
		})
		if err != nil {
			t.Fatalf("%v", err)
		}
		req := &pb.GetRequest{
			Path:     []*pb.Path{pbPath},
			Prefix:   &prefix,
			Encoding: pb.Encoding_JSON_IETF,
		}
		req.Extension = append(req.Extension, &ext_pb.Extension{
			Ext: &ext_pb.Extension_RegisteredExt{
				RegisteredExt: &ext_pb.RegisteredExtension{
					Id:  spb.BUNDLE_VERSION_EXT,
					Msg: bv,
				}}})

		_, err = gClient.Get(ctx, req)
		gotRetStatus, ok := status.FromError(err)
		if !ok {
			t.Fatal("got a non-grpc error from grpc call")
		}
		if gotRetStatus.Code() != codes.NotFound {
			t.Log("err: ", err)
			t.Fatalf("got return code %v, want %v", gotRetStatus.Code(), codes.OK)
		}
	})
}

/*
func TestBulkSet(t *testing.T) {
	s := createServer(t, 8088)
	go runServer(t, s)
	defer s.Stop()

	prepareDbTranslib(t)

	//t.Log("Start gNMI client")
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	//targetAddr := "30.57.185.38:8080"
	targetAddr := "127.0.0.1:8088"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("Set Multiple mtu", func(t *testing.T) {
		req := &pb.SetRequest{
			Prefix: &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
			Update: []*pb.Update{
				newPbUpdate("interface[name=Ethernet0]/config/mtu", `{"mtu": 9104}`),
				newPbUpdate("interface[name=Ethernet4]/config/mtu", `{"mtu": 9105}`),
			}}
		runTestSetRaw(t, ctx, gClient, req, codes.OK)
	})

	t.Run("Update and Replace", func(t *testing.T) {
		aclKeys := `"name": "A002", "type": "ACL_IPV4"`
		req := &pb.SetRequest{
			Replace: []*pb.Update{
				newPbUpdate(
					"openconfig-acl:acl/acl-sets/acl-set",
					`{"acl-set": [{`+aclKeys+`, "config":{`+aclKeys+`}}]}`),
			},
			Update: []*pb.Update{
				newPbUpdate(
					"interfaces/interface[name=Ethernet0]/config/description",
					`{"description": "Bulk update 1"}`),
				newPbUpdate(
					"openconfig-interfaces:interfaces/interface[name=Ethernet4]/config/description",
					`{"description": "Bulk update 2"}`),
			}}
		runTestSetRaw(t, ctx, gClient, req, codes.OK)
	})

	aclPath1, _ := ygot.StringToStructuredPath("/acl/acl-sets")
	aclPath2, _ := ygot.StringToStructuredPath("/openconfig-acl:acl/acl-sets")

	t.Run("Multiple deletes", func(t *testing.T) {
		req := &pb.SetRequest{
			Delete: []*pb.Path{aclPath1, aclPath2},
		}
		runTestSetRaw(t, ctx, gClient, req, codes.OK)
	})

	t.Run("Invalid Update Path", func(t *testing.T) {
		req := &pb.SetRequest{
			Delete: []*pb.Path{aclPath1, aclPath2},
			Update: []*pb.Update{
				newPbUpdate("interface[name=Ethernet0]/config/mtu", `{"mtu": 9104}`),
			}}
		runTestSetRaw(t, ctx, gClient, req, codes.Unknown)
	})

	t.Run("Invalid Replace Path", func(t *testing.T) {
		req := &pb.SetRequest{
			Delete: []*pb.Path{aclPath1, aclPath2},
			Replace: []*pb.Update{
				newPbUpdate("interface[name=Ethernet0]/config/mtu", `{"mtu": 9104}`),
			}}
		runTestSetRaw(t, ctx, gClient, req, codes.Unknown)
	})

	t.Run("Invalid Delete Path", func(t *testing.T) {
		req := &pb.SetRequest{
			Prefix: &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
			Delete: []*pb.Path{aclPath1, aclPath2},
		}
		runTestSetRaw(t, ctx, gClient, req, codes.Unknown)
	})

}*/

func newPbUpdate(path, value string) *pb.Update {
	p, _ := ygot.StringToStructuredPath(path)
	v := &pb.TypedValue_JsonIetfVal{JsonIetfVal: extractJSON(value)}
	return &pb.Update{
		Path: p,
		Val:  &pb.TypedValue{Value: v},
	}
}

type loginCreds struct {
	Username, Password string
}

func (c *loginCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{
		"username": c.Username,
		"password": c.Password,
	}, nil
}

func (c *loginCreds) RequireTransportSecurity() bool {
	return true
}

func TestAuthCapabilities(t *testing.T) {
	mock1 := gomonkey.ApplyFunc(UserPwAuth, func(username string, passwd string) (bool, error) {
		return true, nil
	})
	defer mock1.Reset()

	s := createAuthServer(t, 8089)
	go runServer(t, s)
	defer s.Stop()

	currentUser, _ := user.Current()
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	cred := &loginCreds{Username: currentUser.Username, Password: "dummy"}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)), grpc.WithPerRPCCredentials(cred)}

	targetAddr := "127.0.0.1:8089"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var req pb.CapabilityRequest
	resp, err := gClient.Capabilities(ctx, &req)
	if err != nil {
		t.Fatalf("Failed to get Capabilities: %v", err)
	}
	if len(resp.SupportedModels) == 0 {
		t.Fatalf("No Supported Models found!")
	}
}

func TestTableKeyOnDeletion(t *testing.T) {
	s := createKeepAliveServer(t, 8081)
	go runServer(t, s)
	defer s.Stop()

	fileName := "../testdata/NEIGH_STATE_TABLE_MAP.txt"
	neighStateTableByte, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var neighStateTableJson interface{}
	json.Unmarshal(neighStateTableByte, &neighStateTableJson)

	fileName = "../testdata/NEIGH_STATE_TABLE_key_deletion_57.txt"
	neighStateTableDeletedByte57, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var neighStateTableDeletedJson57 interface{}
	json.Unmarshal(neighStateTableDeletedByte57, &neighStateTableDeletedJson57)

	fileName = "../testdata/NEIGH_STATE_TABLE_MAP_2.txt"
	neighStateTableByteTwo, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var neighStateTableJsonTwo interface{}
	json.Unmarshal(neighStateTableByteTwo, &neighStateTableJsonTwo)

	fileName = "../testdata/NEIGH_STATE_TABLE_key_deletion_59.txt"
	neighStateTableDeletedByte59, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var neighStateTableDeletedJson59 interface{}
	json.Unmarshal(neighStateTableDeletedByte59, &neighStateTableDeletedJson59)

	fileName = "../testdata/NEIGH_STATE_TABLE_key_deletion_61.txt"
	neighStateTableDeletedByte61, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	var neighStateTableDeletedJson61 interface{}
	json.Unmarshal(neighStateTableDeletedByte61, &neighStateTableDeletedJson61)

	namespace, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 6, namespace)
	defer rclient.Close()
	prepareStateDb(t, namespace)

	tests := []struct {
		desc     string
		q        client.Query
		wantNoti []client.Notification
		paths    []string
	}{
		{
			desc: "Testing deletion of NEIGH_STATE_TABLE:10.0.0.57",
			q:    createStateDbQueryOnChangeMode(t, "NEIGH_STATE_TABLE"),
			wantNoti: []client.Notification{
				client.Update{Path: []string{"NEIGH_STATE_TABLE"}, TS: time.Unix(0, 200), Val: neighStateTableJson},
				client.Update{Path: []string{"NEIGH_STATE_TABLE"}, TS: time.Unix(0, 200), Val: neighStateTableDeletedJson57},
			},
			paths: []string{
				"NEIGH_STATE_TABLE|10.0.0.57",
			},
		},
		{
			desc: "Testing deletion of NEIGH_STATE_TABLE:10.0.0.59 and NEIGH_STATE_TABLE 10.0.0.61",
			q:    createStateDbQueryOnChangeMode(t, "NEIGH_STATE_TABLE"),
			wantNoti: []client.Notification{
				client.Update{Path: []string{"NEIGH_STATE_TABLE"}, TS: time.Unix(0, 200), Val: neighStateTableJsonTwo},
				client.Update{Path: []string{"NEIGH_STATE_TABLE"}, TS: time.Unix(0, 200), Val: neighStateTableDeletedJson59},
				client.Update{Path: []string{"NEIGH_STATE_TABLE"}, TS: time.Unix(0, 200), Val: neighStateTableDeletedJson61},
			},
			paths: []string{
				"NEIGH_STATE_TABLE|10.0.0.59",
				"NEIGH_STATE_TABLE|10.0.0.61",
			},
		},
	}

	var mutexNoti sync.RWMutex
	var mutexPaths sync.Mutex
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			c := client.New()
			defer c.Close()
			var gotNoti []client.Notification
			q.NotificationHandler = func(n client.Notification) error {
				if nn, ok := n.(client.Update); ok {
					nn.TS = time.Unix(0, 200)
					mutexNoti.Lock()
					currentNoti := gotNoti
					mutexNoti.Unlock()

					mutexNoti.RLock()
					gotNoti = append(currentNoti, nn)
					mutexNoti.RUnlock()
				}
				return nil
			}

			go func() {
				c.Subscribe(context.Background(), q)
			}()

			time.Sleep(time.Millisecond * 500) // half a second for subscribe request to sync

			mutexPaths.Lock()
			paths := tt.paths
			mutexPaths.Unlock()

			rclient.Del(paths...)

			time.Sleep(time.Millisecond * 1500)

			mutexNoti.Lock()
			if diff := pretty.Compare(tt.wantNoti, gotNoti); diff != "" {
				t.Log("\n Want: \n", tt.wantNoti)
				t.Log("\n Got : \n", gotNoti)
				t.Errorf("unexpected updates:\n%s", diff)
			}
			mutexNoti.Unlock()
		})
	}
}

func TestCPUUtilization(t *testing.T) {
	mock := gomonkey.ApplyFunc(sdc.PollStats, func() {
		var i uint64
		for i = 0; i < 3000; i++ {
			sdc.WriteStatsToBuffer(&linuxproc.Stat{})
		}
	})

	defer mock.Reset()
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.Stop()

	tests := []struct {
		desc string
		q    client.Query
		want []client.Notification
		poll int
	}{
		{
			desc: "poll query for CPU Utilization",
			poll: 10,
			q: client.Query{
				Target:  "OTHERS",
				Type:    client.Poll,
				Queries: []client.Path{{"platform", "cpu"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			want: []client.Notification{
				client.Connected{},
				client.Sync{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			c := client.New()
			var gotNoti []client.Notification
			q.NotificationHandler = func(n client.Notification) error {
				if nn, ok := n.(client.Update); ok {
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				} else {
					gotNoti = append(gotNoti, n)
				}
				return nil
			}

			wg := new(sync.WaitGroup)
			wg.Add(1)

			go func() {
				defer wg.Done()
				if err := c.Subscribe(context.Background(), q); err != nil {
					t.Errorf("c.Subscribe(): got error %v, expected nil", err)
				}
			}()

			wg.Wait()

			for i := 0; i < tt.poll; i++ {
				if err := c.Poll(); err != nil {
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				}
			}

			if len(gotNoti) == 0 {
				t.Errorf("expected non zero notifications")
			}

			c.Close()
		})
	}
}

func TestClientConnections(t *testing.T) {
	s := createRejectServer(t, 8081)
	go runServer(t, s)
	defer s.Stop()

	tests := []struct {
		desc string
		q    client.Query
		want []client.Notification
		poll int
	}{
		{
			desc: "Reject OTHERS/proc/uptime",
			poll: 10,
			q: client.Query{
				Target:  "OTHERS",
				Type:    client.Poll,
				Queries: []client.Path{{"proc", "uptime"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			want: []client.Notification{
				client.Connected{},
				client.Sync{},
			},
		},
		{
			desc: "Reject COUNTERS/Ethernet*",
			poll: 10,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet*"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			want: []client.Notification{
				client.Connected{},
				client.Sync{},
			},
		},
		{
			desc: "Reject COUNTERS/Ethernet68",
			poll: 10,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet68"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			want: []client.Notification{
				client.Connected{},
				client.Sync{},
			},
		},
	}

	var clients []*cacheclient.CacheClient

	for i, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			var gotNoti []client.Notification
			q.NotificationHandler = func(n client.Notification) error {
				if nn, ok := n.(client.Update); ok {
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				} else {
					gotNoti = append(gotNoti, n)
				}
				return nil
			}

			wg := new(sync.WaitGroup)
			wg.Add(1)

			go func() {
				defer wg.Done()
				c := client.New()
				clients = append(clients, c)
				err := c.Subscribe(context.Background(), q)
				if err == nil && i == len(tests)-1 { // reject third
					t.Errorf("Expecting rejection message as no connections are allowed")
				}
				if err != nil && i < len(tests)-1 { // accept first two
					t.Errorf("Expecting accepts for first two connections")
				}
			}()

			wg.Wait()
		})
	}

	for _, cacheClient := range clients {
		cacheClient.Close()
	}
}

func TestConnectionDataSet(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	defer s.ForceStop()

	tests := []struct {
		desc string
		q    client.Query
		want []client.Notification
		poll int
	}{
		{
			desc: "poll query for COUNTERS/Ethernet*",
			poll: 10,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet*"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			want: []client.Notification{
				client.Connected{},
				client.Sync{},
			},
		},
	}
	namespace, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 6, namespace)
	defer rclient.Close()

	for _, tt := range tests {
		prepareStateDb(t, namespace)
		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{"127.0.0.1:8081"}
			c := client.New()

			wg := new(sync.WaitGroup)
			wg.Add(1)

			go func() {
				defer wg.Done()
				if err := c.Subscribe(context.Background(), q); err != nil {
					t.Errorf("c.Subscribe(): got error %v, expected nil", err)
				}
			}()

			wg.Wait()

			resultMap, err := rclient.HGetAll("TELEMETRY_CONNECTIONS").Result()

			if resultMap == nil {
				t.Errorf("result Map is nil, expected non nil, err: %v", err)
			}
			if len(resultMap) != 1 {
				t.Errorf("result for TELEMETRY_CONNECTIONS should be 1")
			}

			for key, _ := range resultMap {
				if !strings.Contains(key, "COUNTERS_DB|COUNTERS|Ethernet*") {
					t.Errorf("key is expected to contain correct query, received: %s", key)
				}
			}

			c.Close()
		})
	}
}

func TestConnectionsKeepAlive(t *testing.T) {
	s := createKeepAliveServer(t, 8081)
	go runServer(t, s)
	defer s.Stop()

	tests := []struct {
		desc    string
		q       client.Query
		want    []client.Notification
		poll    int
	}{
		{
			desc: "Testing KeepAlive with goroutine count",
			poll: 3,
			q: client.Query{
				Target: "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet*"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			want: []client.Notification{
				client.Connected{},
				client.Sync{},
			},
		},
	}
	for _, tt := range(tests) {
		var clients []*cacheclient.CacheClient
		for i := 0; i < 5; i++ {
			t.Run(tt.desc, func(t *testing.T) {
				q := tt.q
				q.Addrs = []string{"127.0.0.1:8081"}
				c := client.New()
				clients = append(clients, c)
				wg := new(sync.WaitGroup)
				wg.Add(1)

				go func() {
					defer wg.Done()
					if err := c.Subscribe(context.Background(), q); err != nil {
						t.Errorf("c.Subscribe(): got error %v, expected nil", err)
					}
				}()

				wg.Wait()
				after_subscribe := runtime.NumGoroutine()
				t.Logf("Num go routines after client subscribe: %d", after_subscribe)
				time.Sleep(10 * time.Second)
				after_sleep := runtime.NumGoroutine()
				t.Logf("Num go routines after sleep, should be less, as keepalive should close idle connections: %d", after_sleep)
				if after_sleep > after_subscribe {
					t.Errorf("Expecting goroutine after sleep to be less than or equal to after subscribe, after_subscribe: %d, after_sleep: %d", after_subscribe, after_sleep)
				}
			})
		}
		for _, cacheClient := range(clients) {
			cacheClient.Close()
		}
	}
}

func TestClient(t *testing.T) {
	var mutexDeInit sync.RWMutex
	var mutexHB sync.RWMutex
	var mutexIdx sync.RWMutex

	// sonic-host:device-test-event is a test event.
	// Events client will drop it on floor.
	events := []sdc.Evt_rcvd{
		{"test0", 7, 777},
		{"test1", 6, 677},
		{"{\"sonic-host:device-test-event\"", 5, 577},
		{"test2", 5, 577},
		{"test3", 4, 477},
	}

	HEARTBEAT_SET := 5
	heartbeat := 0
	event_index := 0
	rcv_timeout := sdc.SUBSCRIBER_TIMEOUT
	deinit_done := false

	mock1 := gomonkey.ApplyFunc(sdc.C_init_subs, func(use_cache bool) unsafe.Pointer {
		return nil
	})
	defer mock1.Reset()

	mock2 := gomonkey.ApplyFunc(sdc.C_recv_evt, func(h unsafe.Pointer) (int, sdc.Evt_rcvd) {
		rc := (int)(0)
		var evt sdc.Evt_rcvd
		mutexIdx.Lock()
		current_index := event_index
		mutexIdx.Unlock()
		if current_index < len(events) {
			evt = events[current_index]
			mutexIdx.RLock()
			event_index = current_index + 1
			mutexIdx.RUnlock()
		} else {
			time.Sleep(time.Millisecond * time.Duration(rcv_timeout))
			rc = -1
		}
		return rc, evt
	})
	defer mock2.Reset()

	mock3 := gomonkey.ApplyFunc(sdc.Set_heartbeat, func(val int) {
		mutexHB.RLock()
		heartbeat = val
		mutexHB.RUnlock()
	})
	defer mock3.Reset()

	mock4 := gomonkey.ApplyFunc(sdc.C_deinit_subs, func(h unsafe.Pointer) {
		mutexDeInit.RLock()
		deinit_done = true
		mutexDeInit.RUnlock()
	})
	defer mock4.Reset()

	mock5 := gomonkey.ApplyMethod(reflect.TypeOf(&queue.PriorityQueue{}), "Put", func(pq *queue.PriorityQueue, item ...queue.Item) error {
		return fmt.Errorf("Queue error")
	})
	defer mock5.Reset()

	mock6 := gomonkey.ApplyMethod(reflect.TypeOf(&queue.PriorityQueue{}), "Len", func(pq *queue.PriorityQueue) int {
		return 150000 // Max size for pending events in PQ is 102400
	})
	defer mock6.Reset()

	s := createServer(t, 8081)
	go runServer(t, s)

	qstr := fmt.Sprintf("all[heartbeat=%d]", HEARTBEAT_SET)
	q := createEventsQuery(t, qstr)
	q.Addrs = []string{"127.0.0.1:8081"}

	tests := []struct {
		desc     string
		pub_data []string
		wantErr  bool
		wantNoti []client.Notification
		pause    int
		poll     int
	}{
		{
			desc: "dropped event",
			poll: 3,
		},
		{
			desc: "queue error",
			poll: 3,
		},
		{
			desc: "base client create",
			poll: 3,
		},
	}

	sdc.C_init_subs(true)

	var mutexNoti sync.RWMutex

	for testNum, tt := range tests {
		mutexHB.RLock()
		heartbeat = 0
		mutexHB.RUnlock()

		mutexIdx.RLock()
		event_index = 0
		mutexIdx.RUnlock()

		mutexDeInit.RLock()
		deinit_done = false
		mutexDeInit.RUnlock()

		t.Run(tt.desc, func(t *testing.T) {
			c := client.New()
			defer c.Close()

			var gotNoti []string
			q.NotificationHandler = func(n client.Notification) error {
				if nn, ok := n.(client.Update); ok {
					nn.TS = time.Unix(0, 200)
					str := fmt.Sprintf("%v", nn.Val)

					mutexNoti.Lock()
					currentNoti := gotNoti
					mutexNoti.Unlock()

					mutexNoti.RLock()
					gotNoti = append(currentNoti, str)
					mutexNoti.RUnlock()
				}
				return nil
			}

			go func() {
				c.Subscribe(context.Background(), q)
			}()

			// wait for half second for subscribeRequest to sync
			// and to receive events via notification handler.

			time.Sleep(time.Millisecond * 2000)

			if testNum > 1 {
				mutexNoti.Lock()
				// -1 to discount test event, which receiver would drop.
				if (len(events) - 1) != len(gotNoti) {
					t.Errorf("noti[%d] != events[%d]", len(gotNoti), len(events)-1)
				}

				mutexHB.Lock()
				if heartbeat != HEARTBEAT_SET {
					t.Errorf("Heartbeat is not set %d != expected:%d", heartbeat, HEARTBEAT_SET)
				}
				mutexHB.Unlock()

				fmt.Printf("DONE: Expect events:%d - 1 gotNoti=%d\n", len(events), len(gotNoti))
				mutexNoti.Unlock()
			}
		})

		if testNum == 0 {
			mock6.Reset()
		}

		if testNum == 1 {
			mock5.Reset()
		}
		time.Sleep(time.Millisecond * 1000)

		mutexDeInit.Lock()
		if deinit_done == false {
			t.Errorf("Events client deinit *NOT* called.")
		}
		mutexDeInit.Unlock()
		// t.Log("END of a TEST")
	}

	s.Stop()
}

func TestTableData2MsiUseKey(t *testing.T) {
	tblPath := sdc.CreateTablePath("STATE_DB", "NEIGH_STATE_TABLE", "|", "10.0.0.57")
	newMsi := make(map[string]interface{})
	sdc.TableData2Msi(&tblPath, true, nil, &newMsi)
	newMsiData, _ := json.MarshalIndent(newMsi, "", "  ")
	t.Logf(string(newMsiData))
	expectedMsi := map[string]interface{}{
		"10.0.0.57": map[string]interface{}{
			"peerType": "e-BGP",
			"state":    "Established",
		},
	}
	expectedMsiData, _ := json.MarshalIndent(expectedMsi, "", "  ")
	t.Logf(string(expectedMsiData))

	if !reflect.DeepEqual(newMsi, expectedMsi) {
		t.Errorf("Msi data does not match for use key = true")
	}
}

func TestRecoverFromJSONSerializationPanic(t *testing.T) {
	panicMarshal := func(v interface{}) ([]byte, error) {
		panic("json.Marshal panics and is unable to serialize JSON")
	}
	mock := gomonkey.ApplyFunc(json.Marshal, panicMarshal)
	defer mock.Reset()

	tblPath := sdc.CreateTablePath("STATE_DB", "NEIGH_STATE_TABLE", "|", "10.0.0.57")
	msi := make(map[string]interface{})
	sdc.TableData2Msi(&tblPath, true, nil, &msi)

	typedValue, err := sdc.Msi2TypedValue(msi)
	if typedValue != nil && err != nil {
		t.Errorf("Test should recover from panic and have nil TypedValue/Error after attempting JSON serialization")
	}

}

func TestGnmiSetBatch(t *testing.T) {
	mockCode :=
		`
print('No Yang validation for test mode...')
print('%s')
`
	mock1 := gomonkey.ApplyGlobalVar(&sdc.PyCodeForYang, mockCode)
	defer mock1.Reset()

	sdcfg.Init()
	s := createServer(t, 8090)
	go runServer(t, s)

	prepareDbTranslib(t)

	//t.Log("Start gNMI client")
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	targetAddr := "127.0.0.1:8090"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var emptyRespVal interface{}

	tds := []struct {
		desc          string
		pathTarget    string
		textPbPath    string
		wantRetCode   codes.Code
		wantRespVal   interface{}
		attributeData string
		operation     op_t
		valTest       bool
	}{
		{
			desc:       "Set APPL_DB in batch",
			pathTarget: "",
			textPbPath: `
						origin: "sonic-db",
                        elem: <name: "APPL_DB" > elem: <name: "localhost" > elem:<name:"DASH_QOS" >
                `,
			attributeData: "../testdata/batch.txt",
			wantRetCode:   codes.OK,
			wantRespVal:   emptyRespVal,
			operation:     Replace,
			valTest:       false,
		},
	}

	for _, td := range tds {
		if td.valTest == true {
			// wait for 2 seconds for change to sync
			time.Sleep(2 * time.Second)
			t.Run(td.desc, func(t *testing.T) {
				runTestGet(t, ctx, gClient, td.pathTarget, td.textPbPath, td.wantRetCode, td.wantRespVal, td.valTest)
			})
		} else {
			t.Run(td.desc, func(t *testing.T) {
				runTestSet(t, ctx, gClient, td.pathTarget, td.textPbPath, td.wantRetCode, td.wantRespVal, td.attributeData, td.operation)
			})
		}
	}
	s.Stop()
}

func TestGNMINative(t *testing.T) {
	mock1 := gomonkey.ApplyFunc(dbus.SystemBus, func() (conn *dbus.Conn, err error) {
		return &dbus.Conn{}, nil
	})
	defer mock1.Reset()
	mock2 := gomonkey.ApplyMethod(reflect.TypeOf(&dbus.Object{}), "Go", func(obj *dbus.Object, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
		ret := &dbus.Call{}
		ret.Err = nil
		ret.Body = make([]interface{}, 2)
		ret.Body[0] = int32(0)
		ch <- ret
		return &dbus.Call{}
	})
	defer mock2.Reset()
	mock3 := gomonkey.ApplyFunc(sdc.RunPyCode, func(text string) error {return nil})
	defer mock3.Reset()

	sdcfg.Init()
	s := createServer(t, 8080)
	go runServer(t, s)
	defer s.Stop()
	ns, _ := sdcfg.GetDbDefaultNamespace()
	initFullConfigDb(t, ns)
	initFullCountersDb(t, ns)

	path, _ := os.Getwd()
	path = filepath.Dir(path)

	// This test is used for single database configuration
	// Run tests not marked with multidb
	cmd := exec.Command("bash", "-c", "cd "+path+" && "+"pytest -m 'not multidb'")
	if result, err := cmd.Output(); err != nil {
		fmt.Println(string(result))
		t.Errorf("Fail to execute pytest: %v", err)
	} else {
		fmt.Println(string(result))
	}

	var counters [int(common_utils.COUNTER_SIZE)]uint64
	err := common_utils.GetMemCounters(&counters)
	if err != nil {
		t.Errorf("Error: Fail to read counters, %v", err)
	}
	for i := 0; i < int(common_utils.COUNTER_SIZE); i++ {
		cnt := common_utils.CounterType(i)
		counterName := cnt.String()
		if counterName == "GNMI set" && counters[i] == 0 {
			t.Errorf("GNMI set counter should not be 0")
		}
		if counterName == "GNMI get" && counters[i] == 0 {
			t.Errorf("GNMI get counter should not be 0")
		}
	}
	s.Stop()
}

// Test configuration with multiple databases
func TestGNMINativeMultiDB(t *testing.T) {
	sdcfg.Init()
	err := test_utils.SetupMultiInstance()
	if err != nil {
		t.Fatalf("error Setting up MultiInstance files with err %T", err)
	}

	/* https://www.gopherguides.com/articles/test-cleanup-in-go-1-14*/
	t.Cleanup(func() {
		if err := test_utils.CleanUpMultiInstance(); err != nil {
			t.Fatalf("error Cleaning up MultiInstance files with err %T", err)

		}
	})

	s := createServer(t, 8080)
	go runServer(t, s)
	defer s.Stop()

	path, _ := os.Getwd()
	path = filepath.Dir(path)

	// This test is used for multiple database configuration
	// Run tests marked with multidb
	cmd := exec.Command("bash", "-c", "cd "+path+" && "+"pytest -m 'multidb'")
	if result, err := cmd.Output(); err != nil {
		fmt.Println(string(result))
		t.Errorf("Fail to execute pytest: %v", err)
	} else {
		fmt.Println(string(result))
	}
}

func TestServerPort(t *testing.T) {
	s := createServer(t, -8080)
	port := s.Port()
	if port != 0 {
		t.Errorf("Invalid port: %d", port)
	}
	s.Stop()
}

func TestNilServerStop(t *testing.T) {
	// Create a server with nil grpc server, such that s.Stop is called with nil value
	t.Log("Expecting s.Stop to log error as server is nil")
	s := &Server{}
	s.Stop()
}

func TestNilServerForceStop(t *testing.T) {
	// Create a server with nil grpc server, such that s.ForceStop is called with nil value
	t.Log("Expecting s.ForceStop to log error as server is nil")
	s := &Server{}
	s.ForceStop()
}

func TestInvalidServer(t *testing.T) {
	s := createInvalidServer(t, 9000)
	if s != nil {
		t.Errorf("Should not create invalid server")
	}
}

func TestParseOrigin(t *testing.T) {
	var test_paths []*gnmipb.Path
	var err error

	_, err = ParseOrigin(test_paths)
	if err != nil {
		t.Errorf("ParseOrigin failed for empty path: %v", err)
	}

	test_origin := "sonic-test"
	path, err := xpath.ToGNMIPath(test_origin + ":CONFIG_DB/VLAN")
	test_paths = append(test_paths, path)
	origin, err := ParseOrigin(test_paths)
	if err != nil {
		t.Errorf("ParseOrigin failed to get origin: %v", err)
	}
	if origin != test_origin {
		t.Errorf("ParseOrigin return wrong origin: %v", origin)
	}
	test_origin = "sonic-invalid"
	path, err = xpath.ToGNMIPath(test_origin + ":CONFIG_DB/PORT")
	test_paths = append(test_paths, path)
	origin, err = ParseOrigin(test_paths)
	if err == nil {
		t.Errorf("ParseOrigin should fail for conflict")
	}
}

/*
func TestMasterArbitration(t *testing.T) {
	s := createServer(t, 8088)
	// Turn on Master Arbitration
	s.ReqFromMaster = ReqFromMasterEnabledMA
	go runServer(t, s)
	defer s.Stop()

	prepareDbTranslib(t)

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	//targetAddr := "30.57.185.38:8080"
	targetAddr := "127.0.0.1:8088"
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	maExt0 := &ext_pb.Extension{
		Ext: &ext_pb.Extension_MasterArbitration{
			MasterArbitration: &ext_pb.MasterArbitration{
				ElectionId: &ext_pb.Uint128{High: 0, Low: 0},
			},
		},
	}
	maExt1 := &ext_pb.Extension{
		Ext: &ext_pb.Extension_MasterArbitration{
			MasterArbitration: &ext_pb.MasterArbitration{
				ElectionId: &ext_pb.Uint128{High: 0, Low: 1},
			},
		},
	}
	maExt1H0L := &ext_pb.Extension{
		Ext: &ext_pb.Extension_MasterArbitration{
			MasterArbitration: &ext_pb.MasterArbitration{
				ElectionId: &ext_pb.Uint128{High: 1, Low: 0},
			},
		},
	}
	regExt := &ext_pb.Extension{
		Ext: &ext_pb.Extension_RegisteredExt{
			RegisteredExt: &ext_pb.RegisteredExtension{},
		},
	}

	// By default ElectionID starts from 0 so this test does not change it.
	t.Run("MasterArbitrationOnElectionIdZero", func(t *testing.T) {
		req := &pb.SetRequest{
			Prefix: &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
			Update: []*pb.Update{
				newPbUpdate("interface[name=Ethernet0]/config/mtu", `{"mtu": 9104}`),
			},
			Extension: []*ext_pb.Extension{maExt0},
		}
		_, err = gClient.Set(ctx, req)
		if err != nil {
			t.Fatal("Did not expected an error: " + err.Error())
		}
		if _, ok := status.FromError(err); !ok {
			t.Fatal("Got a non-grpc error from grpc call")
		}
		reqEid0 := maExt0.GetMasterArbitration().GetElectionId()
		expectedEID0 := uint128{High: reqEid0.GetHigh(), Low: reqEid0.GetLow()}
		if s.masterEID.Compare(&expectedEID0) != 0 {
			t.Fatalf("Master EID update failed. Want %v, got %v", expectedEID0, s.masterEID)
		}
	})
	// After this test ElectionID is one.
	t.Run("MasterArbitrationOnElectionIdZeroThenOne", func(t *testing.T) {
		req := &pb.SetRequest{
			Prefix: &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
			Update: []*pb.Update{
				newPbUpdate("interface[name=Ethernet0]/config/mtu", `{"mtu": 9104}`),
			},
			Extension: []*ext_pb.Extension{maExt0},
		}
		if _, err = gClient.Set(ctx, req); err != nil {
			t.Fatal("Did not expected an error: " + err.Error())
		}
		reqEid0 := maExt0.GetMasterArbitration().GetElectionId()
		expectedEID0 := uint128{High: reqEid0.GetHigh(), Low: reqEid0.GetLow()}
		if s.masterEID.Compare(&expectedEID0) != 0 {
			t.Fatalf("Master EID update failed. Want %v, got %v", expectedEID0, s.masterEID)
		}
		req = &pb.SetRequest{
			Prefix: &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
			Update: []*pb.Update{
				newPbUpdate("interface[name=Ethernet0]/config/mtu", `{"mtu": 9104}`),
			},
			Extension: []*ext_pb.Extension{maExt1},
		}
		if _, err = gClient.Set(ctx, req); err != nil {
			t.Fatal("Set gRPC failed")
		}
		reqEid1 := maExt1.GetMasterArbitration().GetElectionId()
		expectedEID1 := uint128{High: reqEid1.GetHigh(), Low: reqEid1.GetLow()}
		if s.masterEID.Compare(&expectedEID1) != 0 {
			t.Fatalf("Master EID update failed. Want %v, got %v", expectedEID1, s.masterEID)
		}
	})
	// Multiple ElectionIDs with the last being one.
	t.Run("MasterArbitrationOnElectionIdMultipleIdsZeroThenOne", func(t *testing.T) {
		req := &pb.SetRequest{
			Prefix: &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
			Update: []*pb.Update{
				newPbUpdate("interface[name=Ethernet0]/config/mtu", `{"mtu": 9104}`),
			},
			Extension: []*ext_pb.Extension{maExt0, maExt1, regExt},
		}
		_, err = gClient.Set(ctx, req)
		if err != nil {
			t.Fatal("Did not expected an error: " + err.Error())
		}
		if _, ok := status.FromError(err); !ok {
			t.Fatal("Got a non-grpc error from grpc call")
		}
		reqEid1 := maExt1.GetMasterArbitration().GetElectionId()
		expectedEID1 := uint128{High: reqEid1.GetHigh(), Low: reqEid1.GetLow()}
		if s.masterEID.Compare(&expectedEID1) != 0 {
			t.Fatalf("Master EID update failed. Want %v, got %v", expectedEID1, s.masterEID)
		}
	})
	// ElectionIDs with the high word set to 1 and low word to 0.
	t.Run("MasterArbitrationOnElectionIdHighOne", func(t *testing.T) {
		req := &pb.SetRequest{
			Prefix: &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
			Update: []*pb.Update{
				newPbUpdate("interface[name=Ethernet0]/config/mtu", `{"mtu": 9104}`),
			},
			Extension: []*ext_pb.Extension{maExt1H0L},
		}
		_, err = gClient.Set(ctx, req)
		if err != nil {
			t.Fatal("Did not expected an error: " + err.Error())
		}
		if _, ok := status.FromError(err); !ok {
			t.Fatal("Got a non-grpc error from grpc call")
		}
		reqEid10 := maExt1H0L.GetMasterArbitration().GetElectionId()
		expectedEID10 := uint128{High: reqEid10.GetHigh(), Low: reqEid10.GetLow()}
		if s.masterEID.Compare(&expectedEID10) != 0 {
			t.Fatalf("Master EID update failed. Want %v, got %v", expectedEID10, s.masterEID)
		}
	})
	// As the ElectionID is one, a request with ElectionID==0 will fail.
	// Also a request without Election ID will fail.
	t.Run("MasterArbitrationOnElectionIdZeroThenNone", func(t *testing.T) {
		req := &pb.SetRequest{
			Prefix: &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
			Update: []*pb.Update{
				newPbUpdate("interface[name=Ethernet0]/config/mtu", `{"mtu": 9104}`),
			},
			Extension: []*ext_pb.Extension{maExt0},
		}
		_, err = gClient.Set(ctx, req)
		if err == nil {
			t.Fatal("Expected a PermissionDenied error")
		}
		ret, ok := status.FromError(err)
		if !ok {
			t.Fatal("Got a non-grpc error from grpc call")
		}
		if ret.Code() != codes.PermissionDenied {
			t.Fatalf("Expected PermissionDenied. Got %v", ret.Code())
		}
		reqEid10 := maExt1H0L.GetMasterArbitration().GetElectionId()
		expectedEID10 := uint128{High: reqEid10.GetHigh(), Low: reqEid10.GetLow()}
		if s.masterEID.Compare(&expectedEID10) != 0 {
			t.Fatalf("Master EID update failed. Want %v, got %v", expectedEID10, s.masterEID)
		}
		req = &pb.SetRequest{
			Prefix: &pb.Path{Elem: []*pb.PathElem{{Name: "interfaces"}}},
			Update: []*pb.Update{
				newPbUpdate("interface[name=Ethernet0]/config/mtu", `{"mtu": 9104}`),
			},
			Extension: []*ext_pb.Extension{},
		}
		_, err = gClient.Set(ctx, req)
		if err != nil {
			t.Fatal("Expected a successful set call.")
		}
		if s.masterEID.Compare(&expectedEID10) != 0 {
			t.Fatalf("Master EID update failed. Want %v, got %v", expectedEID10, s.masterEID)
		}
	})
}*/

func TestSaveOnSet(t *testing.T) {
	// Fail client creation
	fakeDBC := gomonkey.ApplyFuncReturn(ssc.NewDbusClient, nil, fmt.Errorf("Fail Create"))
	if err := SaveOnSetEnabled(); err == nil {
		t.Error("Expected Client Failure")
	}
	fakeDBC.Reset()

	// Successful Dbus call
	goodDbus := gomonkey.ApplyFuncReturn(ssc.DbusApi, nil, nil)
	if err := SaveOnSetEnabled(); err != nil {
		t.Error("Unexpected DBUS failure")
	}
	goodDbus.Reset()

	// Fail Dbus call
	badDbus := gomonkey.ApplyFuncReturn(ssc.DbusApi, nil, fmt.Errorf("Fail Send"))
	defer badDbus.Reset()
	if err := SaveOnSetEnabled(); err == nil {
		t.Error("Expected DBUS failure")
	}
}

func TestPopulateAuthStructByCommonName(t *testing.T) {
	// check auth with nil cert name
	err := PopulateAuthStructByCommonName("certname1", nil, "")
	if err == nil {
		t.Errorf("PopulateAuthStructByCommonName with empty config table should failed: %v", err)
	}
}

func CreateAuthorizationCtx() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cert := x509.Certificate{
		Subject: pkix.Name{
			CommonName: "certname1",
		},
	}
	verifiedCerts := make([][]*x509.Certificate, 1)
	verifiedCerts[0] = make([]*x509.Certificate, 1)
	verifiedCerts[0][0] = &cert
	p := peer.Peer{
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{
				VerifiedChains: verifiedCerts,
			},
		},
	}
	ctx = peer.NewContext(ctx, &p)
	return ctx, cancel
}

func TestClientCertAuthenAndAuthor(t *testing.T) {
	if !swsscommon.SonicDBConfigIsInit() {
		swsscommon.SonicDBConfigInitialize()
	}

	var configDb = swsscommon.NewDBConnector("CONFIG_DB", uint(0), true)
	var gnmiTable = swsscommon.NewTable(configDb, "GNMI_CLIENT_CERT")
	configDb.Flushdb()

	// initialize err variable
	err := status.Error(codes.Unauthenticated, "")

	// when config table is empty, will authorize with PopulateAuthStruct
	mockpopulate := gomonkey.ApplyFunc(PopulateAuthStruct, func(username string, auth *common_utils.AuthInfo, r []string) error {
		return nil
	})
	defer mockpopulate.Reset()

	// check auth with nil cert name
	ctx, cancel := CreateAuthorizationCtx()
	ctx, err = ClientCertAuthenAndAuthor(ctx, "", false)
	if err != nil {
		t.Errorf("CommonNameMatch with empty config table should success: %v", err)
	}

	cancel()

	// check get 1 cert name
	ctx, cancel = CreateAuthorizationCtx()
	configDb.Flushdb()
	gnmiTable.Hset("certname1", "role", "role1")
	ctx, err = ClientCertAuthenAndAuthor(ctx, "GNMI_CLIENT_CERT", false)
	if err != nil {
		t.Errorf("CommonNameMatch with correct cert name should success: %v", err)
	}

	cancel()

	// check get multiple cert names
	ctx, cancel = CreateAuthorizationCtx()
	configDb.Flushdb()
	gnmiTable.Hset("certname1", "role", "role1")
	gnmiTable.Hset("certname2", "role", "role2")
	ctx, err = ClientCertAuthenAndAuthor(ctx, "GNMI_CLIENT_CERT", false)
	if err != nil {
		t.Errorf("CommonNameMatch with correct cert name should success: %v", err)
	}

	cancel()

	// check a invalid cert cname
	ctx, cancel = CreateAuthorizationCtx()
	configDb.Flushdb()
	gnmiTable.Hset("certname2", "role", "role2")
	ctx, err = ClientCertAuthenAndAuthor(ctx, "GNMI_CLIENT_CERT", false)
	if err == nil {
		t.Errorf("CommonNameMatch with invalid cert name should fail: %v", err)
	}

	cancel()

	swsscommon.DeleteTable(gnmiTable)
	swsscommon.DeleteDBConnector(configDb)
}

type MockServerStream struct {
	grpc.ServerStream
}

func (x *MockServerStream) Context() context.Context {
	return context.Background()
}

type MockPingServer struct {
	MockServerStream
}

func (x *MockPingServer) Send(m *gnoi_system_pb.PingResponse) error {
	return nil
}

type MockTracerouteServer struct {
	MockServerStream
}

func (x *MockTracerouteServer) Send(m *gnoi_system_pb.TracerouteResponse) error {
	return nil
}

type MockSetPackageServer struct {
	MockServerStream
}

func (x *MockSetPackageServer) Send(m *gnoi_system_pb.SetPackageResponse) error {
	return nil
}

func (x *MockSetPackageServer) SendAndClose(m *gnoi_system_pb.SetPackageResponse) error {
	return nil
}

func (x *MockSetPackageServer) Recv() (*gnoi_system_pb.SetPackageRequest, error) {
	return nil, nil
}

func TestGnoiAuthorization(t *testing.T) {
	s := createServer(t, 8081)
	go runServer(t, s)
	systemSrv := &SystemServer{Server: s}
	mockAuthenticate := gomonkey.ApplyFunc(s.Authenticate, func(ctx context.Context, req *spb_jwt.AuthenticateRequest) (*spb_jwt.AuthenticateResponse, error) {
		return nil, nil
	})
	defer mockAuthenticate.Reset()

	err := systemSrv.Ping(new(gnoi_system_pb.PingRequest), new(MockPingServer))
	if err == nil {
		t.Errorf("Ping should failed, because not implement.")
	}

	systemSrv.Traceroute(new(gnoi_system_pb.TracerouteRequest), new(MockTracerouteServer))
	if err == nil {
		t.Errorf("Traceroute should failed, because not implement.")
	}

	systemSrv.SetPackage(new(MockSetPackageServer))
	if err == nil {
		t.Errorf("SetPackage should failed, because not implement.")
	}

	ctx := context.Background()
	systemSrv.SwitchControlProcessor(ctx, new(gnoi_system_pb.SwitchControlProcessorRequest))
	if err == nil {
		t.Errorf("SwitchControlProcessor should failed, because not implement.")
	}

	s.Refresh(ctx, new(spb_jwt.RefreshRequest))
	if err == nil {
		t.Errorf("Refresh should failed, because not implement.")
	}

	s.ClearNeighbors(ctx, new(sgpb.ClearNeighborsRequest))
	if err == nil {
		t.Errorf("ClearNeighbors should failed, because not implement.")
	}

	s.CopyConfig(ctx, new(sgpb.CopyConfigRequest))
	if err == nil {
		t.Errorf("CopyConfig should failed, because not implement.")
	}

	s.Stop()
}

func init() {
	// Enable logs at UT setup
	flag.Lookup("v").Value.Set("10")
	flag.Lookup("log_dir").Value.Set("/tmp/telemetrytest")

	// Inform gNMI server to use redis tcp localhost connection
	sdc.UseRedisLocalTcpPort = true
}

func TestMain(m *testing.M) {
	defer test_utils.MemLeakCheck()
	m.Run()
}
