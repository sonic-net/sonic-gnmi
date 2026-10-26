package gnmi

import (
	"crypto/tls"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
)

func TestGetConfigDBWildcardPreservesTables(t *testing.T) {
	s := createServer(t, ServerPort)
	go runServer(t, s)
	defer s.ForceStop()
	defer ResetDataSetsAndMappings(t)

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	conn, err := grpc.Dial(TargetAddr, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", TargetAddr, err)
	}
	defer conn.Close()

	ResetDataSetsAndMappings(t)
	AddDataSet(t, ConfigDbNum, "../testdata/CONFIG_DB_RUNNING_CONFIG.txt")

	ctx, cancel := context.WithTimeout(context.Background(), QueryTimeout*time.Second)
	defer cancel()
	runTestGet(t, ctx, pb.NewGNMIClient(conn), "CONFIG_DB", `elem: <name: "*" >`, codes.OK,
		[]byte(`{"ACL_TABLE":{"DATAACL":{"ports":["Ethernet0","Ethernet4"],"type":"L3"}},"BGP_NEIGHBOR":{"10.0.0.1":{"asn":"65000","name":"leaf"}},"DEVICE_METADATA":{"localhost":{"hostname":"sonic","type":"ToRRouter"}},"NTP_SERVER":{"10.0.0.1":{}}}`), true)
	runTestGet(t, ctx, pb.NewGNMIClient(conn), "CONFIG_DB", `elem: <name: "BGP_NEIGHBOR" >`, codes.OK,
		[]byte(`{"10.0.0.1":{"asn":"65000","name":"leaf"}}`), true)
}
