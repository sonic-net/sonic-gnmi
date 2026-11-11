//go:build e2e

package e2e_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	pb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// TestCertificateReadonlyGet exercises the packaged process, not Go server handlers.
// Run only in a newly owned disposable container with private network and IPC.
func TestCertificateReadonlyGet(t *testing.T) {
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires a disposable native-runtime container")
	}
	binary := os.Getenv("TELEMETRY_BIN")
	wantHash := os.Getenv("TELEMETRY_SHA256")
	if !filepath.IsAbs(binary) || len(wantHash) != 64 {
		t.Fatal("set TELEMETRY_BIN and verified TELEMETRY_SHA256")
	}
	if got := fileHash(t, binary); got != wantHash {
		t.Fatalf("artifact hash: got %s want %s", got, wantHash)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root := t.TempDir()
	sock := filepath.Join(root, "redis.sock")
	config := "/var/run/redis/sonic-db/database_config.json"
	if _, err := os.Lstat("/var/run/redis/redis.sock"); !os.IsNotExist(err) {
		t.Fatal("existing default Redis socket; refusing shared runtime")
	}
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:6379", time.Second); err == nil {
		conn.Close()
		t.Fatal("existing default Redis listener; refusing shared runtime")
	}
	previous, err := os.ReadFile(config)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var mode os.FileMode
	if info, err := os.Lstat(config); err == nil {
		if !info.Mode().IsRegular() {
			t.Fatal("database config is not a regular file")
		}
		mode = info.Mode().Perm()
	}
	check(t, os.MkdirAll(filepath.Dir(config), 0755))
	t.Cleanup(func() {
		if previous == nil {
			check(t, os.Remove(config))
		} else {
			check(t, os.WriteFile(config, previous, mode))
			check(t, os.Chmod(config, mode))
			if got := fileHash(t, config); got != hash(previous) {
				t.Error("database configuration was not restored")
			}
		}
		if _, err := os.Stat(sock); !os.IsNotExist(err) {
			t.Error("owned Redis socket remains after cleanup")
		}
		t.Log("cleanup: database configuration restored; Redis socket absent")
	})
	dbs := map[string]any{}
	for name, id := range map[string]int{"APPL_DB": 0, "ASIC_DB": 1, "COUNTERS_DB": 2, "CONFIG_DB": 4, "STATE_DB": 6} {
		separator := ":"
		if name == "CONFIG_DB" || name == "STATE_DB" {
			separator = "|"
		}
		dbs[name] = map[string]any{"id": id, "separator": separator, "instance": "redis"}
	}
	data, err := json.Marshal(map[string]any{
		"INSTANCES": map[string]any{"redis": map[string]any{"hostname": "127.0.0.1", "port": 6379, "unix_socket_path": sock}},
		"DATABASES": dbs, "VERSION": "1.0",
	})
	check(t, err)
	check(t, os.WriteFile(config, data, 0600))
	startProcess(t, root, "redis", exec.Command("redis-server", "--port", "0", "--save", "", "--appendonly", "no",
		"--unixsocket", sock, "--unixsocketperm", "700", "--dir", root))
	db := redis.NewClient(&redis.Options{Network: "unix", Addr: sock, DB: 4, DialTimeout: time.Second,
		ReadTimeout: time.Second, WriteTimeout: time.Second, MaxRetries: -1})
	t.Cleanup(func() { check(t, db.Close()) })
	waitFor(t, ctx, func() error { return db.Ping(ctx).Err() })
	check(t, db.HSet(ctx, "DEVICE_METADATA|localhost", "cloudtype", "Private", "hostname", "artifact").Err())
	check(t, db.Set(ctx, "CONFIG_DB_INITIALIZED", "1", 0).Err())
	check(t, db.HSet(ctx, "GNMI_CLIENT_CERT|go-control", "role@", "gnmi_readwrite,gnmi_config_db_readwrite").Err())
	check(t, db.HSet(ctx, "GNMI_CLIENT_CERT|go-readonly", "role@", "gnmi_config_db_readonly").Err())
	role, err := db.HGet(ctx, "GNMI_CLIENT_CERT|go-readonly", "role@").Result()
	check(t, err)
	if role != "gnmi_config_db_readonly" {
		t.Fatalf("unexpected case role %q", role)
	}
	ca, server, control, readonly := certificates(t, root)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	check(t, err)
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	check(t, listener.Close())
	command := exec.Command(binary, "--port", fmt.Sprint(port), "--bind_address", "127.0.0.1", "--unix_socket=",
		"--client_auth", "cert", "--config_table_name", "GNMI_CLIENT_CERT", "--ca_crt", filepath.Join(root, "ca.pem"),
		"--server_crt", server.cert, "--server_key", server.key, "-logtostderr")
	command.Env = append(os.Environ(), "DB_CONFIG_PATH="+config)
	process := startProcess(t, root, "telemetry", command)
	controlClient := connect(t, address, ca, control)
	request := &pb.GetRequest{Encoding: pb.Encoding_JSON_IETF, Path: []*pb.Path{{Origin: "sonic-db"}}}
	for _, name := range []string{"CONFIG_DB", "localhost", "DEVICE_METADATA", "localhost", "cloudtype"} {
		request.Path[0].Elem = append(request.Path[0].Elem, &pb.PathElem{Name: name})
	}
	// Only the separate control identity is used for readiness, never the case CN.
	waitFor(t, ctx, func() error {
		probe, done := context.WithTimeout(ctx, 2*time.Second)
		defer done()
		_, err := controlClient.Capabilities(probe, &pb.CapabilityRequest{})
		return err
	})
	if got := fileHash(t, fmt.Sprintf("/proc/%d/exe", process.Process.Pid)); got != wantHash {
		t.Fatal("running executable differs from the selected artifact")
	} else {
		t.Logf("running telemetry sha256=%s", got)
	}
	client := connect(t, address, ca, readonly)
	rpc, done := context.WithTimeout(ctx, 10*time.Second)
	defer done()
	response, err := client.Get(rpc, request)
	check(t, err)
	var values []string
	for _, notification := range response.Notification {
		for _, update := range notification.Update {
			var value string
			check(t, json.Unmarshal(update.Val.GetJsonIetfVal(), &value))
			values = append(values, value)
		}
	}
	if len(values) != 1 || values[0] != "Private" {
		t.Fatalf("Get returned %q; want exactly [Private]", values)
	}
	t.Logf("readonly certificate Get: value=Private artifact_sha256=%s", wantHash)
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	check(t, err)
	return hash(data)
}

func waitFor(t *testing.T, ctx context.Context, probe func() error) {
	t.Helper()
	for {
		err := probe()
		if err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("readiness timed out: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func startProcess(t *testing.T, root, name string, command *exec.Cmd) *exec.Cmd {
	t.Helper()
	log, err := os.Create(filepath.Join(root, name+".log"))
	check(t, err)
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		defer log.Close()
		select {
		case err := <-done:
			t.Errorf("%s exited before cleanup: %v", name, err)
		default:
			_ = command.Process.Signal(syscall.SIGTERM)
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("%s cleanup: %v", name, err)
				} else {
					t.Logf("cleanup: %s exited successfully", name)
				}
			case <-time.After(10 * time.Second):
				_ = command.Process.Kill()
				<-done
				t.Errorf("%s required forced kill", name)
			}
		}
		if t.Failed() {
			data, _ := os.ReadFile(log.Name())
			if len(data) > 16000 {
				data = data[len(data)-16000:]
			}
			t.Logf("%s diagnostics: %s", name, data)
		}
	})
	return command
}

type identity struct{ cert, key string }

func certificates(t *testing.T, root string) (*x509.CertPool, identity, identity, identity) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(t, err)
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "go-e2e-ca"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	check(t, err)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	check(t, os.WriteFile(filepath.Join(root, "ca.pem"), caPEM, 0600))
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("invalid generated CA")
	}
	var identities []identity
	for index, cn := range []string{"server", "go-control", "go-readonly"} {
		leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		check(t, err)
		leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(index + 2)), Subject: pkix.Name{CommonName: cn},
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		if cn == "server" {
			leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			leaf.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		}
		encoded, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, key)
		check(t, err)
		private, err := x509.MarshalECPrivateKey(leafKey)
		check(t, err)
		id := identity{filepath.Join(root, cn+".pem"), filepath.Join(root, cn+".key")}
		check(t, os.WriteFile(id.cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: encoded}), 0600))
		check(t, os.WriteFile(id.key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}), 0600))
		identities = append(identities, id)
	}
	return pool, identities[0], identities[1], identities[2]
}

func connect(t *testing.T, address string, roots *x509.CertPool, id identity) pb.GNMIClient {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(id.cert, id.key)
	check(t, err)
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		RootCAs: roots, Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12,
	})), grpc.WithDisableRetry())
	check(t, err)
	t.Cleanup(func() { check(t, conn.Close()) })
	return pb.NewGNMIClient(conn)
}
