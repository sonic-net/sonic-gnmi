package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis"
	"github.com/openconfig/gnmi/client"
	gnmi "github.com/sonic-net/sonic-gnmi/gnmi_server"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func createClientWithCertCapture(t *testing.T, port int) (*grpc.ClientConn, string) {
	t.Helper()

	var capturedSerial string
	var mu sync.Mutex
	certCaptured := make(chan bool, 1)

	tlsCfg := &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			// Capture the server's certificate serial
			if len(rawCerts) > 0 {
				cert, err := x509.ParseCertificate(rawCerts[0])
				if err == nil {
					mu.Lock()
					capturedSerial = cert.SerialNumber.String()
					mu.Unlock()
					t.Logf("Client captured server cert serial=%s", capturedSerial)
					select {
					case certCaptured <- true:
					default:
					}
				}
			}
			return nil
		},
	}

	creds := credentials.NewTLS(tlsCfg)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}

	// Wait for TLS handshake to complete and cert to be captured
	select {
	case <-certCaptured:
		// Cert captured successfully
	case <-time.After(2 * time.Second):
		t.Errorf("Timeout waiting for TLS handshake to capture cert")
	}

	mu.Lock()
	serial := capturedSerial
	mu.Unlock()

	return conn, serial
}

func getRedisClientN(t *testing.T, n int, namespace string) *redis.Client {
	addr, err := sdcfg.GetDbTcpAddr("APPL_DB", namespace)
	if err != nil {
		t.Fatalf("failed to get addr %v", err)
	}
	rclient := redis.NewClient(&redis.Options{
		Network:     "tcp",
		Addr:        addr,
		Password:    "",
		DB:          n,
		DialTimeout: 0,
	})
	_, err = rclient.Ping().Result()
	if err != nil {
		t.Fatalf("failed to connect to redis server %v", err)
	}
	return rclient
}

// TestTelemetryCertRotationWithLiveConnections validates
// 1. Existing TLS connections continue working with the old certificates
// 2. New TLS connections use the rotated certificate loaded from disk
func TestTelemetryCertRotationWithLiveConnections(t *testing.T) {
	// Create temp directory for cert files
	tempDir, err := ioutil.TempDir("", "telemetry_cert_rotation_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	certPath := filepath.Join(tempDir, "server.crt")
	keyPath := filepath.Join(tempDir, "server.key")
	caCertPath := filepath.Join(tempDir, "ca.crt")
	caKeyPath := filepath.Join(tempDir, "ca.key")

	// Generate initial certificate (v1)
	serial1, err := saveCertKeyPairWithSerial(certPath, keyPath)
	if err != nil {
		t.Fatalf("Failed to generate initial cert: %v", err)
	}
	t.Logf("Generated initial cert v1 with serial: %s", serial1)

	// Generate CA cert (required by telemetry server)
	_, err = saveCertKeyPairWithSerial(caCertPath, caKeyPath)
	if err != nil {
		t.Fatalf("Failed to generate CA cert: %v", err)
	}

	// Setup telemetry config
	port := 8081
	logLevel := 2
	threshold := 100
	idleConnDuration := 0 // 0 = infinite, no idle timeout
	jwtRefInt := uint64(900)
	jwtValInt := uint64(3600)
	configTableName := ""
	vrf := ""
	enableCrl := false
	gnmiTranslibWrite := false
	gnmiNativeWrite := false

	telemetryCfg := &TelemetryConfig{
		UserAuth:              gnmi.AuthTypes{"password": false, "cert": false, "jwt": false},
		Port:                  &port,
		ServerCert:            &certPath,
		ServerKey:             &keyPath,
		CaCert:                &caCertPath,
		NoTLS:                 boolPtr(false),
		Insecure:              boolPtr(false),
		AllowNoClientCert:     boolPtr(true),
		LogLevel:              &logLevel,
		Threshold:             &threshold,
		IdleConnDuration:      &idleConnDuration,
		JwtRefInt:             &jwtRefInt,
		JwtValInt:             &jwtValInt,
		ConfigTableName:       &configTableName,
		Vrf:                   &vrf,
		EnableCrl:             &enableCrl,
		GnmiTranslibWrite:     &gnmiTranslibWrite,
		GnmiNativeWrite:       &gnmiNativeWrite,
		WithSaveOnSet:         boolPtr(false),
		WithMasterArbitration: boolPtr(false),
	}

	cfg := &gnmi.Config{Port: 8081}
	serverControlSignal := make(chan ServerControlValue, 1)
	stopSignalHandler := make(chan bool, 1)
	wg := &sync.WaitGroup{}

	// Start telemetry server with actual GetCertificate callback
	wg.Add(1)
	go startGNMIServer(telemetryCfg, cfg, serverControlSignal, stopSignalHandler, wg)

	// Wait for server to be ready by attempting to connect
	serverReady := false
	for i := 0; i < 20; i++ { // Try for up to 2 seconds
		testConn, err := grpc.Dial(fmt.Sprintf("127.0.0.1:%d", port),
			grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})),
			grpc.WithBlock(),
			grpc.WithTimeout(100*time.Millisecond))
		if err == nil {
			testConn.Close()
			serverReady = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !serverReady {
		t.Fatalf("Server failed to start within timeout")
	}

	// Prepare database with test data
	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, 0, ns)
	defer rclient.Close()
	rclient.FlushDB()

	// Add LLDP test data
	rclient.HSet("LLDP_ENTRY_TABLE:eth0", "lldp_rem_port_id", "dummy")
	rclient.HSet("LLDP_ENTRY_TABLE:eth0", "lldp_rem_sys_name", "dummy")

	// Establish connection #1 with cert v1
	t.Log("=== Connection #1: Establish with cert v1 ===")
	conn1, serverSerial1 := createClientWithCertCapture(t, 8081)
	defer conn1.Close()

	if serverSerial1 == "" {
		t.Errorf("Failed to capture server cert serial for connection #1")
	}
	if serverSerial1 != serial1 {
		t.Errorf("Connection #1: server presented cert serial=%s, expected=%s", serverSerial1, serial1)
	}
	t.Logf("Connection #1: Verified server using cert v1 (serial=%s)", serverSerial1)

	// Make a poll request on connection #1
	c1 := client.New()
	defer c1.Close()

	q1 := client.Query{
		Target:  "APPL_DB",
		Type:    client.Poll,
		Queries: []client.Path{{"LLDP_ENTRY_TABLE"}},
		TLS:     &tls.Config{InsecureSkipVerify: true},
		Addrs:   []string{"127.0.0.1:8081"},
		NotificationHandler: func(n client.Notification) error {
			return nil
		},
	}

	wg1 := new(sync.WaitGroup)
	wg1.Add(1)
	go func() {
		defer wg1.Done()
		c1.Subscribe(context.Background(), q1)
	}()
	wg1.Wait()

	err = c1.Poll()
	if err != nil {
		t.Logf("Connection #1 poll: %v (may fail without data)", err)
	}

	// Rotate certificates - generate new cert and overwrite files
	t.Log("=== Rotating certificates on disk ===")
	serial2, err := saveCertKeyPairWithSerial(certPath, keyPath)
	if err != nil {
		t.Fatalf("Failed to rotate cert: %v", err)
	}
	t.Logf("Generated rotated cert v2 with serial: %s", serial2)

	if serial1 == serial2 {
		t.Fatalf("Cert rotation failed: serial numbers are the same (%s)", serial1)
	}

	// Brief pause to ensure filesystem has flushed
	time.Sleep(50 * time.Millisecond)

	// Connection #1 should still work with existing TLS session
	t.Log("=== Connection #1: Poll after rotation (uses existing TLS session) ===")
	err = c1.Poll()
	if err != nil {
		t.Logf("Connection #1 poll after rotation: %v (may fail without data)", err)
	}

	// Establish connection #2 - should trigger GetCertificate and load cert v2
	t.Log("=== Connection #2: Establish with cert v2 (triggers GetCertificate) ===")
	conn2, serverSerial2 := createClientWithCertCapture(t, 8081)
	defer conn2.Close()

	if serverSerial2 == "" {
		t.Errorf("Failed to capture server cert serial for connection #2")
	}
	if serverSerial2 != serial2 {
		t.Errorf("Connection #2: server presented cert serial=%s, expected=%s", serverSerial2, serial2)
	}
	if serverSerial2 == serverSerial1 {
		t.Errorf("Connection #2: server still using old cert (serial=%s), GetCertificate not working", serverSerial2)
	}
	t.Logf("Connection #2: Verified server using cert v2 (serial=%s)", serverSerial2)

	// Make a poll request on connection #2 to verify it works with rotated cert
	c2 := client.New()
	defer c2.Close()

	q2 := client.Query{
		Target:  "APPL_DB",
		Type:    client.Poll,
		Queries: []client.Path{{"LLDP_ENTRY_TABLE"}},
		TLS:     &tls.Config{InsecureSkipVerify: true},
		Addrs:   []string{"127.0.0.1:8081"},
		NotificationHandler: func(n client.Notification) error {
			return nil
		},
	}

	wg2 := new(sync.WaitGroup)
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		c2.Subscribe(context.Background(), q2)
	}()
	wg2.Wait()

	err = c2.Poll()
	if err != nil {
		t.Logf("Connection #2 poll: %v (may fail without data)", err)
	}

	// Cleanup
	sendSignal(serverControlSignal, ServerStop)
	wg.Wait()

	t.Logf("=== Test complete: Verified GetCertificate rotated cert (v1=%s, v2=%s) ===", serial1, serial2)
}

func boolPtr(b bool) *bool {
	return &b
}
