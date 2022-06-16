package gnmi

// server_test covers gNMI get, subscribe (stream and poll) test
// Prerequisite: redis-server should be running.
import (
	"crypto/tls"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"fmt"

	testcert "github.com/sonic-net/sonic-gnmi/testdata/tls"

	"testing"

	_ "github.com/openconfig/gnmi/client"
	_ "github.com/openconfig/ygot/ygot"
	_ "github.com/google/gnxi/utils"
	_ "github.com/jipanyang/gnxi/utils/xpath"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	// Register supported client types.
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

func createServer(t *testing.T, port int64) *Server {
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Errorf("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	cfg := &Config{Port: port, TestMode: true}
	s, err := NewServer(cfg, opts)
	if err != nil {
		t.Errorf("Failed to create gNMI server: %v", err)
	}
	return s
}

func createAuthServer(t *testing.T, port int64) *Server {
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Errorf("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	cfg := &Config{Port: port, TestMode: true, UserAuth: AuthTypes{"password": true, "cert": true, "jwt": true}}
	s, err := NewServer(cfg, opts)
	if err != nil {
		t.Errorf("Failed to create gNMI server: %v", err)
	}
	return s
}

func runServer(t *testing.T, s *Server) {
	//t.Log("Starting RPC server on address:", s.Address())
	err := s.Serve() // blocks until close
	if err != nil {
		t.Fatalf("gRPC server err: %v", err)
	}
	//t.Log("Exiting RPC server on address", s.Address())
}

func TestAll(t *testing.T) {
	s := createServer(t, 8080)
	go runServer(t, s)
	defer s.s.Stop()

	path, _ := os.Getwd()
	path = filepath.Dir(path)

	var cmd *exec.Cmd
	cmd = exec.Command("bash", "-c", "cd "+path+" && "+"pytest -m noauth")
	if result, err := cmd.Output(); err != nil {
		fmt.Println(string(result))
		t.Errorf("Fail to execute pytest: %v", err)
	} else {
		fmt.Println(string(result))
	}
}

func TestAuth(t *testing.T) {
	s := createAuthServer(t, 8080)
	go runServer(t, s)
	defer s.s.Stop()

	path, _ := os.Getwd()
	path = filepath.Dir(path)

	var cmd *exec.Cmd
	cmd = exec.Command("bash", "-c", "cd "+path+" && "+"pytest -m auth")
	if result, err := cmd.Output(); err != nil {
		fmt.Println(string(result))
		t.Errorf("Fail to execute pytest: %v", err)
	} else {
		fmt.Println(string(result))
	}
}

func init() {
	// Enable logs at UT setup
	flag.Lookup("v").Value.Set("10")
	flag.Lookup("log_dir").Value.Set("/tmp/gnmitest")

	// Inform gNMI server to use redis tcp localhost connection
	sdc.UseRedisLocalTcpPort = true
}
